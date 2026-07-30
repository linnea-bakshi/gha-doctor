package api

import (
	"strings"
	"testing"
)

// mkMatrixRuns builds n identical clean runs of workflow "CI" whose "test"
// matrix has the given shard durations (suffix -> minutes).
func mkMatrixRuns(n int, shards map[string]float64) ([]Run, map[int64][]Job) {
	var runs []Run
	jobs := map[int64][]Job{}
	for i := 0; i < n; i++ {
		id := int64(i + 1)
		runs = append(runs, mkRun(id, "CI", "sha", "success", 10))
		for suffix, dur := range shards {
			jobs[id] = append(jobs[id], mkJob(id, "test ("+suffix+")", "success", 1, dur))
		}
	}
	return runs, jobs
}

func TestMatrixImbalanceFlagged(t *testing.T) {
	runs, jobs := mkMatrixRuns(6, map[string]float64{
		"windows-latest, 3.12": 10,
		"ubuntu-latest, 3.12":  2,
		"ubuntu-latest, 3.11":  2,
		"macos-latest, 3.12":   2,
	})
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix == nil {
		t.Fatal("Matrix = nil, want measured")
	}
	if a.Matrix.GroupsMeasured != 1 || len(a.Matrix.Imbalanced) != 1 {
		t.Fatalf("measured=%d imbalanced=%d, want 1/1", a.Matrix.GroupsMeasured, len(a.Matrix.Imbalanced))
	}
	g := a.Matrix.Imbalanced[0]
	if g.Workflow != "CI" || g.Job != "test" || g.Shards != 4 || g.RunsMeasured != 6 {
		t.Errorf("group = %+v", g)
	}
	approx(t, "wall", g.P50WallMin, 10)
	approx(t, "ideal", g.P50IdealMin, 4)
	approx(t, "saving", g.P50SavingMin, 6)
	approx(t, "ratio", g.Ratio, 2.5)
	if !strings.Contains(g.SlowestShard, "windows-latest") {
		t.Errorf("SlowestShard = %q, want windows shard", g.SlowestShard)
	}
	approx(t, "slowestP50", g.SlowestP50, 10)
	approx(t, "fastestP50", g.FastestP50, 2)
	if !strings.HasPrefix(g.SlowestShard, "(") || !strings.HasSuffix(g.SlowestShard, ")") || strings.HasPrefix(g.SlowestShard, "((") {
		t.Errorf("SlowestShard = %q, want exactly one leading paren", g.SlowestShard)
	}
}

func TestMatrixBalancedNotFlagged(t *testing.T) {
	runs, jobs := mkMatrixRuns(6, map[string]float64{
		"a": 5, "b": 5.5, "c": 4.8,
	})
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix == nil || a.Matrix.GroupsMeasured != 1 {
		t.Fatalf("Matrix = %+v, want 1 measured group", a.Matrix)
	}
	if len(a.Matrix.Imbalanced) != 0 {
		t.Errorf("Imbalanced = %+v, want none", a.Matrix.Imbalanced)
	}
}

func TestMatrixBigRatioTinyMinutesNotFlagged(t *testing.T) {
	// 4x ratio but the straggler only costs ~0.45m — noise, not a finding.
	runs, jobs := mkMatrixRuns(6, map[string]float64{
		"a": 0.6, "b": 0.15, "c": 0.15,
	})
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix == nil || len(a.Matrix.Imbalanced) != 0 {
		t.Fatalf("Matrix = %+v, want measured but not flagged", a.Matrix)
	}
}

func TestMatrixTooFewRunsNotMeasured(t *testing.T) {
	runs, jobs := mkMatrixRuns(4, map[string]float64{"a": 10, "b": 2, "c": 2})
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix != nil {
		t.Fatalf("Matrix = %+v, want nil below %d runs", a.Matrix, matrixMinRuns)
	}
}

func TestMatrixTwoShardsNotMeasured(t *testing.T) {
	// Two shards of different platforms are expected to differ.
	runs, jobs := mkMatrixRuns(6, map[string]float64{"windows": 10, "linux": 2})
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix != nil {
		t.Fatalf("Matrix = %+v, want nil below %d shards", a.Matrix, matrixMinShards)
	}
}

