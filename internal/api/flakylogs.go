package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// FlakyTestStats names the tests behind flaky jobs, extracted from the logs
// of failed job instances whose commit later passed (the same-SHA fail+pass
// pairs computeFlaky found). A test named here failed in a run that the
// project itself treated as non-reproducible — the strongest flakiness
// signal available without instrumenting the test suite.
type FlakyTestStats struct {
	Available   bool        `json:"available"`
	Note        string      `json:"note,omitempty"` // why unavailable / empty
	LogsTotal   int         `json:"logs_total"`     // flaky-failure logs that exist in the sample window
	LogsSampled int         `json:"logs_sampled"`   // how many we fetched
	JobsSkipped int         `json:"jobs_skipped"`   // fetch errors (expired logs etc.)
	Tests       []FlakyTest `json:"tests,omitempty"`

	// Artifact fallback: when a flaky run's sampled logs named nothing AND
	// every failed job in that run was flaky-proven, its uploaded JUnit XML
	// test reports are consulted (up to 2 such runs). Attribution is
	// run-level — artifacts belong to the run, not to a specific job.
	ArtifactRunsChecked int                 `json:"artifact_runs_checked,omitempty"`
	ArtifactTests       []FlakyArtifactTest `json:"artifact_tests,omitempty"`
	ArtifactNote        string              `json:"artifact_note,omitempty"`
}

// FlakyArtifactTest is one failing test recorded by a JUnit XML report in a
// flaky run's artifacts. Runs counts consulted flaky runs whose reports
// record it failing; Commits the distinct SHAs among those.
type FlakyArtifactTest struct {
	Name     string `json:"name"`
	Artifact string `json:"artifact"`
	Runs     int    `json:"runs"`
	Commits  int    `json:"commits"`
}

// FlakyTest is one test (or suite entry) seen failing in flaky-job logs.
type FlakyTest struct {
	Name      string   `json:"name"`
	Framework string   `json:"framework"` // pytest / go / cargo / jest / vitest / playwright / cypress / mocha / ava / rspec / minitest / phpunit / exunit / maven / gradle / dotnet / xctest / swift-testing / node-core / ...
	Failures  int      `json:"failures"`  // sampled logs it failed in
	Commits   int      `json:"commits"`   // distinct commits it flaked on
	Jobs      []string `json:"jobs"`      // distinct job names (base name, matrix collapsed)
}

// flakyFail is one failed job instance from a flaky (same-SHA fail+pass)
// group — the population --flaky-logs samples from.
type flakyFail struct {
	job Job
	wf  string
	sha string
}

