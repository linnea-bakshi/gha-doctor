package api

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Analysis is the computed report over a repo's recent run history.
type Analysis struct {
	Repo        string          `json:"repo"`
	RunsSampled int             `json:"runs_sampled"`
	Since       time.Time       `json:"since"`
	Workflows   []WorkflowStats `json:"workflows"`
	FlakyJobs   []FlakyJob      `json:"flaky_jobs"`
	SlowSteps   []StepStats     `json:"slowest_steps"`
	Waste       WasteStats      `json:"waste"`
	Cost        CostStats       `json:"cost"`
	Cache       CacheStats      `json:"cache"`
	CacheLogs   *CacheLogStats  `json:"cache_logs,omitempty"` // opt-in (--cache-logs N)
}

// CacheStats summarizes the repo's Actions cache: how close it is to the
// 10 GB per-repo limit (past which GitHub evicts and cold builds return),
// how much is stale, and how much is pinned to PR refs that can never be
// reused by other branches.
type CacheStats struct {
	Available  bool         `json:"available"`
	Note       string       `json:"note,omitempty"` // why unavailable
	Count      int          `json:"count"`
	TotalMB    float64      `json:"total_mb"`
	LimitPct   float64      `json:"limit_pct"` // share of the 10 GB per-repo limit
	StaleCount int          `json:"stale_count"`
	StaleMB    float64      `json:"stale_mb"` // not accessed in 7+ days
	PRRefCount int          `json:"pr_ref_count"`
	PRRefMB    float64      `json:"pr_ref_mb"` // held by refs/pull/* (unreachable from other branches)
	Largest    []CacheEntry `json:"largest,omitempty"`
}

