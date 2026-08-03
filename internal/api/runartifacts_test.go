package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// artifactDeepServer serves a failed run whose job log is an unrecognized
// build failure, plus run artifacts holding a JUnit XML test report.
// logText and artifacts are knobs so tests can flip the gating conditions.
func artifactDeepServer(t *testing.T, logText string, arts []Artifact, zips map[int64][]byte, hits map[string]int) *Client {
	t.Helper()
	t0 := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	count := func(key string) {
		if hits != nil {
			hits[key]++
		}
	}
	mux.HandleFunc("/repos/o/r/actions/runs/77", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Run{
			ID: 77, Name: "CI", WorkflowID: 7, RunNumber: 5, RunAttempt: 1,
			Status: "completed", Conclusion: "failure", Event: "push",
			RunStartedAt: t0, UpdatedAt: t0.Add(100 * time.Second),
		})
	})
	mux.HandleFunc("/repos/o/r/actions/runs/77/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{{
			ID: 771, Name: "test", RunAttempt: 1, Status: "completed", Conclusion: "failure",
			CreatedAt: t0, StartedAt: t0.Add(5 * time.Second), CompletedAt: t0.Add(95 * time.Second),
			Steps: []Step{{
				Name: "Run tests", Number: 1, Status: "completed", Conclusion: "failure",
				StartedAt: t0.Add(5 * time.Second), CompletedAt: t0.Add(95 * time.Second),
			}},
		}}})
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/7/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{}})
	})
	mux.HandleFunc("/repos/o/r/actions/jobs/771/logs", func(w http.ResponseWriter, r *http.Request) {
		count("logs")
		fmt.Fprint(w, logText)
	})
	mux.HandleFunc("/repos/o/r/actions/runs/77/artifacts", func(w http.ResponseWriter, r *http.Request) {
		count("list-artifacts")
		json.NewEncoder(w).Encode(map[string]any{"artifacts": arts})
	})
	for id, data := range zips {
		id, body := id, data
		mux.HandleFunc(fmt.Sprintf("/repos/o/r/actions/artifacts/%d/zip", id), func(w http.ResponseWriter, r *http.Request) {
			count(fmt.Sprintf("zip-%d", id))
			w.Write(body)
		})
	}
	c, srv := testClient(mux)
	c.Token = "test-token"
	t.Cleanup(srv.Close)
	return c
}

func TestArtifactTestsFallback(t *testing.T) {
	junit := `<testsuite><testcase classname="tests.Payment" name="test_refund"><failure message="boom"/></testcase><testcase classname="tests.Payment" name="test_charge"/></testsuite>`
	zipData := buildZip(t, map[string]string{"junit.xml": junit})
	arts := []Artifact{
		{ID: 1, Name: "coverage-report", SizeInBytes: 500}, // excluded by name
		{ID: 2, Name: "test-results (ubuntu)", SizeInBytes: int64(len(zipData))},
	}
	hits := map[string]int{}
	c := artifactDeepServer(t, "gcc: error: something unrelated failed\n", arts, map[int64][]byte{2: zipData}, hits)
	run, err := c.GetRun("o", "r", 77)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Jobs) != 1 || len(d.Jobs[0].FailedTests) != 0 {
		t.Fatalf("precondition broken: log should name no tests, jobs = %+v", d.Jobs)
	}
	if len(d.ArtifactTests) != 1 {
		t.Fatalf("ArtifactTests = %+v, want the one failing junit case", d.ArtifactTests)
	}
	at := d.ArtifactTests[0]
	if at.Name != "tests.Payment.test_refund" || at.Artifact != "test-results (ubuntu)" {
		t.Errorf("artifact test = %+v, want tests.Payment.test_refund from test-results (ubuntu)", at)
	}
	if d.ArtifactTestNote != "" {
		t.Errorf("note should be empty when tests were named, got %q", d.ArtifactTestNote)
	}
}

func TestArtifactTestsSkippedWhenLogsNamedTests(t *testing.T) {
	// pytest short summary — the log extractor names the test, so the
	// artifact endpoints must never be hit.
	logText := "2026-08-03T12:00:00.000Z =========================== short test summary info ============================\n" +
		"2026-08-03T12:00:00.000Z FAILED tests/test_x.py::test_y - AssertionError\n" +
		"2026-08-03T12:00:00.000Z ========================= 1 failed, 3 passed in 2.31s ==========================\n"
	hits := map[string]int{}
	c := artifactDeepServer(t, logText, []Artifact{{ID: 2, Name: "test-results", SizeInBytes: 100}}, nil, hits)
	run, err := c.GetRun("o", "r", 77)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Jobs) != 1 || len(d.Jobs[0].FailedTests) == 0 {
		t.Fatalf("precondition broken: log should name a test, jobs = %+v", d.Jobs)
	}
	if hits["list-artifacts"] != 0 {
		t.Errorf("artifact listing was hit %d times; console output already told the story", hits["list-artifacts"])
	}
	if len(d.ArtifactTests) != 0 {
		t.Errorf("ArtifactTests = %+v, want none", d.ArtifactTests)
	}
}

func TestArtifactTestsNoteWhenReportsRecordNoFailures(t *testing.T) {
	junit := `<testsuite><testcase classname="A" name="ok1"/><testcase classname="A" name="ok2"/></testsuite>`
	zipData := buildZip(t, map[string]string{"junit.xml": junit})
	arts := []Artifact{{ID: 2, Name: "junit-results", SizeInBytes: int64(len(zipData))}}
	c := artifactDeepServer(t, "make: *** [all] Error 2\n", arts, map[int64][]byte{2: zipData}, nil)
	run, err := c.GetRun("o", "r", 77)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ArtifactTests) != 0 {
		t.Fatalf("ArtifactTests = %+v, want none", d.ArtifactTests)
	}
	if !strings.Contains(d.ArtifactTestNote, "no failures") || !strings.Contains(d.ArtifactTestNote, "2 test cases") {
		t.Errorf("note = %q, want the no-failures honesty note with the case count", d.ArtifactTestNote)
	}
}

func TestArtifactTestsNoteWhenExpired(t *testing.T) {
	arts := []Artifact{{ID: 2, Name: "test-results", SizeInBytes: 100, Expired: true}}
	c := artifactDeepServer(t, "make: *** [all] Error 2\n", arts, nil, nil)
	run, err := c.GetRun("o", "r", 77)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.ArtifactTestNote, "expired") {
		t.Errorf("note = %q, want the expired-artifacts note", d.ArtifactTestNote)
	}
}
