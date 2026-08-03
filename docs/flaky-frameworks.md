# Flaky-test frameworks

`gha-doctor --flaky-logs N` names the tests behind flaky jobs by reading the
logs of failed runs whose commit later passed — the project's own history
proving the failure didn't reproduce. Extraction anchors on each test
framework's **own failure-summary format**; every pattern below was written
against real CI logs, and anything that doesn't match one of them is
reported as "no recognizable test failures" rather than guessed at (see
[honesty.md](honesty.md)).

This page is the reference: one entry per supported framework family, with
the exact log shapes that are recognized.

The same extractors power `--run` deep dives: a red run's report names the
failing tests from the failed job's log (authenticated runs, up to 2 failed
jobs). There, too, no recognized output means no names — a build or infra
failure yields an honest log tail, not invented test names.

## Supported frameworks

### pytest (Python)

Short summary and verbose forms:

```
FAILED tests/test_lowlevel.py::test_pyopenssl_redirect - ConnectionError: ...
ERROR  tests/test_x.py::TestC::test_y - RuntimeError: ...
tests/test_x.py::test_y FAILED [ 12%]
```

Extracted name: `tests/test_lowlevel.py::test_pyopenssl_redirect`.

### go test

```
--- FAIL: TestClient/retries_on_502 (0.01s)
```

Parent tests collapse into their captured subtests (both lines appear in go
output; counting both would double-count one failure).

### cargo test (Rust)

```
test download::resume ... FAILED
```

### cargo-nextest (Rust)

