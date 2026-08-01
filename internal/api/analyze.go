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
	Repo        string           `json:"repo"`
	RunsSampled int              `json:"runs_sampled"`
	Since       time.Time        `json:"since"`
	Workflows   []WorkflowStats  `json:"workflows"`
	FlakyJobs   []FlakyJob       `json:"flaky_jobs"`
	SlowSteps   []StepStats      `json:"slowest_steps"`
	Waste       WasteStats       `json:"waste"`
	Cost        CostStats        `json:"cost"`
	Cache       CacheStats       `json:"cache"`
	Artifacts   ArtifactStats    `json:"artifacts"`
	Matrix      *MatrixStats     `json:"matrix,omitempty"`       // omitted when no matrix group had enough clean runs
	Superseded  *SupersededStats `json:"superseded,omitempty"`   // omitted when the sample has no PR-event runs
	Feedback    *FeedbackStats   `json:"pr_feedback,omitempty"`  // omitted below minFeedbackPushes qualifying pushes
	CacheLogs   *CacheLogStats   `json:"cache_logs,omitempty"`   // opt-in (--cache-logs N)
	FlakyTests  *FlakyTestStats  `json:"flaky_tests,omitempty"`  // opt-in (--flaky-logs N)
	ZombieCrons []ZombieCron     `json:"zombie_crons,omitempty"` // scheduled workflows failing on repeat

	// flakyFails is the sampling population for --flaky-logs: every failed
	// job instance from a same-SHA fail+pass group. Not serialized.
	flakyFails []flakyFail

	// RunPoints holds one point per decisive sampled run, for the --html
	// charts. Excluded from --json: the aggregates above are the contract;
	// a per-run dump belongs to the runs API, not this report.
	RunPoints []RunPoint `json:"-"`
}

// RunPoint is one decisive run as a chart point: when it started, how long
// it ran (wall clock), and whether it succeeded. Non-decisive runs
// (skipped/cancelled/action_required) are excluded for the same reason they
// are excluded from success rates and percentiles.
type RunPoint struct {
	Workflow string
	Start    time.Time
	Minutes  float64
	Success  bool
}

// ArtifactStats summarizes artifact storage: who uploads the weight, how
// long it is kept, and what storage converges to if the current upload
// rate continues (rate × retention = steady state). Storage is billed at
// $0.008/GB-day on private repos; public repos store free, so the estimate
// shows what the same habits would cost on a private one.
type ArtifactStats struct {
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"` // why unavailable
	Count     int    `json:"count"`          // exact repo-wide count (incl. expired entries still listed)

	// The sample: up to the 300 most recent uploads (the artifacts API
	// cannot sort by size). WindowDays spans the sample's created_at
	// range — the denominator for the production rate.
	Sampled     bool    `json:"sampled,omitempty"`
	SampleCount int     `json:"sample_count"`
	WindowDays  float64 `json:"window_days"`
	ActiveCount int     `json:"active_count"` // not yet expired, in sample
	ActiveMB    float64 `json:"active_mb"`

	Producers []ArtifactProducer `json:"producers,omitempty"` // per artifact name, by sampled volume

	// Steady-state estimate: sum over producers of rate × retention.
	// Only computed when the sample spans >= 3 days — below that a burst
	// (one busy afternoon of CI) would extrapolate into fiction.
	EstStorageGB  float64 `json:"est_storage_gb,omitempty"`
	EstUSDPerMo   float64 `json:"est_usd_per_month,omitempty"`
	EstimateBasis string  `json:"estimate_basis,omitempty"`
}

// ArtifactProducer aggregates uploads sharing one artifact name.
type ArtifactProducer struct {
	Name          string  `json:"name"`
	Count         int     `json:"count"`
	TotalMB       float64 `json:"total_mb"`
	AvgMB         float64 `json:"avg_mb"`
	RetentionDays float64 `json:"retention_days"` // median expires_at − created_at
	SteadyGB      float64 `json:"steady_gb,omitempty"`
}

