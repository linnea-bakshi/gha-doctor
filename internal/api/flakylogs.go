package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
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
}

// FlakyTest is one test (or suite entry) seen failing in flaky-job logs.
type FlakyTest struct {
	Name      string   `json:"name"`
	Framework string   `json:"framework"` // pytest / go / cargo / jest / vitest / playwright / mocha / ava / rspec / minitest / phpunit / exunit / maven / gradle / dotnet / xctest / swift-testing
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
	// swift-testing: "✘ Test testOverflow() failed after 0.519 seconds with 1 issue."
	//                "✘ Test testOverflow() recorded an issue at File.swift:342:19: ..."
	// Name must be a call ("testOverflow()", "foo(bar:)") or a quoted
	// display name — the run summary "✘ Test run with 452 tests ... failed
	// after ..." can never match. "✘ Suite ..." lines don't start "✘ Test ".
	swiftTestingFailRE = regexp.MustCompile(`^✘ Test (?:"([^"]+)"|(\w+\([^)]*\))) (?:failed after|recorded an issue)`)
)

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
	phpunitSection := false
	mochaGate := false
	xctestSummary := 0 // >0: inside "Failing tests:", lines left before giving up
	var mochaAccum []string
	// jest default-reporter state: the stats line is the framework
	// fingerprint (vitest says "Test Files", so it can't sneak in here);
	// a FAIL header sets the suite every ● title is qualified with.
	jestLog := strings.Contains(text, "Test Suites: ")
	vitestLog := strings.Contains(text, "Test Files ")
	jestSuite := ""
	jestVerbose := map[string]bool{} // names added from ✕ lines
	jestDotLeaf := map[string]bool{} // leaf titles of ● blocks
	for _, raw := range strings.Split(text, "\n") {
		_, line, _ := splitLogTS(strings.TrimRight(raw, "\r"))
		line = stripANSI(line)
		trimmed := strings.TrimSpace(line)

		if mochaAccum != nil {
			if trimmed == "" || len(mochaAccum) >= 8 {
				mochaAccum = nil // not a mocha block after all
			} else {
				done := strings.HasSuffix(trimmed, ":")
				mochaAccum = append(mochaAccum, strings.TrimSuffix(trimmed, ":"))
				if done {
					add("mocha", strings.Join(mochaAccum, " › "))
					mochaAccum = nil
				}
				continue
			}
		}

		wasMinitestHeader := prevMinitestHeader
		prevMinitestHeader = minitestHeaderRE.MatchString(trimmed)

		if phpunitSection && phpunitCloseRE.MatchString(trimmed) {
			phpunitSection = false
		}
		if phpunitOpenRE.MatchString(trimmed) {
			phpunitSection = true
			continue
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
		case cargoFailRE.MatchString(line):
			add("cargo", cargoFailRE.FindStringSubmatch(line)[1])
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
						add("mocha", strings.TrimSuffix(t, ":"))
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
// failing tests. fails comes from computeFlaky (same-SHA fail+pass groups).
func (c *Client) analyzeFlakyLogs(owner, repo string, fails []flakyFail, sample int, progress func(string)) *FlakyTestStats {
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
	sem := make(chan struct{}, 4)
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
	if len(byName) == 0 {
		noun := "logs"
		if st.LogsSampled == 1 {
			noun = "log"
		}
		st.Note = fmt.Sprintf("no recognizable test failures in %d sampled %s (formats understood: pytest, go test, cargo test, jest/vitest, playwright, mocha, ava, rspec, minitest, phpunit, exunit, maven surefire, gradle/junit, dotnet xunit/vstest) — the failures may be build/infra errors rather than tests", st.LogsSampled, noun)
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