Only nextest's end-of-run **Summary section** is parsed (anchored on a
live astral-sh/uv run; retry/crash/timeout variants on cargo-nextest
0.9.140 run against a probe crate): the `Summary [ ... ] N tests run:`
line opens it, and each final failure repeats exactly once beneath —
inline `TRY n FAIL` lines repeat per retry and are deliberately not
parsed. Failing statuses `FAIL`, `TMT` (timeout) and crash codes
(`SEGV`, `ABRT`, …) count; `FLAKY m/n` and `LEAK` entries ultimately
**passed** and never count (same bar as JUnit `flakyFailure`). Names are
emitted exactly as nextest cites them: `binary test-path`. The libtest
output nextest captures per failure is indented four spaces, so the
column-0-anchored `cargo test` extractor never fires on it — while a
genuine sibling `cargo test` invocation in the same job (doctests;
nextest can't run them) still extracts on its own.

```
     Summary [  89.095s] 4765 tests run: 4764 passed, 1 failed, 4 skipped
        FAIL [   1.004s] (2914/4765) uv::sync show_settings::run_pep723_script_preview_features
```

### jest (JavaScript)

The `--verbose` reporter's `✕` failure lines (duration suffix optional):

```
✕ uploads a file (12 ms)
```

The **default** reporter — what most CI logs contain — prints no `✕` lines
at all: failures are `●` blocks under a `FAIL <path>` suite header (and
repeat in a `Summary of all failing tests` section on projects past jest's
`summaryThreshold`). Those are parsed too, gated three ways: the log must
carry jest's own `Test Suites:` stats line, a `FAIL` header must have set
the current suite (the name is qualified with it), and the reporter's
non-test `●` blocks (`Console`, `Test suite failed to run`,
validation/deprecation warnings) are excluded:

```
FAIL e2e/__tests__/requireAfterTeardown.test.ts
  ● prints useful error for requires after test is done
```

Extracted name:
`e2e/__tests__/requireAfterTeardown.test.ts › prints useful error for requires after test is done`.
When `--verbose` prints both forms for one failure, the bare `✕` name
collapses into the qualified `●` twin (never counted twice).

### vitest (JavaScript)

Modern vitest marks failing tree lines with `×` (U+00D7 — not jest's `✕`),
and always ends with a `Failed Tests` summary whose `FAIL` headers carry
the full chain. Only those headers are parsed (so the tree lines can't
double-count), gated on vitest's own `Test Files` stats line — which jest
never prints — plus the required ` > ` chain, which jest's plain
`FAIL <path>` headers lack:

```
 FAIL  test/typechecker.test.ts > Typechecker > fails the run when the typechecker crashes
```

Extracted name:
`test/typechecker.test.ts › Typechecker › fails the run when the typechecker crashes`.

### Playwright

Numbered failure entries; `[project]` prefix kept, `:line:col` stripped so
the same test aggregates across commits even as the file shifts:

```
1) [webkit] › tests/page.spec.ts:827:5 › drag and drop ────────────
```

Extracted name: `[webkit] › tests/page.spec.ts › drag and drop`.

### mocha (JavaScript)

Numbered failure blocks (multi-line suite › title joined), gated on mocha's
`N failing` summary line — which exunit/phpunit logs never print, so their
numbered lists can't be mistaken for mocha's:

```
  3 failing

  1) http server
       keeps the connection alive:
```

### Cypress

Cypress runs each spec file through mocha, so its failures arrive as mocha's
numbered blocks — but every spec restarts numbering, and same-named failures
in different specs are different tests (seen live on cypress-realworld-app:
two specs both failed with "An uncaught error was detected outside of a
test", which unqualified would dedupe into one). Cypress's `(Run Starting)`
banner switches the mocha extractor into cypress mode: each
`Running:  <spec>  (i of n)` progress line sets the spec file that qualifies
every name captured while it's current.

```
  (Run Starting)

  Running:  components/cssClassHappyPath.cy.js                  (3 of 17)

  1 failing

  1) Widget - CSS class field
       should expose a CSS class field:
```

Extracted name:
`components/cssClassHappyPath.cy.js › Widget - CSS class field › should expose a CSS class field`.

### ava (JavaScript)

```
✘ [fail]: retries the flaky endpoint
```

### RSpec (Ruby)

The `Failed examples:` block:

```
rspec ./spec/api_spec.rb:12 # Api::Client does a thing
```

### minitest (Ruby)

The name line directly after a `Failure:` / `Error:` header (numbered or
not) — the header gate keeps arbitrary `Class#test_x` prose out:

```
Failure:
UserTest#test_login [test/user_test.rb:143]:
```

### PHPUnit

Numbered entries **inside** `There was 1 failure:` / `There were N errors:`
sections only — skipped/risky/deprecation lists use the same numbered shape
and are excluded (a laravel run failing with only deprecations proved the
false positive live). Data-provider suffixes are dropped so cases
aggregate; `.phpt` test files are supported:

```
There was 1 failure:

1) Tests\CarbonTest::testDiffForHumans with data set #3
```

### ExUnit (Elixir)

```
1) test works with converged deps (Mix.Tasks.DepsTest)
```

### Maven Surefire (Java)

```
[ERROR]   ClientTest.testRetry:34 expected: <200> but was: <502>
```

### Gradle / JUnit (Java, Kotlin, Spock)

Test-event lines — the first segment must look like a class name, so
`> Task :x:test FAILED` can't match. `@RepeatedTest` repetitions collapse
into one test:

```
ClientTest > [1] region = eu > uploads() FAILED
```

### .NET (xunit v3 / Microsoft.Testing.Platform, VSTest)

MTP's `failed` lines require a dotted fully-qualified name (prose like
"failed to connect" can't match); VSTest's `Failed` lines require the
`[duration]` suffix:

```
failed Namespace.ClassTests.Method(arg: 3)
Failed Namespace.ClassTests.Method [12 ms]
```

### XCTest (Swift / Objective-C)

All four shapes normalize to `Class.method` so one failure aggregates
across formats — Darwin and Linux `Test Case` lines, xcodebuild's
end-of-run `Failing tests:` summary, and xcbeautify's renderers (the
`##[error]` GitHub annotation form and the `✖` default form; bare method
names collapse into a same-log qualified twin when both appear for one
failure, seen live on Alamofire):

```
Test Case '-[AlamofireTests.SessionTests testRetry]' failed (21.346 seconds).
Test Case 'SessionTests.testRetry' failed (0.003 seconds)
	SwiftUITests.testSampleApp()
##[error] testRetry, XCTAssertEqual failed: ("502") is not equal to ("200")
```

### swift-testing

Failure and issue lines for named or display-named tests — the run summary
(`✘ Test run with 452 tests failed after …`) and `✘ Suite` lines
structurally can't match:

```
✘ Test testOverflow() failed after 0.519 seconds with 1 issue.
✘ Test "parses RFC 3339 dates" recorded an issue at DateTests.swift:342:19: ...
```

### Python unittest (incl. Django's test runner)

The classic failure block: a full-width `=` separator directly above
`FAIL:`/`ERROR:` and the test's qualified name in parens (anchored on a
live django/django run). Python ≥3.11 puts the fully qualified
`module.Class.test_x` in the parens; older interpreters put
`module.Class`, so the method is appended — both normalize to the same
dotted name. Subtest decorations (`(i=3)`, `[msg]`) are dropped so
subtests aggregate. Ungated `FAIL:` lines (no separator above) never
match. pytest can't arm the gate: its section rules always carry text
between the `=` runs.

```
======================================================================
FAIL: test_database_sharing_in_threads (backends.sqlite.tests.ThreadSharing.test_database_sharing_in_threads)
----------------------------------------------------------------------
```

### LLVM lit

Inline `FAIL: suite :: path` progress lines and the end-of-run
`Failed Tests (N):` summary (anchored on a live llvm-project run), gated
on lit's own banner/stats fingerprint. lit **embeds** the failing test's
own output — lldb's dotest prints a full unittest failure block inside
the lit failure — so in lit logs the unittest extractor stands down:
one failure, one name, lit's.

```
FAIL: lldb-api :: functionalities/scripted_frame_provider/TestFrameProvider.py (383 of 3796)
Failed Tests (1):
  lldb-api :: functionalities/scripted_frame_provider/TestFrameProvider.py
```

### meson test

Only the end-of-run `Summary of Failures:` section is parsed (anchored on
a live systemd run; statuses `FAIL`/`ERROR`/`TIMEOUT`/`UNEXPECTEDPASS`),
closing at meson's `Ok:` stats — the identical live-progress lines above
the summary can't double-count, and prose outside the section can't
match.

```
Summary of Failures:
1353/1904 libsystemd - systemd:test-varlink   FAIL   0.36s   exit status 1
```

### GoogleTest (C++)

Inline failure prints and the end-of-run `listed below:` section (anchored
on live opencv and tesseract runs), gated on gtest's `[==========]` run
banner. Both prints dedupe to one name; `, where GetParam() = ...` clauses
and `(N ms)` durations are dropped, parameterized instance suffixes
(`/0`) are kept; the `[  FAILED  ] N tests, listed below:` count line has
no `.` in its "name" and can't match.

```
[  FAILED  ] Test_TensorFlow_layers.batch_norm_11/0, where GetParam() = OCV/CPU (0 ms)
[  FAILED  ] 1 test, listed below:
[  FAILED  ] Test_TensorFlow_layers.batch_norm_11/0, where GetParam() = OCV/CPU
```

### CTest (C++/CMake)

Only entries under CTest's `The following tests FAILED:` section are
parsed (anchored on live or-tools and google/benchmark runs; statuses seen
live include `Failed`, `Timeout`, `ILLEGAL`, `Exit code 0xc0000409`,
`Subprocess aborted`; `Disabled`/`Not Run` are excluded). When
`CTEST_OUTPUT_ON_FAILURE` embeds a failing gtest binary's own output, the
orchestrator's names win and the gtest extractor stands down — one
failure, one name (same rule as lit-embeds-unittest).

```
The following tests FAILED:
	245 - java_mathopt_JniSolverTest (Failed)
```

### Bazel

Per-target summary lines (anchored on a live protobuf run), gated on
bazel's `Executed N out of M tests` stats line. Flaky-retry
(`FAILED in 2 out of 3 in 15.3s`) and `TIMEOUT` forms count;
`FAILED TO BUILD` and `NO STATUS` are build problems, not test failures,
and can't match.

```
//upb/conformance:test_conformance_upb                                   FAILED in 1.2s
```

### Node.js core test harness (`tools/test.py`)

Node core's own Python harness (nodejs/node CI, anchored on live
test-macOS and test-internet runs). Each failing test prints a block
opened by `=== release <name> ===` (or `=== debug <name> ===`) with
`Path: <suite>/<name>` directly beneath — only that adjacent pair
matches, and the `Path:` value must end with the block's own name, so
neither line alone can be mistaken for a failure in prose or embedded
output.

