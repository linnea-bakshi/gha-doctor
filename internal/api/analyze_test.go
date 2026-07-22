package api

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

func min(m float64) time.Duration { return time.Duration(m * float64(time.Minute)) }

// mkJob builds a completed job that started at t0 and ran for durMin minutes.
func mkJob(runID int64, name, conclusion string, attempt int, durMin float64, labels ...string) Job {
	if len(labels) == 0 {
		labels = []string{"ubuntu-latest"}
	}
	return Job{
		RunID:       runID,
		RunAttempt:  attempt,
		Name:        name,
		Status:      "completed",
		Conclusion:  conclusion,
		CreatedAt:   t0.Add(-30 * time.Second),
		StartedAt:   t0,
		CompletedAt: t0.Add(min(durMin)),
		Labels:      labels,
	}
}

func mkRun(id int64, name, sha, conclusion string, durMin float64) Run {
	return Run{
		ID: id, Name: name, HeadSHA: sha,
		Status: "completed", Conclusion: conclusion, RunAttempt: 1,
		RunStartedAt: t0, CreatedAt: t0, UpdatedAt: t0.Add(min(durMin)),
	}
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestPercentile(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("empty percentile = %v, want 0", got)
	}
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	approx(t, "p50", percentile(sorted, 0.50), 5)
	approx(t, "p95", percentile(sorted, 0.95), 9)
	approx(t, "p0", percentile(sorted, 0), 1)
	approx(t, "p100", percentile(sorted, 1), 10)
}

func TestRunnerMultiplier(t *testing.T) {
	cases := []struct {
		labels []string
		want   float64
	}{
		{[]string{"ubuntu-latest"}, 1},
		{[]string{"self-hosted", "linux"}, 1},
		{[]string{"windows-latest"}, 2},
		{[]string{"Windows-2022"}, 2},
		{[]string{"macos-14"}, 10},
		{[]string{"macOS-latest"}, 10},
		{nil, 1},
	}
	for _, c := range cases {
		if got := runnerMultiplier(c.labels); got != c.want {
			t.Errorf("runnerMultiplier(%v) = %v, want %v", c.labels, got, c.want)
		}
	}
}