// CacheEntry is a single cache highlighted in the report.
type CacheEntry struct {
	Key            string    `json:"key"`
	Ref            string    `json:"ref"`
	SizeMB         float64   `json:"size_mb"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
}

// cacheLimitMB is GitHub's per-repository Actions cache limit (10 GB);
// beyond it the oldest caches are evicted.
const cacheLimitMB = 10 * 1024

// staleAfter is how long a cache can go unaccessed before we flag it.
// (GitHub itself evicts after 7 days of no access.)
const staleAfter = 7 * 24 * time.Hour

// WorkflowStats aggregates runs of one workflow.
type WorkflowStats struct {
	Name        string  `json:"name"`
	Runs        int     `json:"runs"`
	SuccessRate float64 `json:"success_rate"`
	P50Minutes  float64 `json:"p50_minutes"`
	P95Minutes  float64 `json:"p95_minutes"`
	AvgQueueSec float64 `json:"avg_queue_seconds"`
	EstUSD      float64 `json:"est_usd"` // estimated billable cost across sample
}

// FlakyJob is a job that both failed and succeeded on the same commit.
type FlakyJob struct {
	Workflow      string  `json:"workflow"`
	Job           string  `json:"job"`
	FlakyCommits  int     `json:"flaky_commits"`
	Failures      int     `json:"failures"`
	Runs          int     `json:"runs"`
	FlakeRate     float64 `json:"flake_rate"`
	WastedMinutes float64 `json:"wasted_minutes"`
}

// StepStats aggregates a step's duration across runs.
type StepStats struct {
	Job        string  `json:"job"`
	Step       string  `json:"step"`
	Count      int     `json:"count"`
	P50Minutes float64 `json:"p50_minutes"`
	TotalMin   float64 `json:"total_minutes"`
}

// WasteStats estimates minutes that bought nothing.
type WasteStats struct {
	FailedRunMinutes float64 `json:"failed_run_minutes"`
	RetryMinutes     float64 `json:"retry_minutes"`
	TotalMinutes     float64 `json:"total_minutes"`
	ComputeMinutes   float64 `json:"compute_minutes"` // all billable-weighted minutes sampled
}

// CostStats estimates what the sampled runs would cost on GitHub-hosted
// runners at public pay-as-you-go rates (Linux $0.008/min, Windows 2x,
// macOS 10x), with each job rounded UP to the whole minute the way GitHub
// bills. Public repos on standard runners are free — the estimate then shows
// what the same usage would cost on a private repo.
type CostStats struct {
	BillableMinutes float64 `json:"billable_minutes"` // ceil per job, multiplier-weighted
	EstimatedUSD    float64 `json:"estimated_usd"`
	WastedUSD       float64 `json:"wasted_usd"`       // failed-run + retried-attempt share
	RoundingMinutes float64 `json:"rounding_minutes"` // billed-but-unused due to per-job round-up (weighted)
	RoundingUSD     float64 `json:"rounding_usd"`
	SelfHostedJobs  int     `json:"self_hosted_jobs"` // excluded from the estimate (not billed by GitHub)
}

// pricePerLinuxMinute is GitHub's public pay-as-you-go rate for standard
// Linux runners; Windows/macOS are expressed via runnerMultiplier.
const pricePerLinuxMinute = 0.008

// Analyze fetches up to maxRuns completed runs and their jobs, then computes
// statistics. progress (optional) receives status lines.
func (c *Client) Analyze(owner, repo string, maxRuns int, progress func(string)) (*Analysis, error) {
	if progress == nil {
		progress = func(string) {}
	}
	progress(fmt.Sprintf("fetching up to %d completed runs for %s/%s…", maxRuns, owner, repo))
	runs, err := c.ListRuns(owner, repo, maxRuns)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("no completed workflow runs found for %s/%s", owner, repo)
	}
	progress(fmt.Sprintf("got %d runs; fetching jobs (this is the slow part)…", len(runs)))

	// Fetch jobs concurrently.
	jobsByRun := make(map[int64][]Job, len(runs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var fetchErr error
	done := 0
	for _, r := range runs {
		wg.Add(1)
		go func(runID int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			jobs, err := c.ListJobs(owner, repo, runID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && fetchErr == nil {
				fetchErr = err
			}
			jobsByRun[runID] = jobs
			done++
			if done%50 == 0 {
				progress(fmt.Sprintf("  %d/%d runs…", done, len(runs)))
			}
		}(r.ID)
	}
	wg.Wait()
	if fetchErr != nil && len(jobsByRun) == 0 {
		return nil, fetchErr
	}

	a := &Analysis{
		Repo:        owner + "/" + repo,
		RunsSampled: len(runs),
		Since:       runs[len(runs)-1].RunStartedAt,
	}
	a.computeWorkflowStats(runs, jobsByRun)
	a.computeFlaky(runs, jobsByRun)
	a.computeSlowSteps(jobsByRun)
	a.computeWaste(runs, jobsByRun)
	a.computeCost(runs, jobsByRun)

	progress("fetching cache usage…")
	caches, err := c.ListCaches(owner, repo)
	if err != nil {
		var note string
		var rle *RateLimitError
		if errors.As(err, &rle) {
			note = "cache data unavailable: " + rle.Message
		} else {
			note = "cache data unavailable (private repos need a token with actions:read; set GITHUB_TOKEN or run `gh auth login`)"
		}
		a.Cache = CacheStats{Available: false, Note: note}
	} else {
		a.computeCacheStats(caches, time.Now())
	}
	if c.CacheLogSample > 0 {
		a.CacheLogs = c.analyzeCacheLogs(owner, repo, jobsByRun, c.CacheLogSample, progress)
	}
	return a, nil
}

// computeCacheStats summarizes cache entries against the 10 GB repo limit.
func (a *Analysis) computeCacheStats(caches []ActionsCache, now time.Time) {
	cs := CacheStats{Available: true, Count: len(caches)}
	sorted := append([]ActionsCache(nil), caches...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SizeInBytes > sorted[j].SizeInBytes })
	for _, c := range sorted {
		mb := float64(c.SizeInBytes) / (1024 * 1024)
		cs.TotalMB += mb
		if !c.LastAccessedAt.IsZero() && now.Sub(c.LastAccessedAt) > staleAfter {
			cs.StaleCount++
			cs.StaleMB += mb
		}
		if strings.HasPrefix(c.Ref, "refs/pull/") {
			cs.PRRefCount++
			cs.PRRefMB += mb
		}
		if len(cs.Largest) < 5 {
			cs.Largest = append(cs.Largest, CacheEntry{
				Key: c.Key, Ref: c.Ref, SizeMB: mb, LastAccessedAt: c.LastAccessedAt,
			})
		}
	}
	cs.LimitPct = cs.TotalMB / cacheLimitMB * 100
	a.Cache = cs
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func (a *Analysis) computeWorkflowStats(runs []Run, jobsByRun map[int64][]Job) {
	type acc struct {
		durations []float64
		queueSecs []float64
		total     int
		success   int
	}
	byWF := map[string]*acc{}
	for _, r := range runs {
		w := byWF[r.Name]
		if w == nil {
			w = &acc{}
			byWF[r.Name] = w
		}
		w.total++
		if r.Conclusion == "success" {
			w.success++
		}
		w.durations = append(w.durations, r.UpdatedAt.Sub(r.RunStartedAt).Minutes())
		for _, j := range jobsByRun[r.ID] {
			if !j.StartedAt.IsZero() && !j.CreatedAt.IsZero() && j.StartedAt.After(j.CreatedAt) {
				w.queueSecs = append(w.queueSecs, j.StartedAt.Sub(j.CreatedAt).Seconds())
			}
		}
	}
	for name, w := range byWF {
		sort.Float64s(w.durations)
		var qs float64
		for _, q := range w.queueSecs {
			qs += q
		}
		avgQ := 0.0
		if len(w.queueSecs) > 0 {
			avgQ = qs / float64(len(w.queueSecs))
		}
		a.Workflows = append(a.Workflows, WorkflowStats{
			Name:        name,
			Runs:        w.total,
			SuccessRate: float64(w.success) / float64(w.total),
			P50Minutes:  percentile(w.durations, 0.50),
			P95Minutes:  percentile(w.durations, 0.95),
			AvgQueueSec: avgQ,
		})
	}
	sort.Slice(a.Workflows, func(i, j int) bool { return a.Workflows[i].Runs > a.Workflows[j].Runs })
}

// computeFlaky finds jobs that failed and later succeeded for the same
// commit (same head SHA), i.e. the failure was not caused by the code.
func (a *Analysis) computeFlaky(runs []Run, jobsByRun map[int64][]Job) {
	runByID := map[int64]Run{}
	for _, r := range runs {
		runByID[r.ID] = r
	}
	type key struct{ wf, job, sha string }
	type obs struct {
		fail, pass    int
		failedMinutes float64
	}
	byKey := map[key]*obs{}
	for runID, jobs := range jobsByRun {
		r := runByID[runID]
		for _, j := range jobs {
			if j.Conclusion != "success" && j.Conclusion != "failure" {
				continue
			}
			k := key{r.Name, j.Name, r.HeadSHA}
			o := byKey[k]
			if o == nil {
				o = &obs{}
				byKey[k] = o
			}
			if j.Conclusion == "success" {
				o.pass++
			} else {
				o.fail++
				if !j.CompletedAt.IsZero() && !j.StartedAt.IsZero() {
					o.failedMinutes += j.CompletedAt.Sub(j.StartedAt).Minutes()
				}
			}
		}
	}
	type agg struct {
		flakyCommits, failures, runs int
		wasted                       float64
	}
	byJob := map[[2]string]*agg{}
	for k, o := range byKey {
		jk := [2]string{k.wf, k.job}
		g := byJob[jk]
		if g == nil {
			g = &agg{}
			byJob[jk] = g
		}
		g.runs += o.fail + o.pass
		if o.fail > 0 && o.pass > 0 { // flaked on this commit
			g.flakyCommits++
			g.failures += o.fail
			g.wasted += o.failedMinutes
		}
	}
	for jk, g := range byJob {
		if g.flakyCommits == 0 {
			continue
		}
		a.FlakyJobs = append(a.FlakyJobs, FlakyJob{
			Workflow:      jk[0],
			Job:           jk[1],
			FlakyCommits:  g.flakyCommits,
			Failures:      g.failures,
			Runs:          g.runs,
			FlakeRate:     float64(g.failures) / float64(g.runs),
			WastedMinutes: g.wasted,
		})
	}
	sort.Slice(a.FlakyJobs, func(i, j int) bool {
		return a.FlakyJobs[i].WastedMinutes > a.FlakyJobs[j].WastedMinutes
	})
}

func (a *Analysis) computeSlowSteps(jobsByRun map[int64][]Job) {
	type acc struct {
		durs  []float64
		total float64
	}
	byStep := map[[2]string]*acc{}
	for _, jobs := range jobsByRun {
		for _, j := range jobs {
			for _, s := range j.Steps {
				if s.StartedAt.IsZero() || s.CompletedAt.IsZero() || s.Conclusion == "skipped" {
					continue
				}
				d := s.CompletedAt.Sub(s.StartedAt).Minutes()
				if d <= 0 {
					continue
				}
				k := [2]string{baseJobName(j.Name), s.Name}
				o := byStep[k]
				if o == nil {
					o = &acc{}
					byStep[k] = o
				}
				o.durs = append(o.durs, d)
				o.total += d
			}
		}
	}
	for k, o := range byStep {
		sort.Float64s(o.durs)
		a.SlowSteps = append(a.SlowSteps, StepStats{
			Job: k[0], Step: k[1],
			Count:      len(o.durs),
			P50Minutes: percentile(o.durs, 0.5),
			TotalMin:   o.total,
		})
	}
	sort.Slice(a.SlowSteps, func(i, j int) bool { return a.SlowSteps[i].TotalMin > a.SlowSteps[j].TotalMin })
	if len(a.SlowSteps) > 10 {
		a.SlowSteps = a.SlowSteps[:10]
	}
}

// runnerMultiplier maps runner labels to GitHub's billing multiplier.
func runnerMultiplier(labels []string) float64 {
	for _, l := range labels {
		ll := strings.ToLower(l)
		if strings.HasPrefix(ll, "macos") {
			return 10
		}
		if strings.HasPrefix(ll, "windows") {
			return 2
		}
	}
	return 1
}

func (a *Analysis) computeWaste(runs []Run, jobsByRun map[int64][]Job) {
	for _, r := range runs {
		jobs := jobsByRun[r.ID]
		maxAttempt := 1
		for _, j := range jobs {
			if j.RunAttempt > maxAttempt {
				maxAttempt = j.RunAttempt
			}
		}
		for _, j := range jobs {
			if j.StartedAt.IsZero() || j.CompletedAt.IsZero() {
				continue
			}
			mins := j.CompletedAt.Sub(j.StartedAt).Minutes() * runnerMultiplier(j.Labels)
			if mins < 0 {
				continue
			}
			a.Waste.ComputeMinutes += mins
			switch {
			case j.RunAttempt < maxAttempt:
				// an earlier attempt that had to be redone
				a.Waste.RetryMinutes += mins
			case r.Conclusion == "failure" && j.Conclusion == "failure":
				a.Waste.FailedRunMinutes += mins
			}
		}
	}
	a.Waste.TotalMinutes = a.Waste.FailedRunMinutes + a.Waste.RetryMinutes
}

// isSelfHosted reports whether a job ran on a self-hosted runner
// (not billed by GitHub, so excluded from cost estimates).
func isSelfHosted(labels []string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return true
		}
	}
	return false
}

// computeCost estimates billable cost the way GitHub meters it: each job's
// duration is rounded UP to the whole minute, weighted by the OS multiplier,
// and priced at the public Linux per-minute rate. The delta between rounded
// and actual time is surfaced as rounding overhead — many short jobs quietly
// pay a full minute each.
func (a *Analysis) computeCost(runs []Run, jobsByRun map[int64][]Job) {
	wfIdx := make(map[string]int, len(a.Workflows))
	for i, wf := range a.Workflows {
		wfIdx[wf.Name] = i
	}
	for _, r := range runs {
		jobs := jobsByRun[r.ID]
		maxAttempt := 1
		for _, j := range jobs {
			if j.RunAttempt > maxAttempt {
				maxAttempt = j.RunAttempt
			}
		}
		for _, j := range jobs {
			if j.StartedAt.IsZero() || j.CompletedAt.IsZero() {
				continue
			}
			raw := j.CompletedAt.Sub(j.StartedAt).Minutes()
			if raw < 0 {
				continue
			}
			if isSelfHosted(j.Labels) {
				a.Cost.SelfHostedJobs++
				continue
			}
			mult := runnerMultiplier(j.Labels)
			billable := math.Ceil(raw)
			if billable == 0 && raw > 0 {
				billable = 1
			}
			weighted := billable * mult
			a.Cost.BillableMinutes += weighted
			a.Cost.RoundingMinutes += (billable - raw) * mult
			usd := weighted * pricePerLinuxMinute
			a.Cost.EstimatedUSD += usd
			if j.RunAttempt < maxAttempt || (r.Conclusion == "failure" && j.Conclusion == "failure") {
				a.Cost.WastedUSD += usd
			}
			if i, ok := wfIdx[r.Name]; ok {
				a.Workflows[i].EstUSD += usd
			}
		}
	}
	a.Cost.RoundingUSD = a.Cost.RoundingMinutes * pricePerLinuxMinute
}

// baseJobName strips a trailing matrix suffix like "test (ubuntu-latest, 3.12)".
func baseJobName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}
