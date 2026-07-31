package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseTestFailuresPytest(t *testing.T) {
	log := logts(
		"=========================== short test summary info ============================",
		"FAILED tests/test_socket.py::TestSocket::test_timeout - TimeoutError: timed out",
		"ERROR tests/test_setup.py::test_fixture",
		"PASSED tests/test_ok.py::test_fine",
		"= 1 failed, 1 error in 3.21s =",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"pytest", "tests/test_socket.py::TestSocket::test_timeout"},
		{"pytest", "tests/test_setup.py::test_fixture"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresPytestVerbose(t *testing.T) {
	log := logts(
		"tests/test_a.py::test_one PASSED [ 50%]",
		"tests/test_a.py::test_two FAILED [100%]",
	)
	got := parseTestFailures(log)
	if len(got) != 1 || got[0].name != "tests/test_a.py::test_two" {
		t.Errorf("got %v", got)
	}
}

func TestParseTestFailuresGoSubtestCollapse(t *testing.T) {
	log := logts(
		"--- FAIL: TestServer (2.31s)",
		"    --- FAIL: TestServer/retries (1.11s)",
		"--- FAIL: TestOther (0.10s)",
		"FAIL",
		"FAIL\tgithub.com/x/y\t3.421s",
	)
	got := parseTestFailures(log)
	// TestServer is a parent of a captured subtest -> dropped; TestOther kept.
	want := []testFailure{
		{"go", "TestServer/retries"},
		{"go", "TestOther"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresCargoJestRspecMaven(t *testing.T) {
	log := logts(
		"test net::tests::resolver_falls_back ... FAILED",
		"test net::tests::resolver_ok ... ok",
		"  ✕ retries the request on 503 (43 ms)",
		"  ✓ succeeds on 200 (3 ms)",
		"Failed examples:",
		"rspec ./spec/models/user_spec.rb:42 # User validates email uniqueness",
		"[ERROR]   OrderServiceTest.testConcurrentCheckout:118 expected:<2> but was:<1>",
		"[ERROR] Tests run: 40, Failures: 1, Errors: 0, Skipped: 2",
		"  1) [chromium] \u203a tests\\ui-mode.spec.ts:827:5 \u203a should update state \u2500\u2500\u2500",
		"  2) tests/basic.spec.ts:12:3 \u203a loads the page",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"cargo", "net::tests::resolver_falls_back"},
		{"jest", "retries the request on 503"},
		{"rspec", "User validates email uniqueness"},
		{"maven", "OrderServiceTest.testConcurrentCheckout"},
		{"playwright", "[chromium] \u203a tests\\ui-mode.spec.ts \u203a should update state"},
		{"playwright", "tests/basic.spec.ts \u203a loads the page"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresANSIAndDedup(t *testing.T) {
	log := logts(
		"\x1b[31mFAILED\x1b[0m tests/test_x.py::test_y - AssertionError",
		"FAILED tests/test_x.py::test_y - AssertionError",
	)
	got := parseTestFailures(log)
	if len(got) != 1 || got[0].name != "tests/test_x.py::test_y" {
		t.Errorf("ANSI/dedup failed: %v", got)
	}
}

func TestPickFlakyFailLogsRoundRobin(t *testing.T) {
	now := time.Now()
	mk := func(id int64, name string, age time.Duration) flakyFail {
		return flakyFail{job: Job{ID: id, Name: name, CompletedAt: now.Add(-age)}, wf: "ci", sha: "s"}
	}
	fails := []flakyFail{
		mk(1, "a", 3*time.Hour), mk(2, "a", 1*time.Hour), mk(3, "a", 2*time.Hour),
		mk(4, "b (1, x)", 1*time.Hour),
	}
	got := pickFlakyFailLogs(fails, 3)
	// Round 0: newest "a" (id 2) + newest "b" (id 4); round 1: next "a" (id 3).
	if len(got) != 3 || got[0].job.ID != 2 || got[1].job.ID != 4 || got[2].job.ID != 3 {
		t.Errorf("got %v", []int64{got[0].job.ID, got[1].job.ID, got[2].job.ID})
	}
}

func TestAnalyzeFlakyLogsEndToEnd(t *testing.T) {
	failLog := logts(
		"--- FAIL: TestFlaky (1.02s)",
		"FAIL",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/jobs/1/logs"),
			strings.HasSuffix(r.URL.Path, "/jobs/2/logs"):
			fmt.Fprint(w, failLog)
		case strings.HasSuffix(r.URL.Path, "/jobs/3/logs"):
			http.NotFound(w, r) // expired log
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	now := time.Now()
	fails := []flakyFail{
		{job: Job{ID: 1, Name: "test (ubuntu)", CompletedAt: now}, wf: "ci", sha: "aaa"},
		{job: Job{ID: 2, Name: "test (ubuntu)", CompletedAt: now.Add(-time.Hour)}, wf: "ci", sha: "bbb"},
		{job: Job{ID: 3, Name: "test (macos)", CompletedAt: now}, wf: "ci", sha: "aaa"},
	}
	st := c.analyzeFlakyLogs("o", "r", fails, 10, func(string) {})
	if !st.Available {
		t.Fatalf("not available: %s", st.Note)
	}
	if st.LogsTotal != 3 || st.LogsSampled != 2 || st.JobsSkipped != 1 {
		t.Errorf("total=%d sampled=%d skipped=%d", st.LogsTotal, st.LogsSampled, st.JobsSkipped)
	}
	if len(st.Tests) != 1 {
		t.Fatalf("tests = %+v", st.Tests)
	}
	tt := st.Tests[0]
	if tt.Name != "TestFlaky" || tt.Framework != "go" || tt.Failures != 2 || tt.Commits != 2 {
		t.Errorf("test = %+v", tt)
	}
	if len(tt.Jobs) != 1 || tt.Jobs[0] != "test" {
		t.Errorf("jobs = %v (matrix should collapse to base name)", tt.Jobs)
	}
}

func TestAnalyzeFlakyLogsNoToken(t *testing.T) {
	c := &Client{Token: ""}
	st := c.analyzeFlakyLogs("o", "r", []flakyFail{{}}, 5, func(string) {})
	if st.Available || !strings.Contains(st.Note, "auth") {
		t.Errorf("st = %+v", st)
	}
}

func TestAnalyzeFlakyLogsNoFlakes(t *testing.T) {
	c := &Client{Token: "t"}
	st := c.analyzeFlakyLogs("o", "r", nil, 5, func(string) {})
	if st.Available || !strings.Contains(st.Note, "no flaky-job failures") {
		t.Errorf("st = %+v", st)
	}
}

func TestAnalyzeFlakyLogsUnrecognized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, logts("make: *** [all] Error 2"))
	}))
	defer srv.Close()
	c := &Client{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	st := c.analyzeFlakyLogs("o", "r", []flakyFail{
		{job: Job{ID: 1, Name: "build", CompletedAt: time.Now()}, wf: "ci", sha: "aaa"},
	}, 5, func(string) {})
	if !st.Available || len(st.Tests) != 0 {
		t.Fatalf("st = %+v", st)
	}
	if !strings.Contains(st.Note, "no recognizable test failures") {
		t.Errorf("note = %q", st.Note)
	}
}