func TestBaseJobName(t *testing.T) {
	cases := map[string]string{
		"test (ubuntu-latest, 3.12)": "test",
		"build":                      "build",
		"lint (go)":                  "lint",
		" (weird)":                   " (weird)", // no name before matrix -> unchanged
	}
	for in, want := range cases {
		if got := baseJobName(in); got != want {
			t.Errorf("baseJobName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeFlaky(t *testing.T) {
	// Same SHA "abc": run 1 fails job "test", run 2 passes it -> flaky.
	// SHA "def": job fails only (real breakage) -> not flaky.
	runs := []Run{
		mkRun(1, "CI", "abc", "failure", 10),
		mkRun(2, "CI", "abc", "success", 10),
		mkRun(3, "CI", "def", "failure", 10),
	}
	jobs := map[int64][]Job{
		1: {mkJob(1, "test", "failure", 1, 8)},
		2: {mkJob(2, "test", "success", 1, 8)},
		3: {mkJob(3, "test", "failure", 1, 8)},
	}
	var a Analysis
	a.computeFlaky(runs, jobs)
	if len(a.FlakyJobs) != 1 {
		t.Fatalf("got %d flaky jobs, want 1: %+v", len(a.FlakyJobs), a.FlakyJobs)
	}
	f := a.FlakyJobs[0]
	if f.Workflow != "CI" || f.Job != "test" {
		t.Errorf("flaky job identity = %s/%s", f.Workflow, f.Job)
	}
	if f.FlakyCommits != 1 || f.Failures != 1 {
		t.Errorf("FlakyCommits=%d Failures=%d, want 1/1", f.FlakyCommits, f.Failures)
	}
	approx(t, "WastedMinutes", f.WastedMinutes, 8)
}

func TestComputeFlakyIgnoresSkippedAndCancelled(t *testing.T) {
	runs := []Run{
		mkRun(1, "CI", "abc", "cancelled", 5),
		mkRun(2, "CI", "abc", "success", 5),
	}
	jobs := map[int64][]Job{
		1: {mkJob(1, "test", "cancelled", 1, 3)},
		2: {mkJob(2, "test", "success", 1, 3)},
	}
	var a Analysis
	a.computeFlaky(runs, jobs)
	if len(a.FlakyJobs) != 0 {
		t.Errorf("cancelled+success should not count as flaky: %+v", a.FlakyJobs)
	}
}

func TestComputeWorkflowStats(t *testing.T) {
	runs := []Run{
		mkRun(1, "CI", "a", "success", 10),
		mkRun(2, "CI", "b", "failure", 20),
		mkRun(3, "Docs", "c", "success", 2),
	}
	jobs := map[int64][]Job{
		1: {mkJob(1, "test", "success", 1, 9)},
		2: {mkJob(2, "test", "failure", 1, 19)},
	}
	var a Analysis
	a.computeWorkflowStats(runs, jobs)
	if len(a.Workflows) != 2 {
		t.Fatalf("got %d workflows, want 2", len(a.Workflows))
	}
	// Sorted by run count desc -> CI first.
	ci := a.Workflows[0]
	if ci.Name != "CI" || ci.Runs != 2 {
		t.Fatalf("first workflow = %+v, want CI with 2 runs", ci)
	}
	approx(t, "CI success rate", ci.SuccessRate, 0.5)
	approx(t, "CI p50", ci.P50Minutes, 10)
	approx(t, "CI avg queue", ci.AvgQueueSec, 30) // jobs created 30s before start
}

func TestComputeSlowSteps(t *testing.T) {
	j1 := mkJob(1, "test (ubuntu-latest)", "success", 1, 10)
	j1.Steps = []Step{
		{Name: "Build", Conclusion: "success", StartedAt: t0, CompletedAt: t0.Add(min(6))},
		{Name: "Skipped", Conclusion: "skipped", StartedAt: t0, CompletedAt: t0.Add(min(60))},
		{Name: "Zero", Conclusion: "success"}, // zero times -> ignored
	}
	j2 := mkJob(2, "test (macos-14)", "success", 1, 10)
	j2.Steps = []Step{
		{Name: "Build", Conclusion: "success", StartedAt: t0, CompletedAt: t0.Add(min(4))},
	}
	var a Analysis
	a.computeSlowSteps(map[int64][]Job{1: {j1}, 2: {j2}})
	if len(a.SlowSteps) != 1 {
		t.Fatalf("got %d slow steps, want 1 (matrix variants merged): %+v", len(a.SlowSteps), a.SlowSteps)
	}
	s := a.SlowSteps[0]
	if s.Job != "test" || s.Step != "Build" || s.Count != 2 {
		t.Errorf("slow step = %+v", s)
	}
	approx(t, "TotalMin", s.TotalMin, 10)
}

func TestComputeWaste(t *testing.T) {
	// Run 1: attempt 1 failed (5 min), attempt 2 succeeded (5 min), run succeeded
	// -> attempt-1 minutes are retry waste.
	r1 := mkRun(1, "CI", "a", "success", 12)
	// Run 2: failed outright on macOS (3 min * 10x = 30 billable minutes wasted).
	r2 := mkRun(2, "CI", "b", "failure", 4)
	jobs := map[int64][]Job{
		1: {
			mkJob(1, "test", "failure", 1, 5),
			mkJob(1, "test", "success", 2, 5),
		},
		2: {mkJob(2, "test", "failure", 1, 3, "macos-14")},
	}
	var a Analysis
	a.computeWaste([]Run{r1, r2}, jobs)
	approx(t, "RetryMinutes", a.Waste.RetryMinutes, 5)
	approx(t, "FailedRunMinutes", a.Waste.FailedRunMinutes, 30)
	approx(t, "TotalMinutes", a.Waste.TotalMinutes, 35)
	approx(t, "ComputeMinutes", a.Waste.ComputeMinutes, 40)
}

func TestComputeCost(t *testing.T) {
	// Run 1 (success): linux job 0.5 min -> billed 1 min ($0.008), rounding 0.5 min.
	// Run 2 (failure): macOS job 2.5 min -> billed 3 min * 10x = 30 weighted min
	//                  ($0.24), all wasted; rounding 0.5*10 = 5 weighted min.
	// Run 3 (success): self-hosted job 60 min -> excluded entirely.
	r1 := mkRun(1, "CI", "a", "success", 1)
	r2 := mkRun(2, "Release", "b", "failure", 3)
	r3 := mkRun(3, "CI", "c", "success", 60)
	jobs := map[int64][]Job{
		1: {mkJob(1, "quick", "success", 1, 0.5)},
		2: {mkJob(2, "mac", "failure", 1, 2.5, "macos-14")},
		3: {mkJob(3, "beefy", "success", 1, 60, "self-hosted", "linux")},
	}
	var a Analysis
	runs := []Run{r1, r2, r3}
	a.computeWorkflowStats(runs, jobs)
	a.computeCost(runs, jobs)

	approx(t, "BillableMinutes", a.Cost.BillableMinutes, 1+30)
	approx(t, "EstimatedUSD", a.Cost.EstimatedUSD, 0.008+0.24)
	approx(t, "WastedUSD", a.Cost.WastedUSD, 0.24)
	approx(t, "RoundingMinutes", a.Cost.RoundingMinutes, 0.5+5)
	approx(t, "RoundingUSD", a.Cost.RoundingUSD, 5.5*0.008)
	if a.Cost.SelfHostedJobs != 1 {
		t.Errorf("SelfHostedJobs = %d, want 1", a.Cost.SelfHostedJobs)
	}
	// Per-workflow attribution.
	for _, wf := range a.Workflows {
		switch wf.Name {
		case "CI":
			approx(t, "CI EstUSD", wf.EstUSD, 0.008)
		case "Release":
			approx(t, "Release EstUSD", wf.EstUSD, 0.24)
		}
	}
}

func TestComputeCostRetryAttemptIsWasted(t *testing.T) {
	// Attempt 1 failed then attempt 2 passed: attempt-1 cost is waste even
	// though the run concluded successfully.
	r := mkRun(1, "CI", "a", "success", 10)
	jobs := map[int64][]Job{1: {
		mkJob(1, "test", "failure", 1, 4),
		mkJob(1, "test", "success", 2, 4),
	}}
	var a Analysis
	a.computeCost([]Run{r}, jobs)
	approx(t, "EstimatedUSD", a.Cost.EstimatedUSD, 8*0.008)
	approx(t, "WastedUSD", a.Cost.WastedUSD, 4*0.008)
}

func TestComputeCacheStats(t *testing.T) {
	now := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	mb := int64(1024 * 1024)
	caches := []ActionsCache{
		{Key: "go-mod-a", Ref: "refs/heads/main", SizeInBytes: 4000 * mb, LastAccessedAt: now.Add(-time.Hour)},
		{Key: "go-mod-old", Ref: "refs/heads/main", SizeInBytes: 3000 * mb, LastAccessedAt: now.Add(-8 * 24 * time.Hour)},
		{Key: "pr-cache", Ref: "refs/pull/42/merge", SizeInBytes: 2500 * mb, LastAccessedAt: now.Add(-9 * 24 * time.Hour)},
	}
	var a Analysis
	a.computeCacheStats(caches, now)
	cs := a.Cache
	if !cs.Available || cs.Count != 3 {
		t.Fatalf("available=%v count=%d", cs.Available, cs.Count)
	}
	if got, want := cs.TotalMB, 9500.0; got != want {
		t.Errorf("TotalMB = %v, want %v", got, want)
	}
	if cs.LimitPct < 92 || cs.LimitPct > 93 { // 9500/10240 ≈ 92.8%
		t.Errorf("LimitPct = %v, want ≈92.8", cs.LimitPct)
	}
	if cs.StaleCount != 2 || cs.StaleMB != 5500 {
		t.Errorf("stale = %d/%v MB, want 2/5500", cs.StaleCount, cs.StaleMB)
	}
	if cs.PRRefCount != 1 || cs.PRRefMB != 2500 {
		t.Errorf("pr = %d/%v MB, want 1/2500", cs.PRRefCount, cs.PRRefMB)
	}
	if len(cs.Largest) != 3 || cs.Largest[0].Key != "go-mod-a" {
		t.Errorf("largest = %+v", cs.Largest)
	}
}

func TestComputeCacheStatsEmpty(t *testing.T) {
	var a Analysis
	a.computeCacheStats(nil, time.Now())
	if !a.Cache.Available || a.Cache.Count != 0 || a.Cache.LimitPct != 0 {
		t.Errorf("empty cache stats = %+v", a.Cache)
	}
}
