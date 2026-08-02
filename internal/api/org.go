package api

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"
)

// RepoInfo is a repository as returned by the list-repos endpoints.
type RepoInfo struct {
	Name     string    `json:"name"`
	FullName string    `json:"full_name"`
	Fork     bool      `json:"fork"`
	Archived bool      `json:"archived"`
	Private  bool      `json:"private"`
	PushedAt time.Time `json:"pushed_at"`
}

// OrgAnalysis is a fleet-level view over an org's (or user's) repos:
// run-level statistics only, one API request per repo, so a whole org
// fits in a normal rate-limit budget. Numbers are wall-clock run
// minutes — billable job minutes are usually higher (parallel jobs) —
// so this is a triage view; drill into a repo with --repo for exact
// per-job billing.
type OrgAnalysis struct {
	Org           string         `json:"org"`
	ReposListed   int            `json:"repos_listed"`
	ReposScanned  int            `json:"repos_scanned"`
	SkippedForks  int            `json:"skipped_forks"`
	SkippedArch   int            `json:"skipped_archived"`
	Repos         []OrgRepoStats `json:"repos"` // repos with runs, sorted by Est30dMinutes desc
	QuietRepos    int            `json:"quiet_repos"`
	TotalEst30d   float64        `json:"total_est_30d_minutes"`
	TotalFailRate float64        `json:"total_fail_rate"` // run-weighted across scanned repos
	Errors        []string       `json:"errors,omitempty"`
}

// OrgRepoStats summarizes one repo's recent completed runs.
type OrgRepoStats struct {
	Repo          string    `json:"repo"`
	RunsSampled   int       `json:"runs_sampled"`
	WindowDays    float64   `json:"window_days"` // oldest sampled run → now
	FailRate      float64   `json:"fail_rate"`   // failure / (success+failure)
	P50Minutes    float64   `json:"p50_minutes"` // wall-clock per run
	P95Minutes    float64   `json:"p95_minutes"`
	TotalMinutes  float64   `json:"total_minutes"`   // across the sample
	Est30dMinutes float64   `json:"est_30d_minutes"` // wall-clock run minutes per 30 days
	Extrapolated  bool      `json:"extrapolated"`    // sample truncated inside 30d; rate extrapolated
	Truncated     bool      `json:"truncated"`       // sample exhausted with <3d of signal; figure is a lower bound
	LastRun       time.Time `json:"last_run"`
}

// ListOrgRepos lists repos for an org, falling back to the user endpoint
// if the name is a user account. Sorted by most recently pushed.
func (c *Client) ListOrgRepos(org string, maxPages int) ([]RepoInfo, error) {
	repos, err := c.listRepos("/orgs/"+url.PathEscape(org)+"/repos", maxPages)
	if err == nil {
		return repos, nil
	}
	var nf *NotFoundError
	if errors.As(err, &nf) {
		return c.listRepos("/users/"+url.PathEscape(org)+"/repos", maxPages)
	}
	return nil, err
}

