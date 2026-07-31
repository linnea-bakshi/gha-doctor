package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// FlakyTestStats names the tests behind flaky jobs, extracted from the logs
// of failed job instances whose commit later passed (the same-SHA fail+pass
// pairs computeFlaky found). A test named here failed in a run that the
// project itself treated as non-reproducible — the strongest flakiness
// signal available without instrumenting the test suite.
type FlakyTestStats struct {
	Available   bool        `json:"available"`
	Note        string      `json:"note,omitempty"` // why unavailable / empty
	LogsTotal   int         `json:"logs_total"`     // flaky-failure logs that exist in the sample window
	LogsSampled int         `json:"logs_sampled"`   // how many we fetched
	JobsSkipped int         `json:"jobs_skipped"`   // fetch errors (expired logs etc.)
	Tests       []FlakyTest `json:"tests,omitempty"`
}

// FlakyTest is one test (or suite entry) seen failing in flaky-job logs.
type FlakyTest struct {
	Name      string   `json:"name"`
	Framework string   `json:"framework"` // pytest / go / cargo / jest / rspec / maven
	Failures  int      `json:"failures"`  // sampled logs it failed in
	Commits   int      `json:"commits"`   // distinct commits it flaked on
	Jobs      []string `json:"jobs"`      // distinct job names (base name, matrix collapsed)
}

// flakyFail is one failed job instance from a flaky (same-SHA fail+pass)
// group — the population --flaky-logs samples from.
type flakyFail struct {
	job Job
	wf  string
	sha string
}

