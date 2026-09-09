package api

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PRDeep is the deep dive into one pull request: every workflow run on the
// PR's head commit, how long the PR waits for a CI verdict, and a full
// RunDeep dive into the latest failed run (when there is one).
type PRDeep struct {
	Repo       string    `json:"repo"`
	Number     int       `json:"number"`
	Title      string    `json:"title,omitempty"`
	Author     string    `json:"author,omitempty"`
	State      string    `json:"state"` // open, closed, or merged
	Draft      bool      `json:"draft,omitempty"`
	URL        string    `json:"url,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	BaseBranch string    `json:"base_branch,omitempty"`
	Commits    int       `json:"commits"`
	CreatedAt  time.Time `json:"created_at"`
	HeadSHA    string    `json:"head_sha"`

	// Runs on the head commit, oldest first. RunsNote explains scope
	// caveats (older commits not analyzed, truncation, or zero runs).
	Runs     []PRRun `json:"runs"`
	RunsNote string  `json:"runs_note,omitempty"`

	// RetriedRuns counts head-commit runs on attempt >1 — someone (or
	// something) hit re-run, the classic smell of a flaky check.
	RetriedRuns int `json:"retried_runs,omitempty"`

	// FeedbackSec is how long the head commit waited for a decisive CI
	// signal: first run created → first failure completing (a red X is
	// actionable immediately), or → last run completing when all passed.
	FeedbackSec  float64 `json:"feedback_sec,omitempty"`
	FeedbackNote string  `json:"feedback_note,omitempty"`

	// Deep is the full dive into the chosen run (the latest failed run on
	// the head commit). DeepNote says which run was chosen and why, or why
	// no dive was performed.
	Deep     *RunDeep `json:"deep,omitempty"`
	DeepNote string   `json:"deep_note,omitempty"`
}

// PRRun is one workflow run on the PR's head commit.
type PRRun struct {
	RunID      int64   `json:"run_id"`
	Workflow   string  `json:"workflow"`
	Event      string  `json:"event,omitempty"`
	Status     string  `json:"status"`
	Conclusion string  `json:"conclusion,omitempty"`
	Attempt    int     `json:"attempt,omitempty"`
	WallSec    float64 `json:"wall_sec,omitempty"` // run start → last update (completed runs)
	URL        string  `json:"url,omitempty"`
}

// pullRequest is the slice of the REST PR object gha-doctor needs.
type pullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Merged    bool      `json:"merged"`
	Draft     bool      `json:"draft"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	Commits   int       `json:"commits"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// GetPR fetches one pull request.
func (c *Client) getPR(owner, repo string, number int) (*pullRequest, error) {
	var pr pullRequest
	if err := c.get(fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), nil, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// listRunsForSHA returns every workflow run whose head commit is sha (one
// page of 100 — a single commit exceeding that is vanishingly rare, and the
// caller notes truncation). Fork PRs are covered: pull_request runs live on
// the base repository and carry the PR head SHA.
func (c *Client) listRunsForSHA(owner, repo, sha string) ([]Run, bool, error) {
	var resp struct {
		WorkflowRuns []Run `json:"workflow_runs"`
	}
	params := url.Values{"head_sha": {sha}, "per_page": {"100"}}
	if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/runs", owner, repo), params, &resp); err != nil {
		return nil, false, err
	}
	return resp.WorkflowRuns, len(resp.WorkflowRuns) == 100, nil
}

// AnalyzePR builds the PR deep dive: head-commit runs, feedback time, and a
// RunDeep dive into the latest failed run when one exists. logTail is
// forwarded to the nested run dive.
func (c *Client) AnalyzePR(owner, repo string, number, logTail int, progress func(string)) (*PRDeep, error) {
	if progress == nil {
		progress = func(string) {}
	}
	progress(fmt.Sprintf("fetching PR #%d…", number))
	pr, err := c.getPR(owner, repo, number)
	if err != nil {
		return nil, err
	}
	state := pr.State
	if pr.Merged {
		state = "merged"
	}
	d := &PRDeep{
		Repo:       owner + "/" + repo,
		Number:     pr.Number,
		Title:      pr.Title,
		Author:     pr.User.Login,
		State:      state,
		Draft:      pr.Draft,
		URL:        pr.HTMLURL,
		Branch:     pr.Head.Ref,
		BaseBranch: pr.Base.Ref,
		Commits:    pr.Commits,
		CreatedAt:  pr.CreatedAt,
		HeadSHA:    pr.Head.SHA,
	}

	progress(fmt.Sprintf("fetching runs for head commit %.7s…", pr.Head.SHA))
	runs, truncated, err := c.listRunsForSHA(owner, repo, pr.Head.SHA)
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].RunStartedAt.Before(runs[j].RunStartedAt) })

	var notes []string
	if pr.Commits > 1 {
		notes = append(notes, fmt.Sprintf("PR has %d commits; only the head commit's runs are analyzed", pr.Commits))
	}
	if truncated {
		notes = append(notes, "head commit has 100+ runs; showing the first page")
	}
	if len(runs) == 0 {
		notes = append(notes, fmt.Sprintf("no workflow runs found for head commit %.7s — CI may not have triggered yet, or runs were deleted", pr.Head.SHA))
	}
	d.RunsNote = strings.Join(notes, "; ")

	var latestFailed *Run
	for i := range runs {
		r := &runs[i]
		p := PRRun{
			RunID:      r.ID,
			Workflow:   r.Name,
			Event:      r.Event,
			Status:     r.Status,
			Conclusion: r.Conclusion,
			Attempt:    r.RunAttempt,
			URL:        r.HTMLURL,
		}
		if r.Status == "completed" && r.UpdatedAt.After(r.RunStartedAt) {
			p.WallSec = r.UpdatedAt.Sub(r.RunStartedAt).Seconds()
		}
		if r.RunAttempt > 1 {
			d.RetriedRuns++
		}
		if r.Status == "completed" && isRedConclusion(r.Conclusion) {
			if latestFailed == nil || r.RunStartedAt.After(latestFailed.RunStartedAt) {
				latestFailed = r
			}
		}
		d.Runs = append(d.Runs, p)
	}

	d.FeedbackSec, d.FeedbackNote = prFeedback(runs)

	switch {
	case latestFailed != nil:
		progress(fmt.Sprintf("diving into failed %q run %d…", latestFailed.Name, latestFailed.ID))
		deep, err := c.AnalyzeRun(owner, repo, latestFailed, logTail, progress)
		if err != nil {
			d.DeepNote = fmt.Sprintf("deep dive of failed run %d skipped: %v", latestFailed.ID, err)
		} else {
			d.Deep = deep
			d.DeepNote = fmt.Sprintf("diving into the latest failed run on the head commit (%s #%d)", latestFailed.Name, latestFailed.RunNumber)
		}
	case len(runs) > 0:
		d.DeepNote = "no failed runs on the head commit — use --run <id> for a timeline of any single run"
	}
	return d, nil
}

// isRedConclusion reports whether a completed run's conclusion is a
// developer-actionable failure.
func isRedConclusion(c string) bool {
	switch c {
	case "failure", "timed_out", "startup_failure":
		return true
	}
	return false
}

// prFeedback computes how long the head commit waited for a decisive CI
// signal. A failure is decisive the moment it completes; success is only
// decisive once every run has passed.
func prFeedback(runs []Run) (float64, string) {
	if len(runs) == 0 {
		return 0, ""
	}
	start := runs[0].CreatedAt
	for _, r := range runs {
		if r.CreatedAt.Before(start) {
			start = r.CreatedAt
		}
	}
	var firstRed time.Time
	allDone := true
	var lastDone time.Time
	for _, r := range runs {
		if r.Status != "completed" {
			allDone = false
			continue
		}
		if isRedConclusion(r.Conclusion) && (firstRed.IsZero() || r.UpdatedAt.Before(firstRed)) {
			firstRed = r.UpdatedAt
		}
		if r.UpdatedAt.After(lastDone) {
			lastDone = r.UpdatedAt
		}
	}
	switch {
	case !firstRed.IsZero():
		return clampNonNeg(firstRed.Sub(start).Seconds()), "time to first red on the head commit"
	case allDone:
		return clampNonNeg(lastDone.Sub(start).Seconds()), "time to all-green on the head commit"
	default:
		return 0, "runs still in progress — no final verdict yet"
	}
}

var prURLRe = regexp.MustCompile(`^https?://[^/]+/([^/]+)/([^/]+)/pull/(\d+)`)

// ParsePRRef parses a --pr value: a PR number ("123" or "#123") or a full
// PR URL (which also pins the repository).
func ParsePRRef(v string) (number int, owner, repo string, err error) {
	if m := prURLRe.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[3])
		return n, m[1], m[2], nil
	}
	n, convErr := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(v), "#"))
	if convErr != nil || n <= 0 {
		return 0, "", "", fmt.Errorf("--pr wants a PR number (or a PR URL), got %q", v)
	}
	return n, "", "", nil
}