// artifactPricePerGBDay is GitHub's storage rate for Actions artifacts and
// Packages on private repos ($0.008/GB-day ≈ $0.24/GB-month).
const artifactPricePerGBDay = 0.008

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

	// On repos with huge cache counts only the largest entries are
	// examined (walking every page would cost 1,000+ API requests).
	// When Sampled is true, the stale/PR-ref/largest breakdown covers
	// the SampleCount biggest entries; Count/TotalMB/LimitPct are still
	// exact, from the cache-usage endpoint.
	Sampled     bool `json:"sampled,omitempty"`
	SampleCount int  `json:"sample_count,omitempty"`
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
	Name string `json:"name"`
	Runs int    `json:"runs"`
	// Decisive counts runs that actually reached a verdict (success,
	// failure, timed_out, startup_failure). Skipped and cancelled runs —
	// including concurrency auto-cancels, which D001 recommends — are
	// sampled but don't count toward SuccessRate or duration percentiles.
	Decisive    int     `json:"decisive"`
	SuccessRate float64 `json:"success_rate"` // fraction of decisive runs
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

// SupersededStats measures runs a newer push made obsolete while they were
// still running — exactly what `concurrency` + `cancel-in-progress` (D001)
// prevents. Scope is pull_request/pull_request_target events only: cancelling
// in-flight push runs on a release branch is often wrong, and D001
// deliberately doesn't ask for it. Groups are keyed by workflow + head repo +
// head branch, so two forks both pushing a branch named "patch-1" can't fake
// a supersession. No double counting with the failure/retry buckets: failed
// superseded runs and retried attempts keep their minutes there, so this
// number is purely "runs that succeeded pointlessly".
type SupersededStats struct {
	PRRuns    int `json:"pr_runs"`   // PR-event runs in the sample (the denominator)
	Completed int `json:"completed"` // superseded but ran to completion anyway
	Cancelled int `json:"cancelled"` // superseded and cancelled in time (concurrency at work)
	// WastedMinutes is the billable-weighted minutes the completed ones ran
	// past the moment their replacement was created: per job,
	// ceil(actual) − ceil(time-before-supersession), OS-multiplier weighted.
	WastedMinutes float64         `json:"wasted_minutes"`
	WastedUSD     float64         `json:"wasted_usd"`
	Examples      []SupersededRun `json:"examples,omitempty"` // worst offenders by wasted minutes
}

// FeedbackStats measures how long a contributor waits between pushing to a
// PR and the last CI check finishing — the human-time cost of the pipeline,
// as opposed to the billable-minute cost the waste/cost sections price.
//
// A "push" is the set of PR-event runs sharing one head SHA (grouped by head
// repo too, so fork branch-name collisions can't merge two pushes). A push
// qualifies only when the full verdict actually arrived and the wait is
// attributable to this push: every run completed, none was cancelled /
// action_required / stale (verdict withheld — a superseded push is not a
// wait anyone sat through), and nothing was manually re-run later (a re-run
// three days after the push would fake a three-day wait). Skipped runs
// (path filters) neither disqualify nor extend the wait. Wait = last job
// completion across the push's runs − earliest run creation, so queue time
// is included: the contributor waits through it too.
type FeedbackStats struct {
	Pushes     int     `json:"pushes"`  // qualifying pushes the percentiles are computed from
	PRRuns     int     `json:"pr_runs"` // PR-event runs in the sample (context)
	P50Minutes float64 `json:"p50_minutes"`
	P95Minutes float64 `json:"p95_minutes"`
	// Gaters names the critical path: workflows that finished last on some
	// qualifying push. Omitted when only one workflow ran — "the critical
	// path is your only workflow" is zero information.
	Gaters []GatingWorkflow `json:"gating_workflows,omitempty"`
}

// GatingWorkflow is a workflow that was the last check to finish on Count of
// the qualifying pushes. SlackP50Minutes is the median gap between it and
// the second-latest workflow on those pushes — roughly what speeding it up
// would cut from the wait, because feedback then arrives when the
// second-latest check does.
type GatingWorkflow struct {
	Workflow        string  `json:"workflow"`
	Count           int     `json:"count"`
	Share           float64 `json:"share"` // Count / Pushes
	SlackP50Minutes float64 `json:"slack_p50_minutes"`
}