```
=== release test-debugger-probe-activation ===
Path: parallel/test-debugger-probe-activation
##[error]--- stderr ---
Error: Timeout (15000) while waiting for /break (?:on start )?in/i
```

Extracted name: `parallel/test-debugger-probe-activation` — the
suite-qualified form node contributors cite (the same basename exists in
several suite directories).

### Tests inside `docker build`

Docker BuildKit streams RUN-step output as `#12 792.5 <line>` — a prefix
that hides every framework's markers. That prefix is stripped before any
extractor looks at a line (anchored on a live or-tools run whose whole
CTest suite runs inside `docker build`), so tests run in a container
build still get named.

## What deliberately doesn't match

A false "flaky test" name is worse than a miss. Compiler errors, linker
failures, infrastructure timeouts, coverage-upload failures, docs builds,
psalm/static-analysis output, and skipped/risky/deprecation lists all
extract **zero** tests — enforced by a negative corpus of real failure
logs (Go, Python, PHP, Swift build failures and more) that every extractor
must stay silent on. When a flaky job's logs contain none of the shapes
above, the report says *"no recognizable test failures"* and lists what it
saw instead of guessing.

Names are deduped per log, aggregated across logs, and the section always
states how many logs were read out of how many exist. Reading logs needs
auth (`GITHUB_TOKEN` or `gh` login); without it the section says so
honestly instead of silently shrinking.

