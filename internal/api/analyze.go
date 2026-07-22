package api

import (
	"fmt"
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
}

// WorkflowStats aggregates runs of one workflow.
type WorkflowStats struct {
	Name        string  `json:"name"`
	Runs        int     `json:"runs"`
	SuccessRate float64 `json:"success_rate"`
	P50Minutes  float64 `json:"p50_minutes"`
	P95Minutes  float64 `json:"p95_minutes"`
	AvgQueueSec float64 `json:"avg_queue_seconds"`
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
	return a, nil
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

// baseJobName strips a trailing matrix suffix like "test (ubuntu-latest, 3.12)".
func baseJobName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
}
