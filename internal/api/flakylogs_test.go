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

func TestParseTestFailuresXCTest(t *testing.T) {
	// Shapes from realm/realm-swift CI (2026-06-14): Darwin Test Case lines
	// plus xcodebuild's end-of-run "Failing tests:" summary — the same test
	// appears in both and must aggregate to ONE entry (Class.method).
	log := logts(
		"Test Case '-[SwiftUITests.SwiftUITests testSampleApp]' started.",
		"Test Case '-[SwiftUITests.SwiftUITests testSampleApp]' failed (21.346 seconds).",
		"Test Suite 'SwiftUITests' failed at 2026-06-14 04:02:59.651.",
		"Failing tests:",
		"",
		"Test session results, code coverage, and logs:",
		"\t/Users/runner/work/realm-swift/build/Logs/Test/Test-SwiftUITestHost.xcresult",
		"",
		"\tSwiftUITests.testSampleApp()",
		"\tSwiftUITests.testUpdateResultsWithSearchable()",
		"",
		"** TEST EXECUTE FAILED **",
		"",
		"Testing started",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"xctest", "SwiftUITests.testSampleApp"},
		{"xctest", "SwiftUITests.testUpdateResultsWithSearchable"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresXCTestLinux(t *testing.T) {
	// Shape from nicklockwood/SwiftFormat linux CI (2026-07-31): no -[...]
	// wrapper, no trailing period, and a file:line error above it that must
	// not double-count.
	log := logts(
		"/__w/SwiftFormat/Tests/MetadataTests.swift:97: error: MetadataTests.testGenerateRulesDocumentation : XCTAssertEqual failed",
		"Test Case 'MetadataTests.testGenerateRulesDocumentation' failed (0.003 seconds)",
		"Test Case 'MetadataTests.testOther' passed (0.001 seconds)",
		"\t Executed 14 tests, with 1 failure (0 unexpected) in 3.7 (3.7) seconds",
	)
	got := parseTestFailures(log)
	want := []testFailure{{"xctest", "MetadataTests.testGenerateRulesDocumentation"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresXcbeautify(t *testing.T) {
	// Shape from Alamofire CI (2026-07-21): xcbeautify's GitHub Actions
	// renderer turns failing tests into ::error annotations; passing tests
	// keep the ✔ line. Repeated assertions in one test dedupe.
	log := logts(
		"##[error]    testThatWebSocketsCanReceiveAMessageGivenMultipleProtocols, XCTAssertNotNil failed",
		`##[error]    testThatWebSocketsCanReceiveAMessageGivenMultipleProtocols, XCTAssertEqual failed: ("nil") is not equal to ("Optional("first")")`,
		"    ✔ testThatWebSocketsCanReceiveAMessageWithAProtocol (0.046 seconds)",
		"    ✖ testThatUploadsFail, XCTAssertNil failed", // default-renderer form
		"##[error]Process completed with exit code 1.",   // no test name shape
		"Executed 17 tests, with 5 failures (0 unexpected) in 2.471 (2.483) seconds",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"xctest", "testThatWebSocketsCanReceiveAMessageGivenMultipleProtocols"},
		{"xctest", "testThatUploadsFail"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresSwiftTesting(t *testing.T) {
	// Shape from hummingbird-project/hummingbird linux CI (2026-07-28).
	// The run summary "✘ Test run with ..." and "✘ Suite ..." lines must
	// not be swallowed; "recorded an issue" + "failed after" dedupe.
	log := logts(
		"✘ Test testOverflow() recorded an issue at URLDecoderTests.swift:342:19: Expectation failed: .success → .signal(SIGILL → 4)",
		"✘ Test testOverflow() failed after 0.519 seconds with 1 issue.",
		`✘ Test "parses nested keys" failed after 0.1 seconds with 1 issue.`,
		"✔ Test testWriteBody() passed after 1.085 seconds.",
		"✘ Suite DecoderTests failed after 3.038 seconds with 1 issue.",
		"✘ Test run with 452 tests in 37 suites failed after 20.186 seconds with 1 issue.",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"swift-testing", "testOverflow()"},
		{"swift-testing", "parses nested keys"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresXCTestBareCollapse(t *testing.T) {
	// Seen live on Alamofire's "macOS 15, Xcode 16.4" job: the SAME failure
	// prints both a Test Case line (qualified) and an xcbeautify ::error
	// annotation (bare method name). One test, one entry. A bare name with
	// no qualified twin survives.
	log := logts(
		"Test Case 'TLSEvaluationTestCase.testThatExpiredCertificateRequestFails' failed (5.1 seconds)",
		"##[error]    testThatExpiredCertificateRequestFails, XCTAssertEqual failed",
		"##[error]    testThatDataStreamTaskCanStreamData, XCTAssertNil failed",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"xctest", "TLSEvaluationTestCase.testThatExpiredCertificateRequestFails"},
		{"xctest", "testThatDataStreamTaskCanStreamData"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresJestDefaultReporter(t *testing.T) {
	// jest's default reporter (what CI sees) prints NO ✕ lines: failures
	// are "● title" blocks under a "FAIL path" header, repeated in a
	// "Summary of all failing tests" section past summaryThreshold.
	// Shapes taken from a real facebook/jest Node-nightly log.
	log := logts(
		"FAIL e2e/__tests__/requireAfterTeardown.test.ts",
		"  ● Console",
		"    console.log some noise",
		"  ● prints useful error for requires after test is done",
		"    expect(received).toMatchSnapshot()",
		"  ● Validation Warning:",
		"  ● Test suite failed to run",
		"    Cannot find module 'x'",
		" › 2 snapshot tests failed.",
		"Summary of all failing tests",
		" FAIL  e2e/__tests__/requireAfterTeardown.test.ts",
		"  ● prints useful error for requires after test is done",
		"Test Suites: 1 failed, 3 skipped, 128 passed, 129 of 132 total",
		"Tests:       2 failed, 107 skipped, 1242 passed, 1351 total",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"jest", "e2e/__tests__/requireAfterTeardown.test.ts › prints useful error for requires after test is done"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresJestDotNeedsFingerprint(t *testing.T) {
	// ● blocks without jest's "Test Suites:" stats line must extract
	// nothing — some other tool's bullets can't become test names.
	log := logts(
		"FAIL something/somewhere",
		"  ● looks like a test title",
	)
	if got := parseTestFailures(log); len(got) != 0 {
		t.Errorf("expected nothing without the jest fingerprint, got %v", got)
	}
}

func TestParseTestFailuresJestVerboseCollapse(t *testing.T) {
	// --verbose prints BOTH the ✕ listing line and the ● failure block
	// for one failure; the leaf-title collapse must keep exactly one
	// entry (the qualified ● form).
	log := logts(
		"FAIL src/api.test.ts",
		"  describe block",
		"    ✕ retries on 503 (43 ms)",
		"  ● describe block › retries on 503",
		"    expect(received).toBe(200)",
		"Test Suites: 1 failed, 1 total",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"jest", "src/api.test.ts › describe block › retries on 503"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresVitest(t *testing.T) {
	// Modern vitest: × (U+00D7) tree lines + a "Failed Tests" summary with
	// "FAIL path > chain" headers; only the headers are parsed (gated on
	// vitest's "Test Files" stats line, which jest never prints).
	log := logts(
		" ❯ test/typechecker.test.ts (2 tests | 1 failed) 1708ms",
		"     × fails the run when the typechecker crashes (OOM) 924ms",
		"⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯",
		" FAIL  test/typechecker.test.ts > Typechecker > fails the run when the typechecker crashes (OOM)",
		"AssertionError: expected '...' to contain 'Typecheck Error'",
		" Test Files  1 failed | 2 passed (3)",
		"      Tests  1 failed | 9 passed (10)",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"vitest", "test/typechecker.test.ts › Typechecker › fails the run when the typechecker crashes (OOM)"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresUnittest(t *testing.T) {
	// Real shapes from django/django CI (Python >=3.11 puts the fully
	// qualified name in the parens) plus the pre-3.11 form and a subtest
	// decoration; the '=' separator line directly above is the gate.
	log := logts(
		"Importing application model_fields",
		"",
		"======================================================================",
		"FAIL: test_database_sharing_in_threads (backends.sqlite.tests.ThreadSharing.test_database_sharing_in_threads)",
		"----------------------------------------------------------------------",
		"Traceback (most recent call last):",
		"AssertionError: 1 != 2",
		"======================================================================",
		"ERROR: test_old_form (auth_tests.test_views.LoginTest)",
		"----------------------------------------------------------------------",
		"======================================================================",
		"FAIL: test_sub (mod.Case.test_sub) (i=3)",
		"----------------------------------------------------------------------",
		"",
		"Ran 19722 tests in 272.326s",
		"",
		"FAILED (failures=1, skipped=1497, expected failures=4)",
		// ungated FAIL: lines (no '=' separator above) must NOT match
		"FAIL: test_loose (mod.Class.test_loose)",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"unittest", "backends.sqlite.tests.ThreadSharing.test_database_sharing_in_threads"},
		{"unittest", "auth_tests.test_views.LoginTest.test_old_form"},
		{"unittest", "mod.Case.test_sub"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresLit(t *testing.T) {
	// Real shapes from llvm/llvm-project CI: inline FAIL progress line,
	// the "Failed Tests (N):" summary, and — crucially — the failing
	// test's own EMBEDDED unittest output (lldb's dotest prints the
	// classic "======"+"FAIL: x (Mod.Class.x)" block inside the lit
	// failure). One failure must yield ONE name: lit's.
	log := logts(
		"-- Testing: 3796 tests, 64 workers --",
		"Testing:  0.. 10.. 20..",
		"FAIL: lldb-api :: functionalities/scripted/TestFrameProvider.py (383 of 3796)",
		"******************** TEST 'lldb-api :: functionalities/scripted/TestFrameProvider.py' FAILED ********************",
		"Command Output (stderr):",
		"======================================================================",
		"FAIL: test_circular_dependency (TestFrameProvider.FrameProviderTestCase.test_circular_dependency)",
		"----------------------------------------------------------------------",
		"Traceback (most recent call last):",
		"********************",
		"Failed Tests (1):",
		"  lldb-api :: functionalities/scripted/TestFrameProvider.py",
		"",
		"Testing Time: 87.89s",
		"Total Discovered Tests: 34655",
		"  Failed           :     1 (0.00%)",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"lit", "lldb-api :: functionalities/scripted/TestFrameProvider.py"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresMeson(t *testing.T) {
	// Real shapes from systemd/systemd CI. Only the "Summary of Failures:"
	// section is parsed — the identical live-progress line above must not
	// double-count (dedupe) and prose numbers can't match outside the
	// section.
	log := logts(
		"1353/1904 libsystemd - systemd:test-varlink                                        FAIL              0.36s   exit status 1",
		"1360/1904 libsystemd - systemd:test-ok                                             OK                0.10s",
		"Summary of Failures:",
		"1353/1904 libsystemd - systemd:test-varlink                                        FAIL              0.36s   exit status 1",
		"1401/1904 core - systemd:test-timeout                                              TIMEOUT          30.00s",
		"Ok:                1894",
		"Fail:              1",
		"12/15 something that looks like an entry FAIL 1.0s outside the closed section",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"meson", "libsystemd - systemd:test-varlink"},
		{"meson", "core - systemd:test-timeout"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