// SupersededRun is one run that kept going after its replacement appeared.
type SupersededRun struct {
	Workflow      string  `json:"workflow"`
	Branch        string  `json:"branch"`
	URL           string  `json:"url"`
	WastedMinutes float64 `json:"wasted_minutes"`
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
	a.computeMatrixBalance(runs, jobsByRun)
	a.computeSuperseded(runs, jobsByRun)
	a.computeFeedback(runs, jobsByRun)
	a.computeZombieCrons(runs, jobsByRun)

	progress("fetching cache usage…")
	caches, truncated, err := c.ListCaches(owner, repo)
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
		if truncated {
			a.Cache.Sampled = true
			a.Cache.SampleCount = len(caches)
			// The sampled sum undercounts; get exact totals in one call.
			if size, count, uerr := c.CacheUsage(owner, repo); uerr == nil {
				a.Cache.Count = count
				a.Cache.TotalMB = float64(size) / (1024 * 1024)
				a.Cache.LimitPct = a.Cache.TotalMB / cacheLimitMB * 100
			}
		}
	}
	progress("fetching artifact usage…")
	arts, artTotal, artTruncated, err := c.ListArtifacts(owner, repo)
	if err != nil {
		var note string
		var rle *RateLimitError
		if errors.As(err, &rle) {
			note = "artifact data unavailable: " + rle.Message
		} else {
			note = "artifact data unavailable (private repos need a token with actions:read; set GITHUB_TOKEN or run `gh auth login`)"
		}
		a.Artifacts = ArtifactStats{Available: false, Note: note}
	} else {
		a.computeArtifactStats(arts, artTotal, artTruncated)
	}
	if c.CacheLogSample > 0 {
		a.CacheLogs = c.analyzeCacheLogs(owner, repo, jobsByRun, c.CacheLogSample, progress)
	}
	if c.FlakyLogSample > 0 {
		a.FlakyTests = c.analyzeFlakyLogs(owner, repo, a.flakyFails, c.FlakyLogSample, progress)
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

// computeArtifactStats aggregates sampled artifacts per name and projects
// steady-state storage (production rate × retention).
func (a *Analysis) computeArtifactStats(arts []Artifact, total int, truncated bool) {
	as := ArtifactStats{Available: true, Count: total, Sampled: truncated, SampleCount: len(arts)}
	if len(arts) == 0 {
		a.Artifacts = as
		return
	}

	var newest, oldest time.Time
	byName := map[string][]Artifact{}
	for _, art := range arts {
		if newest.IsZero() || art.CreatedAt.After(newest) {
			newest = art.CreatedAt
		}
		if oldest.IsZero() || art.CreatedAt.Before(oldest) {
			oldest = art.CreatedAt
		}
		if !art.Expired {
			as.ActiveCount++
			as.ActiveMB += float64(art.SizeInBytes) / (1024 * 1024)
		}
		byName[art.Name] = append(byName[art.Name], art)
	}
	as.WindowDays = newest.Sub(oldest).Hours() / 24

	for name, group := range byName {
		p := ArtifactProducer{Name: name, Count: len(group)}
		retentions := make([]float64, 0, len(group))
		for _, art := range group {
			p.TotalMB += float64(art.SizeInBytes) / (1024 * 1024)
			if !art.ExpiresAt.IsZero() && art.ExpiresAt.After(art.CreatedAt) {
				retentions = append(retentions, art.ExpiresAt.Sub(art.CreatedAt).Hours()/24)
			}
		}
		p.AvgMB = p.TotalMB / float64(len(group))
		sort.Float64s(retentions)
		p.RetentionDays = percentile(retentions, 0.5)
		as.Producers = append(as.Producers, p)
	}
	sort.Slice(as.Producers, func(i, j int) bool { return as.Producers[i].TotalMB > as.Producers[j].TotalMB })

	// Steady-state projection needs a rate, and a rate needs a window:
	// under 3 days of signal a CI burst would extrapolate into fiction.
	if as.WindowDays >= 3 {
		for i := range as.Producers {
			p := &as.Producers[i]
			p.SteadyGB = (p.TotalMB / as.WindowDays) * p.RetentionDays / 1024
			as.EstStorageGB += p.SteadyGB
		}
		as.EstUSDPerMo = as.EstStorageGB * artifactPricePerGBDay * 30
		as.EstimateBasis = fmt.Sprintf("upload rate over %.1f sampled days × per-name retention", as.WindowDays)
	} else {
		span := fmt.Sprintf("%.1f days", as.WindowDays)
		if as.WindowDays < 1 {
			span = fmt.Sprintf("%.1f hours", as.WindowDays*24)
		}
		as.EstimateBasis = fmt.Sprintf("the %d most recent uploads span only %s — too short to project steady-state storage", len(arts), span)
	}

	if len(as.Producers) > 8 {
		as.Producers = as.Producers[:8]
	}
	a.Artifacts = as
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
		decisive  int
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
		// Only decisive runs count toward the success rate and duration
		// percentiles. Skipped runs never executed; cancelled runs are
		// usually concurrency auto-cancels (the thing D001 recommends);
		// action_required runs are fork PRs awaiting approval. Counting
		// any of those as failures — or their near-zero durations as real
		// durations — would misgrade exactly the repos doing it right.
		switch r.Conclusion {
		case "success":
			w.decisive++
			w.success++
			w.durations = append(w.durations, r.UpdatedAt.Sub(r.RunStartedAt).Minutes())
			a.RunPoints = append(a.RunPoints, RunPoint{Workflow: r.Name, Start: r.RunStartedAt,
				Minutes: r.UpdatedAt.Sub(r.RunStartedAt).Minutes(), Success: true})
		case "failure", "timed_out", "startup_failure":
			w.decisive++
			w.durations = append(w.durations, r.UpdatedAt.Sub(r.RunStartedAt).Minutes())
			a.RunPoints = append(a.RunPoints, RunPoint{Workflow: r.Name, Start: r.RunStartedAt,
				Minutes: r.UpdatedAt.Sub(r.RunStartedAt).Minutes()})
		}
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
		sr := 0.0
		if w.decisive > 0 {
			sr = float64(w.success) / float64(w.decisive)
		}
		a.Workflows = append(a.Workflows, WorkflowStats{
			Name:        name,
			Runs:        w.total,
			Decisive:    w.decisive,
			SuccessRate: sr,
			P50Minutes:  percentile(w.durations, 0.50),
			P95Minutes:  percentile(w.durations, 0.95),
			AvgQueueSec: avgQ,
		})
	}
	sort.Slice(a.Workflows, func(i, j int) bool { return a.Workflows[i].Runs > a.Workflows[j].Runs })
	sort.Slice(a.RunPoints, func(i, j int) bool { return a.RunPoints[i].Start.Before(a.RunPoints[j].Start) })
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
		failedJobs    []Job
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
				o.failedJobs = append(o.failedJobs, j)
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
			for _, fj := range o.failedJobs {
				a.flakyFails = append(a.flakyFails, flakyFail{job: fj, wf: k.wf, sha: k.sha})
			}
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

// supersededExamplesMax caps the worst-offender list.
const supersededExamplesMax = 3

// computeSuperseded finds PR runs that a newer run of the same workflow on
// the same head repo+branch replaced while they were still running, and
// prices the minutes the completed ones burned past that moment. See
// SupersededStats for the scoping and no-double-counting rules.
func (a *Analysis) computeSuperseded(runs []Run, jobsByRun map[int64][]Job) {
	type group struct{ runs []Run }
	groups := make(map[string]*group)
	sup := &SupersededStats{}
	for _, r := range runs {
		if r.Event != "pull_request" && r.Event != "pull_request_target" {
			continue
		}
		sup.PRRuns++
		key := fmt.Sprintf("%d|%s|%s|%s", r.WorkflowID, r.Event, r.HeadRepo.FullName, r.HeadBranch)
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.runs = append(g.runs, r)
	}
	if sup.PRRuns == 0 {
		return
	}
	for _, g := range groups {
		sort.Slice(g.runs, func(i, j int) bool { return g.runs[i].CreatedAt.Before(g.runs[j].CreatedAt) })
		for i, r := range g.runs {
			if r.Status != "completed" {
				continue // an in-flight run's verdict isn't known yet
			}
			// endAt: when this run actually stopped doing work. Prefer the
			// last job completion — UpdatedAt includes post-run bookkeeping,
			// and a "replacement" that lands in that gap superseded nothing.
			endAt := r.UpdatedAt
			if jobs := jobsByRun[r.ID]; len(jobs) > 0 {
				var last time.Time
				for _, j := range jobs {
					if j.CompletedAt.After(last) {
						last = j.CompletedAt
					}
				}
				if !last.IsZero() {
					endAt = last
				}
			}
			// The supersession moment: the first later run with a
			// different SHA that was created before this one finished.
			var supersededAt time.Time
			for _, newer := range g.runs[i+1:] {
				if newer.HeadSHA == r.HeadSHA {
					continue // re-run of the same commit is not a replacement
				}
				if newer.CreatedAt.Before(endAt) {
					supersededAt = newer.CreatedAt
				}
				break // groups are sorted; only the first distinct-SHA successor matters
			}
			if supersededAt.IsZero() {
				continue
			}
			if r.Conclusion == "cancelled" {
				sup.Cancelled++
				continue
			}
			sup.Completed++
			if r.Conclusion == "failure" {
				continue // its minutes are already in the failures bucket
			}
			maxAttempt := 1
			for _, j := range jobsByRun[r.ID] {
				if j.RunAttempt > maxAttempt {
					maxAttempt = j.RunAttempt
				}
			}
			var saved float64
			for _, j := range jobsByRun[r.ID] {
				if j.StartedAt.IsZero() || j.CompletedAt.IsZero() || isSelfHosted(j.Labels) {
					continue
				}
				if j.RunAttempt < maxAttempt {
					continue // retried-attempt minutes live in the retries bucket
				}
				raw := j.CompletedAt.Sub(j.StartedAt).Minutes()
				if raw <= 0 {
					continue
				}
				billActual := math.Ceil(raw)
				if billActual == 0 {
					billActual = 1
				}
				before := supersededAt.Sub(j.StartedAt).Minutes()
				var billIfCancelled float64
				switch {
				case before <= 0:
					billIfCancelled = 0 // still queued at supersession — never runs
				case before >= raw:
					billIfCancelled = billActual // job finished before the replacement appeared
				default:
					billIfCancelled = math.Ceil(before)
					if billIfCancelled == 0 {
						billIfCancelled = 1
					}
				}
				saved += (billActual - billIfCancelled) * runnerMultiplier(j.Labels)
			}
			if saved <= 0 {
				continue
			}
			sup.WastedMinutes += saved
			sup.Examples = append(sup.Examples, SupersededRun{
				Workflow: r.Name, Branch: r.HeadBranch, URL: r.HTMLURL, WastedMinutes: saved,
			})
		}
	}
	sup.WastedUSD = sup.WastedMinutes * pricePerLinuxMinute
	sort.Slice(sup.Examples, func(i, j int) bool { return sup.Examples[i].WastedMinutes > sup.Examples[j].WastedMinutes })
	if len(sup.Examples) > supersededExamplesMax {
		sup.Examples = sup.Examples[:supersededExamplesMax]
	}
	a.Superseded = sup
}

// ZombieCron is a scheduled workflow whose recent runs all fail: a cron
// burning minutes on repeat with nobody watching. Its failed minutes are
// minFeedbackPushes is the honesty gate for FeedbackStats: percentiles from
// fewer than this many qualifying pushes describe luck, not the pipeline.
const minFeedbackPushes = 5

// feedbackBurstWindow bounds which same-SHA runs belong to one push: the
// ones created within this window of the group's earliest run. PR events
// like `labeled` or `ready_for_review` re-trigger workflows on the same SHA
// hours later — counting those as part of the push would fake an hours-long
// wait (seen live: a label sweep re-ran a check 15h after the push on every
// open PR). Runs from later bursts are ignored, not disqualifying: the
// push's own verdict already arrived with the first burst.
const feedbackBurstWindow = 5 * time.Minute

// feedbackGatersMax caps the critical-path list.
const feedbackGatersMax = 3

// computeFeedback measures the wait between a PR push and its last CI check
// finishing, and names the workflows that finish last (the critical path).
// See FeedbackStats for what qualifies a push and why.
func (a *Analysis) computeFeedback(runs []Run, jobsByRun map[int64][]Job) {
	type push struct{ runs []Run }
	groups := make(map[string]*push)
	prRuns := 0
	for _, r := range runs {
		if r.Event != "pull_request" && r.Event != "pull_request_target" {
			continue
		}
		prRuns++
		key := r.HeadRepo.FullName + "|" + r.HeadSHA
		g := groups[key]
		if g == nil {
			g = &push{}
			groups[key] = g
		}
		g.runs = append(g.runs, r)
	}
	if prRuns == 0 {
		return
	}
	type gate struct {
		count  int
		slacks []float64
	}
	gates := make(map[string]*gate)
	var waits []float64
	multiWF := false
	for _, g := range groups {
		ok := true
		var start time.Time
		wfEnd := make(map[string]time.Time)
		var burstStart time.Time
		for _, r := range g.runs {
			if burstStart.IsZero() || r.CreatedAt.Before(burstStart) {
				burstStart = r.CreatedAt
			}
		}
		for _, r := range g.runs {
			if r.CreatedAt.Sub(burstStart) > feedbackBurstWindow {
				continue // a later trigger on the same SHA (label, ready_for_review) — not this push's wait
			}
			if r.Status != "completed" || r.RunAttempt > 1 {
				ok = false // in-flight, or a later manual re-run — the wait isn't this push's
				break
			}
			switch r.Conclusion {
			case "cancelled", "action_required", "stale":
				ok = false // verdict withheld (superseded push, fork awaiting approval)
			case "skipped":
				continue // path-filtered; neither disqualifies nor extends the wait
			}
			if !ok {
				break
			}
			// End of this run's work: prefer the last job completion —
			// UpdatedAt includes post-run bookkeeping. Jobs from a later
			// attempt disqualify the push same as run-level RunAttempt.
			end := r.UpdatedAt
			if jobs := jobsByRun[r.ID]; len(jobs) > 0 {
				var last time.Time
				for _, j := range jobs {
					if j.RunAttempt > 1 {
						ok = false
						break
					}
					if j.CompletedAt.After(last) {
						last = j.CompletedAt
					}
				}
				if !ok {
					break
				}
				if !last.IsZero() {
					end = last
				}
			}
			if start.IsZero() || r.CreatedAt.Before(start) {
				start = r.CreatedAt
			}
			if end.After(wfEnd[r.Name]) {
				wfEnd[r.Name] = end
			}
		}
		if !ok || len(wfEnd) == 0 {
			continue
		}
		if len(wfEnd) > 1 {
			multiWF = true
		}
		var lastWF string
		var lastEnd, secondEnd time.Time
		for name, e := range wfEnd {
			switch {
			case e.After(lastEnd):
				secondEnd = lastEnd
				lastEnd = e
				lastWF = name
			case e.After(secondEnd):
				secondEnd = e
			}
		}
		wait := lastEnd.Sub(start).Minutes()
		if wait <= 0 {
			continue // clock skew; don't let a negative wait poison percentiles
		}
		waits = append(waits, wait)
		// Slack: what cancelling the gating workflow would have saved.
		// With a single workflow that is the whole wait.
		floor := start
		if !secondEnd.IsZero() {
			floor = secondEnd
		}
		gt := gates[lastWF]
		if gt == nil {
			gt = &gate{}
			gates[lastWF] = gt
		}
		gt.count++
		gt.slacks = append(gt.slacks, lastEnd.Sub(floor).Minutes())
	}
	if len(waits) < minFeedbackPushes {
		return
	}
	sort.Float64s(waits)
	fs := &FeedbackStats{
		Pushes:     len(waits),
		PRRuns:     prRuns,
		P50Minutes: percentile(waits, 0.50),
		P95Minutes: percentile(waits, 0.95),
	}
	if multiWF {
		for name, gt := range gates {
			sort.Float64s(gt.slacks)
			fs.Gaters = append(fs.Gaters, GatingWorkflow{
				Workflow:        name,
				Count:           gt.count,
				Share:           float64(gt.count) / float64(len(waits)),
				SlackP50Minutes: percentile(gt.slacks, 0.5),
			})
		}
		sort.Slice(fs.Gaters, func(i, j int) bool {
			if fs.Gaters[i].Count != fs.Gaters[j].Count {
				return fs.Gaters[i].Count > fs.Gaters[j].Count
			}
			return fs.Gaters[i].Workflow < fs.Gaters[j].Workflow
		})
		if len(fs.Gaters) > feedbackGatersMax {
			fs.Gaters = fs.Gaters[:feedbackGatersMax]
		}
	}
	a.Feedback = fs
}

// already counted in the waste bucket — the value here is naming the
// workflow and how long it has been dead.
type ZombieCron struct {
	Workflow string `json:"workflow"`
	URL      string `json:"url"` // most recent failing run
	// Fails counts consecutive failing scheduled runs, newest first,
	// within the sample. Skipped/cancelled runs neither break nor extend
	// the streak; a success breaks it.
	Fails int `json:"consecutive_failures"`
	// StreakOpen is true when the streak reaches the oldest sampled
	// scheduled run of this workflow — the real streak may be longer.
	StreakOpen   bool      `json:"streak_reaches_sample_edge,omitempty"`
	SpanDays     float64   `json:"span_days"` // newest failing run − oldest failing run
	LastFailedAt time.Time `json:"last_failed_at"`
	// MedianMinutes is the median billable-weighted minutes per failing
	// run (per job ceil to whole minute, OS multiplier; self-hosted
	// excluded — those aren't billed by GitHub).
	MedianMinutes float64 `json:"median_billable_min_per_run"`
	// Projections assume the failing cadence continues (span/(fails−1)
	// between runs). Only reported because the span gate below guarantees
	// >= 3 days of signal.
	EstMinPerMo float64 `json:"est_min_per_month"`
	EstUSDPerMo float64 `json:"est_usd_per_month"`
}

// Zombie-cron gates: a streak must be both long (>= zombieMinFails
// consecutive failures — one broken nightly is a bad day, not a zombie)
// and old (>= zombieMinSpanDays — five failures of an every-10-minutes
// cron is under an hour of breakage, which the owner may already be
// fixing). The span gate doubles as the 3-day projection honesty gate.
const (
	zombieMinFails    = 5
	zombieMinSpanDays = 3.0
	zombieMax         = 5 // rendered entries, by est. monthly burn
)

// zombieFailConclusion reports whether a completed scheduled run counts as
// failing for streak purposes.
func zombieFailConclusion(c string) bool {
	return c == "failure" || c == "timed_out" || c == "startup_failure"
}

// computeZombieCrons finds scheduled workflows whose recent runs are an
// unbroken failure streak. See ZombieCron and the gate constants for the
// exact rules.
func (a *Analysis) computeZombieCrons(runs []Run, jobsByRun map[int64][]Job) {
	byWF := make(map[int64][]Run)
	for _, r := range runs {
		if r.Event != "schedule" {
			continue
		}
		byWF[r.WorkflowID] = append(byWF[r.WorkflowID], r)
	}
	var out []ZombieCron
	for _, wfRuns := range byWF {
		sort.Slice(wfRuns, func(i, j int) bool { return wfRuns[i].CreatedAt.After(wfRuns[j].CreatedAt) })
		var streak []Run
		open := true // becomes false when an older non-failing decisive run breaks the streak
		for _, r := range wfRuns {
			if r.Status != "completed" {
				continue // in-flight: verdict unknown
			}
			if zombieFailConclusion(r.Conclusion) {
				streak = append(streak, r)
				continue
			}
			if r.Conclusion == "cancelled" || r.Conclusion == "skipped" ||
				r.Conclusion == "action_required" || r.Conclusion == "stale" {
				continue // non-decisive: neither breaks nor extends
			}
			open = false
			break // a success (or other decisive outcome) ends the streak
		}
		if len(streak) < zombieMinFails {
			continue
		}
		newest, oldest := streak[0], streak[len(streak)-1]
		spanDays := newest.CreatedAt.Sub(oldest.CreatedAt).Hours() / 24
		if spanDays < zombieMinSpanDays {
			continue
		}
		var mins []float64
		for _, r := range streak {
			var m float64
			for _, j := range jobsByRun[r.ID] {
				if j.StartedAt.IsZero() || j.CompletedAt.IsZero() || isSelfHosted(j.Labels) {
					continue
				}
				raw := j.CompletedAt.Sub(j.StartedAt).Minutes()
				if raw <= 0 {
					continue
				}
				bill := math.Ceil(raw)
				if bill == 0 {
					bill = 1
				}
				m += bill * runnerMultiplier(j.Labels)
			}
			mins = append(mins, m)
		}
		sort.Float64s(mins)
		med := percentile(mins, 0.5)
		gapDays := spanDays / float64(len(streak)-1)
		z := ZombieCron{
			Workflow:      newest.Name,
			URL:           newest.HTMLURL,
			Fails:         len(streak),
			StreakOpen:    open,
			SpanDays:      spanDays,
			LastFailedAt:  newest.CreatedAt,
			MedianMinutes: med,
		}
		if gapDays > 0 {
			z.EstMinPerMo = 30 / gapDays * med
			z.EstUSDPerMo = z.EstMinPerMo * pricePerLinuxMinute
		}
		out = append(out, z)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EstUSDPerMo != out[j].EstUSDPerMo {
			return out[i].EstUSDPerMo > out[j].EstUSDPerMo
		}
		return out[i].Workflow < out[j].Workflow
	})
	if len(out) > zombieMax {
		out = out[:zombieMax]
	}
	a.ZombieCrons = out
}

// baseJobName strips a trailing matrix suffix like "test (ubuntu-latest, 3.12)".
func baseJobName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}
