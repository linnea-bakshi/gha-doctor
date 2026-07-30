package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

var deepT0 = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

// deepServer serves a target run (attempt 2: one job re-ran, one carried
// over from attempt 1) plus 4 successful baseline runs of the same
// workflow whose "test" job has a stable step profile.
func deepServer(t *testing.T) *Client {
	t.Helper()
	mkStep := func(name string, num int, startOff, durSec int, concl string) Step {
		st := deepT0.Add(time.Duration(startOff) * time.Second)
		return Step{
			Name: name, Number: num, Status: "completed", Conclusion: concl,
			StartedAt: st, CompletedAt: st.Add(time.Duration(durSec) * time.Second),
		}
	}
	targetJobs := []Job{
		{ // attempt-1 execution of the re-run job (superseded)
			ID: 1, Name: "test", RunAttempt: 1, Status: "completed", Conclusion: "failure",
			CreatedAt: deepT0.Add(-time.Hour), StartedAt: deepT0.Add(-time.Hour),
			CompletedAt: deepT0.Add(-time.Hour + 60*time.Second),
		},
		{ // carried over from attempt 1, not re-run
			ID: 2, Name: "lint", RunAttempt: 1, Status: "completed", Conclusion: "success",
			CreatedAt: deepT0.Add(-time.Hour), StartedAt: deepT0.Add(-time.Hour),
			CompletedAt: deepT0.Add(-time.Hour + 30*time.Second),
		},
		{ // attempt-2 execution: queued 10s, ran 120s
			ID: 3, Name: "test", RunAttempt: 2, Status: "completed", Conclusion: "success",
			CreatedAt: deepT0, StartedAt: deepT0.Add(10 * time.Second),
			CompletedAt: deepT0.Add(130 * time.Second),
			Steps: []Step{
				mkStep("Set up job", 1, 10, 2, "success"),
				mkStep("Run tests", 2, 12, 100, "success"), // baseline p50 is 25s → 4x slower
				mkStep("skipped step", 3, 112, 0, "skipped"),
			},
		},
	}
	baseJobs := func(runID int64) []Job {
		start := deepT0.Add(-time.Duration(runID) * time.Hour)
		return []Job{{
			ID: runID * 10, Name: "test", RunAttempt: 1, Status: "completed", Conclusion: "success",
			CreatedAt: start, StartedAt: start.Add(5 * time.Second),
			CompletedAt: start.Add(65 * time.Second), // 60s duration
			Steps: []Step{
				mkStep("Run tests", 2, 0, 25, "success"),
			},
		}}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/actions/runs/99", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Run{
			ID: 99, Name: "CI", WorkflowID: 7, RunNumber: 42, RunAttempt: 2,
			Status: "completed", Conclusion: "success", Event: "push",
			RunStartedAt: deepT0, UpdatedAt: deepT0.Add(130 * time.Second),
		})
	})
	mux.HandleFunc("/repos/o/r/actions/runs/99/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobs": targetJobs})
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/7/runs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("status"); got != "success" {
			t.Errorf("baseline status param = %q, want success", got)
		}
		var runs []Run
		runs = append(runs, Run{ID: 99, WorkflowID: 7, RunStartedAt: deepT0}) // target must be excluded
		for i := int64(1); i <= 4; i++ {
			runs = append(runs, Run{ID: i, WorkflowID: 7, RunStartedAt: deepT0.Add(-time.Duration(i) * time.Hour)})
		}
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": runs})
	})
	for i := int64(1); i <= 4; i++ {
		id := i
		mux.HandleFunc(fmt.Sprintf("/repos/o/r/actions/runs/%d/jobs", id), func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"jobs": baseJobs(id)})
		})
	}
	c, srv := testClient(mux)
	t.Cleanup(srv.Close)
	return c
}