// testFailure is one extracted failing-test line.
type testFailure struct {
	framework string
	name      string
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// Framework extractors. Each pattern anchors on the framework's own failure
// summary format; a line must match exactly one way. Kept deliberately
// narrow: a false "flaky test" name is worse than a miss (the section says
// what it could not parse).
var (
	// pytest short summary: "FAILED tests/test_x.py::TestC::test_y - AssertionError: ..."
	pytestFailedRE = regexp.MustCompile(`^(?:FAILED|ERROR) +(\S+::\S+)`)
	// pytest verbose: "tests/test_x.py::test_y FAILED [ 12%]"
	pytestVerboseRE = regexp.MustCompile(`^(\S+::\S+) FAILED\b`)
	// go test: "--- FAIL: TestName/sub (0.01s)" (indented for subtests)
	goFailRE = regexp.MustCompile(`^ *--- FAIL: (\S+)`)
	// cargo test: "test module::name ... FAILED"
	cargoFailRE = regexp.MustCompile(`^test (\S+) \.\.\. FAILED$`)
	// jest/vitest verbose listing: "✕ test name (12 ms)"
	jestFailRE = regexp.MustCompile(`^✕ (.+?)(?: \(\d+(?:\.\d+)? ?m?s\))?$`)
	// jest default reporter prints NO ✕ lines — failures are "● title"
	// blocks under a "FAIL path" suite header (and repeated in a
	// "Summary of all failing tests" section when the project exceeds
	// jest's summaryThreshold). Gated three ways: the log must carry
	// jest's own stats line ("Test Suites: ..."), a FAIL header must have
	// set the current suite, and known non-test ● blocks are excluded.
	jestSuiteRE = regexp.MustCompile(`^FAIL +(\S+)`)
	jestDotRE   = regexp.MustCompile(`^● (.+?):?$`)
	// vitest's failure summary: " FAIL  test/x.test.ts > Suite > title" —
	// the " > " chain is required, so jest's plain "FAIL path" headers
	// can't match. Gated on vitest's own stats line ("Test Files  N
	// failed ..."), which jest never prints. Modern vitest marks tree
	// lines with × (U+00D7), not ✕ — those lines are redundant with the
	// summary and deliberately not parsed (no double-count possible).
	vitestFailRE = regexp.MustCompile(`^FAIL +(\S+ > .+)$`)
	// rspec failed examples: "rspec ./spec/foo_spec.rb:12 # Class does a thing"
	rspecFailRE = regexp.MustCompile(`^rspec \.?/\S+:\d+ # (.+)$`)
	// maven surefire summary: "[ERROR]   ClassTest.testMethod:34 expected: ..."
	mavenFailRE = regexp.MustCompile(`^\[ERROR\] +([A-Za-z][A-Za-z0-9_.$]*\.[a-zA-Z_][A-Za-z0-9_]*):\d+`)
	// playwright numbered failure: "1) [project] › tests\x.spec.ts:827:5 › title ───"
	playwrightFailRE = regexp.MustCompile(`^\d+\) ((?:\[[^\]]+\] › )?\S+:\d+:\d+ › .+)$`)
	// playwrightLineRE strips the :line:col so the same test aggregates
	// across commits even when the file shifts underneath it.
	playwrightLineRE = regexp.MustCompile(`:\d+:\d+ › `)
	// gradle test-event line: "ClassName > [1] param = X > method() FAILED"
	// (JUnit/Spock via Gradle). First segment must look like a class name so
	// "> Task :x:test FAILED" and prose can't match.
	gradleFailRE = regexp.MustCompile(`^([A-Za-z_$][\w.$]*(?: > .+)+) FAILED$`)
	// gradleRepetitionRE strips "> repetition 3 of 10" so @RepeatedTest
	// repeats aggregate as one test.
	gradleRepetitionRE = regexp.MustCompile(` > repetition \d+ of \d+$`)
	// minitest failure name line (the line after "Failure:"/"Error:"):
	// "ClassOrSpecDesc#test_name [test/foo_test.rb:143]:" (failures carry the
	// [file:line]; errors end with a bare colon).
	minitestNameRE = regexp.MustCompile(`^(.+#test_.+?)(?: \[\S+:\d+\])?:$`)
	// minitestHeaderRE: "Failure:" / "  1) Failure:" / "Error:" — gates the
	// name line above to the line that directly follows a header.
	minitestHeaderRE = regexp.MustCompile(`^(?:\d+\) )?(?:Failure|Error):$`)
	// phpunit section headers: only failure/error sections list failing
	// tests; skipped/risky/deprecation sections use the SAME numbered list
	// shape and must not be swallowed (seen live on briannesbitt/carbon).
	phpunitOpenRE  = regexp.MustCompile(`^There (?:was|were) \d+ (?:failure|error)s?:$`)
	phpunitCloseRE = regexp.MustCompile(`^(?:There (?:was|were) \d+ \w+|--|FAILURES!|ERRORS!|OK\b.*|Tests: .*)$`)
	// phpunit numbered entry inside an open section: "1) Tests\FooTest::testBar"
	// (data-provider suffix dropped so cases aggregate) or a .phpt path.
	phpunitTestRE = regexp.MustCompile(`^\d+\) ([\w\\]+::\w+)`)
	phpunitPhptRE = regexp.MustCompile(`^\d+\) (\S+\.phpt)$`)
	// exunit numbered failure: "1) test works with converged deps (Mix.Tasks.DepsTest)"
	exunitFailRE = regexp.MustCompile(`^\d+\) (test .+ \([A-Za-z]\S*\))$`)
	// mocha failure list: "  1) suite" + deeper-indented lines ending in the
	// test title with a trailing ":". Gated on mocha's "N failing" summary
	// line, which always precedes the list — exunit/phpunit logs never
	// print it.
	mochaFailingRE = regexp.MustCompile(`^\d+ failing$`)
	mochaStartRE   = regexp.MustCompile(`^\d+\) (\S.*)$`)
	// Cypress runs specs through mocha, so its failures arrive as mocha's
	// numbered blocks — but every spec file restarts numbering, and
	// same-named failures in different specs are different tests (seen live
	// on cypress-realworld-app: two specs each failed with "An uncaught
	// error was detected outside of a test" and deduped into one). The
	// "(Run Starting)" banner gates cypress mode; each "Running:  <spec>
	// (i of n)" progress line sets the spec that qualifies every mocha name
	// captured while it's current.
	cypressRunningRE = regexp.MustCompile(`^Running:\s+(\S+)\s+\(\d+ of \d+\)$`)
	// dotnet MTP (xunit v3 / Microsoft.Testing.Platform): "failed Ns.Class.Method(args...)"
	// with a dotted FQN (prose "failed to X" can't match).
	dotnetMTPFailRE = regexp.MustCompile(`^failed ([A-Za-z_][\w]*(?:\.[\w]+)+(?:\(.*)?)$`)
	// dotnet VSTest: "Failed Ns.Class.Method [12 ms]" — the [duration]
	// suffix is required.
	dotnetVSTestFailRE = regexp.MustCompile(`^Failed ([\w+]+(?:\.[\w+]+)+(?:\([^)]*\))?) \[[\d.,]+ ?m?s\]$`)
	// ava: "✘ [fail]: title Rejected promise returned by test"
	avaFailRE = regexp.MustCompile(`^✘ \[fail\]: (.+)$`)
	// XCTest via xcodebuild (Darwin): "Test Case '-[Module.Class testMethod]' failed (21.346 seconds)."
	// XCTest via swift test (Linux):  "Test Case 'Class.testMethod' failed (0.003 seconds)"
	xctestCaseFailRE = regexp.MustCompile(`^Test Case '(?:-\[([\w.]+) (\w+)\]|([\w.]+\.\w+))' failed \(`)
	// xcodebuild's end-of-run "Failing tests:" summary lists entries like
	// "\tSwiftUITests.testSampleApp()" (blank lines and a "Test session
	// results ... .xcresult" block interleave; neither shape can match).
	xctestSummaryEntryRE = regexp.MustCompile(`^(?:-\[([\w.]+) (\w+)\]|([\w.]+\.\w+)(?:\(\))?)$`)
	// xcbeautify failing-test line: "testMethod, XCTAssertEqual failed: ..."
	// — as a ::error annotation from its GitHub Actions renderer (live on
	// Alamofire) or ✖-prefixed from its default renderer (same formatter,
	// TestCaseFormatter, two renderers). XCTest method names must start
	// with "test", which is the gate that keeps prose out.
	xcbeautifyFailRE = regexp.MustCompile(`^[ \t]*(?:##\[error\] *|✖ *)(test\w+), `)
	// Python unittest (also django's runner, lldb's dotest): the failure
	// block header is a full-width line of '=' directly above
	// "FAIL: test_x (module.Class.test_x)" (or ERROR:). Python >=3.11
	// puts the fully qualified name in the parens; older interpreters put
	// "module.Class" there. Subtest decorations ("(i=3)" / "[msg]")
	// follow in a separate group and are dropped so subtests aggregate.
	unittestFailRE = regexp.MustCompile(`^(?:FAIL|ERROR): (\S+) \(([\w.]+)\)(?: [(\[].*)?$`)
	// unittestSepRE gates the line above: a bare run of '=' (unittest
	// prints 70). pytest's section rules always carry text between the
	// '=' runs, so they can't arm this gate.
	unittestSepRE = regexp.MustCompile(`^={40,}$`)
	// LLVM lit: inline "FAIL: suite :: path (383 of 3796)" progress lines
	// and the end-of-run "Failed Tests (N):" summary with indented
	// "suite :: path" entries. Gated on lit's own fingerprint (the
	// "-- Testing: N tests" banner or the "Total Discovered Tests:"
	// stats) via litLog below.
	litInlineFailRE   = regexp.MustCompile(`^FAIL: (\S+ :: \S+)`)
	litSummaryOpenRE  = regexp.MustCompile(`^(?:Failed Tests|Timed Out Tests|Unresolved Tests) \(\d+\):$`)
	litSummaryEntryRE = regexp.MustCompile(`^(\S+ :: \S+)$`)
	// meson test: the end-of-run "Summary of Failures:" section lists
	// "1353/1904 libsystemd - systemd:test-varlink   FAIL   0.36s   exit status 1"
	// (statuses: FAIL / ERROR / TIMEOUT / UNEXPECTEDPASS). The section
	// closes at meson's own "Ok:" stats line.
	mesonEntryRE = regexp.MustCompile(`^\d+/\d+ (\S.*?) +(?:FAIL|ERROR|TIMEOUT|UNEXPECTEDPASS) +[\d.]+s`)
	// swift-testing: "✘ Test testOverflow() failed after 0.519 seconds with 1 issue."
	//                "✘ Test testOverflow() recorded an issue at File.swift:342:19: ..."
	// Name must be a call ("testOverflow()", "foo(bar:)") or a quoted
	// display name — the run summary "✘ Test run with 452 tests ... failed
	// after ..." can never match. "✘ Suite ..." lines don't start "✘ Test ".
	swiftTestingFailRE = regexp.MustCompile(`^✘ Test (?:"([^"]+)"|(\w+\([^)]*\))) (?:failed after|recorded an issue)`)
	// GoogleTest: "[  FAILED  ] Suite.Test, where GetParam() = OCV/CPU (0 ms)"
	// (inline, seen live on opencv) and the same name again in the
	// end-of-run "[  FAILED  ] N tests, listed below:" section (no
	// duration). The name must contain a '.' — the count line's "N tests,"
	// can't match. The ", where GetParam() = ..." clause and "(N ms)"
	// duration are dropped so both prints dedupe to one name; the
	// parameterized instance suffix ("/0") is part of the name and kept.
	gtestFailRE = regexp.MustCompile(`^\[  FAILED  \] ([\w/]+\.[\w/]+)(?:,| \(|$)`)
	// CTest: entries under "The following tests FAILED:" look like
	// "\t245 - java_mathopt_SolveTest (Failed)" — statuses seen live:
	// (Failed), (Timeout), (ILLEGAL), (Exit code 0xc0000409), (Subprocess
	// aborted). Docker buildx echoes the whole summary twice (streaming +
	// error recap, seen live on or-tools) — dedupe handles it.
	ctestEntryRE = regexp.MustCompile(`^\d+ - (\S.*?) \(([^)]+)\)$`)
	// Bazel: "//upb/conformance:test_conformance_upb   FAILED in 1.2s"
	// (protobuf, live). Flaky retries print "FAILED in 2 out of 3 in
	// 15.3s". "FAILED TO BUILD" and "NO STATUS" carry no "in Ns" and are
	// build problems, not test failures — they can't match.
	bazelFailRE = regexp.MustCompile(`^(//\S+) +(?:FAILED|TIMEOUT) in (?:\d+ out of \d+ in )?[\d.]+s$`)
	// docker buildx streams RUN-step output as "#12 792.5 <line>" (step
	// number + elapsed seconds). Tests that run inside `docker build`
	// (or-tools, live) hide EVERY framework's markers behind it — stripped
	// before any extractor looks at the line. The error-recap block repeats
	// the same lines with a bare "792.5 " prefix; that form is too generic
	// to strip safely, and dedupe makes it unnecessary.
	buildkitPrefixRE = regexp.MustCompile(`^#\d+ \d+\.\d+ `)
	// cargo-nextest (astral-sh/uv, live; variants from cargo-nextest
	// 0.9.140 run against a probe crate): the end-of-run summary opens
	//   "Summary [  89.095s] 4765 tests run: 4764 passed, 1 failed, 4 skipped"
	// ("1/5 tests run:" when fail-fast cancelled the rest) and repeats one
	// status line per FINAL failure:
	//   "     FAIL [   1.004s] (2914/4765) uv::sync show_settings::run_pep723_script_preview_features"
	//   "TRY 3 FAIL [   0.008s] (1/5) nx-probe::suite always_fails"   (retried)
	//   "TRY 3 TMT [   4.003s] (5/5) nx-probe::suite times_out"       (timeout)
	//   "TRY 3 SEGV [   0.134s] (4/5) nx-probe::suite aborts"         (crash)
	// Only the summary section is parsed — inline lines repeat once per
	// retry; the summary lists each failure exactly once. FLAKY and LEAK
	// entries ultimately PASSED (same bar as junit flakyFailure) and are
	// tolerated mid-section, never extracted. Names are emitted exactly as
	// nextest cites them: "binary test-path". The libtest output nextest
	// captures for each failure is indented four spaces, so the
	// column-0-anchored cargo extractor never fires on it — while a
	// genuine sibling `cargo test` invocation in the same job (doctests;
	// nextest can't run them) still extracts on its own.
	nextestSummaryRE = regexp.MustCompile(`^Summary \[ *[\d.]+s\] \d+(?:/\d+)? tests run: `)
	nextestEntryRE   = regexp.MustCompile(`^(?:TRY \d+ )?(?:FAIL|TMT|ABORT|SEGV|ABRT|BUS|ILL|FPE|TRAP) \[ *[\d.]+s\] \(\d+/\d+\) (\S+) (\S+)$`)
	nextestPassRE    = regexp.MustCompile(`^(?:FLAKY \d+/\d+|(?:TRY \d+ )?LEAK) \[`)
	// Node.js core's tools/test.py harness (nodejs/node CI, live): each
	// failing test prints a block opened by "=== release test-x ===" (or
	// "=== debug test-x ===") with "Path: parallel/test-x" directly
	// beneath. The ADJACENT pair is the anchor, and the Path value must
	// end with the block's own name — either line alone is too generic.
	// The qualified "parallel/test-x" form is emitted (how node devs cite
	// tests; the same basename exists in several suite dirs).
	nodeCoreBlockRE = regexp.MustCompile(`^=== (?:release|debug) (\S+) ===$`)
	nodeCorePathRE  = regexp.MustCompile(`^Path: (\S+)$`)
	// sbt (scala/scala3, akka, live): every sbt test task — whatever the
	// inner framework (junit-interface, ScalaTest, munit) — ends a failed
	// run with
	//   "[error] Failed tests:"
	//   "[error] \tdotty.tools.debug.DebugTests"        (one per suite)
	//   "[error] (proj / Test / testOnly) sbt.TestsFailedException: ..."
	// sbt's scripted harness (sbt/sbt, live) prints the same list under
	// "[error] java.lang.RuntimeException: Failed tests:" (names like
	// "lm-coursier/from-no-head") followed by a "\tat ..." stack trace —
	// frames carry a space after "at", so the single-\S+ entry shape
	// can't match them and the section closes. Some builds interpose
	// their own timestamp before sbt's level tag ("[08-01 01:22:59.823]
	// [error] Failed tests:", akka nightly live) — an optional bracketed
	// digits/punctuation prefix absorbs it. Gated on sbt's own exception
	// fingerprints; sbt runs that fail without a test summary (mdoc
	// compile errors on typelevel/cats, lintUnused warnings on sbt/sbt,
	// live) print no such section and extract nothing.
	sbtFailedOpenRE  = regexp.MustCompile(`^(?:\[[0-9 :.-]+\] )?\[error\] (?:java\.lang\.RuntimeException: |\([^)]+\) )?Failed tests:\s*$`)
	sbtFailedEntryRE = regexp.MustCompile(`^(?:\[[0-9 :.-]+\] )?\[error\] \t(\S+)\s*$`)
)

// flakyFrameworkList names every failure-summary format parseTestFailures
// understands, for the "no recognizable test failures" honesty note. Keep in
// lockstep with docs/flaky-frameworks.md.
const flakyFrameworkList = "pytest, unittest, go test, cargo test, jest/vitest, playwright, cypress, mocha, ava, rspec, minitest, phpunit, exunit, maven surefire, gradle/junit, sbt, dotnet xunit/vstest, xctest/swift-testing, xcbeautify, lit, meson, gtest, ctest, bazel, cargo-nextest, node-core test.py"

// xctestName normalizes XCTest identifiers to Class.method so the same test
// aggregates across the formats that carry it: "-[Module.Class method]"
// (Darwin Test Case lines), "Class.method" (Linux), and summary entries
// like "Class.method()" — module prefixes and call parens are dropped.
func xctestName(class, method, dotted string) string {
	if class != "" {
		if i := strings.LastIndex(class, "."); i >= 0 {
			class = class[i+1:]
		}
		return class + "." + method
	}
	dotted = strings.TrimSuffix(dotted, "()")
	parts := strings.Split(dotted, ".")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}

// unittestName normalizes a unittest failure to its fully qualified dotted
// name. Python >=3.11 already puts "module.Class.test_x" in the parens;
// older interpreters put "module.Class" there, so the method is appended.
func unittestName(method, qualified string) string {
	if strings.HasSuffix(qualified, "."+method) || qualified == method {
		return qualified
	}
	return qualified + "." + method
}

// parseTestFailures extracts failing-test names from one job log. Names are
// deduped within the log; go parent tests are dropped when a subtest of
// theirs was also captured (both lines appear in go output).
func parseTestFailures(text string) []testFailure {
	seen := map[string]bool{}
	var out []testFailure
	add := func(fw, name string) {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 200 {
			return
		}
		k := fw + "\x00" + name
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, testFailure{framework: fw, name: name})
	}
	// Stateful gates. Each exists because a real log proved the stateless
	// pattern unsafe: phpunit numbers skipped/deprecated tests with the same
	// list shape as failures; minitest name lines are only unambiguous
	// directly after a Failure:/Error: header; mocha's numbered multi-line
	// blocks would collide with exunit/phpunit numbering without the
	// "N failing" summary line as an admission ticket.
	prevMinitestHeader := false
	prevNodeCoreBlock := ""
	phpunitSection := false
	mochaGate := false
	xctestSummary := 0 // >0: inside "Failing tests:", lines left before giving up
	var mochaAccum []string
	// cypress: mocha names captured while a spec is current are labeled
	// cypress and qualified with the spec file (numbering restarts per spec,
	// so unqualified names from different specs would wrongly dedupe).
	// Detected per-line: cypress styles the "(Run Starting)" banner with
	// ANSI codes INSIDE the parens, so a whole-text Contains can't see it.
	cypressLog := false
	cypressSpec := ""
	addMocha := func(name string) {
		if cypressSpec != "" {
			add("cypress", cypressSpec+" › "+name)
		} else {
			add("mocha", name)
		}
	}
	// jest default-reporter state: the stats line is the framework
	// fingerprint (vitest says "Test Files", so it can't sneak in here);
	// a FAIL header sets the suite every ● title is qualified with.
	jestLog := strings.Contains(text, "Test Suites: ")
	vitestLog := strings.Contains(text, "Test Files ")
	// lit logs EMBED the failing test's own unittest output (lldb's dotest
	// prints the classic "======" + "FAIL: x (Mod.Class.x)" block inside
	// the lit-reported failure, seen live on llvm-project) — one failure,
	// two name forms. lit is the orchestrator; its names win, and the
	// unittest extractor stands down for the whole log.
	litLog := strings.Contains(text, "-- Testing: ") || strings.Contains(text, "Total Discovered Tests: ")
	// gtest binaries always print the "[==========]" run banner. When CTest
	// is the orchestrator (its "The following tests FAILED:" summary is in
	// the log), CTEST_OUTPUT_ON_FAILURE can embed a failing gtest binary's
	// own output — one real failure, two name forms. Same call as
	// lit-embeds-unittest: the orchestrator's names win and the gtest
	// extractor stands down for the whole log (never double-counts; CTest
	// names are stable identifiers even when the inner framework isn't
	// parseable at all, e.g. or-tools' Java tests).
	gtestLog := strings.Contains(text, "[==========]")
	ctestLog := strings.Contains(text, "The following tests FAILED:")
	// bazel's per-target summary lines are distinctive, but still gated on
	// the "Executed N out of M tests" stats line so prose can't match.
	bazelLog := strings.Contains(text, "Executed ") && strings.Contains(text, " out of ")
	// sbt's "Failed tests:" list only means failed *tests* when sbt itself
	// says so: the test task's TestsFailedException or the scripted
	// harness's runner are the fingerprints.
	sbtLog := strings.Contains(text, "sbt.TestsFailedException") || strings.Contains(text, "sbt.scriptedtest.ScriptedRunner")
	prevUnittestSep := false
	litSummary := false
	mesonSummary := false
	nextestSummary := false
	ctestSection := false
	sbtSection := false
	jestSuite := ""
	jestVerbose := map[string]bool{} // names added from ✕ lines
	jestDotLeaf := map[string]bool{} // leaf titles of ● blocks
	for _, raw := range strings.Split(text, "\n") {
		_, line, _ := splitLogTS(strings.TrimRight(raw, "\r"))
		line = stripANSI(line)
		if p := buildkitPrefixRE.FindString(line); p != "" {
			line = line[len(p):]
		}
		trimmed := strings.TrimSpace(line)

		if mochaAccum != nil {
			if trimmed == "" || len(mochaAccum) >= 8 {
				mochaAccum = nil // not a mocha block after all
			} else {
				done := strings.HasSuffix(trimmed, ":")
				mochaAccum = append(mochaAccum, strings.TrimSuffix(trimmed, ":"))
				if done {
					addMocha(strings.Join(mochaAccum, " › "))
					mochaAccum = nil
				}
				continue
			}
		}

		wasMinitestHeader := prevMinitestHeader
		prevMinitestHeader = minitestHeaderRE.MatchString(trimmed)
		wasNodeCoreBlock := prevNodeCoreBlock
		prevNodeCoreBlock = ""
		if m := nodeCoreBlockRE.FindStringSubmatch(trimmed); m != nil {
			prevNodeCoreBlock = m[1]
		}
		wasUnittestSep := prevUnittestSep
		prevUnittestSep = unittestSepRE.MatchString(trimmed)

		if litSummary {
			if m := litSummaryEntryRE.FindStringSubmatch(trimmed); m != nil {
				add("lit", m[1])
				continue
			}
			if trimmed != "" && !litSummaryOpenRE.MatchString(trimmed) {
				litSummary = false
			}
		}
		if litLog && litSummaryOpenRE.MatchString(trimmed) {
			litSummary = true
			continue
		}
		if mesonSummary {
			if m := mesonEntryRE.FindStringSubmatch(trimmed); m != nil {
				add("meson", strings.TrimSpace(m[1]))
				continue
			}
			if trimmed != "" {
				mesonSummary = false
			}
		}
		if trimmed == "Summary of Failures:" {
			mesonSummary = true
			continue
		}
		if ctestSection {
			if m := ctestEntryRE.FindStringSubmatch(trimmed); m != nil {
				if m[2] != "Disabled" && m[2] != "Not Run" {
					add("ctest", m[1])
				}
				continue
			}
			if trimmed != "" {
				ctestSection = false
			}
		}
		if trimmed == "The following tests FAILED:" {
			ctestSection = true
			continue
		}
		if sbtSection {
			if m := sbtFailedEntryRE.FindStringSubmatch(line); m != nil {
				add("sbt", m[1])
				continue
			}
			sbtSection = false
		}
		if sbtLog && sbtFailedOpenRE.MatchString(line) {
			sbtSection = true
			continue
		}
		if nextestSummary {
			if m := nextestEntryRE.FindStringSubmatch(trimmed); m != nil {
				add("nextest", m[1]+" "+m[2])
				continue
			}
			if nextestPassRE.MatchString(trimmed) {
				continue // flaky/leaky entries ultimately passed
			}
			if trimmed != "" {
				nextestSummary = false
			}
		}
		if nextestSummaryRE.MatchString(trimmed) {
			nextestSummary = true
			continue
		}

		if phpunitSection && phpunitCloseRE.MatchString(trimmed) {
			phpunitSection = false
		}
		if phpunitOpenRE.MatchString(trimmed) {
			phpunitSection = true
			continue
		}
		if !cypressLog && trimmed == "(Run Starting)" {
			cypressLog = true
			continue
		}
		if cypressLog {
			if m := cypressRunningRE.FindStringSubmatch(trimmed); m != nil {
				cypressSpec = m[1]
				// Each spec restarts: the previous spec's "N failing"
				// summary must not admit this spec's inline result marks.
				mochaGate = false
				continue
			}
		}
		if mochaFailingRE.MatchString(trimmed) {
			mochaGate = true
			continue
		}
		if trimmed == "Failing tests:" {
			xctestSummary = 40 // realm's live section: blanks + an xcresult block interleave
			continue
		}
		if xctestSummary > 0 {
			xctestSummary--
			if strings.HasPrefix(trimmed, "** TEST") { // "** TEST EXECUTE FAILED **" closes it
				xctestSummary = 0
			} else if m := xctestSummaryEntryRE.FindStringSubmatch(trimmed); m != nil {
				add("xctest", xctestName(m[1], m[2], m[3]))
				continue
			}
		}

		switch {
		case pytestFailedRE.MatchString(line):
			add("pytest", pytestFailedRE.FindStringSubmatch(line)[1])
		case pytestVerboseRE.MatchString(line):
			add("pytest", pytestVerboseRE.FindStringSubmatch(line)[1])
		case goFailRE.MatchString(line):
			add("go", goFailRE.FindStringSubmatch(line)[1])
		case litLog && litInlineFailRE.MatchString(trimmed):
			add("lit", litInlineFailRE.FindStringSubmatch(trimmed)[1])
		case !litLog && wasUnittestSep && unittestFailRE.MatchString(trimmed):
			m := unittestFailRE.FindStringSubmatch(trimmed)
			add("unittest", unittestName(m[1], m[2]))
		case cargoFailRE.MatchString(line):
			add("cargo", cargoFailRE.FindStringSubmatch(line)[1])
		case gtestLog && !ctestLog && gtestFailRE.MatchString(trimmed):
			add("gtest", gtestFailRE.FindStringSubmatch(trimmed)[1])
		case bazelLog && bazelFailRE.MatchString(trimmed):
			add("bazel", bazelFailRE.FindStringSubmatch(trimmed)[1])
		case strings.HasPrefix(trimmed, "✕ ") && jestFailRE.MatchString(trimmed):
			name := jestFailRE.FindStringSubmatch(trimmed)[1]
			jestVerbose[name] = true
			add("jest", name)
		case vitestLog && vitestFailRE.MatchString(trimmed):
			add("vitest", strings.ReplaceAll(vitestFailRE.FindStringSubmatch(trimmed)[1], " > ", " › "))
		case jestLog && jestSuiteRE.MatchString(trimmed):
			jestSuite = jestSuiteRE.FindStringSubmatch(trimmed)[1]
		case jestLog && jestSuite != "" && strings.HasPrefix(trimmed, "● "):
			if m := jestDotRE.FindStringSubmatch(trimmed); m != nil && !jestNonTest(m[1]) {
				title := m[1]
				if i := strings.LastIndex(title, " › "); i >= 0 {
					jestDotLeaf[title[i+len(" › "):]] = true
				} else {
					jestDotLeaf[title] = true
				}
				add("jest", jestSuite+" › "+title)
			}
		case wasNodeCoreBlock != "" && nodeCorePathRE.MatchString(trimmed):
			if p := nodeCorePathRE.FindStringSubmatch(trimmed)[1]; p == wasNodeCoreBlock || strings.HasSuffix(p, "/"+wasNodeCoreBlock) {
				add("node-core", p)
			}
		case rspecFailRE.MatchString(trimmed):
			add("rspec", rspecFailRE.FindStringSubmatch(trimmed)[1])
		case mavenFailRE.MatchString(line):
			add("maven", mavenFailRE.FindStringSubmatch(line)[1])
		case gradleFailRE.MatchString(trimmed):
			add("gradle", gradleRepetitionRE.ReplaceAllString(gradleFailRE.FindStringSubmatch(trimmed)[1], ""))
		case wasMinitestHeader && minitestNameRE.MatchString(trimmed):
			add("minitest", minitestNameRE.FindStringSubmatch(trimmed)[1])
		case dotnetMTPFailRE.MatchString(line):
			add("dotnet", dotnetMTPFailRE.FindStringSubmatch(line)[1])
		case dotnetVSTestFailRE.MatchString(trimmed):
			add("dotnet", dotnetVSTestFailRE.FindStringSubmatch(trimmed)[1])
		case avaFailRE.MatchString(trimmed):
			add("ava", avaFailRE.FindStringSubmatch(trimmed)[1])
		case xctestCaseFailRE.MatchString(trimmed):
			m := xctestCaseFailRE.FindStringSubmatch(trimmed)
			add("xctest", xctestName(m[1], m[2], m[3]))
		case xcbeautifyFailRE.MatchString(line):
			add("xctest", xcbeautifyFailRE.FindStringSubmatch(line)[1])
		case swiftTestingFailRE.MatchString(trimmed):
			m := swiftTestingFailRE.FindStringSubmatch(trimmed)
			if m[1] != "" {
				add("swift-testing", m[1])
			} else {
				add("swift-testing", m[2])
			}
		case exunitFailRE.MatchString(trimmed):
			add("exunit", exunitFailRE.FindStringSubmatch(trimmed)[1])
		case phpunitSection && phpunitTestRE.MatchString(trimmed):
			add("phpunit", phpunitTestRE.FindStringSubmatch(trimmed)[1])
		case phpunitSection && phpunitPhptRE.MatchString(trimmed):
			add("phpunit", phpunitPhptRE.FindStringSubmatch(trimmed)[1])
		default:
			// playwright decorates the line with trailing ─ rules.
			pw := strings.TrimRight(trimmed, "─ ")
			if m := playwrightFailRE.FindStringSubmatch(pw); m != nil {
				add("playwright", playwrightLineRE.ReplaceAllString(m[1], " › "))
				break
			}
			if mochaGate {
				if m := mochaStartRE.FindStringSubmatch(trimmed); m != nil {
					t := m[1]
					if strings.HasSuffix(t, ":") {
						addMocha(strings.TrimSuffix(t, ":"))
					} else {
						mochaAccum = []string{t}
					}
				}
			}
		}
	}
	// Drop go parents whose subtest was also captured: "--- FAIL: TestFoo"
	// always accompanies "--- FAIL: TestFoo/sub"; the subtest is the story.
	goNames := map[string]bool{}
	// Drop bare xctest method names when a qualified Class.method for the
	// same method was captured in the same log: Alamofire's macOS jobs
	// print BOTH "Test Case 'Class.testX' failed" and xcbeautify's
	// "##[error]testX, ..." annotation for one failure (seen live) — two
	// name forms for one test must not count twice.
	xctestQualified := map[string]bool{}
	for _, f := range out {
		switch f.framework {
		case "go":
			goNames[f.name] = true
		case "xctest":
			if i := strings.LastIndex(f.name, "."); i >= 0 {
				xctestQualified[f.name[i+1:]] = true
			}
		}
	}
	filtered := out[:0]
	for _, f := range out {
		if f.framework == "go" {
			parent := false
			for n := range goNames {
				if strings.HasPrefix(n, f.name+"/") {
					parent = true
					break
				}
			}
			if parent {
				continue
			}
		}
		if f.framework == "xctest" && !strings.Contains(f.name, ".") && xctestQualified[f.name] {
			continue
		}
		// jest --verbose prints BOTH a "✕ leaf" listing line and a
		// "● describe › leaf" failure block for one failure — drop the
		// bare verbose name when a ● block with the same leaf title was
		// captured in this log.
		if f.framework == "jest" && jestVerbose[f.name] && jestDotLeaf[f.name] {
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}

// jestNonTest reports whether a jest "● title" block is one of the reporter's
// non-test sections: console output, suite-level failures, and config
// warnings all share the failing-test block shape.
func jestNonTest(title string) bool {
	if title == "Console" || title == "Test suite failed to run" {
		return true
	}
	for _, p := range []string{"Validation Warning", "Validation Error", "Deprecation Warning", "Cannot log after tests are done"} {
		if strings.HasPrefix(title, p) {
			return true
		}
	}
	return false
}

// pickFlakyFailLogs orders flaky failures for sampling: round-robin across
// distinct (workflow, job) groups, newest first within each, so one
// pathological job cannot monopolize the budget.
func pickFlakyFailLogs(fails []flakyFail, n int) []flakyFail {
	byGroup := map[string][]flakyFail{}
	for _, f := range fails {
		k := f.wf + "\x00" + baseJobName(f.job.Name)
		byGroup[k] = append(byGroup[k], f)
	}
	keys := make([]string, 0, len(byGroup))
	for k := range byGroup {
		sort.Slice(byGroup[k], func(a, b int) bool {
			return byGroup[k][a].job.CompletedAt.After(byGroup[k][b].job.CompletedAt)
		})
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []flakyFail
	for round := 0; len(out) < n; round++ {
		added := false
		for _, k := range keys {
			if round < len(byGroup[k]) && len(out) < n {
				out = append(out, byGroup[k][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

// analyzeFlakyLogs fetches up to sample flaky-failure logs and names the
// failing tests. fails comes from computeFlaky (same-SHA fail+pass groups);
// eligibleRuns maps run IDs whose EVERY failed job was flaky-proven to
// those failed jobs — the only runs whose artifacts may honestly be read
// as flaky evidence (see the artifact fallback below).
func (c *Client) analyzeFlakyLogs(owner, repo string, fails []flakyFail, eligibleRuns map[int64][]Job, sample int, progress func(string)) *FlakyTestStats {
	st := &FlakyTestStats{LogsTotal: len(fails)}
	if c.Token == "" {
		st.Note = "naming flaky tests needs auth (job-log downloads 403 without a token, even on public repos); set GITHUB_TOKEN or run `gh auth login`"
		return st
	}
	if len(fails) == 0 {
		st.Note = "no flaky-job failures in the sampled runs to read logs from"
		return st
	}
	picked := pickFlakyFailLogs(fails, sample)
	progress(fmt.Sprintf("reading %d flaky-failure logs for test names…", len(picked)))

	type result struct {
		failures []testFailure
		skipped  bool
	}
	results := make([]result, len(picked))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var abort error
	var mu sync.Mutex
	for i, f := range picked {
		wg.Add(1)
		go func(i int, jobID int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			stop := abort != nil
			mu.Unlock()
			if stop {
				results[i].skipped = true
				return
			}
			text, err := c.GetJobLogs(owner, repo, jobID)
			if err != nil {
				var rle *RateLimitError
				if errors.As(err, &rle) {
					mu.Lock()
					abort = err
					mu.Unlock()
				}
				results[i].skipped = true
				return
			}
			results[i].failures = parseTestFailures(text)
		}(i, f.job.ID)
	}
	wg.Wait()

	type agg struct {
		framework string
		count     int
		commits   map[string]bool
		jobs      map[string]bool
	}
	byName := map[string]*agg{}
	for i, r := range results {
		if r.skipped {
			st.JobsSkipped++
			continue
		}
		st.LogsSampled++
		for _, f := range r.failures {
			g := byName[f.framework+"\x00"+f.name]
			if g == nil {
				g = &agg{framework: f.framework, commits: map[string]bool{}, jobs: map[string]bool{}}
				byName[f.framework+"\x00"+f.name] = g
			}
			g.count++
			g.commits[picked[i].sha] = true
			g.jobs[baseJobName(picked[i].job.Name)] = true
		}
	}
	st.Available = true
	fetched := make([]bool, len(picked))
	named := make([]bool, len(picked))
	for i, r := range results {
		fetched[i] = !r.skipped
		named[i] = len(r.failures) > 0
	}
	c.attachFlakyArtifactTests(owner, repo, st, picked, fetched, named, eligibleRuns, progress)
	if len(byName) == 0 {
		noun := "logs"
		if st.LogsSampled == 1 {
			noun = "log"
		}
		st.Note = fmt.Sprintf("no recognizable test failures in %d sampled %s (formats understood: %s) — the failures may be build/infra errors rather than tests", st.LogsSampled, noun, flakyFrameworkList)
		return st
	}
	for k, g := range byName {
		name := k[strings.IndexByte(k, 0)+1:]
		jobs := make([]string, 0, len(g.jobs))
		for j := range g.jobs {
			jobs = append(jobs, j)
		}
		sort.Strings(jobs)
		st.Tests = append(st.Tests, FlakyTest{
			Name: name, Framework: g.framework,
			Failures: g.count, Commits: len(g.commits), Jobs: jobs,
		})
	}
	sort.Slice(st.Tests, func(i, j int) bool {
		if st.Tests[i].Failures != st.Tests[j].Failures {
			return st.Tests[i].Failures > st.Tests[j].Failures
		}
		return st.Tests[i].Name < st.Tests[j].Name
	})
	if len(st.Tests) > 15 {
		st.Tests = st.Tests[:15]
	}
	return st
}

// How many eligible flaky runs may have their artifacts consulted per
// analysis, and how many artifact-named tests are kept. The download
// budget (maxRunArtifacts) is shared across the consulted runs.
const (
	maxFlakyArtifactRuns  = 2
	maxFlakyArtifactTests = 10
)

// attachFlakyArtifactTests is the JUnit-artifact fallback for --flaky-logs.
// A flaky run's uploaded test reports are consulted only when (a) its
// sampled logs were fetched but named no tests, and (b) every failed job in
// that run was itself flaky-proven (eligibleRuns) — otherwise a genuinely
// broken sibling job's failures would masquerade as flaky. Attribution is
// run-level: a name from a report failed in a run whose every failure the
// project itself treated as non-reproducible.
func (c *Client) attachFlakyArtifactTests(owner, repo string, st *FlakyTestStats, picked []flakyFail, fetched, named []bool, eligibleRuns map[int64][]Job, progress func(string)) {
	if len(eligibleRuns) == 0 {
		return
	}
	type runInfo struct {
		sha     string
		latest  time.Time
		fetched bool
		named   bool
	}
	runs := map[int64]*runInfo{}
	for i, f := range picked {
		if !fetched[i] {
			continue
		}
		ri := runs[f.job.RunID]
		if ri == nil {
			ri = &runInfo{sha: f.sha}
			runs[f.job.RunID] = ri
		}
		ri.fetched = true
		if named[i] {
			ri.named = true
		}
		if f.job.CompletedAt.After(ri.latest) {
			ri.latest = f.job.CompletedAt
		}
	}
	var candidates []int64
	for id, ri := range runs {
		if ri.fetched && !ri.named && eligibleRuns[id] != nil {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := runs[candidates[i]], runs[candidates[j]]
		if !a.latest.Equal(b.latest) {
			return a.latest.After(b.latest)
		}
		return candidates[i] > candidates[j] // deterministic
	})
	if len(candidates) > maxFlakyArtifactRuns {
		candidates = candidates[:maxFlakyArtifactRuns]
	}
	budget := maxRunArtifacts // shared download budget across runs
	type agg struct {
		artifact string
		runs     int
		commits  map[string]bool
	}
	byName := map[string]*agg{}
	reports, cases := 0, 0
	truncated := false
	for _, id := range candidates {
		if budget <= 0 {
			break
		}
		sc, err := c.scanRunArtifactsForTests(owner, repo, id, eligibleRuns[id], budget, maxFlakyArtifactTests, progress)
		if err != nil {
			var rle *RateLimitError
			if errors.As(err, &rle) {
				break
			}
			continue
		}
		st.ArtifactRunsChecked++
		budget -= sc.Scanned
		reports += sc.Reports
		cases += sc.Cases
		if sc.Truncated {
			truncated = true
		}
		for _, at := range sc.Tests {
			g := byName[at.Name]
			if g == nil {
				g = &agg{artifact: at.Artifact, commits: map[string]bool{}}
				byName[at.Name] = g
			}
			g.runs++
			g.commits[runs[id].sha] = true
		}
	}
	for name, g := range byName {
		st.ArtifactTests = append(st.ArtifactTests, FlakyArtifactTest{
			Name: name, Artifact: g.artifact, Runs: g.runs, Commits: len(g.commits),
		})
	}
	sort.Slice(st.ArtifactTests, func(i, j int) bool {
		if st.ArtifactTests[i].Runs != st.ArtifactTests[j].Runs {
			return st.ArtifactTests[i].Runs > st.ArtifactTests[j].Runs
		}
		return st.ArtifactTests[i].Name < st.ArtifactTests[j].Name
	})
	if len(st.ArtifactTests) > maxFlakyArtifactTests {
		st.ArtifactTests = st.ArtifactTests[:maxFlakyArtifactTests]
	}
	switch {
	case len(st.ArtifactTests) > 0:
		// The section speaks for itself — the truncation caveat below
		// still applies when the scan was cut short.
	case st.ArtifactRunsChecked > 0 && reports > 0:
		noun := "runs'"
		if st.ArtifactRunsChecked == 1 {
			noun = "run's"
		}
		st.ArtifactNote = fmt.Sprintf("test reports (JUnit XML/TRX/NUnit3/TestNG) in %d checked flaky %s artifacts record %d test cases and no failures — the flaky failure likely happened outside the reported tests (or the failing shard uploaded no report)", st.ArtifactRunsChecked, noun, cases)
	case st.ArtifactRunsChecked > 0:
		noun := "runs'"
		if st.ArtifactRunsChecked == 1 {
			noun = "run's"
		}
		st.ArtifactNote = fmt.Sprintf("no JUnit XML, TRX, NUnit3 or TestNG test reports found in %d checked flaky %s artifacts", st.ArtifactRunsChecked, noun)
	}
	if truncated {
		note := "the per-artifact parse budget left some report files unread — the failing-test list may be incomplete"
		if st.ArtifactNote != "" {
			st.ArtifactNote += "; " + note
		} else {
			st.ArtifactNote = note
		}
	}
}