// testFailure is one extracted failing-test line.
type testFailure struct {
	framework string
	name      string
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// Framework extractors. Each pattern anchors on the framework's own failure
// summary format; a line must match exactly one way. Kept deliberately
// narrow: a false "flaky test" name is worse than a miss (the section says
// what it could not parse).
var (
	// pytest short summary: "FAILED tests/test_x.py::TestC::test_y - AssertionError: ..."
	pytestFailedRE = regexp.MustCompile(`^(?:FAILED|ERROR) +(\S+::\S+)`)
	// pytest verbose: "tests/test_x.py::test_y FAILED [ 12%]"
	pytestVerboseRE = regexp.MustCompile(`^(\S+::\S+) FAILED\b`)
	// go test: "--- FAIL: TestName/sub (0.01s)" (indented for subtests)
	goFailRE = regexp.MustCompile(`^ *--- FAIL: (\S+)`)
	// cargo test: "test module::name ... FAILED"
	cargoFailRE = regexp.MustCompile(`^test (\S+) \.\.\. FAILED$`)
	// jest/vitest summary: "✕ test name (12 ms)"
	jestFailRE = regexp.MustCompile(`^✕ (.+?)(?: \(\d+(?:\.\d+)? ?m?s\))?$`)
	// rspec failed examples: "rspec ./spec/foo_spec.rb:12 # Class does a thing"
	rspecFailRE = regexp.MustCompile(`^rspec \.?/\S+:\d+ # (.+)$`)
	// maven surefire summary: "[ERROR]   ClassTest.testMethod:34 expected: ..."
	mavenFailRE = regexp.MustCompile(`^\[ERROR\] +([A-Za-z][A-Za-z0-9_.$]*\.[a-zA-Z_][A-Za-z0-9_]*):\d+`)
	// playwright numbered failure: "1) [project] › tests\x.spec.ts:827:5 › title ───"
	playwrightFailRE = regexp.MustCompile(`^\d+\) ((?:\[[^\]]+\] › )?\S+:\d+:\d+ › .+)$`)
	// playwrightLineRE strips the :line:col so the same test aggregates
	// across commits even when the file shifts underneath it.
	playwrightLineRE = regexp.MustCompile(`:\d+:\d+ › `)
)

// parseTestFailures extracts failing-test names from one job log. Names are
// deduped within the log; go parent tests are dropped when a subtest of
// theirs was also captured (both lines appear in go output).
func parseTestFailures(text string) []testFailure {
	seen := map[string]bool{}
	var out []testFailure
	add := func(fw, name string) {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 200 {
			return
		}
		k := fw + "\x00" + name
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, testFailure{framework: fw, name: name})
	}
	for _, raw := range strings.Split(text, "\n") {
		_, line, _ := splitLogTS(strings.TrimRight(raw, "\r"))
		line = stripANSI(line)
		trimmed := strings.TrimSpace(line)
		switch {
		case pytestFailedRE.MatchString(line):
			add("pytest", pytestFailedRE.FindStringSubmatch(line)[1])
		case pytestVerboseRE.MatchString(line):
			add("pytest", pytestVerboseRE.FindStringSubmatch(line)[1])
		case goFailRE.MatchString(line):
			add("go", goFailRE.FindStringSubmatch(line)[1])
		case cargoFailRE.MatchString(line):
			add("cargo", cargoFailRE.FindStringSubmatch(line)[1])
		case strings.HasPrefix(trimmed, "✕ ") && jestFailRE.MatchString(trimmed):
			add("jest", jestFailRE.FindStringSubmatch(trimmed)[1])
		case rspecFailRE.MatchString(trimmed):
			add("rspec", rspecFailRE.FindStringSubmatch(trimmed)[1])
		case mavenFailRE.MatchString(line):
			add("maven", mavenFailRE.FindStringSubmatch(line)[1])
		default:
			// playwright decorates the line with trailing ─ rules.
			pw := strings.TrimRight(trimmed, "─ ")
			if m := playwrightFailRE.FindStringSubmatch(pw); m != nil {
				add("playwright", playwrightLineRE.ReplaceAllString(m[1], " › "))
			}
		}
	}
	// Drop go parents whose subtest was also captured: "--- FAIL: TestFoo"
	// always accompanies "--- FAIL: TestFoo/sub"; the subtest is the story.
	goNames := map[string]bool{}
	for _, f := range out {
		if f.framework == "go" {
			goNames[f.name] = true
		}
	}
	filtered := out[:0]
	for _, f := range out {
		if f.framework == "go" {
			parent := false
			for n := range goNames {
				if strings.HasPrefix(n, f.name+"/") {
					parent = true
					break
				}
			}
			if parent {
				continue
			}
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// pickFlakyFailLogs orders flaky failures for sampling: round-robin across
// distinct (workflow, job) groups, newest first within each, so one
// pathological job cannot monopolize the budget.
func pickFlakyFailLogs(fails []flakyFail, n int) []flakyFail {
	byGroup := map[string][]flakyFail{}
	for _, f := range fails {
		k := f.wf + "\x00" + baseJobName(f.job.Name)
		byGroup[k] = append(byGroup[k], f)
	}
	keys := make([]string, 0, len(byGroup))
	for k := range byGroup {
		sort.Slice(byGroup[k], func(a, b int) bool {
			return byGroup[k][a].job.CompletedAt.After(byGroup[k][b].job.CompletedAt)
		})
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []flakyFail
	for round := 0; len(out) < n; round++ {
		added := false
		for _, k := range keys {
			if round < len(byGroup[k]) && len(out) < n {
				out = append(out, byGroup[k][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

// analyzeFlakyLogs fetches up to sample flaky-failure logs and names the
// failing tests. fails comes from computeFlaky (same-SHA fail+pass groups).
func (c *Client) analyzeFlakyLogs(owner, repo string, fails []flakyFail, sample int, progress func(string)) *FlakyTestStats {
	st := &FlakyTestStats{LogsTotal: len(fails)}
	if c.Token == "" {
		st.Note = "naming flaky tests needs auth (job-log downloads 403 without a token, even on public repos); set GITHUB_TOKEN or run `gh auth login`"
		return st
	}
	if len(fails) == 0 {
		st.Note = "no flaky-job failures in the sampled runs to read logs from"
		return st
	}
	picked := pickFlakyFailLogs(fails, sample)
	progress(fmt.Sprintf("reading %d flaky-failure logs for test names…", len(picked)))

	type result struct {
		failures []testFailure
		skipped  bool
	}
	results := make([]result, len(picked))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	var abort error
	var mu sync.Mutex
	for i, f := range picked {
		wg.Add(1)
		go func(i int, jobID int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			stop := abort != nil
			mu.Unlock()
			if stop {
				results[i].skipped = true
				return
			}
			text, err := c.GetJobLogs(owner, repo, jobID)
			if err != nil {
				var rle *RateLimitError
				if errors.As(err, &rle) {
					mu.Lock()
					abort = err
					mu.Unlock()
				}
				results[i].skipped = true
				return
			}
			results[i].failures = parseTestFailures(text)
		}(i, f.job.ID)
	}
	wg.Wait()

	type agg struct {
		framework string
		count     int
		commits   map[string]bool
		jobs      map[string]bool
	}
	byName := map[string]*agg{}
	for i, r := range results {
		if r.skipped {
			st.JobsSkipped++
			continue
		}
		st.LogsSampled++
		for _, f := range r.failures {
			g := byName[f.framework+"\x00"+f.name]
			if g == nil {
				g = &agg{framework: f.framework, commits: map[string]bool{}, jobs: map[string]bool{}}
				byName[f.framework+"\x00"+f.name] = g
			}
			g.count++
			g.commits[picked[i].sha] = true
			g.jobs[baseJobName(picked[i].job.Name)] = true
		}
	}
	st.Available = true
	if len(byName) == 0 {
		noun := "logs"
		if st.LogsSampled == 1 {
			noun = "log"
		}
		st.Note = fmt.Sprintf("no recognizable test failures in %d sampled %s (formats understood: pytest, go test, cargo test, jest/vitest, playwright, rspec, maven surefire) — the failures may be build/infra errors rather than tests", st.LogsSampled, noun)
		return st
	}
	for k, g := range byName {
		name := k[strings.IndexByte(k, 0)+1:]
		jobs := make([]string, 0, len(g.jobs))
		for j := range g.jobs {
			jobs = append(jobs, j)
		}
		sort.Strings(jobs)
		st.Tests = append(st.Tests, FlakyTest{
			Name: name, Framework: g.framework,
			Failures: g.count, Commits: len(g.commits), Jobs: jobs,
		})
	}
	sort.Slice(st.Tests, func(i, j int) bool {
		if st.Tests[i].Failures != st.Tests[j].Failures {
			return st.Tests[i].Failures > st.Tests[j].Failures
		}
		return st.Tests[i].Name < st.Tests[j].Name
	})
	if len(st.Tests) > 15 {
		st.Tests = st.Tests[:15]
	}
	return st
}