## The fallback: JUnit XML test-report artifacts

Console output isn't the only place failing tests are recorded. Most
runners can write the industry-standard JUnit XML report file (`pytest
--junitxml`, Maven surefire, Gradle, jest-junit, `ctest --output-junit`,
`go test` via gotestsum, …), and many workflows upload it with
`actions/upload-artifact` for dashboards to consume.

In a `--run` deep dive, when **no failed job's log** matched any of the
formats above, gha-doctor lists the run's artifacts, downloads up to 4
whose names look like test reports (`junit`, `surefire`, `test-results`,
`test-report`, … — coverage/screenshot/video uploads are excluded), and
parses every JUnit-shaped XML inside for `<testcase>` entries with a
direct `<failure>` or `<error>` child. That names the failing tests for
*any* framework — including ones with no console extractor — as long as
the run uploads the report.

The honesty rules for this source:

- **Run-level attribution only.** Artifacts belong to the run, not to a
  job, so these names appear in their own "from test-report artifacts"
  section with the source artifact named — never pinned to a failed job.
- **Zero failures recorded ≠ no test failed.** A report covering only the
  green shards proves nothing about the red one; the report says "record
  N test cases and no failures — the failure likely happened outside the
  reported tests (or the failing shard uploaded no report)".
- **Retries that passed don't count.** Surefire's `<flakyFailure>` /
  `<rerunFailure>` and `<skipped>` entries are not failures.
- Needs auth (artifact downloads 403 unauthenticated), caps apply (4
  artifacts, 30 MiB each), and expired artifacts produce a note, not
  silence.

`--flaky-logs` uses the same fallback, with one extra gate. A flaky run's
artifacts are consulted only when its sampled logs named nothing **and
every failed job in that run was itself flaky-proven** (failed and passed
on the same commit) — otherwise a genuinely broken sibling job's failures,
recorded in the same run-scoped artifact, would masquerade as flaky. At
most 2 such runs are checked per analysis, sharing the 4-download budget,
and the names appear in their own run-level subsection.

## Missing a framework?

Open a [rule/feature proposal](https://github.com/linnea-bakshi/gha-doctor/issues)
with a link to a real, public failed-run log — every extractor here was
anchored on one, and that's the bar that keeps false positives out.
