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

func TestParseTestFailuresGradle(t *testing.T) {
	// Shapes from a real junit-team/junit5 CI log (2026-07-31).
	log := logts(
		"ParallelExecutionIntegrationTests > [1] executorServiceType = FORK_JOIN_POOL > testCaseWithFactory() FAILED",
		"ParallelExecutionIntegrationTests > [1] executorServiceType = FORK_JOIN_POOL > canRunTestsIsolated() > repetition 1 of 10 FAILED",
		"ParallelExecutionIntegrationTests > [1] executorServiceType = FORK_JOIN_POOL > canRunTestsIsolated() > repetition 2 of 10 FAILED",
		"> Task :platform-tests:test FAILED", // gradle task line, not a test
		"FAILURE: Build failed with an exception.",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"gradle", "ParallelExecutionIntegrationTests > [1] executorServiceType = FORK_JOIN_POOL > testCaseWithFactory()"},
		// repetitions collapse into one entry
		{"gradle", "ParallelExecutionIntegrationTests > [1] executorServiceType = FORK_JOIN_POOL > canRunTestsIsolated()"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresMinitest(t *testing.T) {
	// Failure shape from sidekiq/sidekiq, error shape + numbered header from
	// minitest/minitest CI logs.
	log := logts(
		"Failure:",
		"Sidekiq::edition predicates#test_0001_.pro? / .ent? / .server? reflect whether the matching constant is defined [test/sidekiq_test.rb:143]:",
		"bin/rails test /home/runner/work/sidekiq/sidekiq/test/sidekiq_test.rb:139",
		"",
		"  1) Failure:",
		"TestMinitestTestAssertions#test_autorun_does_not_affect_fork_success_status [test/minitest/test_minitest_test.rb:1083]:",
		"",
		"  2) Error:",
		"TestMinitestUnorderedHash#test_something:",
		"RuntimeError: boom",
		// name-shaped line WITHOUT a preceding header must not match:
		"SomeClass#test_stray [test/x_test.rb:1]:",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"minitest", "Sidekiq::edition predicates#test_0001_.pro? / .ent? / .server? reflect whether the matching constant is defined"},
		{"minitest", "TestMinitestTestAssertions#test_autorun_does_not_affect_fork_success_status"},
		{"minitest", "TestMinitestUnorderedHash#test_something"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresPhpunitSections(t *testing.T) {
	// Section shapes from briannesbitt/carbon (failures + errors + skipped)
	// and laravel/framework (deprecations) CI logs: only failure/error
	// sections name failing tests; skipped/deprecation lists use the same
	// numbered shape and must NOT be extracted.
	log := logts(
		"4 tests triggered 2 PHP deprecations:",
		"",
		"1) /home/runner/work/framework/framework/tests/Image/GdDriverTest.php:478",
		"Function imagedestroy() is deprecated",
		"",
		"There was 1 failure:",
		"",
		"1) Tests\\Carbon\\TestingAidsTest::testSetTestNow",
		"Failed asserting that two strings are identical.",
		"",
		"--",
		"",
		"There were 2 errors:",
		"",
		"1) Tests\\Carbon\\CreateFromFormatTest::testCreateLastErrors with data set #0 ('x')",
		"2) Tests\\CarbonImmutable\\LastErrorTest::testCreateHandlesLastErrors",
		"",
		"--",
		"",
		"There were 6 skipped tests:",
		"",
		"1) Tests\\Carbon\\SkippedTest::testNope",
		"",
		"FAILURES!",
		"Tests: 1155, Assertions: 4000, Errors: 2, Failures: 1, Skipped: 6.",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"phpunit", "Tests\\Carbon\\TestingAidsTest::testSetTestNow"},
		// data-provider suffix dropped so cases aggregate
		{"phpunit", "Tests\\Carbon\\CreateFromFormatTest::testCreateLastErrors"},
		{"phpunit", "Tests\\CarbonImmutable\\LastErrorTest::testCreateHandlesLastErrors"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresPhpunitPhpt(t *testing.T) {
	// sebastianbergmann/phpunit end-to-end suite names .phpt files.
	log := logts(
		"There was 1 failure:",
		"",
		"1) D:\\a\\phpunit\\phpunit\\tests\\end-to-end\\cli\\coverage\\coverage-missing-driver.phpt",
		"Failed asserting that string matches format description.",
	)
	got := parseTestFailures(log)
	if len(got) != 1 || got[0].framework != "phpunit" || !strings.HasSuffix(got[0].name, "coverage-missing-driver.phpt") {
		t.Errorf("got %v", got)
	}
}

func TestParseTestFailuresExunit(t *testing.T) {
	// Shape from elixir-lang/elixir CI (2026-07-31).
	log := logts(
		"  1) test works with converged dependencies (Mix.Tasks.DepsTest)",
		"     test/mix/tasks/deps_test.exs:12",
		"     Assertion with == failed",
	)
	got := parseTestFailures(log)
	want := []testFailure{{"exunit", "test works with converged dependencies (Mix.Tasks.DepsTest)"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresMocha(t *testing.T) {
	// Multi-line numbered block from mochajs/mocha CI; gated on the
	// "N failing" summary line that always precedes it.
	log := logts(
		"  55 passing (2m)",
		"  1 failing",
		"",
		"  1) --watch",
		"       when enabled",
		"         reruns test when file and directory paths under --watch-files are added:",
		"     UnexpectedError: ",
		"expected",
	)
	got := parseTestFailures(log)
	want := []testFailure{{"mocha", "--watch › when enabled › reruns test when file and directory paths under --watch-files are added"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresMochaUngated(t *testing.T) {
	// Without "N failing" earlier in the log, numbered lines are NOT mocha
	// (they'd swallow exunit/phpunit/prose numbering).
	log := logts(
		"  1) some numbered thing",
		"       detail line:",
	)
	if got := parseTestFailures(log); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestParseTestFailuresDotnet(t *testing.T) {
	// MTP shape from dotnet/efcore CI (2026-07-31) + classic VSTest line.
	log := logts(
		`failed Microsoft.EntityFrameworkCore.SqliteTypeMappingSourceTest.Does_mappings_for_store_type(storeType: "TEXTURAL", clrType: typeof(string))`,
		"  from D:\\a\\efcore\\artifacts\\bin\\EFCore.Sqlite.Tests.dll (net11.0|x64)",
		"skipped Microsoft.EntityFrameworkCore.Query.JsonQuerySqliteTest.Json_predicate", // skipped ≠ failed
		"  Failed Company.Product.Tests.LoginTest.Rejects_bad_password [12 ms]",
		"failed to restore packages", // prose, no dotted FQN
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"dotnet", `Microsoft.EntityFrameworkCore.SqliteTypeMappingSourceTest.Does_mappings_for_store_type(storeType: "TEXTURAL", clrType: typeof(string))`},
		{"dotnet", "Company.Product.Tests.LoginTest.Rejects_bad_password"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresAva(t *testing.T) {
	// Shape from sindresorhus/got CI (ava).
	log := logts(
		"  ✔ create › failed header writes on frozen defaults do not mark headers",
		"  ✘ [fail]: retry › respects backoffLimit Rejected promise returned by test",
		"  1 test failed",
	)
	got := parseTestFailures(log)
	want := []testFailure{{"ava", "retry › respects backoffLimit Rejected promise returned by test"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