func TestMatrixFailedShardExcludesRun(t *testing.T) {
	runs, jobs := mkMatrixRuns(6, map[string]float64{"a": 10, "b": 2, "c": 2})
	// Two extra runs with one failed shard: the survivors' timings would
	// fake balance (or imbalance), so the whole run must be skipped.
	for id := int64(7); id <= 8; id++ {
		runs = append(runs, mkRun(id, "CI", "sha", "failure", 3))
		jobs[id] = []Job{
			mkJob(id, "test (a)", "failure", 1, 3),
			mkJob(id, "test (b)", "success", 1, 2),
			mkJob(id, "test (c)", "success", 1, 2),
		}
	}
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix == nil || len(a.Matrix.Imbalanced) != 1 {
		t.Fatalf("Matrix = %+v, want 1 imbalanced", a.Matrix)
	}
	if got := a.Matrix.Imbalanced[0].RunsMeasured; got != 6 {
		t.Errorf("RunsMeasured = %d, want 6 (failed-shard runs excluded)", got)
	}
}

func TestMatrixSkippedShardIgnoredButRunStillClean(t *testing.T) {
	runs, jobs := mkMatrixRuns(6, map[string]float64{"a": 10, "b": 2, "c": 2})
	for id := int64(1); id <= 6; id++ {
		jobs[id] = append(jobs[id], mkJob(id, "test (d)", "skipped", 1, 0))
	}
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix == nil || len(a.Matrix.Imbalanced) != 1 {
		t.Fatalf("Matrix = %+v, want 1 imbalanced despite skipped shard", a.Matrix)
	}
	if got := a.Matrix.Imbalanced[0].Shards; got != 3 {
		t.Errorf("Shards = %d, want 3 (skipped shard not counted)", got)
	}
}

func TestMatrixOldAttemptsExcluded(t *testing.T) {
	runs, jobs := mkMatrixRuns(6, map[string]float64{"a": 10, "b": 2, "c": 2})
	// Run 1 was re-run: attempt-1 jobs linger in the job list and would
	// double-count shards if not filtered to the latest attempt.
	for i := range jobs[1] {
		jobs[1][i].RunAttempt = 2
	}
	jobs[1] = append(jobs[1],
		mkJob(1, "test (a)", "failure", 1, 1),
		mkJob(1, "test (b)", "success", 1, 2),
		mkJob(1, "test (c)", "success", 1, 2))
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix == nil || len(a.Matrix.Imbalanced) != 1 {
		t.Fatalf("Matrix = %+v, want 1 imbalanced", a.Matrix)
	}
	if got := a.Matrix.Imbalanced[0].RunsMeasured; got != 6 {
		t.Errorf("RunsMeasured = %d, want 6 (attempt-2 clean run still counted)", got)
	}
}

func TestMatrixNonMatrixJobsIgnored(t *testing.T) {
	var runs []Run
	jobs := map[int64][]Job{}
	for i := 0; i < 6; i++ {
		id := int64(i + 1)
		runs = append(runs, mkRun(id, "CI", "sha", "success", 10))
		jobs[id] = []Job{
			mkJob(id, "build", "success", 1, 8),
			mkJob(id, "lint", "success", 1, 1),
		}
	}
	a := &Analysis{}
	a.computeMatrixBalance(runs, jobs)
	if a.Matrix != nil {
		t.Fatalf("Matrix = %+v, want nil (no matrix jobs)", a.Matrix)
	}
}

func TestModalInt(t *testing.T) {
	if got := modalInt([]int{4, 4, 4, 5, 5}); got != 4 {
		t.Errorf("modalInt = %d, want 4", got)
	}
	// Tie goes to the larger value: a growing matrix isn't undercounted.
	if got := modalInt([]int{4, 4, 6, 6}); got != 6 {
		t.Errorf("modalInt tie = %d, want 6", got)
	}
}
