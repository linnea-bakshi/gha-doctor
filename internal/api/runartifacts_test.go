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
	return artifactDeepServerNamed(t, "test", logText, arts, zips, hits)
}

// artifactDeepServerNamed is artifactDeepServer with a configurable failed
// job name (affinity ranking keys on it).
func artifactDeepServerNamed(t *testing.T, jobName, logText string, arts []Artifact, zips map[int64][]byte, hits map[string]int) *Client {
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
			ID: 771, Name: jobName, RunAttempt: 1, Status: "completed", Conclusion: "failure",
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

func TestArtifactAffinityRanking(t *testing.T) {
	// Six same-rank "test-results-*" candidates; the failing shard's own
	// artifact is the LARGEST, so size-only ordering would sort it last
	// and the download cap (4) would drop it. Affinity with the failed
	// job's name must rank it first.
	failJunit := `<testsuite><testcase classname="karma.Safari" name="clone works"><failure message="disconnect"/></testcase></testsuite>`
	okJunit := `<testsuite><testcase classname="karma.Other" name="ok"/></testsuite>`
	failZip := buildZip(t, map[string]string{"junit.xml": failJunit})
	okZip := buildZip(t, map[string]string{"junit.xml": okJunit})
	arts := []Artifact{
		{ID: 1, Name: "test-results-chrome", SizeInBytes: 10},
		{ID: 2, Name: "test-results-firefox", SizeInBytes: 11},
		{ID: 3, Name: "test-results-edge", SizeInBytes: 12},
		{ID: 4, Name: "test-results-jest-ubuntu", SizeInBytes: 13},
		{ID: 5, Name: "test-results-webkit", SizeInBytes: 14},
		{ID: 6, Name: "test-results-bs_safari", SizeInBytes: int64(len(failZip)) + 100000},
	}
	zips := map[int64][]byte{1: okZip, 2: okZip, 3: okZip, 4: okZip, 5: okZip, 6: failZip}
	hits := map[string]int{}
	c := artifactDeepServerNamed(t, "Test (bs_safari)", "gcc: error: unrelated\n", arts, zips, hits)
	run, err := c.GetRun("o", "r", 77)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits["zip-6"] != 1 {
		t.Fatalf("failing shard's artifact was not downloaded (hits=%v) — affinity ranking must keep it inside the cap", hits)
	}
	if len(d.ArtifactTests) != 1 || d.ArtifactTests[0].Name != "karma.Safari.clone works" || d.ArtifactTests[0].Artifact != "test-results-bs_safari" {
		t.Fatalf("ArtifactTests = %+v, want the bs_safari failure", d.ArtifactTests)
	}
}

func TestArtifactTokensAndAffinity(t *testing.T) {
	toks := artifactTokens("test-results-bs_safari")
	for _, want := range []string{"bs", "safari"} {
		if !toks[want] {
			t.Errorf("artifactTokens missing %q: %v", want, toks)
		}
	}
	if toks["test"] || toks["results"] {
		t.Errorf("generic tokens must be dropped: %v", toks)
	}
	failed := []map[string]bool{artifactTokens("Test (bs_safari)")}
	if got := artifactJobAffinity("test-results-bs_safari", failed); got != 2 {
		t.Errorf("affinity(bs_safari artifact) = %d, want 2 (bs + safari)", got)
	}
	if got := artifactJobAffinity("test-results-chrome", failed); got != 0 {
		t.Errorf("affinity(chrome artifact) = %d, want 0", got)
	}
}

// A truncated scan must caveat the list it produced: the jans artifact
// that motivated this carried 650 XML files against the old 200-file cap
// and reported 527 of 3,096 failing tests with no hint of truncation.
func TestArtifactTestsTruncationNote(t *testing.T) {
	oldFiles := maxJUnitZipFiles
	maxJUnitZipFiles = 2
	defer func() { maxJUnitZipFiles = oldFiles }()
	files := map[string]string{}
	for i := 0; i < 4; i++ {
		files[fmt.Sprintf("TEST-s%d.xml", i)] = fmt.Sprintf(
			`<testsuite name="s%d"><testcase classname="C%d" name="t"><failure/></testcase></testsuite>`, i, i)
	}
	zipData := buildZip(t, files)
	arts := []Artifact{{ID: 2, Name: "test-results", SizeInBytes: int64(len(zipData))}}
	c := artifactDeepServer(t, "make: *** [all] Error 2\n", arts, map[int64][]byte{2: zipData}, nil)
	run, err := c.GetRun("o", "r", 77)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ArtifactTests) != 2 {
		t.Fatalf("ArtifactTests = %+v, want the 2 within budget", d.ArtifactTests)
	}
	if !strings.Contains(d.ArtifactTestNote, "may be incomplete") {
		t.Errorf("note = %q, want the truncation caveat alongside the named tests", d.ArtifactTestNote)
	}
}