func (c *Client) listRepos(path string, maxPages int) ([]RepoInfo, error) {
	var all []RepoInfo
	for page := 1; page <= maxPages; page++ {
		var batch []RepoInfo
		params := url.Values{
			"per_page":  {"100"},
			"page":      {fmt.Sprint(page)},
			"sort":      {"pushed"},
			"direction": {"desc"},
		}
		if err := c.get(path, params, &batch); err != nil {
			return all, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// AnalyzeOrg scans up to maxRepos active repos (most recently pushed,
// skipping forks and archived) with runsPerRepo completed runs each.
func (c *Client) AnalyzeOrg(org string, maxRepos, runsPerRepo int, progress func(string)) (*OrgAnalysis, error) {
	if progress == nil {
		progress = func(string) {}
	}
	progress(fmt.Sprintf("listing repos for %s…", org))
	repos, err := c.ListOrgRepos(org, 3) // up to 300 most recently pushed
	if err != nil {
		return nil, err
	}
	oa := &OrgAnalysis{Org: org, ReposListed: len(repos)}
	var candidates []RepoInfo
	for _, r := range repos {
		switch {
		case r.Fork:
			oa.SkippedForks++
		case r.Archived:
			oa.SkippedArch++
		default:
			candidates = append(candidates, r)
		}
	}
	if len(candidates) > maxRepos {
		candidates = candidates[:maxRepos]
	}
	oa.ReposScanned = len(candidates)
	progress(fmt.Sprintf("scanning %d repos (%d runs each)…", len(candidates), runsPerRepo))

	type result struct {
		stats OrgRepoStats
		quiet bool
		err   error
	}
	results := make([]result, len(candidates))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for i, r := range candidates {
		wg.Add(1)
		go func(i int, r RepoInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			runs, err := c.ListRuns(org, r.Name, runsPerRepo)
			mu.Lock()
			done++
			progress(fmt.Sprintf("  [%d/%d] %s: %d runs", done, len(candidates), r.Name, len(runs)))
			mu.Unlock()
			if err != nil {
				var rl *RateLimitError
				if errors.As(err, &rl) {
					results[i] = result{err: err}
					return
				}
				results[i] = result{err: fmt.Errorf("%s: %v", r.Name, err)}
				return
			}
			if len(runs) == 0 {
				results[i] = result{quiet: true}
				return
			}
			results[i] = result{stats: repoRunStats(r.Name, runs, runsPerRepo, time.Now())}
		}(i, r)
	}
	wg.Wait()

	totalRuns := 0
	failWeighted := 0.0
	for _, res := range results {
		switch {
		case res.err != nil:
			var rl *RateLimitError
			if errors.As(res.err, &rl) {
				return nil, res.err // rate limit: no point continuing
			}
			oa.Errors = append(oa.Errors, res.err.Error())
		case res.quiet:
			oa.QuietRepos++
		case res.stats.Repo != "":
			oa.Repos = append(oa.Repos, res.stats)
			oa.TotalEst30d += res.stats.Est30dMinutes
			totalRuns += res.stats.RunsSampled
			failWeighted += res.stats.FailRate * float64(res.stats.RunsSampled)
		}
	}
	if totalRuns > 0 {
		oa.TotalFailRate = failWeighted / float64(totalRuns)
	}
	sort.Slice(oa.Repos, func(i, j int) bool { return oa.Repos[i].Est30dMinutes > oa.Repos[j].Est30dMinutes })
	return oa, nil
}

// repoRunStats computes run-level stats for one repo's sampled runs.
func repoRunStats(name string, runs []Run, requested int, now time.Time) OrgRepoStats {
	st := OrgRepoStats{Repo: name, RunsSampled: len(runs)}
	var durations []float64
	var success, failure int
	oldest := now
	var last30 float64
	for _, r := range runs {
		start := r.RunStartedAt
		if start.IsZero() {
			start = r.CreatedAt
		}
		if start.IsZero() || r.UpdatedAt.Before(start) {
			continue
		}
		min := r.UpdatedAt.Sub(start).Minutes()
		// Skipped runs never executed; keep them out of the duration
		// percentiles so they don't drag p50 toward zero.
		if r.Conclusion != "skipped" {
			durations = append(durations, min)
		}
		st.TotalMinutes += min
		if start.Before(oldest) {
			oldest = start
		}
		if start.After(st.LastRun) {
			st.LastRun = start
		}
		if now.Sub(start) <= 30*24*time.Hour {
			last30 += min
		}
		switch r.Conclusion {
		case "success":
			success++
		case "failure":
			failure++
		}
	}
	if success+failure > 0 {
		st.FailRate = float64(failure) / float64(success+failure)
	}
	sort.Float64s(durations)
	st.P50Minutes = percentile(durations, 0.50)
	st.P95Minutes = percentile(durations, 0.95)
	st.WindowDays = now.Sub(oldest).Hours() / 24
	// If the sample was truncated inside the 30-day window, the busy repo
	// has more runs than we saw. With a few days of signal we extrapolate
	// from the observed rate; with less (e.g. a burst of runs minutes
	// apart), extrapolation explodes, so report the observed minutes as a
	// lower bound instead.
	truncated := len(runs) >= requested && st.WindowDays < 30
	switch {
	case truncated && st.WindowDays >= 3:
		st.Est30dMinutes = st.TotalMinutes / st.WindowDays * 30
		st.Extrapolated = true
	case truncated:
		st.Est30dMinutes = last30
		st.Truncated = true
	default:
		st.Est30dMinutes = last30
	}
	return st
}
