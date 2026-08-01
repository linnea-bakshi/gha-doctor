package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var logT0 = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

func stampedLog(startOff int, lines ...string) string {
	var b strings.Builder
	for i, l := range lines {
		ts := logT0.Add(time.Duration(startOff+i) * time.Second)
		fmt.Fprintf(&b, "%s %s\r\n", ts.Format("2006-01-02T15:04:05.0000000Z"), l)
	}
	return b.String()
}

func TestFailLogTailWindowSlicing(t *testing.T) {
	// Step 1: lines 0-9. Failing step 2: lines 10-19. Post cleanup: 20+.
	text := stampedLog(0,
		"setup a", "setup b", "setup c", "setup d", "setup e",
		"setup f", "setup g", "setup h", "setup i", "setup j",
		"go test ./...", "--- FAIL: TestThing", "    thing_test.go:12: got 1, want 2",
		"FAIL", "exit status 1",
		"##[error]Process completed with exit code 1.", "after1", "after2", "after3", "after4",
		"Post job cleanup.", "cleanup done")
	start := logT0.Add(10 * time.Second)
	end := logT0.Add(19 * time.Second)

	tail := failLogTail(text, start, end, 50)
	if len(tail) == 0 {
		t.Fatal("empty tail")
	}
	joined := strings.Join(tail, "\n")
	if strings.Contains(joined, "Post job cleanup") {
		t.Errorf("tail leaked past the step window:\n%s", joined)
	}
	if !strings.Contains(joined, "--- FAIL: TestThing") {
		t.Errorf("tail missing the failure line:\n%s", joined)
	}
	// Error anchoring: ends a few lines after the ##[error] marker, so
	// after4 (4 lines past it) must be cut.
	if strings.Contains(joined, "after4") {
		t.Errorf("tail not anchored on ##[error]:\n%s", joined)
	}
	if !strings.Contains(joined, "##[error]Process completed") {
		t.Errorf("tail missing the error marker:\n%s", joined)
	}
	// Slack means a line or two of setup may leak in; the bulk must not.
	if strings.Contains(joined, "setup a") {
		t.Errorf("tail contains lines long before the step:\n%s", joined)
	}

	// n limits the tail length.
	short := failLogTail(text, start, end, 3)
	if len(short) != 3 {
		t.Errorf("len(tail) = %d, want 3", len(short))
	}
	if !strings.Contains(strings.Join(short, "\n"), "##[error]") {
		t.Errorf("short tail lost the error line: %v", short)
	}
}

func TestFailLogTailFallbackWithoutWindow(t *testing.T) {
	text := stampedLog(0, "a", "b", "c", "d")
	// Step window far outside the log's timestamps → whole-log fallback.
	start := logT0.Add(10 * time.Hour)
	tail := failLogTail(text, start, start.Add(time.Minute), 2)
	if len(tail) != 2 || tail[0] != "c" || tail[1] != "d" {
		t.Errorf("fallback tail = %v, want [c d]", tail)
	}
	// Zero start time = no window at all.
	tail = failLogTail(text, time.Time{}, time.Time{}, 10)
	if len(tail) != 4 || tail[0] != "a" {
		t.Errorf("no-window tail = %v, want all 4 lines", tail)
	}
}

func TestFailLogTailLongLinesAndBlanks(t *testing.T) {
	long := strings.Repeat("x", maxLogLineLen+50)
	text := stampedLog(0, "ok", long, "", "", "")
	tail := failLogTail(text, time.Time{}, time.Time{}, 10)
	if len(tail) != 2 {
		t.Fatalf("trailing blanks not dropped: %d lines", len(tail))
	}
	if len(tail[1]) != maxLogLineLen+len("…") {
		t.Errorf("long line not truncated: %d chars", len(tail[1]))
	}
	if failLogTail(text, time.Time{}, time.Time{}, 0) != nil {
		t.Error("n=0 should return nil")
	}
}

// failServer: a failed run whose "test" job fails at "Run tests", plus a
// logs endpoint. Baseline list is empty (not under test here).
func failServer(t *testing.T, wantAuth string) *Client {
	t.Helper()
	return failServerBody(t, wantAuth, stampedLog(2, "Set up runner")+stampedLog(10,
		"go test ./...",
		"--- FAIL: TestThing",
		"##[error]Process completed with exit code 1."))
}

// failServerWithLog is failServer with a custom job-log body and no
// Authorization assertion.
func failServerWithLog(t *testing.T, body string) *Client {
	t.Helper()
	return failServerBody(t, "", body)
}

func failServerBody(t *testing.T, wantAuth, body string) *Client {
	t.Helper()
	stepStart := logT0.Add(10 * time.Second)
	jobs := []Job{{
		ID: 7, Name: "test", RunAttempt: 1, Status: "completed", Conclusion: "failure",
		CreatedAt: logT0, StartedAt: logT0.Add(2 * time.Second), CompletedAt: logT0.Add(40 * time.Second),
		Steps: []Step{
			{Name: "Set up job", Number: 1, Status: "completed", Conclusion: "success",
				StartedAt: logT0.Add(2 * time.Second), CompletedAt: stepStart},
			{Name: "Run tests", Number: 2, Status: "completed", Conclusion: "failure",
				StartedAt: stepStart, CompletedAt: logT0.Add(40 * time.Second)},
		},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/actions/runs/5/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/7/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{}})
	})
	mux.HandleFunc("/repos/o/r/actions/jobs/7/logs", func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" {
			if got := r.Header.Get("Authorization"); got != wantAuth {
				t.Errorf("logs Authorization = %q, want %q", got, wantAuth)
			}
		}
		fmt.Fprint(w, body)
	})
	c, srv := testClient(mux)
	t.Cleanup(srv.Close)
	return c
}