func TestAnalyzeRun(t *testing.T) {
	c := deepServer(t)
	run, err := c.GetRun("o", "r", 99)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2", d.Attempt)
	}
	// Only the attempt-2 job belongs on the timeline; lint carried over.
	if len(d.Jobs) != 1 || d.Jobs[0].Name != "test" {
		t.Fatalf("Jobs = %+v, want just the attempt-2 'test' job", d.Jobs)
	}
	if d.CarriedJobs != 1 {
		t.Errorf("CarriedJobs = %d, want 1", d.CarriedJobs)
	}
	if d.RetriedJobs != 1 {
		t.Errorf("RetriedJobs = %d, want 1", d.RetriedJobs)
	}
	j := d.Jobs[0]
	if j.QueueSec != 10 {
		t.Errorf("QueueSec = %.0f, want 10", j.QueueSec)
	}
	if j.StartSec != 10 || j.DurSec != 120 || j.EndSec != 130 {
		t.Errorf("timeline = start %.0f dur %.0f end %.0f, want 10/120/130", j.StartSec, j.DurSec, j.EndSec)
	}
	if d.WallSec != 130 {
		t.Errorf("WallSec = %.0f, want 130", d.WallSec)
	}
	// Baseline: 4 comparable runs, job p50 60s, wall p50 65s.
	if d.BaselineRuns != 4 {
		t.Errorf("BaselineRuns = %d, want 4", d.BaselineRuns)
	}
	if d.BaselineWallP50 != 65 {
		t.Errorf("BaselineWallP50 = %.0f, want 65", d.BaselineWallP50)
	}
	if j.BaselineN != 4 || j.P50Sec != 60 {
		t.Errorf("job baseline = n %d p50 %.0f, want 4/60", j.BaselineN, j.P50Sec)
	}
	// Steps: skipped step dropped; "Run tests" compared, "Set up job" has
	// no baseline (absent from baseline runs).
	if len(j.Steps) != 2 {
		t.Fatalf("Steps = %+v, want 2 (skipped dropped)", j.Steps)
	}
	var runTests *DeepStep
	for i := range j.Steps {
		if j.Steps[i].Name == "Run tests" {
			runTests = &j.Steps[i]
		}
	}
	if runTests == nil || runTests.BaselineN != 4 || runTests.P50Sec != 25 || runTests.DurSec != 100 {
		t.Errorf("Run tests step = %+v, want dur 100 vs p50 25 (n=4)", runTests)
	}
}

func TestAnalyzeRunTooFewBaseline(t *testing.T) {
	mux := http.NewServeMux()
	start := deepT0
	mux.HandleFunc("/repos/o/r/actions/runs/5/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{{
			ID: 1, Name: "only", RunAttempt: 1, Status: "completed", Conclusion: "success",
			CreatedAt: start, StartedAt: start, CompletedAt: start.Add(10 * time.Second),
		}}})
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/3/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{}})
	})
	c, srv := testClient(mux)
	defer srv.Close()
	run := &Run{ID: 5, WorkflowID: 3, RunAttempt: 1, Status: "completed", Conclusion: "success", RunStartedAt: start}
	d, err := c.AnalyzeRun("o", "r", run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.BaselineRuns != 0 || d.BaselineWallP50 != 0 {
		t.Errorf("baseline = %d runs p50 %.0f, want none", d.BaselineRuns, d.BaselineWallP50)
	}
	if d.BaselineNote == "" {
		t.Error("BaselineNote empty, want an explanation for the missing comparison")
	}
	if d.Jobs[0].BaselineN != 0 {
		t.Errorf("job BaselineN = %d, want 0 with no baseline", d.Jobs[0].BaselineN)
	}
}

func TestParseRunID(t *testing.T) {
	cases := []struct {
		in     string
		id     int64
		latest bool
		err    bool
	}{
		{"latest", 0, true, false},
		{"LATEST", 0, true, false},
		{"12345", 12345, false, false},
		{"https://github.com/o/r/actions/runs/678", 678, false, false},
		{"abc", 0, false, true},
		{"-3", 0, false, true},
		{"", 0, false, true},
	}
	for _, c := range cases {
		id, latest, err := ParseRunID(c.in)
		if (err != nil) != c.err || id != c.id || latest != c.latest {
			t.Errorf("ParseRunID(%q) = (%d, %v, %v), want (%d, %v, err=%v)", c.in, id, latest, err, c.id, c.latest, c.err)
		}
	}
}
