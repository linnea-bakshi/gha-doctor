package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	st := c.analyzeFlakyLogs("o", "r", fails, nil, 10, func(string) {})
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
	st := c.analyzeFlakyLogs("o", "r", []flakyFail{{}}, nil, 5, func(string) {})
	if st.Available || !strings.Contains(st.Note, "auth") {
		t.Errorf("st = %+v", st)
	}
}

func TestAnalyzeFlakyLogsNoFlakes(t *testing.T) {
	c := &Client{Token: "t"}
	st := c.analyzeFlakyLogs("o", "r", nil, nil, 5, func(string) {})
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
	}, nil, 5, func(string) {})
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

func TestParseTestFailuresCypress(t *testing.T) {
	// Cypress = mocha per spec file, shapes from a live ToolJet run
	// (Cypress 15). The "(Run Starting)" banner arms cypress mode; each
	// "Running:  spec  (i of n)" line sets the spec that qualifies the
	// mocha names captured after it. The next spec's inline result marks
	// (numbered, no error text) must not ride the previous spec's
	// "N failing" gate — the Running: line resets it.
	log := logts(
		"  (Run Starting)",
		"",
		"  Running:  components/cssClassHappyPath.cy.js                  (3 of 17)",
		"",
		"  Widget - CSS class field",
		"    1) should expose a CSS class field:",
		"",
		"  0 passing (2m)",
		"  1 failing",
		"",
		"  1) Widget - CSS class field",
		"       should expose a CSS class field:",
		"     AssertionError: Timed out retrying after 30000ms",
		"",
		"  Running:  components/modalHappyPath.cy.js                     (5 of 17)",
		"",
		"  Modal widget",
		"    1) should open the modal:",
		"",
		"  0 passing (1m)",
		"  1 failing",
		"",
		"  1) Modal widget",
		"       should open the modal:",
		"     AssertionError: expected modal to be visible",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"cypress", "components/cssClassHappyPath.cy.js › Widget - CSS class field › should expose a CSS class field"},
		{"cypress", "components/modalHappyPath.cy.js › Modal widget › should open the modal"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresCypressSameNameAcrossSpecs(t *testing.T) {
	// From a live cypress-realworld-app run: two specs each failed with
	// the SAME single-line title ("An uncaught error was detected outside
	// of a test"). Unqualified they dedupe into one; the spec file keeps
	// them distinct.
	log := logts(
		"  (Run Starting)",
		"",
		"  Running:  new-transaction.spec.ts                                    (3 of 7)",
		"",
		"  0 passing (274ms)",
		"  1 failing",
		"",
		"  1) An uncaught error was detected outside of a test:",
		"     TypeError: The following error originated from your test code",
		"",
		"  Running:  bankaccounts.spec.ts                                       (6 of 7)",
		"",
		"  0 passing (201ms)",
		"  1 failing",
		"",
		"  1) An uncaught error was detected outside of a test:",
		"     TypeError: The following error originated from your test code",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"cypress", "new-transaction.spec.ts › An uncaught error was detected outside of a test"},
		{"cypress", "bankaccounts.spec.ts › An uncaught error was detected outside of a test"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
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

func TestParseTestFailuresGtest(t *testing.T) {
	// Real shapes from opencv (parameterized, where-clause, retry re-print)
	// and tesseract (plain) CI logs. Inline "(N ms)" prints and the
	// end-of-run "listed below:" section dedupe to one name; the count
	// line has no dot and can't match.
	log := logts(
		"[==========] 5058 tests from 139 test cases ran. (50280 ms total)",
		"[  PASSED  ] 5057 tests.",
		"[  FAILED  ] Test_TensorFlow_layers.batch_norm_11/0, where GetParam() = OCV/CPU (0 ms)",
		"[  FAILED  ] 1 test, listed below:",
		"[  FAILED  ] Test_TensorFlow_layers.batch_norm_11/0, where GetParam() = OCV/CPU",
		"[  FAILED  ] RecodeBeamTest.DoesChinese (1346 ms)",
		"[  FAILED  ] RecodeBeamTest.DoesChinese",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"gtest", "Test_TensorFlow_layers.batch_norm_11/0"},
		{"gtest", "RecodeBeamTest.DoesChinese"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresGtestNoBanner(t *testing.T) {
	// Without gtest's own "[==========]" run banner the extractor stays
	// off — a random bracketed FAILED line in prose is not a test result.
	log := logts("[  FAILED  ] Suite.Test (3 ms)")
	if got := parseTestFailures(log); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestParseTestFailuresCtest(t *testing.T) {
	// Real shapes from or-tools (docker-buildx-prefixed, echoed twice),
	// google/benchmark (Windows exit code), OpenColorIO (ILLEGAL). The
	// buildkit "#12 792.5 " prefix is stripped before parsing; the bare
	// "792.5 " recap prefix is not, and dedupe covers the recap anyway.
	log := logts(
		"#12 792.5 	230 - java_algorithms_KnapsackSolverTest (Disabled)",
		"#12 792.5 ",
		"#12 792.5 The following tests FAILED:",
		"#12 792.5 	245 - java_mathopt_JniSolverTest (Failed)",
		"#12 792.5 	264 - java_mathopt_SolveTest (Failed)",
		"#12 792.5 Errors while running CTest",
		"The following tests FAILED:",
		"	 71 - complexity_benchmark (Exit code 0xc0000409)",
		"	  2 - test_cpu (ILLEGAL)",
		"	245 - java_mathopt_JniSolverTest (Failed)",
		"Errors while running CTest",
		"	99 - not_in_a_section (Failed)",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"ctest", "java_mathopt_JniSolverTest"},
		{"ctest", "java_mathopt_SolveTest"},
		{"ctest", "complexity_benchmark"},
		{"ctest", "test_cpu"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresCtestSuppressesGtest(t *testing.T) {
	// CTEST_OUTPUT_ON_FAILURE embeds a failing gtest binary's own output —
	// one real failure, two name forms. The orchestrator's names win
	// (same call as lit-embeds-unittest).
	log := logts(
		"[==========] 12 tests from 3 test cases ran. (503 ms total)",
		"[  FAILED  ] MathTest.Overflow (3 ms)",
		"The following tests FAILED:",
		"	  7 - math_test (Failed)",
		"Errors while running CTest",
	)
	got := parseTestFailures(log)
	want := []testFailure{{"ctest", "math_test"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresBazel(t *testing.T) {
	// Real shapes from protocolbuffers/protobuf CI. FAILED TO BUILD and
	// NO STATUS are build problems, not test failures; PASSED/SKIPPED
	// obviously excluded; flaky-retry "N out of M" form kept.
	log := logts(
		"//python:conformance_test                                                PASSED in 2.1s",
		"//python:x86_64_test                                                    SKIPPED",
		"//upb/conformance:test_conformance_upb                                   FAILED in 1.2s",
		"//upb/conformance:test_conformance_upb_dynamic_minitable                 FAILED in 1.2s",
		"//src:broken_dep                                                         FAILED TO BUILD",
		"//src:no_status                                                          NO STATUS",
		"//flaky:retry_test                                                       FAILED in 2 out of 3 in 15.3s",
		"//slow:timeout_test                                                      TIMEOUT in 300.0s",
		"Executed 4 out of 77 tests: 72 tests pass, 2 fail locally, and 3 were skipped.",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"bazel", "//upb/conformance:test_conformance_upb"},
		{"bazel", "//upb/conformance:test_conformance_upb_dynamic_minitable"},
		{"bazel", "//flaky:retry_test"},
		{"bazel", "//slow:timeout_test"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresBazelNoStatsLine(t *testing.T) {
	// Without bazel's "Executed N out of M tests" stats line the
	// target-shaped line is just prose.
	log := logts("//upb/conformance:test_conformance_upb                                   FAILED in 1.2s")
	if got := parseTestFailures(log); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestParseTestFailuresNodeCore(t *testing.T) {
	// Node.js core's tools/test.py harness, anchored on live nodejs/node
	// runs (test-macOS 2026-08-03 and test-internet): a failing test
	// prints "=== release test-x ===" with "Path: parallel/test-x"
	// directly beneath. The adjacent pair is the anchor; a Path line
	// whose value doesn't end in the block's own name, or one without a
	// block line directly above, must not match.
	log := logts(
		"=== release test-debugger-probe-activation ===",
		"Path: parallel/test-debugger-probe-activation",
		"##[error]--- stderr ---",
		"Error: Timeout (15000) while waiting for /break (?:on start )?in/i",
		"=== debug test-https-autoselectfamily-slow-timeout ===",
		"Path: internet/test-https-autoselectfamily-slow-timeout",
		"node:events:505",
		"    throw er; // Unhandled 'error' event",
		"Path: parallel/test-orphan-path-line", // no block directly above
		"=== release test-mismatched-name ===",
		"Path: parallel/test-some-other-name", // value doesn't end in block name
		"===",
		"=== 2 tests failed",
		"===",
		"Failed tests:",
		"out/Release/node /home/runner/work/node/node/test/parallel/test-debugger-probe-activation.js",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"node-core", "parallel/test-debugger-probe-activation"},
		{"node-core", "internet/test-https-autoselectfamily-slow-timeout"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// flakyArtifactServer serves flaky-failure job logs plus per-run artifact
// listings and zips, counting hits so tests can assert the gates.
func flakyArtifactServer(t *testing.T, logs map[string]string, artsByRun map[string][]Artifact, zips map[int64][]byte, hits map[string]int) *Client {
	t.Helper()
	var mu sync.Mutex // job logs are fetched concurrently
	hit := func(k string) {
		mu.Lock()
		hits[k]++
		mu.Unlock()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for suffix, text := range logs {
			if strings.HasSuffix(p, suffix) {
				hit("log:" + suffix)
				fmt.Fprint(w, text)
				return
			}
		}
		for run, arts := range artsByRun {
			if strings.HasSuffix(p, "/runs/"+run+"/artifacts") {
				hit("list:" + run)
				json.NewEncoder(w).Encode(map[string]any{"artifacts": arts})
				return
			}
		}
		for id, data := range zips {
			if strings.HasSuffix(p, fmt.Sprintf("/artifacts/%d/zip", id)) {
				hit(fmt.Sprintf("zip:%d", id))
				w.Write(data)
				return
			}
		}
		t.Errorf("unexpected request: %s", p)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return &Client{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestFlakyArtifactFallback(t *testing.T) {
	// Two flaky runs with bespoke (unrecognized) console output; both are
	// eligible and both uploaded a JUnit report naming the same failure.
	junit := `<testsuite><testcase classname="payments.Suite" name="refund_partial"><failure message="boom"/></testcase></testsuite>`
	zipData := buildZip(t, map[string]string{"junit.xml": junit})
	bespoke := logts("HARNESS: verdict=RED code=7")
	now := time.Now()
	hits := map[string]int{}
	c := flakyArtifactServer(t,
		map[string]string{"/jobs/11/logs": bespoke, "/jobs/21/logs": bespoke},
		map[string][]Artifact{
			"100": {{ID: 1, Name: "test-report", SizeInBytes: int64(len(zipData))}},
			"200": {{ID: 2, Name: "test-report", SizeInBytes: int64(len(zipData))}},
		},
		map[int64][]byte{1: zipData, 2: zipData}, hits)
	fails := []flakyFail{
		{job: Job{ID: 11, RunID: 100, Name: "suite", CompletedAt: now}, wf: "ci", sha: "aaa"},
		{job: Job{ID: 21, RunID: 200, Name: "suite", CompletedAt: now.Add(-time.Hour)}, wf: "ci", sha: "bbb"},
	}
	eligible := map[int64][]Job{
		100: {{Name: "suite", Conclusion: "failure"}},
		200: {{Name: "suite", Conclusion: "failure"}},
	}
	st := c.analyzeFlakyLogs("o", "r", fails, eligible, 10, func(string) {})
	if !st.Available || len(st.Tests) != 0 {
		t.Fatalf("console extraction should name nothing: %+v", st)
	}
	if st.ArtifactRunsChecked != 2 {
		t.Errorf("ArtifactRunsChecked = %d, want 2", st.ArtifactRunsChecked)
	}
	if len(st.ArtifactTests) != 1 {
		t.Fatalf("ArtifactTests = %+v, want one aggregated entry", st.ArtifactTests)
	}
	at := st.ArtifactTests[0]
	if at.Name != "payments.Suite.refund_partial" || at.Artifact != "test-report" || at.Runs != 2 || at.Commits != 2 {
		t.Errorf("artifact test = %+v, want refund_partial across 2 runs / 2 commits", at)
	}
	if st.ArtifactNote != "" {
		t.Errorf("note should be empty when tests were named: %q", st.ArtifactNote)
	}
	if hits["list:100"] != 1 || hits["list:200"] != 1 || hits["zip:1"] != 1 || hits["zip:2"] != 1 {
		t.Errorf("hits = %v", hits)
	}
}

func TestFlakyArtifactSkippedWhenLogsNamed(t *testing.T) {
	// The console log names the flaky test — artifact endpoints must never
	// be hit even though the run is eligible.
	pytest := logts(
		"=========================== short test summary info ============================",
		"FAILED tests/test_x.py::test_y - AssertionError",
		"========================= 1 failed, 3 passed in 2.31s ==========================",
	)
	hits := map[string]int{}
	c := flakyArtifactServer(t, map[string]string{"/jobs/11/logs": pytest}, nil, nil, hits)
	fails := []flakyFail{{job: Job{ID: 11, RunID: 100, Name: "suite", CompletedAt: time.Now()}, wf: "ci", sha: "aaa"}}
	eligible := map[int64][]Job{100: {{Name: "suite", Conclusion: "failure"}}}
	st := c.analyzeFlakyLogs("o", "r", fails, eligible, 10, func(string) {})
	if len(st.Tests) != 1 {
		t.Fatalf("console should name the test: %+v", st)
	}
	if st.ArtifactRunsChecked != 0 || len(st.ArtifactTests) != 0 {
		t.Errorf("artifacts should not be consulted: %+v", st)
	}
	for k := range hits {
		if strings.HasPrefix(k, "list:") || strings.HasPrefix(k, "zip:") {
			t.Errorf("unexpected artifact hit %s", k)
		}
	}
}

func TestFlakyArtifactSkippedWhenRunIneligible(t *testing.T) {
	// Console named nothing, but the run is NOT in eligibleRuns (a sibling
	// job failed for real) — artifacts must stay untouched.
	hits := map[string]int{}
	c := flakyArtifactServer(t, map[string]string{"/jobs/11/logs": logts("HARNESS: verdict=RED")}, nil, nil, hits)
	fails := []flakyFail{{job: Job{ID: 11, RunID: 100, Name: "suite", CompletedAt: time.Now()}, wf: "ci", sha: "aaa"}}
	st := c.analyzeFlakyLogs("o", "r", fails, map[int64][]Job{}, 10, func(string) {})
	if st.ArtifactRunsChecked != 0 || len(st.ArtifactTests) != 0 || st.ArtifactNote != "" {
		t.Errorf("artifacts should not be consulted: %+v", st)
	}
	for k := range hits {
		if strings.HasPrefix(k, "list:") {
			t.Errorf("unexpected artifact hit %s", k)
		}
	}
}

func TestFlakyArtifactNoteWhenReportsRecordNoFailures(t *testing.T) {
	junit := `<testsuite><testcase classname="a.B" name="ok1"/><testcase classname="a.B" name="ok2"/></testsuite>`
	zipData := buildZip(t, map[string]string{"junit.xml": junit})
	hits := map[string]int{}
	c := flakyArtifactServer(t,
		map[string]string{"/jobs/11/logs": logts("HARNESS: verdict=RED")},
		map[string][]Artifact{"100": {{ID: 1, Name: "test-report", SizeInBytes: int64(len(zipData))}}},
		map[int64][]byte{1: zipData}, hits)
	fails := []flakyFail{{job: Job{ID: 11, RunID: 100, Name: "suite", CompletedAt: time.Now()}, wf: "ci", sha: "aaa"}}
	eligible := map[int64][]Job{100: {{Name: "suite", Conclusion: "failure"}}}
	st := c.analyzeFlakyLogs("o", "r", fails, eligible, 10, func(string) {})
	if len(st.ArtifactTests) != 0 {
		t.Fatalf("no failures recorded, got %+v", st.ArtifactTests)
	}
	if !strings.Contains(st.ArtifactNote, "2 test cases and no failures") || !strings.Contains(st.ArtifactNote, "1 checked flaky run's") {
		t.Errorf("note = %q", st.ArtifactNote)
	}
}

func TestParseTestFailuresNextest(t *testing.T) {
	// Real shapes from astral-sh/uv job 91793774249 (FAIL + Summary) and
	// from cargo-nextest 0.9.140 run locally with retries, a crashing test
	// and a terminating slow-timeout (TRY n FAIL / SEGV / TMT / FLAKY).
	// Only the Summary section is parsed: inline lines repeat per retry;
	// the summary lists each final failure exactly once. FLAKY entries
	// ultimately passed and must be tolerated mid-section, not extracted.
	log := logts(
		"        FAIL [   1.004s] (2914/4765) uv::sync show_settings::run_pep723_script_preview_features",
		"  stdout ───",
		"",
		"    running 1 test",
		"    test show_settings::run_pep723_script_preview_features ... FAILED",
		"",
		"    failures:",
		"        show_settings::run_pep723_script_preview_features",
		"",
		"    test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 249 filtered out; finished in 1.00s",
		"  TRY 1 FAIL [   0.006s] (───) nx-probe::suite always_fails",
		"  TRY 2 FAIL [   0.006s] (───) nx-probe::suite always_fails",
		"  TRY 3 FAIL [   0.007s] (1/5) nx-probe::suite always_fails",
		"     Summary [  89.095s] 4765 tests run: 4760 passed (1 flaky), 3 failed, 1 timed out, 4 skipped",
		"   FLAKY 2/3 [   0.007s] (3/5) nx-probe::suite flaky_passes_second_try",
		"        FAIL [   1.004s] (2914/4765) uv::sync show_settings::run_pep723_script_preview_features",
		"  TRY 3 FAIL [   0.007s] (1/5) nx-probe::suite always_fails",
		"  TRY 3 SEGV [   0.134s] (4/5) nx-probe::suite aborts",
		"   TRY 3 TMT [   4.003s] (5/5) nx-probe::suite times_out",
		"error: test run failed",
		"        FAIL [   1.000s] (1/2) outside::section not_extracted_after_close",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"nextest", "uv::sync show_settings::run_pep723_script_preview_features"},
		{"nextest", "nx-probe::suite always_fails"},
		{"nextest", "nx-probe::suite aborts"},
		{"nextest", "nx-probe::suite times_out"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresNextestFailFast(t *testing.T) {
	// Fail-fast cancellation: the Summary counts line reads "1/5 tests run:".
	log := logts(
		"  Cancelling due to test failure: 1 test still running",
		"     Summary [   0.313s] 1/5 tests run: 0 passed, 1 failed, 0 skipped",
		"  TRY 3 FAIL [   0.008s] (1/5) nx-probe::suite always_fails",
	)
	got := parseTestFailures(log)
	want := []testFailure{{"nextest", "nx-probe::suite always_fails"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseTestFailuresNextestDoctestSibling(t *testing.T) {
	// nextest cannot run doctests, so repos run plain `cargo test` for them
	// in the same job. Genuine libtest output sits at column 0 — nextest
	// indents its captured copy by four spaces, which is what keeps the
	// cargo extractor off it — and must still extract as cargo: two
	// invocations, two real failures, two names.
	log := logts(
		"     Summary [   1.000s] 10 tests run: 9 passed, 1 failed, 0 skipped",
		"        FAIL [   0.100s] (1/10) mycrate::lib mod_a::test_b",
		"test mod_c::test_d ... FAILED",
		"test result: FAILED. 3 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.50s",
	)
	got := parseTestFailures(log)
	want := []testFailure{
		{"nextest", "mycrate::lib mod_a::test_b"},
		{"cargo", "mod_c::test_d"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
