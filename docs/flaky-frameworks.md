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

## Missing a framework?

Open a [rule/feature proposal](https://github.com/linnea-bakshi/gha-doctor/issues)
with a link to a real, public failed-run log — every extractor here was
anchored on one, and that's the bar that keeps false positives out.
