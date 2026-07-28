// Package api fetches workflow run history from the GitHub REST API and
// computes reliability/performance statistics.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RateLimitError marks a 403 caused by API rate limiting (as opposed to
// missing permissions), so callers can word their hints accurately.
type RateLimitError struct{ Message string }

func (e *RateLimitError) Error() string { return e.Message }

// NotFoundError marks a 404, so callers can fall back (e.g. org → user).
type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string { return "GET " + e.Path + ": 404 Not Found" }

// Client is a minimal GitHub REST client.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client

	// CacheLogSample, when > 0, makes Analyze sample that many job logs to
	// measure the real cache hit/miss rate (one API request per job).
	CacheLogSample int
}

// NewClient resolves a token from GITHUB_TOKEN/GH_TOKEN or `gh auth token`.
// A missing token is not fatal: public repos work unauthenticated at a low
// rate limit.
func NewClient() *Client {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			token = strings.TrimSpace(string(out))
		}
	}
	return &Client{
		Token:   token,
		BaseURL: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) get(path string, params url.Values, out any) error {
	u := c.BaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		msg := "GitHub API rate limit exceeded"
		if ts, perr := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); perr == nil {
			wait := time.Until(time.Unix(ts, 0)).Round(time.Minute)
			if wait > 0 {
				msg += fmt.Sprintf(" (resets in ~%s)", wait)
			}
		}
		return &RateLimitError{Message: msg + "; set GITHUB_TOKEN or run `gh auth login` for 5000 req/h"}
	}
	if resp.StatusCode == 404 {
		return &NotFoundError{Path: path}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, truncate(string(body), 200))
	}
	return json.Unmarshal(body, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- API types (subset of fields we use) ----

// Run is a workflow run (latest attempt view from the list endpoint).
type Run struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	WorkflowID   int64     `json:"workflow_id"`
	HeadSHA      string    `json:"head_sha"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	RunAttempt   int       `json:"run_attempt"`
	RunStartedAt time.Time `json:"run_started_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Job is a single job execution, possibly from an earlier attempt.
type Job struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id"`
	RunAttempt  int       `json:"run_attempt"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Labels      []string  `json:"labels"`
	Steps       []Step    `json:"steps"`
}

// Step is one step within a job.
type Step struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	Number      int       `json:"number"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// ListRuns fetches up to max completed workflow runs, newest first.
func (c *Client) ListRuns(owner, repo string, max int) ([]Run, error) {
	var all []Run
	page := 1
	for len(all) < max {
		per := 100
		if rem := max - len(all); rem < per {
			per = rem
		}
		var resp struct {
			WorkflowRuns []Run `json:"workflow_runs"`
		}
		params := url.Values{
			"per_page": {fmt.Sprint(per)},
			"page":     {fmt.Sprint(page)},
			"status":   {"completed"},
		}
		if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/runs", owner, repo), params, &resp); err != nil {
			return all, err
		}
		if len(resp.WorkflowRuns) == 0 {
			break
		}
		all = append(all, resp.WorkflowRuns...)
		if len(resp.WorkflowRuns) < per {
			break
		}
		page++
	}
	return all, nil
}

// ListJobs fetches all jobs for a run across all attempts.
func (c *Client) ListJobs(owner, repo string, runID int64) ([]Job, error) {
	var all []Job
	page := 1
	for {
		var resp struct {
			Jobs []Job `json:"jobs"`
		}
		params := url.Values{
			"per_page": {"100"},
			"page":     {fmt.Sprint(page)},
			"filter":   {"all"}, // include jobs from earlier attempts
		}
		if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs", owner, repo, runID), params, &resp); err != nil {
			return all, err
		}
		all = append(all, resp.Jobs...)
		if len(resp.Jobs) < 100 {
			break
		}
		page++
	}
	return all, nil
}

// ActionsCache is one entry in a repo's Actions cache.
type ActionsCache struct {
	ID             int64     `json:"id"`
	Ref            string    `json:"ref"`
	Key            string    `json:"key"`
	SizeInBytes    int64     `json:"size_in_bytes"`
	CreatedAt      time.Time `json:"created_at"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
}

// maxCachePages caps cache-entry pagination. Some repos hold six-figure
// cache counts (nodejs/node: 137k+); walking every page would cost 1,000+
// API requests — a third of an authenticated hourly rate limit — for one
// report. Sorted by size descending, 3 pages = the 300 largest entries,
// which is where all the reclaimable weight lives. Totals come from
// CacheUsage instead, which is a single request.
const maxCachePages = 3

// CacheUsage returns the repo's total active Actions cache size and entry
// count in one request. Works unauthenticated on public repos.
func (c *Client) CacheUsage(owner, repo string) (sizeBytes int64, count int, err error) {
	var resp struct {
		SizeInBytes int64 `json:"active_caches_size_in_bytes"`
		Count       int   `json:"active_caches_count"`
	}
	if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/cache/usage", owner, repo), nil, &resp); err != nil {
		return 0, 0, err
	}
	return resp.SizeInBytes, resp.Count, nil
}

// ListCaches fetches up to maxCachePages*100 of the largest Actions cache
// entries for a repo. Works unauthenticated on public repos; private repos
// need actions:read. truncated reports whether more entries exist.
func (c *Client) ListCaches(owner, repo string) (caches []ActionsCache, truncated bool, err error) {
	var all []ActionsCache
	for page := 1; ; page++ {
		var resp struct {
			TotalCount    int            `json:"total_count"`
			ActionsCaches []ActionsCache `json:"actions_caches"`
		}
		params := url.Values{
			"per_page":  {"100"},
			"page":      {fmt.Sprint(page)},
			"sort":      {"size_in_bytes"},
			"direction": {"desc"},
		}
		if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/caches", owner, repo), params, &resp); err != nil {
			return all, false, err
		}
		all = append(all, resp.ActionsCaches...)
		if len(resp.ActionsCaches) < 100 {
			return all, false, nil
		}
		if page >= maxCachePages {
			return all, len(all) < resp.TotalCount, nil
		}
	}
}

// maxLogBytes caps how much of a single job log we read (logs can be huge;
// cache marker lines are tiny and scattered, so 10 MB covers real cases).
const maxLogBytes = 10 << 20

// GetJobLogs fetches the plain-text log for one job. GitHub answers with a
// redirect to blob storage, which net/http follows (dropping the auth header
// cross-domain, as the signed URL requires). Requires a token: this endpoint
// 403s unauthenticated even on public repos.
func (c *Client) GetJobLogs(owner, repo string, jobID int64) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/actions/jobs/%d/logs", c.BaseURL, owner, repo, jobID)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return "", &RateLimitError{Message: "GitHub API rate limit exceeded"}
	}
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return "", &NotFoundError{Path: fmt.Sprintf("/repos/%s/%s/actions/jobs/%d/logs", owner, repo, jobID)}
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("GET job %d logs: %s: %s", jobID, resp.Status, truncate(string(body), 200))
	}
	return string(body), nil
}
