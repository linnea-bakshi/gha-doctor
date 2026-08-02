package api

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// nodeDeprecationRe matches the runner's Node-runtime deprecation notice
// ("Node 20 is being deprecated. This workflow is running with Node 24 …"),
// which it prints between a step's real output and its post steps.
var nodeDeprecationRe = regexp.MustCompile(`^Node \d+ is being deprecated\b`)

// How many failing jobs get a log tail (one logs request each — a red
// matrix can have dozens of failures, but the first ones tell the story).
const maxFailLogJobs = 2

// Lines longer than this are cut: minified output and base64 blobs would
// otherwise swallow the terminal.
const maxLogLineLen = 300

// Extra lines kept after the last ##[error] marker so the tail shows the
// runner's summary lines ("Process completed with exit code 1"), not just
// the error itself.
const logAfterError = 3

// Cap on failing tests stored per job for a --run deep dive. A broken
// import can fail an entire suite; past this the count says more than more
// names would (FailedTestsMore carries the remainder).
const maxRunFailedTests = 20

// Slack around the failing step's API timestamps when slicing the job log:
// the jobs API reports whole seconds while log lines carry sub-second
// stamps, so a strict window can drop the first or last line of the step.
const logWindowSlack = 2 * time.Second

// attachFailLogs fetches the job log for up to maxFailLogJobs failed jobs
// and attaches the tail of the failing step's section to each DeepJob.
// Requires a token (the logs endpoint 403s unauthenticated even on public
// repos); without one it leaves a note instead of failing the dive.
func (c *Client) attachFailLogs(owner, repo string, d *RunDeep, byName map[string]Job, tail int, progress func(string)) {
	var failing []int
	for i, j := range d.Jobs {
		if j.Conclusion == "failure" || j.Conclusion == "timed_out" {
			failing = append(failing, i)
		}
	}
	if len(failing) == 0 {
		return
	}
	if c.Token == "" {
		d.LogNote = "failing-step log tails need authentication — run with GITHUB_TOKEN set (or via `gh auth login`)"
		return
	}
	if len(failing) > maxFailLogJobs {
		failing = failing[:maxFailLogJobs]
	}
	for _, i := range failing {
		dj := &d.Jobs[i]
		src, ok := byName[dj.Name]
		if !ok {
			continue
		}
		var failStep *Step
		for k := range src.Steps {
			st := &src.Steps[k]
			if st.Conclusion == "failure" || st.Conclusion == "timed_out" {
				failStep = st
				break
			}
		}
		progress(fmt.Sprintf("fetching log for failed job %q…", dj.Name))
		text, err := c.GetJobLogs(owner, repo, src.ID)
		if err != nil {
			progress(fmt.Sprintf("  log unavailable: %v", err))
			continue
		}
		var start, end time.Time
		if failStep != nil {
			dj.LogStep = failStep.Name
			start, end = failStep.StartedAt, failStep.CompletedAt
		}
		dj.LogTail = failLogTail(text, start, end, tail)
		// Name the failing tests, if the log speaks a recognized
		// framework's failure format. The whole job log is scanned (not
		// just the failing step's window): summaries often print in a
		// later reporting step, and non-test output extracts nothing by
		// design (proven against the negative log corpus).
		tests := parseTestFailures(text)
		for k, tf := range tests {
			if k == maxRunFailedTests {
				dj.FailedTestsMore = len(tests) - maxRunFailedTests
				break
			}
			dj.FailedTests = append(dj.FailedTests, RunFailedTest{Name: tf.name, Framework: tf.framework})
		}
	}
}

// failLogTail returns the last n useful lines of the failing step's slice
// of a job log. Lines are matched to the step by their timestamp prefix
// (the jobs API gives the step's start/completion times); if any
// ##[error] marker is present the tail ends just after the last one, so
// the error is on screen even when cleanup chatter follows.
func failLogTail(text string, start, end time.Time, n int) []string {
	if n <= 0 {
		return nil
	}
	var window []string
	useWindow := !start.IsZero()
	if useWindow {
		start = start.Add(-logWindowSlack)
	}
	if !end.IsZero() {
		end = end.Add(logWindowSlack)
	}
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimRight(raw, "\r")
		ts, rest, hasTS := splitLogTS(raw)
		if useWindow {
			if !hasTS || ts.Before(start) {
				continue
			}
			if !end.IsZero() && ts.After(end) {
				break
			}
		} else if hasTS {
			// No step window: strip prefixes but keep everything.
		}
		window = append(window, rest)
	}
	if len(window) == 0 && useWindow {
		// Timestamps didn't line up (edited logs, clock skew) — fall back
		// to the whole log rather than showing nothing.
		return failLogTail(text, time.Time{}, time.Time{}, n)
	}
	// Drop trailing blank lines and runner bookkeeping — the window's +2s
	// slack can catch the first post-step lines ("Post job cleanup."),
	// which say nothing about the failure.
	trimNoise := func() {
		for len(window) > 0 && isLogNoise(window[len(window)-1]) {
			window = window[:len(window)-1]
		}
	}
	trimNoise()
	// Anchor on the last ##[error] marker if the window has one.
	lastErr := -1
	for i, l := range window {
		if strings.Contains(l, "##[error]") {
			lastErr = i
		}
	}
	if lastErr >= 0 {
		// Keep a little context after the marker, but never so much that
		// the final n-line tail pushes the error itself off screen, and
		// never across a ##[group] boundary — that's the next step (the
		// window's slack can catch its first lines), not failure evidence.
		after := logAfterError
		if strings.Contains(window[lastErr], "Process completed with exit code") {
			// That marker is always the terminal line of a failing step;
			// anything after it is post-step chatter, not evidence.
			after = 0
		}
		if after > n-1 {
			after = n - 1
		}
		cut := lastErr + 1
		for cut < len(window) && cut <= lastErr+after && !strings.HasPrefix(window[cut], "##[group]") && !isLogNoise(window[cut]) {
			cut++
		}
		if cut < len(window) {
			window = window[:cut]
		}
		trimNoise() // the kept context can itself end in bookkeeping
	}
	if len(window) > n {
		window = window[len(window)-n:]
	}
	for i, l := range window {
		if len(l) > maxLogLineLen {
			window[i] = l[:maxLogLineLen] + "…"
		}
	}
	return window
}

// isLogNoise reports whether a trailing line is runner bookkeeping rather
// than failure evidence: blank lines, group delimiters, and post-step
// cleanup chatter.
func isLogNoise(l string) bool {
	switch strings.TrimSpace(l) {
	case "", "Post job cleanup.", "Cleaning up orphan processes":
		return true
	}
	// Runner-emitted deprecation chatter ("Node 20 is being deprecated. …")
	// shows up between a step's last line and its post steps.
	if nodeDeprecationRe.MatchString(l) {
		return true
	}
	return strings.HasPrefix(l, "##[endgroup]")
}

// splitLogTS splits a job-log line into its timestamp prefix and the rest.
// Runner logs stamp every line like "2026-07-30T11:00:00.1234567Z body".
func splitLogTS(line string) (time.Time, string, bool) {
	sp := strings.IndexByte(line, ' ')
	if sp <= 0 {
		return time.Time{}, line, false
	}
	ts, err := time.Parse(time.RFC3339Nano, line[:sp])
	if err != nil {
		return time.Time{}, line, false
	}
	return ts, line[sp+1:], true
}
