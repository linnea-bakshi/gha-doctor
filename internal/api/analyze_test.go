package api

import (
	"fmt"
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
	// The flaky failure (job ID 1) is the --flaky-logs sampling population;
	// the "def" failure never passed, so it must NOT be in it.
	if len(a.flakyFails) != 1 || a.flakyFails[0].job.RunID != 1 || a.flakyFails[0].sha != "abc" {
		t.Errorf("flakyFails = %+v, want exactly the run-1 failure @ abc", a.flakyFails)
	}
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

func TestComputeWorkflowStatsDecisiveOnly(t *testing.T) {
	// 2 decisive runs (1 success, 1 failure) + a pile of skipped/cancelled
	// runs with near-zero durations. Success rate must be 50% (not 1/6) and
	// the skipped runs' durations must not drag p50 toward zero.
	runs := []Run{
		mkRun(1, "CI", "a", "success", 10),
		mkRun(2, "CI", "b", "failure", 12),
		mkRun(3, "CI", "c", "skipped", 0),
		mkRun(4, "CI", "d", "skipped", 0),
		mkRun(5, "CI", "e", "cancelled", 1),
		mkRun(6, "CI", "f", "action_required", 0),
	}
	var a Analysis
	a.computeWorkflowStats(runs, map[int64][]Job{})
	if len(a.Workflows) != 1 {
		t.Fatalf("got %d workflows, want 1", len(a.Workflows))
	}
	ci := a.Workflows[0]
	if ci.Runs != 6 || ci.Decisive != 2 {
		t.Fatalf("runs/decisive = %d/%d, want 6/2", ci.Runs, ci.Decisive)
	}
	approx(t, "success rate over decisive runs", ci.SuccessRate, 0.5)
	if ci.P50Minutes < 9 {
		t.Errorf("p50 = %v, skipped/cancelled durations leaked into percentiles", ci.P50Minutes)
	}

	// All-skipped workflow: no verdicts, rate must be 0 with Decisive 0.
	runs = []Run{mkRun(1, "Nightly", "a", "skipped", 0)}
	a = Analysis{}
	a.computeWorkflowStats(runs, map[int64][]Job{})
	if got := a.Workflows[0]; got.Decisive != 0 || got.SuccessRate != 0 {
		t.Errorf("all-skipped workflow = %+v, want Decisive 0, SuccessRate 0", got)
	}
}

func TestComputeArtifactStats(t *testing.T) {
	day := 24 * time.Hour
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	mb := int64(1024 * 1024)
	var arts []Artifact
	// "test-results": 10 uploads over 9 days, 100 MB each, 90-day retention.
	for i := 0; i < 10; i++ {
		created := t0.Add(time.Duration(i) * day)
		arts = append(arts, Artifact{
			Name: "test-results", SizeInBytes: 100 * mb,
			CreatedAt: created, ExpiresAt: created.Add(90 * day),
		})
	}
	// "coverage": 2 uploads, 7-day retention, one already expired.
	arts = append(arts,
		Artifact{Name: "coverage", SizeInBytes: 10 * mb, CreatedAt: t0, ExpiresAt: t0.Add(7 * day), Expired: true},
		Artifact{Name: "coverage", SizeInBytes: 10 * mb, CreatedAt: t0.Add(9 * day), ExpiresAt: t0.Add(16 * day)},
	)

	var a Analysis
	a.computeArtifactStats(arts, 500, true)
	ar := a.Artifacts

	if !ar.Available || !ar.Sampled || ar.Count != 500 || ar.SampleCount != 12 {
		t.Fatalf("header fields: %+v", ar)
	}
	if ar.WindowDays != 9 {
		t.Errorf("WindowDays = %v, want 9", ar.WindowDays)
	}
	if ar.ActiveCount != 11 || ar.ActiveMB != 1010 {
		t.Errorf("active = %d/%v MB, want 11/1010", ar.ActiveCount, ar.ActiveMB)
	}
	if len(ar.Producers) != 2 || ar.Producers[0].Name != "test-results" {
		t.Fatalf("producers = %+v", ar.Producers)
	}
	p := ar.Producers[0]
	if p.Count != 10 || p.TotalMB != 1000 || p.AvgMB != 100 || p.RetentionDays != 90 {
		t.Errorf("test-results producer = %+v", p)
	}
	// rate 1000MB/9d × 90d retention = 10000 MB ≈ 9.77 GB steady state.
	if p.SteadyGB < 9.7 || p.SteadyGB > 9.8 {
		t.Errorf("SteadyGB = %v, want ≈9.77", p.SteadyGB)
	}
	if ar.EstStorageGB <= p.SteadyGB { // coverage adds a little
		t.Errorf("EstStorageGB = %v, want > %v", ar.EstStorageGB, p.SteadyGB)
	}
	// $ = GB × 0.008 × 30
	wantUSD := ar.EstStorageGB * 0.008 * 30
	if diff := ar.EstUSDPerMo - wantUSD; diff > 0.001 || diff < -0.001 {
		t.Errorf("EstUSDPerMo = %v, want %v", ar.EstUSDPerMo, wantUSD)
	}
}

func TestComputeArtifactStatsShortWindowSkipsEstimate(t *testing.T) {
	// A one-afternoon burst must not extrapolate into a steady-state claim.
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var arts []Artifact
	for i := 0; i < 50; i++ {
		created := t0.Add(time.Duration(i) * time.Minute)
		arts = append(arts, Artifact{
			Name: "burst", SizeInBytes: 1 << 30,
			CreatedAt: created, ExpiresAt: created.Add(90 * 24 * time.Hour),
		})
	}
	var a Analysis
	a.computeArtifactStats(arts, 50, false)
	ar := a.Artifacts
	if ar.EstStorageGB != 0 || ar.EstUSDPerMo != 0 {
		t.Errorf("short window must not project: %+v", ar)
	}
	if ar.EstimateBasis == "" {
		t.Error("want an explanatory basis note")
	}
	if len(ar.Producers) != 1 || ar.Producers[0].SteadyGB != 0 {
		t.Errorf("producers = %+v", ar.Producers)
	}
}

func TestComputeArtifactStatsEmpty(t *testing.T) {
	var a Analysis
	a.computeArtifactStats(nil, 0, false)
	if !a.Artifacts.Available || a.Artifacts.Count != 0 {
		t.Errorf("empty stats = %+v", a.Artifacts)
	}
}

func TestComputeArtifactStatsProducerCap(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	var arts []Artifact
	for i := 0; i < 20; i++ {
		arts = append(arts, Artifact{
			Name: fmt.Sprintf("name-%d", i), SizeInBytes: int64(i+1) << 20,
			CreatedAt: t0.Add(time.Duration(i) * 24 * time.Hour),
			ExpiresAt: t0.Add(time.Duration(i+90) * 24 * time.Hour),
		})
	}
	var a Analysis
	a.computeArtifactStats(arts, 20, false)
	if len(a.Artifacts.Producers) != 8 {
		t.Fatalf("producers = %d, want capped at 8", len(a.Artifacts.Producers))
	}
	if a.Artifacts.Producers[0].Name != "name-19" {
		t.Errorf("want biggest first, got %s", a.Artifacts.Producers[0].Name)
	}
}

// jobAt builds a completed job with an explicit start time.
func jobAt(runID int64, name string, attempt int, start time.Time, durMin float64, labels ...string) Job {
	if len(labels) == 0 {
		labels = []string{"ubuntu-latest"}
	}
	return Job{
		RunID: runID, RunAttempt: attempt, Name: name,
		Status: "completed", Conclusion: "success",
		StartedAt: start, CompletedAt: start.Add(min(durMin)),
		Labels: labels,
	}
}

// prRun builds a completed pull_request run.
func prRun(id, wfID int64, headRepo, branch, sha, conclusion string, created time.Time, durMin float64) Run {
	r := Run{
		ID: id, Name: fmt.Sprintf("wf-%d", wfID), WorkflowID: wfID,
		Event: "pull_request", HeadBranch: branch, HeadSHA: sha,
		Status: "completed", Conclusion: conclusion, RunAttempt: 1,
		RunStartedAt: created, CreatedAt: created, UpdatedAt: created.Add(min(durMin)),
		HTMLURL: fmt.Sprintf("https://github.com/o/r/actions/runs/%d", id),
	}
	r.HeadRepo.FullName = headRepo
	return r
}

func TestComputeSuperseded(t *testing.T) {
	runs := []Run{
		// Group A: run 1 superseded at t0+4m by run 2 (different SHA).
		prRun(1, 1, "octo/fork1", "feat", "aaa", "success", t0, 10),
		prRun(2, 1, "octo/fork1", "feat", "bbb", "success", t0.Add(min(4)), 10),
		// Group B: superseded but cancelled in time.
		prRun(3, 1, "octo/fork1", "other", "ccc", "cancelled", t0, 10),
		prRun(4, 1, "octo/fork1", "other", "ddd", "success", t0.Add(min(2)), 5),
		// Same branch name, different fork: must NOT count as superseding run 1.
		prRun(5, 1, "octo/fork2", "feat", "eee", "success", t0.Add(min(1)), 10),
		// Same-SHA successor is a re-run, not a replacement.
		prRun(6, 2, "octo/fork1", "feat", "fff", "success", t0, 10),
		prRun(7, 2, "octo/fork1", "feat", "fff", "success", t0.Add(min(3)), 10),
		// Superseded AND failed: counted, but minutes stay in the failures bucket.
		prRun(8, 3, "octo/fork1", "feat", "ggg", "failure", t0, 10),
		prRun(9, 3, "octo/fork1", "feat", "hhh", "success", t0.Add(min(2)), 5),
	}
	jobsByRun := map[int64][]Job{
		1: {
			jobAt(1, "build", 1, t0, 10),                             // attempt 1 of 2: retries bucket, skipped
			jobAt(1, "build", 2, t0, 10),                             // ceil(10)-ceil(4) = 6 saved
			jobAt(1, "quick", 2, t0, 3),                              // done before supersession: 0
			jobAt(1, "late", 2, t0.Add(min(5)), 2, "windows-latest"), // queued at supersession: 2 min x2 = 4
		},
		8: {jobAt(8, "build", 1, t0, 10)},
	}
	var a Analysis
	a.computeSuperseded(runs, jobsByRun)
	sup := a.Superseded
	if sup == nil {
		t.Fatal("Superseded = nil, want stats")
	}
	if sup.PRRuns != 9 {
		t.Errorf("PRRuns = %d, want 9", sup.PRRuns)
	}
	if sup.Completed != 2 { // runs 1 and 8
		t.Errorf("Completed = %d, want 2", sup.Completed)
	}
	if sup.Cancelled != 1 { // run 3
		t.Errorf("Cancelled = %d, want 1", sup.Cancelled)
	}
	approx(t, "WastedMinutes", sup.WastedMinutes, 10) // 6 + 0 + 4; run 8 excluded
	approx(t, "WastedUSD", sup.WastedUSD, 10*0.008)
	if len(sup.Examples) != 1 || sup.Examples[0].Branch != "feat" || sup.Examples[0].WastedMinutes != 10 {
		t.Errorf("Examples = %+v, want one 10-min example on feat", sup.Examples)
	}
}

func TestComputeSupersededBookkeepingGapNotCounted(t *testing.T) {
	// All jobs finish at t0+3m; the run record is updated at t0+10m; the
	// "replacement" lands at t0+5m — inside the bookkeeping gap. The run
	// was never superseded while working, so nothing must be counted.
	runs := []Run{
		prRun(1, 1, "octo/fork1", "feat", "aaa", "success", t0, 10),
		prRun(2, 1, "octo/fork1", "feat", "bbb", "success", t0.Add(min(5)), 5),
	}
	jobsByRun := map[int64][]Job{1: {jobAt(1, "build", 1, t0, 3)}}
	var a Analysis
	a.computeSuperseded(runs, jobsByRun)
	if a.Superseded.Completed != 0 || a.Superseded.WastedMinutes != 0 {
		t.Errorf("Superseded = %+v, want nothing counted for a bookkeeping-gap replacement", a.Superseded)
	}
}

func TestComputeSupersededInProgressSkipped(t *testing.T) {
	r1 := prRun(1, 1, "octo/fork1", "feat", "aaa", "", t0, 60)
	r1.Status = "in_progress"
	runs := []Run{
		r1,
		prRun(2, 1, "octo/fork1", "feat", "bbb", "success", t0.Add(min(5)), 5),
	}
	var a Analysis
	a.computeSuperseded(runs, nil)
	if a.Superseded.Completed != 0 || a.Superseded.Cancelled != 0 {
		t.Errorf("Superseded = %+v, want in-flight runs left unclassified", a.Superseded)
	}
}

func TestComputeSupersededNoPRRuns(t *testing.T) {
	push := mkRun(1, "ci", "aaa", "success", 5)
	push.Event = "push"
	var a Analysis
	a.computeSuperseded([]Run{push}, nil)
	if a.Superseded != nil {
		t.Fatalf("Superseded = %+v, want nil when the sample has no PR runs", a.Superseded)
	}
}

// schedRun builds a completed schedule-event run.
func schedRun(id, wfID int64, conclusion string, created time.Time) Run {
	return Run{
		ID: id, Name: fmt.Sprintf("cron-%d", wfID), WorkflowID: wfID,
		Event: "schedule", Status: "completed", Conclusion: conclusion,
		RunAttempt: 1, RunStartedAt: created, CreatedAt: created,
		UpdatedAt: created.Add(min(5)),
		HTMLURL:   fmt.Sprintf("https://github.com/o/r/actions/runs/%d", id),
	}
}

func day(d float64) time.Duration { return time.Duration(d * 24 * float64(time.Hour)) }

func TestComputeZombieCrons(t *testing.T) {
	var runs []Run
	jobsByRun := map[int64][]Job{}
	// WF 1: a real zombie — 6 daily failures, newest at t0, plus an older
	// success that closes the streak. A cancelled run in the middle must
	// neither break nor extend it.
	for i := 0; i < 6; i++ {
		id := int64(100 + i)
		runs = append(runs, schedRun(id, 1, "failure", t0.Add(-day(float64(i)))))
		jobsByRun[id] = []Job{jobAt(id, "nightly", 1, t0.Add(-day(float64(i))), 7.5)} // ceil -> 8 billable
	}
	runs = append(runs, schedRun(150, 1, "cancelled", t0.Add(-day(5.5))))
	runs = append(runs, schedRun(151, 1, "success", t0.Add(-day(6))))
	// WF 2: 6 failures but all within one afternoon — span gate must drop it.
	for i := 0; i < 6; i++ {
		runs = append(runs, schedRun(int64(200+i), 2, "failure", t0.Add(-min(float64(i*10)))))
	}
	// WF 3: only 4 consecutive failures (below the streak gate).
	for i := 0; i < 4; i++ {
		runs = append(runs, schedRun(int64(300+i), 3, "failure", t0.Add(-day(float64(i)))))
	}
	runs = append(runs, schedRun(304, 3, "success", t0.Add(-day(4))))
	// WF 4: newest scheduled run SUCCEEDS — not a zombie no matter the history.
	runs = append(runs, schedRun(400, 4, "success", t0))
	for i := 1; i < 8; i++ {
		runs = append(runs, schedRun(int64(400+i), 4, "failure", t0.Add(-day(float64(i)))))
	}
	// A pull_request failure stream must be ignored entirely.
	for i := 0; i < 6; i++ {
		runs = append(runs, prRun(int64(500+i), 5, "octo/f", "b", "s", "failure", t0.Add(-day(float64(i))), 5))
	}

	var a Analysis
	a.computeZombieCrons(runs, jobsByRun)
	if len(a.ZombieCrons) != 1 {
		t.Fatalf("ZombieCrons = %+v, want exactly 1 (wf 1)", a.ZombieCrons)
	}
	z := a.ZombieCrons[0]
	if z.Workflow != "cron-1" || z.Fails != 6 {
		t.Errorf("zombie = %s/%d fails, want cron-1/6", z.Workflow, z.Fails)
	}
	if z.StreakOpen {
		t.Error("StreakOpen = true, but an older success closed the streak")
	}
	if z.SpanDays < 4.9 || z.SpanDays > 5.1 {
		t.Errorf("SpanDays = %v, want ~5", z.SpanDays)
	}
	if z.MedianMinutes != 8 {
		t.Errorf("MedianMinutes = %v, want 8 (ceil of 7.5)", z.MedianMinutes)
	}
	// cadence = 5 days / 5 gaps = 1/day -> 30 runs/mo * 8 min = 240 min/mo.
	if z.EstMinPerMo < 239 || z.EstMinPerMo > 241 {
		t.Errorf("EstMinPerMo = %v, want ~240", z.EstMinPerMo)
	}
	if z.EstUSDPerMo < 1.9 || z.EstUSDPerMo > 1.95 {
		t.Errorf("EstUSDPerMo = %v, want ~1.92", z.EstUSDPerMo)
	}
	if z.LastFailedAt != t0 {
		t.Errorf("LastFailedAt = %v, want %v", z.LastFailedAt, t0)
	}
}

func TestComputeZombieCronsOpenStreak(t *testing.T) {
	// Every sampled scheduled run fails: the streak reaches the sample
	// edge and must be flagged as possibly longer.
	var runs []Run
	for i := 0; i < 5; i++ {
		runs = append(runs, schedRun(int64(1+i), 1, "failure", t0.Add(-day(float64(i)))))
	}
	var a Analysis
	a.computeZombieCrons(runs, map[int64][]Job{})
	if len(a.ZombieCrons) != 1 {
		t.Fatalf("ZombieCrons = %+v, want 1", a.ZombieCrons)
	}
	if !a.ZombieCrons[0].StreakOpen {
		t.Error("StreakOpen = false, want true when no success was sampled")
	}
	if a.ZombieCrons[0].MedianMinutes != 0 {
		t.Errorf("MedianMinutes = %v, want 0 with no job data", a.ZombieCrons[0].MedianMinutes)
	}
}

func TestComputeZombieCronsTimedOutCounts(t *testing.T) {
	var runs []Run
	for i := 0; i < 5; i++ {
		c := "failure"
		if i%2 == 0 {
			c = "timed_out"
		}
		runs = append(runs, schedRun(int64(1+i), 1, c, t0.Add(-day(float64(i)))))
	}
	var a Analysis
	a.computeZombieCrons(runs, map[int64][]Job{})
	if len(a.ZombieCrons) != 1 || a.ZombieCrons[0].Fails != 5 {
		t.Fatalf("ZombieCrons = %+v, want one 5-fail streak incl. timed_out", a.ZombieCrons)
	}
}