func TestAnalyzeRunAttachesFailLog(t *testing.T) {
	c := failServer(t, "Bearer tok")
	c.Token = "tok"
	run := &Run{ID: 5, Name: "CI", WorkflowID: 7, RunAttempt: 1, Status: "completed",
		Conclusion: "failure", RunStartedAt: logT0}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Jobs) != 1 {
		t.Fatalf("Jobs = %d, want 1", len(d.Jobs))
	}
	j := d.Jobs[0]
	if j.LogStep != "Run tests" {
		t.Errorf("LogStep = %q, want %q", j.LogStep, "Run tests")
	}
	joined := strings.Join(j.LogTail, "\n")
	if !strings.Contains(joined, "--- FAIL: TestThing") || !strings.Contains(joined, "##[error]") {
		t.Errorf("LogTail = %v, want failure lines", j.LogTail)
	}
	if strings.Contains(joined, "Set up runner") {
		t.Errorf("LogTail includes lines from before the failing step: %v", j.LogTail)
	}
	if d.LogNote != "" {
		t.Errorf("LogNote = %q, want empty when a token is present", d.LogNote)
	}
	// The same log names the failing test via the shared extractors.
	if len(j.FailedTests) != 1 || j.FailedTests[0].Name != "TestThing" || j.FailedTests[0].Framework != "go" {
		t.Errorf("FailedTests = %+v, want [TestThing (go)]", j.FailedTests)
	}
}

func TestAnalyzeRunFailedTestsCap(t *testing.T) {
	// 25 recognized failures → 20 stored + FailedTestsMore = 5.
	var lines []string
	lines = append(lines, "=========================== short test summary info ============================")
	for i := 0; i < 25; i++ {
		lines = append(lines, fmt.Sprintf("FAILED tests/test_mod.py::test_case_%02d - AssertionError", i))
	}
	c := failServerWithLog(t, stampedLog(10, lines...))
	c.Token = "tok"
	run := &Run{ID: 5, Name: "CI", WorkflowID: 7, RunAttempt: 1, Status: "completed",
		Conclusion: "failure", RunStartedAt: logT0}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	j := d.Jobs[0]
	if len(j.FailedTests) != maxRunFailedTests {
		t.Fatalf("FailedTests = %d, want %d", len(j.FailedTests), maxRunFailedTests)
	}
	if j.FailedTestsMore != 5 {
		t.Errorf("FailedTestsMore = %d, want 5", j.FailedTestsMore)
	}
	if j.FailedTests[0].Framework != "pytest" {
		t.Errorf("Framework = %q, want pytest", j.FailedTests[0].Framework)
	}
}

func TestAnalyzeRunLogNoteWithoutToken(t *testing.T) {
	c := failServer(t, "IGNORED") // logs endpoint must never be hit
	run := &Run{ID: 5, Name: "CI", WorkflowID: 7, RunAttempt: 1, Status: "completed",
		Conclusion: "failure", RunStartedAt: logT0}
	d, err := c.AnalyzeRun("o", "r", run, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.LogNote == "" || !strings.Contains(d.LogNote, "auth") {
		t.Errorf("LogNote = %q, want an auth hint", d.LogNote)
	}
	if len(d.Jobs) != 1 || d.Jobs[0].LogTail != nil {
		t.Errorf("LogTail should be empty without a token")
	}
}

func TestAnalyzeRunLogTailDisabled(t *testing.T) {
	c := failServer(t, "IGNORED")
	c.Token = "tok"
	run := &Run{ID: 5, Name: "CI", WorkflowID: 7, RunAttempt: 1, Status: "completed",
		Conclusion: "failure", RunStartedAt: logT0}
	d, err := c.AnalyzeRun("o", "r", run, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Jobs) != 1 || d.Jobs[0].LogTail != nil || d.LogNote != "" {
		t.Errorf("logTail=0 must fetch nothing (tail=%v note=%q)", d.Jobs[0].LogTail, d.LogNote)
	}
}

func TestFailLogTailStopsAtNextGroup(t *testing.T) {
	// The window's slack catches the first lines of the NEXT step; the
	// after-error context must not include them.
	text := stampedLog(0,
		"FAIL",
		"##[error]Process completed with exit code 1.",
		"##[group]Run actions/upload-artifact@abc",
		"with:",
		"  name: pytest-artifacts")
	tail := failLogTail(text, logT0, logT0.Add(2*time.Second), 20)
	joined := strings.Join(tail, "\n")
	if strings.Contains(joined, "upload-artifact") || strings.Contains(joined, "with:") {
		t.Errorf("tail leaked into the next step's group:\n%s", joined)
	}
	if !strings.Contains(joined, "##[error]") {
		t.Errorf("tail lost the error marker:\n%s", joined)
	}
}
