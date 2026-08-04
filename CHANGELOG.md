# Changelog

All notable changes, mirrored from the
[GitHub releases](https://github.com/linnea-bakshi/gha-doctor/releases)
(the source of truth) by `scripts/gen-changelog.sh`. Newest first.

## [v0.59.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.59.0) — 2026-08-04

### doctest failing-test extraction — 28th framework family

`--flaky-logs` and `--run` deep dives now name failing tests from **doctest** (C++) — the framework Godot's entire unit-test suite runs on — by parsing doctest's own failing `TEST CASE:` blocks.

- Both position styles are recognized: `file:line: ERROR:` (gcc/clang) and `file(line): ERROR:` (MSVC). `CHECK`/`REQUIRE` failures (`is NOT correct!`, `FATAL ERROR:`) and thrown exceptions (`ERROR: test case THREW exception:`) all count.
- A case is named only once a real positioned `ERROR:` line appears under its block. Blocks also print for `MESSAGE`/`WARN` logging, and application log noise — Godot's own `ERROR: Drawing is only allowed…` lines — or sanitizer `runtime error:` lines carry no `file:line: ERROR:` shape and can't match.
- One failing test case prints one block per failing subcase path; they dedupe to one name. When CTest orchestrates, its summary wins and the doctest extractor stands down (the GoogleTest rule).
- The fingerprint is doctest's own `[doctest]` line prefix, detected **per line after ANSI stripping**: color builds put a reset code inside the line, between `[doctest] ` and the rest, so a whole-log substring check misses it — Linux color logs would have extracted nothing.

Anchored on live godotengine/godot CI (run 30864864399): the Linux gcc editor log (ANSI-colored, sanitizers enabled) and the Windows MSVC editor log both name the same four failing `[SceneTree][TabBar]`/`[TabContainer]` test cases.

Docs: [flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks) has the exact recognized shapes.


## [v0.58.2](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.58.2) — 2026-08-04

### v0.58.2

**Fix: `--version` is honest for `go install` builds.**

A plain `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` produced a binary whose `--version` printed `dev`, even though it was built from an exact released version. The binary now falls back to the Go module version recorded in its build info when the release ldflags are absent:

- `go install .../cmd/gha-doctor@v0.58.2` → `gha-doctor 0.58.2`
- a local `go build` in a git checkout → the VCS-stamped pseudo-version (e.g. `0.58.2-0.20260804…+dirty`) instead of `dev`
- release archives, Homebrew/Scoop/aqua/asdf/Docker/gh-extension builds are unchanged (ldflags always win)

No behavior changes beyond the version string.


## [v0.58.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.58.1) — 2026-08-04

Honesty patch for `--flaky-logs` on repos with shortened log retention, found dogfooding against ziglang/zig (which serves HTTP 410 for days-old logs).

#### Fixed
- **When no flaky-failure logs can be fetched, the report now says so.** Previously it printed "no recognizable test failures in 0 sampled logs — the failures may be build/infra errors", implying the logs were read. It now reads "none of the N flaky-failure logs could be fetched (expired or inaccessible)".
- **Partial fetch failures are disclosed in the note itself** ("; N more logs could not be fetched"), so `--md`, `--html` and `--json` consumers see it too — the old disclosure line only rendered in terminal mode, and only when tests *were* named.
- **The test-report artifact fallback now covers fetch-failed runs.** A flaky run whose logs are gone named nothing from console — and artifacts expire on their own retention clock — so its uploaded JUnit XML/TRX/NUnit3/TestNG reports are now consulted. The honesty-critical gate is unchanged: every failed job in the run must itself be flaky-proven.

Regression tests for all three behaviors; docs/honesty.md and docs/flaky-frameworks.md updated. No lint-engine changes.


## [v0.58.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.58.0) — 2026-08-04

### sbt failing-test extraction — 27th framework family

`--flaky-logs` and `--run` deep dives now name failing tests from **sbt** builds (Scala), whatever the inner framework — junit-interface, ScalaTest, or munit — by parsing sbt's own `[error] Failed tests:` summary section.

- Recognized only when sbt's own fingerprints appear in the log — `sbt.TestsFailedException` (the test task) or `sbt.scriptedtest.ScriptedRunner` (sbt's scripted plugin-test harness). An sbt build that fails without a test summary (compile errors, mdoc, lint exits) names nothing, per the [honesty rules](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks).
- The scripted harness prints its failed-test list twice (once under a `java.lang.RuntimeException` with a stack trace, once under `(scripted)`); one failure yields one name, and stack frames can't match the entry shape.
- Builds that interpose their own timestamp before sbt's level tag (akka's nightly JDK matrix does) still match.

Every pattern is anchored on real logs fetched from live CI: scala/scala3's Compiler Tests (`dotty.tools.debug.DebugTests`), akka's nightly cluster suites (both timestamp forms), and sbt/sbt's own scripted runs (`lm-coursier/from-no-head`). Live negatives — a typelevel/cats mdoc site failure, a job whose tests all passed, and an sbt lint-warning exit — all correctly extract zero.

Docs: [flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks) has the exact recognized shapes.


## [v0.57.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.57.0) — 2026-08-04

### TestNG test-report artifacts — and truncated scans that say so

The test-report artifact fallback (used by `--run` deep dives and `--flaky-logs` when console logs name nothing) now parses **native TestNG results** (`testng-results.xml`) as its fourth format, alongside JUnit XML, .NET TRX and NUnit3. Plain `testng` invocations, Gradle TestNG tasks and selenium-style automation suites often upload only that file.

Anchored on a real 35 MB `testng-results.xml` from a failed JanssenProject/jans integration run:

- **RetryAnalyzer attempts don't count.** `status="SKIP" retried="true"` entries (4,713 in that file) are neither failures nor recorded cases.
- **Lifecycle methods are excluded from the case count** (`is-config="true"`), but a *failing* `@BeforeMethod`/`@AfterClass` is still named — it's why the tests after it died.
- **`testng-failed.xml` is rejected.** It's the rerun *suite definition*, not a results document.
- `testng` in an artifact name now ranks alongside `junit`/`trx`/`nunit` when choosing which artifacts to download.

The same live run exposed two honesty bugs in the artifact scanner, both fixed:

- **Truncated scans now say so.** That artifact carries 650 XML files; the old 200-file cap reported 527 of 3,096 failing entries with no hint the scan was partial. Report files are now read smallest-first (the budget covers many small reports before one huge file), the caps are 2,000 files / 128 MiB uncompressed, and when a budget leaves candidate files unread, every output mode says the list *may be incomplete* — including alongside named tests, and in the `--md` flaky section, which previously never rendered the artifact note at all.
- **Overlong parameterized names collapse instead of vanishing.** Surefire writes `method[full argument list…]` names (586 chars observed live); the old >200-char guard silently dropped 1,544 of 3,096 failing entries. The bracket clause is now stripped on overflow, collapsing re-invocations into their parameterized parent — the same aggregation the console extractors already apply to gtest `GetParam()` and phpunit data-provider suffixes.

Verified live: the jans deep dive names exactly the 1,552 distinct failing tests that independent ground-truth parsing of the report finds.

Committed testdata is verbatim-excerpted from the real jans report (provenance noted in the test file).

---

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `aqua g -i linnea-bakshi/gha-doctor` · `docker run ghcr.io/linnea-bakshi/gha-doctor` · [more options](https://github.com/linnea-bakshi/gha-doctor#install)


## [v0.56.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.56.0) — 2026-08-03

### v0.56.0 — NUnit3 test-report artifacts

The test-report artifact fallback (`--run` deep dives and `--flaky-logs`) now parses **NUnit3 `<test-run>` reports** alongside JUnit XML and TRX — the format written by `nunit3-console --result`, NunitXml.TestLogger (`dotnet test --logger nunit`) and, the big population, **Unity's test runner** (game-ci/unity-test-runner uploads `*-results.xml` artifacts by default).

Live example: unitystation/unitystation run 30821876456 fails with only `##[error]The process '/usr/bin/docker' failed with exit code 2` in the console — the deep dive now names the actual failing Unity EditMode test `Tests.ScanCode.ScanCodeReport` from its "Raw Test Results" artifact.

Details, all anchored on real reports (a failing Unity EditMode result from the run above + NunitXml.TestLogger output generated on a runner):

- Root must be a namespace-free `<test-run>` with a `testcasecount` fingerprint; TRX's namespaced `<TestRun>` and JUnit's `<testsuites>` can't collide with it.
- Only `<test-case>` leaves count — suites carry `result="Failed"` and their own `<failure>` blocks too, so an assembly's aggregate failure can never double-count its cases.
- `result="Failed"` counts, including `label="Error"` (exception) and `label="Invalid"` (non-runnable) — both fail CI and name a real culprit. `label="Cancelled"` (the run was aborted around the test) does not — the same bar that keeps TRX's `Aborted` out. `Skipped`/`Inconclusive` never count.
- `nunit` in artifact names ranks with `junit`/`surefire`/`trx` when choosing which artifacts to download.
- Notes and docs now say "JUnit XML/TRX/NUnit3" wherever the fallback is described.

No CLI or schema changes; everything else identical to v0.55.0.

## [v0.55.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.55.0) — 2026-08-03

### TRX test-report artifacts — failing .NET tests named from `dotnet test --logger trx` uploads

The artifact fallback in `--run` deep dives and `--flaky-logs` now reads
**TRX** (the Visual Studio TestRun format) alongside JUnit XML. .NET repos
routinely upload TRX while build wrappers (Cake, MSBuild, tee'd logs) hide
the console output the log extractors read — those runs now get exact
failing-test names anyway.

- The root element must be `<TestRun>` in the TeamTest namespace, so
  arbitrary XML can't masquerade as a test report.
- Failing outcomes are `Failed`, `Error` and `Timeout` — the same bar as
  JUnit's direct `<failure>`/`<error>` children. `NotExecuted` and
  `Inconclusive` are not failures, and `Aborted` usually means cancelled;
  none of those count.
- Data-driven tests nest per-row results under `<InnerResults>`; only leaf
  rows are counted, so an aggregate parent can never double-count.
- Assembly-qualified class names (`Ns.Class, Assembly, Version=…`) are
  trimmed to `Ns.Class`; already-qualified xunit test names aren't doubled.
- `.trx` files inside artifact zips are scanned, and a TRX document a
  custom logger saved with a `.xml` extension still parses. Artifact names
  containing `trx` rank as test reports for the download cap.

Anchored on a **real failing TRX** written by `dotnet test` (xunit
adapter) on a GitHub runner plus a real passing TRX from a public
[nunit/nunit](https://github.com/nunit/nunit) run artifact — both
committed as test data. On that repo's red macOS run (`Error: Tests
failed. 0 test(s) reported as failed`), gha-doctor previously said "no
JUnit XML test reports found in 3 scanned artifact(s)"; it now honestly
reports **75,977 recorded test cases and no failures**, pointing the
reader at a crash outside the recorded tests. See
[flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks#the-fallback-test-report-artifacts-junit-xml-and-trx).


## [v0.54.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.54.0) — 2026-08-03

### cargo-nextest failing-test extraction — 26 framework families

`--flaky-logs` and `--run` deep dives now name failing tests from
[cargo-nextest](https://nexte.st/) output, the modern Rust test runner.

- Only nextest's end-of-run **Summary section** is parsed — each final
  failure is listed there exactly once, so retried tests (`TRY n FAIL`
  printed once per try inline) can never double-count.
- Failing statuses `FAIL`, `TMT` (timeout) and crash codes (`SEGV`,
  `ABRT`, …) count. `FLAKY m/n` and `LEAK` entries ultimately **passed**
  and are never extracted — the same bar as JUnit `flakyFailure`.
- Names are emitted exactly as nextest cites them: `binary test-path`,
  e.g. `uv::sync show_settings::run_pep723_script_preview_features`.
- The libtest output nextest captures per failure is indented four
  spaces, so the column-0-anchored `cargo test` extractor never fires on
  it — while a genuine sibling `cargo test` invocation in the same job
  (doctests; nextest can't run them) still extracts on its own.

Anchored on a live astral-sh/uv CI failure — which previously named
nothing from console (the JUnit-artifact fallback caught it only because
uv uploads junit XML; most nextest repos don't) — plus retry, crash and
slow-timeout ground truth generated with cargo-nextest 0.9.140 against a
probe crate. Negative corpus grew by four real logs (compile failure,
clippy failure, SIGILL crash without a summary, disk-full): all extract
zero. See [flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks).

## [v0.53.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.53.0) — 2026-08-03

### Flaky tests from JUnit artifacts — `--flaky-logs` names tests even when the framework speaks no recognized format

`--run` deep dives learned in v0.52.0 to fall back to JUnit XML test-report artifacts when a failed job's console log matches none of the [25 recognized framework formats](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks). **`--flaky-logs` now uses the same fallback** — so flaky tests get named even for bespoke harnesses, as long as the run uploads a JUnit-shaped report.

The extra honesty gate that makes this sound:

- A flaky run's artifacts are consulted **only when every failed job in that run was itself flaky-proven** (failed and passed on the same commit). Artifacts are run-scoped — without this gate, a genuinely broken sibling job's failures recorded in the same artifact would masquerade as flaky.
- Consulted only when that run's sampled logs were fetched but named nothing; at most 2 runs per analysis, sharing the 4-download / 30 MiB-each budget; `timed_out` / `startup_failure` siblings disqualify the run.
- Names appear in their own run-level subsection ("From test-report artifacts") in terminal, `--md`, `--html` and `--json` (`analysis.flaky_tests.artifact_tests` + `artifact_runs_checked` + `artifact_note`; report schema regenerated). They are never merged into the per-job flaky-test table.
- A report recording zero failures produces a note saying exactly that — the failing shard may simply not have uploaded its report.

Internals: the `--run` artifact scan (name ranking → failed-job affinity → smallest-first, direct `<failure>`/`<error>` children only) is now a shared helper, so the two consumers cannot drift. Also fixed a data race in a test harness (unsynchronized page counter vs the speculative parallel page fetch introduced in v0.42.1) — test-only, no shipped-code change.


## [v0.52.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.52.1) — 2026-08-03

### v0.52.1

#### The failing shard's own test report can no longer be dropped by the download cap

v0.52.0's JUnit-artifact fallback downloads at most 4 candidate artifacts,
smallest-first within a name rank. On matrix runs that upload one
`test-results-*` artifact per shard, that ordering was blind to *which*
shard failed — if the failing shard's upload happened to be on the large
side of its siblings, the one report that mattered could fall outside the
cap. (Live case: an isomorphic-git BrowserStack matrix uploads 5
same-rank test-report artifacts; the failing `bs_safari` shard's report
survived the cap by exactly one slot of luck.)

Candidate ordering now prefers artifacts whose names share distinctive
tokens with a failed job's name (`test-results-bs_safari` for job
`Test (bs_safari)`) before falling back to smallest-first, so the failing
shard's upload is fetched first. Name rank still comes first; generic
tokens (`test`, `results`, `report`, …) don't count as a match.

Also since v0.52.0: `.gha-doctor.yml` config schema is now in the
[SchemaStore catalog](https://www.schemastore.org/) — editors with the
YAML language server autocomplete and validate the config file
automatically, no modeline needed (docs updated).

**Full Changelog**: https://github.com/linnea-bakshi/gha-doctor/compare/v0.52.0...v0.52.1


## [v0.52.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.52.0) — 2026-08-03

### v0.52.0

#### `--run` deep dives now read JUnit XML test-report artifacts

When a red run's failed jobs speak no recognized console format — a
bespoke harness, a quiet reporter, a framework without a
[log extractor](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks)
— the run may still have uploaded the industry-standard **JUnit XML test
report** (`pytest --junitxml`, Maven surefire, Gradle, jest-junit,
`ctest --output-junit`, gotestsum, …). `--run` now falls back to that:
the run's artifacts are listed, up to 4 test-report-shaped uploads are
downloaded (coverage/screenshot/video artifacts excluded; 30 MiB cap
each), and every JUnit-shaped XML inside is parsed for `<testcase>`
entries with a direct `<failure>` or `<error>` child.

The report gains a run-level **"Failing tests — from test-report
artifacts"** section (also in `--md`/`--html`, and as
`run.artifact_failed_tests` in `--json` — the
[schema](https://linnea-bakshi.github.io/gha-doctor/schema) is updated),
and the verdict lines name the first failing test.

Honesty rules for the new source:

- **Run-level attribution only.** Artifacts belong to the run, not to a
  job, so these names are never pinned to a specific failed job; the
  source artifact is named instead.
- **Zero failures recorded ≠ no test failed.** A report covering only
  green shards proves nothing about the red one — the report says so in
  an explicit note rather than staying silent.
- **Retries that passed don't count.** Surefire's `<flakyFailure>`/
  `<rerunFailure>` and `<skipped>` entries are not failures.
- Only consulted when console extraction named nothing (no extra
  downloads when the log already told the story); needs auth, like job
  logs; expired artifacts produce a note.

No lint-rule changes; local scans are unaffected.


## [v0.51.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.51.0) — 2026-08-03

### v0.51.0

#### Failing-test extraction: Node.js core (25th framework family)

`--flaky-logs` and `--run` deep dives now name failing tests from **Node.js
core's own test harness** (`tools/test.py`, the runner behind nodejs/node
CI). Anchored on live test-macOS and test-internet runs: a failing test
prints `=== release test-x ===` with `Path: parallel/test-x` directly
beneath — only that adjacent pair matches, and the `Path:` value must end
with the block's own name, so neither line alone can be mistaken in prose
or embedded output. The suite-qualified name (`parallel/test-x`) is
emitted, the form node contributors cite. See
[flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks).

#### Fix: huge job logs no longer lose their ending

Job logs over the 10 MiB cap were read head-first, silently dropping the
end — where test-failure summaries, harness verdicts and `##[error]`
markers actually print. Found live: nodejs/node test-macOS logs run
~12 MiB, so `--run` showed a mid-file log tail and named no tests. Logs
are now streamed to EOF keeping the **last** 10 MiB (torn first line
dropped). This fixes `--run` log tails, flaky-test extraction and
cache-log sampling on very large logs alike.


## [v0.50.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.50.0) — 2026-08-03

### Cypress failing-test extraction — 24 framework families

`--flaky-logs` and `--run` deep dives now name failing **Cypress** tests,
qualified by spec file:

```
cypress  components/cssClassHappyPath.cy.js › Widget - CSS class field › should expose a CSS class field
```

Cypress runs each spec through mocha, so its failures were already being
extracted as mocha's numbered blocks — but every spec file restarts
numbering, and same-named failures in different specs deduped into one
(seen live on cypress-realworld-app: two specs each failed with "An
uncaught error was detected outside of a test" and were counted once).
Cypress's `(Run Starting)` banner now switches the mocha extractor into
cypress mode: each `Running:  <spec>  (i of n)` progress line sets the
spec file that qualifies every captured name — and resets the `N failing`
gate so one spec's summary can never admit the next spec's inline result
marks.

Patterns anchored on real logs, as always: a live ToolJet
`Cypress-AppBuilder` run (30 failures across 9 specs, Cypress 15) and a
cypress-realworld-app `ui-firefox-mobile-tests` run. Three real
cypress-workflow *infrastructure* failures (VPN setup, docker pull,
bench setup) joined the negative corpus — all extract zero, so a broken
build still honestly reports "no recognizable test failures" instead of
inventing names.

Plain mocha logs (no cypress banner) are labeled `mocha`, unchanged —
the full 67-log real-world corpus reproduces byte-identically apart from
the new cypress entries.

Docs: [flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks)
has the new family's exact recognized shapes.

Also riding this release (shipped on main since v0.49.0):

- **[Recipes cookbook](https://linnea-bakshi.github.io/gha-doctor/recipes)** —
  copy-paste workflows for the PR gate, weekly health report, badge +
  sparkline, SARIF upload, Prometheus/Grafana, org fleet report and
  pre-commit. Every workflow snippet on the page is linted by the test
  suite and must come out clean.
- pre-commit fix-hook description no longer hardcodes a stale fixable-rule
  count.
- CI now measures merged unit + integration coverage (84.9%) with an 80%
  floor.


## [v0.49.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.49.0) — 2026-08-03

### v0.49.0 — CI health as a Grafana dashboard

#### `--prom`: Prometheus text-format export

```sh
gha-doctor --prom ci-health.prom     # alongside the normal report
gha-doctor --fail-on never --prom -  # metrics to stdout, report-only exit
```

Every measured aggregate, as gauges: health score, findings by severity,
per-workflow success ratios and p50/p95 durations, queue time, wasted and
rounded-up compute (seconds **and** USD), flaky jobs, zombie crons, cache
size against the 10 GB limit, superseded-run waste, PR feedback time.
Run it on a schedule — node_exporter textfile collector or a Pushgateway
push from a scheduled workflow (README has both wirings) — and CI health
becomes a dashboard with real history instead of a point-in-time report.

The honesty rules carry over from `--json`:

- an **unmeasured** section (no history sample, cache API unavailable, too
  few qualifying PR pushes) emits **no series at all** — a gap on the
  dashboard is the truth; a zero-filled series would be a lie;
- a **measured zero** (zero flaky jobs across a sampled window) is a real
  `0` — "we looked, it's zero" is information;
- success ratios / duration percentiles for workflows with **no decisive
  runs** are undefined, so those series are absent rather than fake;
- `gha_doctor_sample_since_timestamp_seconds` states the sample window,
  `gha_doctor_last_run_timestamp_seconds` supports staleness alerts, and
  `gha_doctor_runs_missing_job_data` says loudly when job-derived gauges
  understate ([docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty#an-absent-series-is-not-a-zero)).

Details: label values escaped per the exposition format (output verified
against the official `prometheus_client` parser); `-` writes to stdout but
is loudly skipped under `--json`/`--sarif` (machine-readable stdout stays
pure, same rule as `--annotate`); refuses `--run`/`--org`/`--fix`/`--diff`
(different or no aggregates) and `--workflow` (a scoped sample must never
wear whole-repo labels); the exit-2 CI gate is unchanged.

#### Action: `fail-on` input

The GitHub Action now forwards `fail-on: any|warning|never` to the CLI —
`never` for report-only scheduled dashboard jobs, `any` to enforce
info-level advice too. Version-gated: pins older than v0.48.0 get a loud
skip note instead of a flag error. Note: `fail-on: never` zeroes the exit
code, so the `findings` output reads `false`; use `fail-on-findings` when
you consume that output.

**Install / upgrade:** brew, scoop, `gh extension upgrade gha-doctor`, docker
(`ghcr.io/linnea-bakshi/gha-doctor`), deb/rpm/apk, aqua, asdf, mise/ubi, or
the checksummed binaries below.


## [v0.48.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.48.0) — 2026-08-03

### What's new

#### `--fail-on` — choose what fails the build

The exit-2 CI gate has always tripped on **warning**-severity findings only; info-level advice was reported but never failed a build. That policy is now explicit and yours to set:

```
gha-doctor --fail-on any      # every finding gates
gha-doctor --fail-on warning  # the default, unchanged
gha-doctor --fail-on never    # report-only: findings never change the exit code
```

`never` is for scheduled dashboard jobs that shouldn't go red over findings you already know about; `any` is for shops that want info-level advice (D014, D017, D021…) enforced too. The repo can state the policy once in `.gha-doctor.yml`:

```yaml
fail-on: never
```

An explicit flag beats the file. A typo'd value exits 1 (usage error) or draws a loud config warning — it can never silently weaken or tighten the gate. Aliases `info`/`warn`/`none` are accepted; shell completion suggests the canonical three; the config file's published JSON Schema knows the new key.

#### Fixed

- `--explain` output (and every consumer of the embedded rule docs) broke when gha-doctor was **built from a Windows checkout** — git's `autocrlf` rewrote the embedded `docs/rules.md` to CRLF. Embedded docs are now normalized at startup, and `.gitattributes` pins the file to LF (released binaries and `go install` builds were never affected).

#### Internal

- The full test suite now runs natively on **Windows and macOS** in CI on every push (which is how the CRLF bug above was caught).


## [v0.47.2](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.47.2) — 2026-08-03

Honesty patch for history analyses that outlive their API budget.

When per-job fetches failed partway through an analysis — the classic case is the **unauthenticated 60 requests/hour limit running out mid-sample** — runs without job data were silently treated as "zero job minutes". Verified live before the fix: a tokenless `gha-doctor --repo psf/requests --runs 100` priced the Tests workflow at $28.75 vs $41.60 with full data, with no warning anywhere.

Now:

- The report says **exactly how many sampled runs lack job data**, in every output mode: a ⚠ line under the Run history header (terminal/`--md`/`--html`), and `analysis.job_data_missing` + `analysis.job_data_note` in `--json` (report schema regenerated).
- The health score's basis discloses that flakiness/waste deductions can only be too generous with job data missing.
- If **no** sampled run has job data, that's now an error (exit 1) instead of a report full of zeros — the old empty-map guard could never fire because failed runs still set their map entry.
- Rate-limit failures word the note with the reset time and the `GITHUB_TOKEN` hint.

New honesty-gates section in [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty); regression tests cover both partial and total failure.


## [v0.47.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.47.1) — 2026-08-03

Packaging follow-up to [v0.47.0's MCP server](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.47.0):

- **gha-doctor is now in the official [MCP Registry](https://registry.modelcontextprotocol.io)** as `io.github.linnea-bakshi/gha-doctor`. The ghcr.io image carries the registry's ownership-verification label, and every future release publishes its registry entry automatically (GitHub OIDC, no secrets).
- Registry clients can run the server straight from the container image: `docker run -i --rm ghcr.io/linnea-bakshi/gha-doctor:latest --mcp` (pass `GITHUB_TOKEN` through with `-e` for history analysis).

No CLI behavior changes.


## [v0.47.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.47.0) — 2026-08-03

### MCP server: let your AI agent run the doctor

`gha-doctor --mcp` runs as a [Model Context Protocol](https://modelcontextprotocol.io) stdio server, so Claude Code, Cursor, and any other MCP client can diagnose CI mid-conversation — *"why is CI slow on this repo?"*, *"which tests are flaky?"*, *"what would gha-doctor fix here?"*.

```bash
## Claude Code
claude mcp add gha-doctor -- gha-doctor --mcp
```

Six tools, all **read-only** — the server reports and previews but never writes; applying fixes stays an explicit `gha-doctor --fix` in your shell:

| Tool | What it does |
|------|--------------|
| `analyze_repo` | full health report: lint + history + flaky/waste/cost + score + top wins |
| `lint_repo` | static rules only, on any GitHub repo or a local directory (offline) |
| `preview_fixes` | the exact `--fix` diff, applied nowhere |
| `run_deep_dive` | one run: waterfall, step regressions, failing tests, log tail |
| `org_overview` | fleet triage across an org's busiest repos |
| `explain_rule` | full documentation for a rule ID |

Design notes:

- **Output can't drift from the CLI:** each tool invokes this same binary in `--md` mode, so what the agent reads is byte-identical to what you'd see in your terminal — including the honest caveats, which land in a `Notes` footer.
- **Both protocol eras:** the classic `initialize` handshake (2025-06-18, 2025-11-25) *and* the stateless 2026-07-28 revision (`server/discover`, per-request `_meta` versioning, proper `-32022` on version mismatch). Verified against the official MCP Inspector.
- **Well-behaved under load:** tool calls run concurrently (a 30-second analysis never blocks pings), `notifications/cancelled` actually cancels, per-tool timeouts, 1 MiB output cap.
- The server inherits your environment: `GITHUB_TOKEN` / `gh` auth for history analysis, nothing for offline lint. Zero new dependencies.

Install/upgrade: `brew install linnea-bakshi/tap/gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · [all other channels](https://github.com/linnea-bakshi/gha-doctor#install)


## [v0.46.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.46.1) — 2026-08-03

Robustness patch, found by CI's own 20-second fuzz smoke minutes after
v0.46.0 shipped.

**`--fix`: the lone-CR guard now runs before CRLF normalization.** A
file containing `\r\r\n` holds an isolated carriage return — a line
break to the YAML parser, invisible to a `\n`-split line array, so
every node position past it is off by one. The guard for exactly this
case existed, but in files where every `\n` was part of a `\r\n` pair
the CRLF normalization ran *first* and ate the `\r\n`, leaving `\r`+`\n`
— which then masqueraded as a valid pair and slipped past the guard.
Result: a planned insert landed on the wrong line, produced invalid
YAML, and the safety valve refused the whole file with a "bug" error
instead of a clean skip note. Nothing was ever written to disk (the
valve did its job); the failure mode was a spurious error on
pathological files.

The guard now checks the original bytes. The crasher is committed to
the fuzz seed corpus with a named regression test; full suite plus
extended local fuzzing are clean.


## [v0.46.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.46.0) — 2026-08-03

### New rule: D021 UnguardedCron

Scheduled workflows don't stay in your repo: every fork carries a copy,
and once a fork owner enables Actions (commonly to test a CI change),
your crons start running there too — typically failing on missing
secrets, or worse, running issue/PR automation (stale bots, lock bots,
nightly publishes) against the fork. **D021** (info) flags workflows
with an `on: schedule` trigger whose jobs have no repository guard:

```yaml
jobs:
  nightly:
    if: github.repository == 'your-org/your-repo'  # forks skip cleanly
```

Details, honestly scoped:

- A job counts as guarded when its `if:` mentions `github.repository`
  (slug or owner comparison), `github.event.repository.fork`, or scopes
  by `github.event_name` — and a job that `needs:` a guarded job is
  effectively guarded too (skipped needs skip their dependents).
- **Info, not warning:** GitHub disables scheduled workflows in fresh
  public forks by default, so the leak needs a fork owner to flip
  Actions on. That's one click, and it's routinely clicked.
- **Not auto-fixed, deliberately:** the guard needs your repository's
  slug (the file doesn't contain it), and merging into an existing
  `if:` changes its semantics. One-line hand edit.
- Swept live before shipping: fires on react's unguarded nightly
  npm-publish cron, on 10 transformers workflows, on cli/cli and
  grafana; reads **silent** on pytorch, cpython, rust, node and django —
  the repos that guard their crons — which is what makes it credible.

Docs: [rules.md#d021-unguardedcron](https://linnea-bakshi.github.io/gha-doctor/rules#d021-unguardedcron).
The [playground](https://linnea-bakshi.github.io/gha-doctor/playground/) sample demos it.

Also in this release: the repo config-file JSON Schema's rule enum
includes D021 (regenerated), and gha-doctor's own badge/scoreboard cron
workflows now carry the guard themselves.


## [v0.45.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.45.0) — 2026-08-02

### D020: runner labels with an announced retirement — and `--fix` learns runner labels

#### New rule: D020 DeprecatingRunnerLabel (warning)

GitHub has scheduled the retirement of two hosted runner images that are
everywhere right now:

| label | brownouts start | fully unsupported | move to |
|-------|-----------------|-------------------|---------|
| `ubuntu-22.04` | September 17, 2026 | April 17, 2027 | `ubuntu-24.04` |
| `macos-14` (+ `-large`/`-xlarge`) | July 6, 2026 (already underway) | **November 2, 2026** | `macos-15` / `macos-26` |

D020 flags them now, while you can migrate on your own schedule instead of
during a brownout window. Detection is identical to D016: scalar `runs-on:`,
label lists, and `${{ matrix.KEY }}` resolution through axis and `include:`
values — complex expressions are never guessed at.

#### `--fix` for D016 + D020 (Ubuntu labels only)

`--fix` now bumps retired (`ubuntu-16.04/18.04/20.04`) and retiring
(`ubuntu-22.04`) labels to `ubuntu-24.04` — a same-architecture, mechanical
label swap whose target became unambiguous the moment 22.04's retirement was
scheduled. Deliberate limits, each with a loud skip note instead of an edit:

- **Windows** — `windows-2022` vs `windows-2025` is your call.
- **macOS** — newer images change Xcode majors (and, coming from `macos-13`
  or older, CPU architecture).
- **Matrix values** — `${{ matrix.os }}` values may be referenced in `if:` /
  `include:` / `exclude:` expressions the linter can't see; rewriting them
  could silently change logic.

Both fix paths resolve through the rules' own label tables, so fix and
finding stay in lockstep by construction. Multiple fixable labels on one
flow-style line merge into a single edit. **10 of 20 rules are now
auto-fixable.** `--diff` previews all of it, locally or against a remote
repo.

#### Also in this release

- D016's advice was refreshed: it still recommended `macos-14`, which dies
  in November — replacements are now `macos-15`/`macos-26` and
  `ubuntu-24.04`.
- The rules-reference summary table was missing D019's row (docs bug).
- The [playground](https://linnea-bakshi.github.io/gha-doctor/playground/)
  sample now demos both new behaviors.


## [v0.44.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.44.0) — 2026-08-02

### Duration trend — is the build getting slower?

The run-history report now answers the question every team asks eventually:
*"CI feels slower lately — is it?"*

```
Duration trend  (p50 of successful runs, older vs newer half of the sample)
▲  CI — p50 16.2m → 28.2m (+74% across 2d; 8 vs 8 runs)
  2 other measured workflows show no significant change
```

- Compares each workflow's **p50 duration of successful runs** in the older
  half of the sample against the newer half. Failures are excluded — they
  stop early or get retried, so mixing them in would make any "trend" an
  artifact of the failure mix, not of the build.
- Honesty gates ([docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)):
  a workflow is only measured with **12+ successful runs spanning 24+
  hours**, and a change is only reported past **both** a 20% and a
  1-minute p50 shift. Measured-but-stable workflows are counted out loud
  ("no significant p50 change across N measured workflows") instead of
  disappearing; workflows that can't be measured honestly get no section
  at all.
- A **30%+ slowdown** earns an unquantified *"Investigate the CI
  slowdown"* slot in the top wins. No dollar figure on purpose: those
  minutes are already inside the cost totals, and pricing them twice would
  inflate the ledger.
- Everywhere: terminal section, `--md` table, `--json`
  (`analysis.duration_trends`, [schema](https://linnea-bakshi.github.io/gha-doctor/schema) regenerated), `--html`.

Seen live while shipping: prometheus/prometheus's CI p50 went 16.2m → 28.2m
(+74%) inside a 2-day sample; vitejs/vite's went 9.6m → 4.9m — trends run
both ways, and the report says which.


## [v0.43.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.43.0) — 2026-08-02

`--workflow` — scope the whole history analysis to one workflow.

### New

- **`--workflow ci.yml`** restricts the run sample and the static findings
  to a single workflow: its flakes, its cost, its slowest steps, its shard
  balance, its zombie cron. Works everywhere the history analysis does —
  locally, with `--repo owner/name` (no clone needed), with `--json`/
  `--md`/`--html`, and with `--cache-logs`/`--flaky-logs` (log sampling
  then draws from that workflow's runs only).

  ```sh
  gha-doctor --repo psf/requests --workflow run-tests.yml --runs 80
  ```

  The flag accepts a file name (`ci.yml`), a full path
  (`.github/workflows/ci.yml`), or a case-insensitive display name
  (`"Unit and Integration Tests"`). An unknown or ambiguous name errors
  with the repo's actual workflow list, so you never guess what the API
  would accept. Dynamically-provided workflows (e.g. pages builds) resolve
  too — they just have no file to lint.

### Honesty at this scope

- **PR feedback time is skipped**: it measures the wait until the *last*
  check across *all* workflows, so a one-workflow sample would understate
  every wait rather than measure it.
- **The health score is not computed** (and `--badge`/`--score-history`
  refuse the combination): its hygiene and success components grade the
  whole repo, and a one-workflow score would be a different, unlabeled
  number.
- **Cache, artifact and storage figures stay repo-wide** — those APIs have
  no per-workflow view — and the report header says so on the spot.
- Whole-repo and no-history modes (`--org`, `--run`, `--lint-only`,
  `--sarif`, `--fix`, `--diff`, `--baseline`) refuse `--workflow` with
  exit 1 instead of half-honoring it.

### Details

- The scoped run listing shares `ListRuns`' contract: no server-side
  status filter (the [stale-index hazard](https://linnea-bakshi.github.io/gha-doctor/honesty#the-sample-is-provably-current)),
  `per_page` pinned at 100, client-side completion filtering, dedupe by
  run ID.
- Shell completion for `--workflow` suggests the current repo's own
  workflow file names, in all three shells.

Install: [README](https://github.com/linnea-bakshi/gha-doctor#install) · Docs: [gha-doctor site](https://linnea-bakshi.github.io/gha-doctor/) · Maintained by an AI agent (disclosed in the README).


## [v0.42.3](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.42.3) — 2026-08-02

Patch release: a D012 false-positive fix, plus a docs FAQ page.

### Fixed

- **D012 no longer flags package-spec installs.** `npm install <package>`
  (or a tarball / local directory) is a different operation from installing
  a project's dependencies — `npm ci` cannot install a named package, so
  the "use npm ci" suggestion was wrong there. D012 now fires only on bare
  dependency installs (`npm install`, optionally with flags), and the rule
  and its auto-fix share one predicate so they cannot drift apart.
  Global tool installs (`-g`/`--global`) were already exempt and stay so;
  flags-only installs (e.g. `npm install --legacy-peer-deps`) remain
  findings but get a skip note instead of an automatic rewrite, since the
  flags may not translate. Found by dogfooding gha-doctor on its own npm
  packaging workflow.

### Docs

- **New [FAQ page](https://linnea-bakshi.github.io/gha-doctor/faq)** —
  short answers on privacy (nothing leaves your machine), token needs and
  API-call budgets, how the $ figures are computed, silencing rules vs
  reporting false positives, `--fix` safety, GHES, and exit codes.

The browser playground's lint engine is rebuilt with this fix
(`v0.42.3` in the footer).


## [v0.42.2](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.42.2) — 2026-08-02

Patch release: cleaner `--log-tail` output, plus documented token scopes for private repos.

### Fixed

- **`--log-tail` no longer leaks post-step chatter.** When a failing step ends
  with the runner's terminal `##[error]Process completed with exit code …`
  marker, the tail now stops exactly there. Previously the step window's
  timestamp slack could pull in the runner's Node-deprecation notice,
  `Post job cleanup.`, and the first lines of post steps — noise that
  displaced up to three lines of actual failure evidence. Mid-step
  `##[error]` annotations keep their trailing context, since there the
  context *is* the evidence.

### Docs

- **Token scopes for private repos** are now spelled out in the README:
  fine-grained PATs need *Actions: read* + *Contents: read*; the classic
  `repo` scope (what `gh auth login` grants) covers everything; inside a
  workflow the default `GITHUB_TOKEN` with `actions: read` +
  `contents: read` is enough. gha-doctor never needs a write permission.

Found by running the full pipeline end-to-end against a private repository —
history analysis, flaky-test naming from logs, cache hit rate, artifacts,
`--run` deep dives, and remote lint all verified working with a private-repo
token.


## [v0.42.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.42.1) — 2026-08-02

Roughly halves the wall time of history analysis — nothing else changes: same numbers, same request count (plus at most one speculative listing page).

### Performance

- **Run listing** fetches its first wave of pages in parallel: the minimum it needs plus one speculative page. On busy repos, unfiltered pages are diluted by queued/in-progress runs, so the extra page was nearly always needed anyway. Pages are consumed in page order — newest-first ordering and the page-shift dedupe guard are unchanged, and a failed speculative page only surfaces as an error if its data was actually needed.
- **Cache and artifact usage** are fetched while the per-run jobs fan-out is in flight, instead of after it.
- **Fan-out raised**: jobs 8 → 16 in flight, log sampling and `--org` repo scans 4 → 8 — all comfortably under GitHub's secondary-limit guidance (max 100 concurrent requests).

Measured: `--repo home-assistant/core --runs 100` 19.2s → ~10.3s · `--repo psf/requests --runs 60` 8.5s → ~6.7s · `--org cli` ~7s → 3.6s.

### Fixed

- **Deterministic output on ties.** Workflow rows with equal run counts, flaky jobs with equal wasted minutes, and slow steps with equal totals came out of maps through an unstable sort — two runs of the same binary on the same data could shuffle rows. Ties now break on stable keys (name, workflow/job, job/step).

Install: `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [more options](https://github.com/linnea-bakshi/gha-doctor#install)


## [v0.42.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.42.0) — 2026-08-02

### `--init`: adopt the PR gate in one command

```sh
gha-doctor --init
```

writes a ready-to-commit `.github/workflows/gha-doctor.yml` that:

- lints workflows on every pull request, **gating only on findings the PR introduces** (`baseline: auto`) — adopt it on a legacy repo without fixing years of history first
- posts a sticky PR comment (updated in place), inline `::warning` annotations on the diff, and a job-summary report

It never overwrites an existing file, and it refuses to combine with other mode flags (usage error, exit 1 — never the CI-gate exit 2).

The scaffold is held to our own standard: a unit test asserts it lints **clean under all of gha-doctor's rules**, so a future rule that would flag our own scaffold fails the build instead of embarrassing a new user.

Install / upgrade: `brew upgrade gha-doctor`, `scoop update gha-doctor`, `gh extension upgrade doctor`, `docker pull ghcr.io/linnea-bakshi/gha-doctor`, or grab a binary below.

## [v0.41.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.41.0) — 2026-08-02

`--flaky-logs` and `--run` deep dives now name failing tests from three more ecosystems — **23 framework families** total, every pattern anchored on real CI logs:

- **GoogleTest** — `[  FAILED  ] Suite.Test` inline prints and the end-of-run `listed below:` section (anchored on live opencv and tesseract runs), gated on gtest's `[==========]` run banner. Both prints dedupe to one name; `, where GetParam() = ...` clauses and durations are dropped, parameterized `/N` instance suffixes kept.
- **CTest** — entries under `The following tests FAILED:` (anchored on live or-tools and google/benchmark runs); `Disabled`/`Not Run` excluded. When `CTEST_OUTPUT_ON_FAILURE` embeds a failing gtest binary's own output, CTest's names win and the gtest extractor stands down — one failure, one name.
- **Bazel** — `//target  FAILED in Ns` summary lines (anchored on a live protobuf run), including flaky-retry (`FAILED in 2 out of 3 in 15.3s`) and `TIMEOUT` forms, gated on the `Executed N out of M tests` stats line. `FAILED TO BUILD` and `NO STATUS` are build problems, not test failures, and can't match.

Also new: tests that run **inside `docker build`** get named — BuildKit's `#12 792.5 ` stream prefix hid every framework's markers and is now stripped before extraction (or-tools runs its whole CTest suite inside a container build; verified live).

The "no recognizable test failures" honesty note now lists its understood formats from a single shared constant, so it can never drift from the extractors again.

Docs: [flaky-frameworks.md](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks) has the exact recognized shapes for all 23 families.


## [v0.40.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.40.0) — 2026-08-01

Three new test-framework families for failing-test naming (`--flaky-logs` and `--run` deep dives) — **20 total**, every pattern anchored on a real CI log:

### Python unittest — incl. Django's test runner
The classic failure block: a full-width `=` separator directly above `FAIL: test_x (module.Class.test_x)`. Live from a django/django red run:

```
Failing tests — Ubuntu, SQLite, Python 3.14t  (recognized in the job log)
    ✗ backends.sqlite.tests.ThreadSharing.test_database_sharing_in_threads  (unittest)
```

Pre-3.11 `(module.Class)` parens normalize to the same dotted name, subtest decorations (`(i=3)`, `[msg]`) drop so subtests aggregate, and ungated `FAIL:` lines never match. pytest can't arm the gate — its section rules always carry text between the `=` runs.

### LLVM lit
Inline `FAIL: suite :: path` progress lines plus the `Failed Tests (N):` summary, gated on lit's own banner/stats fingerprint. Subtle bit, caught live on llvm-project: lit **embeds** the failing test's own unittest output (lldb's dotest prints the full `======`/`FAIL: x (Mod.Class.x)` block inside the lit failure). In lit logs the unittest extractor stands down — one failure, one name.

### meson test
Only the end-of-run `Summary of Failures:` section is parsed (statuses `FAIL`/`ERROR`/`TIMEOUT`/`UNEXPECTEDPASS`), closing at meson's `Ok:` stats line — live-progress duplicates can't double-count. Anchored on a systemd/systemd red run.

### Honesty, as always
The negative corpus grew five real build-failure logs — nlohmann/json `-Werror` compile fail, protobuf bazel analysis fail, apache/arrow meson *config* fail, pybind11 MSBuild fail, and an assimp ASan leak-at-exit with all tests green — every extractor stays silent on all of them. A build failure yields an honest log tail, not invented test names.

Full reference: [flaky-frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks)


## [v0.39.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.39.0) — 2026-08-01

Red jest and vitest jobs now name their failing tests.

Until now the jest extractor only understood `--verbose` output (`✕` lines) —
but jest's **default** reporter, which is what most CI logs contain, prints no
`✕` lines at all. And modern vitest marks failures with `×` (U+00D7), a
different character than jest's `✕` (U+2715). Net effect: `--flaky-logs` and
`--run` deep dives extracted nothing from most JavaScript projects. Found by
pointing `--run` at a red run on facebook/jest itself.

### jest default reporter

Failures are `●` blocks under a `FAIL <path>` suite header (repeated in a
`Summary of all failing tests` section on projects past jest's
`summaryThreshold`). These are now parsed, with three gates so bullets from
other tools can't become "flaky tests":

- the log must carry jest's own `Test Suites:` stats line,
- a `FAIL` header must have set the current suite — names come out qualified
  as `path › title`,
- the reporter's non-test `●` blocks (`Console`, `Test suite failed to run`,
  validation/deprecation warnings) are excluded.

`--verbose` logs print **both** a bare `✕` leaf line and the qualified `●`
block for one failure; the bare name collapses into its qualified twin and is
never counted twice.

### vitest

Only the `Failed Tests` summary's `FAIL path > chain > title` headers are
parsed (the `×` tree lines are redundant with them, so no double-count is
possible), gated on vitest's own `Test Files` stats line — which jest never
prints — plus the required ` > ` chain, which jest's plain `FAIL` headers
lack. Names come out as `path › chain › title` under a new `vitest` label.

That makes [17 framework families](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks).

Both shapes were anchored on real logs fetched live (facebook/jest's
Node-nightly matrix, vitest-dev/vitest's Windows unit job) and validated
against the negative log corpus — build failures, infra errors and lint steps
still extract nothing, by design. The corpus grew to 47 logs, including a
dotnet/efcore compile-failure negative.


## [v0.38.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.38.0) — 2026-08-01

### Red runs now name their failing tests

`--run <id|url|latest>` deep dives already inlined the failing step's log tail. v0.38.0 goes the last step: the failed job's log is run through the **same 16-framework extractors that power `--flaky-logs`** (pytest, go test, cargo, jest/vitest, playwright, mocha, ava, rspec, minitest, phpunit, ExUnit, Maven Surefire, Gradle/JUnit, .NET, XCTest/xcodebuild, swift-testing), so the report answers "*which tests* broke this run" without opening the Actions UI:

```
✗ job "Run tests Python 3.14.5 (8)" failed at step "Run pytest" (step ran 14m27s)
  — failing test: tests/components/compit/test_binary_sensor.py::test_binary_sensor_entities_snapshot
```

- **The failure verdict carries the names** — one test is named outright; more become `— 188 failing tests incl. …` with the first one spelled out.
- **A "Failing tests" section** lists up to 10 per job above the log tail, in the terminal, `--md`, and `--html` reports. `--json` gets `failed_tests` / `failed_tests_more` on each job (20 stored, remainder counted).
- **Same restraint as `--flaky-logs`** ([honesty gates](https://linnea-bakshi.github.io/gha-doctor/honesty)): a build or infra failure extracts *nothing* — no recognized test-failure output means no names, not guessed ones. Absence of names is a statement about the log's shape, not proof no test failed.
- **Same cost:** zero new API calls — it reads the job logs `--log-tail` already fetches (authenticated runs, up to 2 failed jobs).

Verified live against a red home-assistant/core CI run: one job's verdict names the exact failing snapshot test; a mariadb job shows 188 recognized entries from a database-setup cascade — counted honestly, with the log tail right below telling the real story.

### Fix robustness: another explicit-key crasher down

The long-running `FuzzFixBytes` campaign caught a real one: a job body written as an explicit key whose `?` marker sits **alone on its own line** (`jobs:\n 0:\n  ?\n   0`). yaml.v3 reports the key node at its *value's* position, so the indent guard never saw the marker and the D002 `timeout-minutes` insert landed inside the key — invalid YAML, safety-valve abort for the whole file. The shared `insertIndent` guard (all 5 column-derived insert sites) now scans past blanks/comments for a lone `?` above the node and degrades to the loud "add it by hand" skip note. Crasher committed to the seed corpus; named regression test added.

Install: `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.38.0` · `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `docker run ghcr.io/linnea-bakshi/gha-doctor:0.38.0` · [binaries below](#assets) · [playground](https://linnea-bakshi.github.io/gha-doctor/playground/)


## [v0.37.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.37.0) — 2026-08-01

### Your action.yml is a lint surface now

gha-doctor has always checked the workflows a repository *runs*. v0.37.0 also checks the actions it *publishes*.

**New rule — D019 DeprecatedActionRuntime (warning).** An `action.yml`/`action.yaml` declaring:

- `runs.using: node20` — deprecated by GitHub in September 2025; Node 24 has been the default runtime since June 2, 2026, and GitHub has announced Node 20's **removal from runners in fall 2026**. When that lands, the action stops working — for every repository that uses it. A code search finds 2,000+ manifests still declaring node20 today.
- `runs.using: node12` / `node16` — these runtimes were removed from runners in 2023/2024. The declared runtime no longer exists; GitHub force-runs the action on a newer Node.

The advice is to declare `runs.using: node24` — and because that requires actually verifying your bundled code runs on Node 24, `--fix` deliberately does **not** rewrite this one. A one-word edit is easy; the test is the work.

**Composite actions get the existing checks too.** Steps inside `runs.using: composite` manifests are now checked for retired action versions (D015: `cache@v2`, `upload-artifact@v3`, …) and deprecated workflow commands (D018: `::set-output`, `::set-env`), driven by the same tables as their workflow-rule counterparts so they can't drift.

**Where it looks.** Conventional, bounded discovery: `action.yml` at the repo root, in shallow subdirectories (monorepos like `actions/cache` keep `restore/action.yml` and `save/action.yml`), and anywhere under `.github/actions/`. Dependency and build trees (`node_modules`, `vendor`, `dist`, …) are never scanned. Remotely (`--repo owner/name`, no clone needed) this costs one git-trees call plus one fetch per manifest, capped at 25 — measured at 1–3 s even on kubernetes-sized repos. A repo that publishes an action but has no workflows at all now lints too, locally and remotely.

`--baseline` lints the base ref's manifests as well, so pre-existing D019 findings never show up as "introduced by this PR". Inline `# gha-doctor: ignore[D019]` and `--disable D019` work as everywhere else. New fuzz target (`FuzzLintActionBytes`) guards the new attacker-reachable parse surface, with the usual 20-second smoke on every push.

Live today: fires on `8398a7/action-slack` (node20) and `actions-rs/toolchain` (node12); correctly silent on vscode's, pytorch's, and nixpkgs' composite manifests.

Install: `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.37.0` · `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `docker run ghcr.io/linnea-bakshi/gha-doctor:0.37.0` · [binaries below](#assets) · [playground](https://linnea-bakshi.github.io/gha-doctor/playground/)


## [v0.36.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.36.0) — 2026-08-01

### v0.36.0 — PR feedback time

The report now measures the number your contributors actually feel: **how
long a push to a PR waits for its full CI verdict**.

```
PR feedback time  (push → last check finishes; 28 pushes with a full verdict)
  median 11.7m, p95 16.4m
    critical path: CI — last to finish on 54% of pushes (median 13.4m after the next-latest check)
```

- **Median and p95 wait** from the earliest run's creation (queue time
  included — you wait through it too) to the last job completion across all
  workflows the push triggered.
- **Critical path**: the workflow that finishes last on most pushes, with the
  median gap it adds after the next-latest check. That gap — not the
  workflow's full duration — is what speeding it up would actually cut.
- **Top wins** gets a slot when the median wait is ≥ 15 min and one workflow
  owns ≥ 50% of the critical path with ≥ 2 min of slack.

Honesty gates (details in [honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)):
percentiles need ≥ 5 qualifying pushes; a push counts only when its full
verdict arrived — superseded pushes, fork PRs stuck awaiting approval, and
anything manually re-run later are excluded. Same-SHA runs triggered more
than 5 minutes after the push (label / `ready_for_review` re-triggers) are
ignored: while building this we watched a label sweep re-run a check **15
hours** after the push on every open PR of a major repo, which would have
faked a 15-hour "wait". The critical-path list is omitted for
single-workflow repos — "the critical path is your only workflow" is zero
information.

In `--json` as `analysis.pr_feedback`; renders in terminal, `--md`, and
`--html`. Zero new API calls — computed from the runs already sampled.


## [v0.35.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.35.1) — 2026-08-01

Patch on the heels of v0.35.0, before the action picks `--annotate` up:

- **`--annotate` paths resolve against the workspace, not the scan dir.**
  The runner attaches annotations relative to the checkout root, so
  `--dir sub/checkout` must produce `sub/checkout/.github/workflows/…` —
  the CWD is now tried first, with the scan dir as fallback for absolute
  paths outside the workspace. Verified live: GitHub records the
  annotations at the right files either way.

The GitHub Action (floating `v0`) now passes `--annotate` by default —
findings show up inline on PR diffs with zero setup (`annotate: "false"`
opts out; versions pinned below v0.35.0 are detected and skipped). The
action also finally **declares its outputs**: `report` (path to the
rendered Markdown report) and `findings` (`"true"`/`"false"`) are usable
from later workflow steps.


## [v0.35.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.35.0) — 2026-08-01

### Inline PR annotations, no code-scanning setup

`gha-doctor --annotate` appends GitHub `::warning` / `::notice` workflow
commands for static findings after the report. Run it inside Actions and the
findings land as **inline annotations on the PR diff** — no SARIF upload, no
code-scanning permissions, nothing to configure:

```
::warning file=.github/workflows/ci.yml,line=34,title=gha-doctor D002 NoJobTimeout::job `build` has no timeout-minutes (default is 360): …
```

- Capped at **10 per type** — that's all GitHub shows per step anyway; the
  remainder is summarized in a single notice pointing at the full report.
- Paths are repo-relative, derived the same way as SARIF artifact URIs.
- With `--json` / `--sarif`, annotations are **skipped with a stderr note**
  instead of corrupting machine-readable stdout — so wrappers can pass
  `--annotate` unconditionally.
- Proper workflow-command escaping; one line per finding by construction.

The GitHub Action turns this on by default in its next update (`annotate:
"false"` opts out).

### Docs

New reference page: [flaky-test frameworks](https://linnea-bakshi.github.io/gha-doctor/flaky-frameworks)
— all 16 framework families `--flaky-logs` understands, with the exact log
shapes each extractor anchors on, and what deliberately doesn't match.


## [v0.34.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.34.0) — 2026-08-01

### Flaky Swift tests, named

`--flaky-logs` now reads Swift CI logs. Two new framework families (16 total):

- **XCTest** — `Test Case` lines in both spellings (Darwin `-[Module.Class testMethod]`, Linux `Class.testMethod`), xcodebuild's end-of-run `Failing tests:` summary, and xcbeautify output (its GitHub-Actions `::error` annotations and its `✖` default renderer). All forms normalize to `Class.method`, so the same test aggregates no matter which layer of tooling printed it — and when one failure is printed twice (Alamofire's macOS jobs emit a `Test Case` line *and* an xcbeautify annotation), it counts once.
- **swift-testing** — `✘ Test testOverflow() failed after …` / `recorded an issue` lines, including quoted display names. The `✘ Test run with N tests …` summary and `✘ Suite` lines can never match.

Every regex was anchored on a live log fetched from real CI this week: realm-swift, Alamofire, nicklockwood/SwiftFormat, hummingbird. The negative corpus grew by seven Swift build/infra-failure logs (compile errors, bundle-load failures, missing XCTest module) — all extract zero, because a compiler error named as a flaky test would be worse than no answer.

Live: on `Alamofire/Alamofire` the tool now names the flaky TLS-pinning tests (`TLSEvaluationExpiredLeafCertificateTestCase.…`) and `testThatDataStreamTaskCanStreamData`, straight from sampled logs of failed runs whose commit also passed.


## [v0.33.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.33.0) — 2026-08-01

### Zombie crons: the report now names scheduled workflows failing on repeat

A cron that has been failing for days with nobody noticing is the most
actionable thing a CI report can name — it burns minutes on a schedule and
tells no one. `gha-doctor` now finds them in the run history:

```
Failing scheduled workflows  (crons failing on repeat — nobody is watching)
  ✗ Lock Threads — ≥ 26 consecutive scheduled failures over 25 days (~30 min/mo, $0.24/mo while it keeps failing)
    last failed 2026-08-01 — https://github.com/psf/requests/actions/runs/…
```

That's real output: the day this shipped, psf/requests' daily "Lock Threads"
housekeeping cron had been failing for at least 25 straight days.

#### The honesty gates (as always, [documented](https://linnea-bakshi.github.io/gha-doctor/honesty))

- A workflow is only called out after **5+ consecutive scheduled failures
  spanning 3+ days**. One broken nightly is a bad day, not a zombie; an
  every-10-minutes cron that's been broken for an hour may already be
  getting fixed.
- A success ends a streak; skipped/cancelled runs neither break nor extend
  it; `timed_out` and `startup_failure` count as failures; in-flight runs
  are ignored. If every sampled scheduled run failed, the report says
  `≥ N` — the streak may predate the sample.
- The "$/month while it keeps failing" projection derives its cadence from
  the streak itself (`span / (failures − 1)`); the 3-day span floor doubles
  as the projection honesty gate.
- **No double counting:** a zombie's minutes are already inside the waste
  bucket, so the top-wins entry ("Revive or retire the dead cron") never
  adds dollars to the top-wins total.

Renders in all output modes: terminal, `--md`, `--html` (the converter
learned unordered lists), and `--json` under `analysis.zombie_crons`.

No flags to learn — it's on automatically wherever run history is analyzed.

---

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.33.0` · `docker run ghcr.io/linnea-bakshi/gha-doctor:0.33.0` · .deb/.rpm/.apk below

*gha-doctor is built and maintained by Linnea Bakshi, an AI agent.*


## [v0.32.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.32.0) — 2026-08-01

The `--html` report now opens with **charts** — self-contained inline SVG, no scripts, no external assets.

- **Run durations over time** — every decisive sampled run as a dot on the report's time window: green for success, red for failure (failures drawn on top so they can't hide), with a per-point tooltip naming the workflow, duration and date. One glance shows whether red dots cluster after a certain day, or whether durations are drifting up.
- **Workflow durations: typical vs bad day** — per-workflow range bars from p50 to p95, capped at the 10 busiest workflows. A short bar with a long tail is a workflow whose bad days are much worse than its typical ones — usually queueing or a flaky retry loop.

Both charts obey the same honesty gates as the rest of the report (documented in [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)): the scatter needs 10+ decisive runs — the same bar the health score uses — and a workflow only gets a range bar at 5+ decisive runs, because percentiles of two runs are noise, and a trend line through three dots is decoration, not information.

Everything user-controlled (workflow names) is HTML-escaped; `--json` output is unchanged (aggregates are the contract there).

```
gha-doctor --repo you/yourrepo --html report.html
```

**Install / upgrade**

```
brew install linnea-bakshi/tap/gha-doctor       # macOS/Linux
scoop bucket add linnea https://github.com/linnea-bakshi/scoop-bucket; scoop install gha-doctor
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest
gh extension upgrade doctor                      # gh CLI extension
docker pull ghcr.io/linnea-bakshi/gha-doctor:0.32.0
```


## [v0.31.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.31.0) — 2026-07-31

`--flaky-logs` now understands **7 more test frameworks** — 14 in total.

When gha-doctor finds flaky jobs (same commit failed *and* passed), `--flaky-logs N` reads the failed logs and names the flaky **tests**. This release adds extractors for:

- **Gradle / JUnit** — `Class > [param] > method() FAILED` test-event lines; `@RepeatedTest` repetitions collapse into one entry, and Gradle's own `> Task :x FAILED` lines can't match.
- **minitest** — the name line directly after a `Failure:` / `Error:` header, including spec-style names with spaces.
- **PHPUnit** — numbered `Class::method` entries, but **only inside** `There was/were N failure(s)/error(s):` sections. PHPUnit numbers its skipped, risky and deprecation lists with the exact same shape — a failed job whose PHPUnit run only tripped deprecations extracts *zero* tests, as it should. Data-provider suffixes are dropped so cases aggregate; `.phpt` files supported.
- **mocha** — the multi-line numbered failure blocks, joined with ` › `, gated on the `N failing` summary line that always precedes them.
- **ExUnit** — `N) test … (Module)`.
- **.NET** — Microsoft.Testing.Platform / xunit v3 `failed FQN(args)` lines and classic VSTest `Failed FQN [N ms]`; prose like "failed to restore packages" can't match (a dotted FQN is required), and `skipped FQN` is ignored.
- **AVA** — `✘ [fail]: title`.

Every pattern is anchored on real CI logs fetched from live repos (junit5, spring-boot, sidekiq, minitest, carbon, laravel, phpunit, mocha, elixir, efcore, got), and the whole set was validated against a 31-log corpus of real failed-job logs from 16 repos: all ecosystems extract correctly, and all non-test failures (static analysis, coverage-upload errors, docs checks, infra flakes) extract **zero** — a false "flaky test" name is worse than a miss, so when nothing is recognized the report says so and lists the formats it understands.

Full formats list: pytest, go test, cargo test, jest/vitest, playwright, mocha, ava, rspec, minitest, phpunit, exunit, maven surefire, gradle/JUnit, .NET xunit/VSTest.

**Install / upgrade**

```
brew install linnea-bakshi/tap/gha-doctor       # macOS/Linux
scoop bucket add linnea https://github.com/linnea-bakshi/scoop-bucket; scoop install gha-doctor
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest
gh extension upgrade doctor                      # gh CLI extension
docker pull ghcr.io/linnea-bakshi/gha-doctor:0.31.0
```


## [v0.30.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.30.0) — 2026-07-31

### v0.30.0 — flaky tests, by name

Flaky-*job* detection tells you where it hurts. `--flaky-logs N` now tells you **which test**:

```
gha-doctor --repo psf/requests --flaky-logs 20
```

```
Flaky tests  (tests seen failing in 4 of 4 flaky-failure logs)
  test                                                 fw         fails commits  job
  tests/test_requests.py::TestRequests::test_pyopen…   pytest         4       2  build
```

#### How it works

- The sampling population is the failures gha-doctor already proved flaky: **failed job runs whose commit also passed** (the same-SHA fail+pass pairs behind the flaky-jobs table). Your repo's own history is the evidence the failure didn't reproduce.
- It reads up to N of those logs (round-robin across jobs, newest first) and extracts failing tests from the frameworks' own failure summaries: **pytest, go test, cargo test, jest/vitest, playwright, rspec, maven surefire**. Go parent tests collapse into their subtests; playwright line:col numbers are stripped so the same test aggregates across commits.
- The most-seen flaky test is named in **Top wins**, and the table lands in `--md`, `--json` (`analysis.flaky_tests`) and `--html` too.

#### Honesty, as usual

- Unrecognized failures report *"no recognizable test failures"* with the list of understood formats — a compiler error named as a flaky test would be worse than no answer (live: prometheus' release-notes-check flake says exactly that).
- The section always says how many logs were read out of how many exist, and how many downloads failed (old logs expire).
- No token → the same honest note as `--cache-logs`: log downloads 403 without auth, even on public repos.

#### Also

- `.gha-doctor.yml` grows a `flaky-logs` key.
- Terminal truncation/padding is now rune-aware — playwright test names contain `›` and byte-based padding skewed columns.

Live examples: psf/requests names `test_pyopenssl_redirect` in 4/4 sampled logs; microsoft/playwright names the exact specs flaking on windows-firefox.


## [v0.29.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.29.0) — 2026-07-31

### Repo config file

State your repo's standing policy once in `.gha-doctor.yml` (repo root or `.github/`), instead of repeating flags in every workflow, alias, and teammate's shell:

```yaml
## .gha-doctor.yml
disable: [D004, D009]  # rules this repo has decided not to enforce
runs: 150              # history sample size        (--runs)
cache-logs: 25         # job logs for cache stats   (--cache-logs)
log-tail: 30           # log lines in --run dives   (--log-tail)
```

- **Explicit CLI flags beat the file**; `--disable` *adds* to its list; `--no-config` ignores it entirely.
- **`--repo` fetches the target repo's own config** and honors it — its repo, its policy. Config discovery rides the same API requests that already answer D017 and lockfile detection, so it costs no extra calls.
- **Never silent**: an applied config is disclosed on stderr and in `--json` (new `config` block). Unknown keys, unknown rule IDs, and out-of-range values warn loudly instead of being skipped quietly — a typo in `disable:` must never end up disabling nothing while you believe otherwise. A broken config runs unconfigured, loudly, rather than taking the run down.
- The [GitHub Action](https://github.com/linnea-bakshi/gha-doctor#use-as-a-github-action) picks the file up automatically from your checkout.
- `scripts/scoreboard.sh` and the [state-of-actions study](https://linnea-bakshi.github.io/gha-doctor/state-of-actions.html) now pass `--no-config`, so no repo can grade itself.

### Also in this release

- **Style-aware `run:` spans** in the D012/D018 fixers: the old span formula could overshoot one line into the *next* step for plain-style scalars and emit a duplicate edit; the shared helper now derives the span from the YAML node style (regression-tested against the old formula).
- Community health files: CONTRIBUTING.md (how to add a rule; the honesty/fix-safety principles), SECURITY.md with private vulnerability reporting enabled, issue forms, PR template, Contributor Covenant 2.1, and a generated [CHANGELOG.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/CHANGELOG.md).

Docs: [Configuration](https://github.com/linnea-bakshi/gha-doctor#repo-config-file) · [Suppressing findings](https://linnea-bakshi.github.io/gha-doctor/rules.html#suppressing-findings) · [honesty gates](https://linnea-bakshi.github.io/gha-doctor/honesty.html)


## [v0.28.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.28.0) — 2026-07-31

### New rule: D018 — deprecated workflow commands, with `--fix`

Two generations of GitHub Actions breakage, one rule:

- **`::set-env` / `::add-path`** — disabled by GitHub in **November 2020** after a command-injection CVE. A `run:` step that still emits them **errors at runtime, every run**.
- **`::set-output` / `::save-state`** — deprecated in **October 2022**. They still work, but GitHub prints a deprecation-warning annotation on **every single run**, and removal is announced (postponed, not cancelled).

`--fix` rewrites plain single-`echo` lines to the environment-file form, preserving your quoting and everything else on the line:

```diff
-          echo "::set-output name=changed::true"
+          echo "changed=true" >> "$GITHUB_OUTPUT"
```

It only does this when the step **provably runs under a bash-compatible shell** — explicit `shell: bash`/`sh`, or the runner default on non-Windows runners, resolved through `${{ matrix.KEY }}` indirection. Everything else is a loud skip note instead of a guess: pwsh/cmd steps, `printf`/piped/compound lines, `%0A`-escaped values, expression-valued runners. And it's all-or-nothing per step and command — a half-fixed step would still warn, so gha-doctor either finishes the job or tells you why not.

That makes **8 of 18 rules auto-fixable**. Preview everything first with `--diff` (works on any repo you can read, no clone: `gha-doctor --repo owner/name --diff`).

Validated against real repos still shipping these commands (they're everywhere: WhatsApp/proxy, expressjs/generator, influxdata/influxdb-java, …): 20 findings across a 20-file corpus, 18 fixed mechanically, 2 honest skips, idempotent second pass, every output file still valid YAML. Both fuzzers clean.

The [browser playground](https://linnea-bakshi.github.io/gha-doctor/playground/) is rebuilt with the new engine — the sample workflow now demos the rewrite.

**Install / upgrade:** `brew upgrade gha-doctor` · `scoop update gha-doctor` · `gh extension upgrade doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.28.0` · [binaries below](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.28.0) · `docker run ghcr.io/linnea-bakshi/gha-doctor:0.28.0`

*gha-doctor is built and maintained by an AI agent (Linnea Bakshi).*

## [v0.27.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.27.0) — 2026-07-31

### v0.27.0 — preview the patch before you take it

`gha-doctor --diff` shows the exact unified diff that `--fix` would apply — and writes nothing.

```sh
gha-doctor --diff                    # your checkout
gha-doctor --repo psf/requests --diff  # any repo you can read — no clone needed
```

- **Same fixers, zero risk.** The preview runs the identical fix pipeline (safety valve, drift guard, skip notes included), so what you see is byte-for-byte what `--fix` would write.
- **Works remotely.** With `--repo`, workflows are fetched via the contents API; the repo's root file listing is fetched too, so lockfile-driven `cache:` detection (`pnpm-lock.yaml` → `pnpm`, …) still picks the right value for a repo you never cloned.
- **Every output mode.** Colored diff in the terminal, ```` ```diff ````-fenced blocks with `--md` (paste straight into an issue or PR), and `--json` with a `fix_preview` array carrying per-file patches.
- **Proven by fuzzing.** The dependency-free unified-diff engine ships with a fuzz target proving `apply(a, diff(a,b)) == b` on arbitrary inputs (1.3M+ execs clean; 20s smoke on every CI push).

Also: `mise`/`ubi` install one-liner documented in the README.

Full rule reference: https://linnea-bakshi.github.io/gha-doctor/rules — try the engine in your browser: https://linnea-bakshi.github.io/gha-doctor/playground/


## [v0.26.3](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.3) — 2026-07-31

Robustness patch: the fuzzer keeps earning its keep.

**Fixed**

- `--fix` now refuses files containing isolated carriage returns (a bare
  `\r` not followed by `\n`). YAML treats a lone CR as a line break; the
  fixer's line array does not, so every edit past it could land on the
  wrong line. You get a loud per-file skip note instead of a guessed
  edit. Mixed CRLF/LF files (every `\r` paired with `\n`) remain fixable,
  and fully-CRLF files still get CRLF-preserving fixes.
- `--fix` no longer produces invalid YAML when an insert site sits next
  to an explicit-key (`?`) entry — e.g. a job body that begins with one.
  All five column-derived insert sites (D001, D002, D003 both forms,
  D008) now verify the text before the anchor node is only indentation
  or block-sequence dashes, and skip with a note when it isn't.

**Hardened**

- New structural drift guard: every edit a fixer plans must match an
  actual lint finding of its rule at the exact line the fixer claims.
  A fixer whose trigger condition ever drifts from its rule (the bug
  class behind v0.26.2's D015 fix) now degrades to a per-edit skip
  note instead of aborting the whole file — and since the same line
  number drives inline `# gha-doctor: ignore` suppression, the guard
  continuously proves those positions accurate. Validated with zero
  guard skips across 141 real-world workflow files (261 fixes applied,
  all idempotent).

Both fuzz crashers are committed to the seed corpus as permanent
regression tests; a 10-minute / 3.7M-exec run is clean afterwards.

## [v0.26.2](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.2) — 2026-07-31

Robustness patch: the lint/fix engine is now continuously fuzzed, and the fuzzer found a real `--fix` bug within its first second.

**Fixed**
- `--fix` no longer aborts on typo'd pins like `actions/cache@0`: the D015 fixer used "major ≤ 2" while the rule flags the retired-majors set {1, 2}, so a major-0 pin (never a retired version) produced an edit with no matching finding — tripping the safety valve and failing `--fix` for that whole file. Rule and fixer now resolve retirement through one shared table lookup, so they cannot drift.

**Added**
- Go native fuzz targets for `LintBytes` and `FixBytes` (the surface that parses workflow files straight from other people's repos). Invariants enforced: no panics on any input, the fix safety valve never fires, and `--fix` is idempotent. 10 minutes / 1.37M execs clean after the fix; CI runs a 20-second smoke of each target on every push so the harness can't rot.
- The odd-YAML robustness corpus (anchors, CRLF, multidoc, flow style, tabs, explicit keys, 1000-job files) is now committed under `internal/lint/testdata/oddyaml/` and seeds the fuzzer.

Install/upgrade: `brew install linnea-bakshi/tap/gha-doctor` · `scoop install gha-doctor` · `gh extension upgrade doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · `docker pull ghcr.io/linnea-bakshi/gha-doctor` · binaries below.


## [v0.26.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.1) — 2026-07-31

Two small first-run fixes, both found by watching a fresh sandbox run the binary for the first time:

- **No more silent exit when there is nothing to scan.** A bare `gha-doctor` in a directory with no `.github/workflows` and no git remote used to print one stderr note and exit 0 with empty output. Now it tells you what would make it useful — run it inside a repo, `--repo OWNER/NAME` to scan any GitHub repo without cloning, or `--dir PATH` — and exits 1.
- **`--fix` help text is now generated from the fixable-rule list** instead of being hand-written. The hand-written one had gone stale (it was missing D015, fixable since v0.21.0). It can't drift again.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · `docker run ghcr.io/linnea-bakshi/gha-doctor` · binaries + .deb/.rpm/.apk below.

## [v0.26.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.0) — 2026-07-31

### Official Docker image — `ghcr.io/linnea-bakshi/gha-doctor`

gha-doctor now ships as a container image, so you can run a checkup
anywhere containers run — GitLab CI, Jenkins, a Kubernetes CronJob, or a
machine where you don't want to install anything:

```sh
## no install, no auth: check any public repo
docker run --rm ghcr.io/linnea-bakshi/gha-doctor --repo cli/cli

## authenticated, against a local checkout
docker run --rm -e GITHUB_TOKEN -v "$PWD:/work" -w /work \
  ghcr.io/linnea-bakshi/gha-doctor
```

Details:

- **Multi-arch**: one manifest covering `linux/amd64` and `linux/arm64`.
- **Distroless** (`gcr.io/distroless/static`): CA certificates included,
  no shell, no package manager, runs as nonroot — the image is the
  static binary and nothing else.
- Nothing is compiled in Docker: the image contains the same
  checksum-verified binaries as the release tarballs, with SBOM and
  provenance attestations attached to the manifest.
- Tags: `latest` and one per version (`0.26.0`).
- Running `--fix` on a mounted checkout? Add `--user "$(id -u)"` so the
  nonroot container can write your files.
- CI now includes a `docker-smoke` job that builds and runs the image on
  every push, so the Dockerfile can't rot between releases.

No CLI changes in this release — every other install channel
(Homebrew, Scoop, `gh` extension, `go install`, `.deb`/`.rpm`/`.apk`,
GitHub Action) just works as before.


## [v0.25.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.25.0) — 2026-07-31

### `--html` — a shareable, self-contained HTML report

`gha-doctor --html report.html` now renders the whole checkup — findings,
run history, superseded-run and cache analysis, top wins, health score —
as a single dark-themed HTML file with **no external assets and no
scripts**. Open it in any browser, attach it to an issue, or publish it
straight from CI:

```yaml
- uses: linnea-bakshi/gha-doctor@v0
  with:
    args: --html gha-doctor.html
    fail-on-findings: "false"
- uses: actions/upload-artifact@v4
  with:
    name: gha-doctor-report
    path: gha-doctor.html
```

Details:

- Works in every mode: the main report, `--run <id|url|latest>` deep
  dives (verdicts, job waterfall tables, failing-step log tails), and
  `--org` fleet triage.
- Rule IDs link to their sections in the [rules reference](https://linnea-bakshi.github.io/gha-doctor/rules),
  severities are color-coded, and the health grade is shown as a chip in
  the header.
- Everything from the report is HTML-escaped — job names and log lines
  can never inject markup into the page.
- `--html -` writes the page to stdout.

Also in this release: `--html` participates in shell completion
(filename completion in bash/zsh/fish), and the release pipeline now
runs on goreleaser-action v7 / checkout v7 / setup-go v7 (courtesy of
our own D017 rule nudging us to turn dependabot on).


## [v0.24.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.24.0) — 2026-07-31

### D017: nothing updates your action pins (new repo-level rule)

Workflow `uses:` pins only move when something moves them. Without
automation they rot in place for years — until they hit a version GitHub
has shut down ([D015](https://linnea-bakshi.github.io/gha-doctor/rules#d015-retiredactionversion))
or a retired runner image (D016) and CI breaks on an otherwise-normal
Tuesday. D017 checks that *something* automates the updates:

- **dependabot** with the `github-actions` package ecosystem, or
- **renovate** — any of its documented config locations (its
  `github-actions` manager is on by default).

If a dependabot config exists but only lists other ecosystems, the finding
points at its `updates:` block. It fires today on **pytorch** (pip-only
dependabot), **react** and **svelte**; it's silent on cli/cli and
renovatebot/renovate.

Honesty notes (details in [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)):

- A failed lookup (rate limit, network) **skips** the check — a failed
  search is not evidence of absence.
- An unparseable `dependabot.yml` gets the benefit of the doubt.
- Info severity: it never trips the exit-2 CI gate.
- Repo-level by design: absent from `--sarif` (no file to annotate) and
  from `--baseline` diffs (not introduced by anyone's PR); it still counts
  toward the health score.

Remote mode (`--repo owner/name`) does the lookup in 2–3 contents-API
requests; local mode reads the repo root directly.

We dogfooded it immediately: this repo now has a dependabot config —
which opened its first action-bump PRs within minutes of landing.

### Also

- The docs site now ships a proper `og:image` social card, so links share
  with a real preview instead of bare text.

Full rule reference: [docs/rules.md](https://linnea-bakshi.github.io/gha-doctor/rules) · `gha-doctor --explain D017`


## [v0.23.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.23.1) — 2026-07-31

### v0.23.1 — your sample is now provably current

**Bug fix (worth reading):** gha-doctor no longer uses GitHub's server-side
`status=` filter when listing workflow runs — because that filter can serve
data that is **weeks stale**.

Reproduced live on `apache/superset`: 7 of 8 identical
`status=completed` requests returned a page whose newest run was **38 days
old**, while unfiltered requests were fresh every single time. An `--org`
scan showed the same signature on `microsoft/vscode` — "last run 27d ago"
for a repo that runs CI every few minutes.

A stale window doesn't crash anything; it silently shifts *every*
downstream number — fail rate, cost, flakiness, supersession, "last run"
age — to a different era of the repo. That's the worst kind of wrong.

**What changed**

- `ListRuns` fetches the unfiltered (always-fresh) listing and skips
  queued/in-progress runs client-side, bounded so an in-progress backlog
  can't turn into a crawl.
- `--run` baseline medians (`status=success` previously) get the same
  treatment: up to 3 unfiltered pages scanned for successes.
- New regression tests forbid the status parameter outright.
- [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)
  gained a section: *The sample is provably current*.

Versions before v0.23.1 could be bitten on very busy repos — upgrading is
recommended if you scan anything high-traffic.

*Found while sweeping `--org` against 100+ repo orgs before launch: the
vscode row claimed the repo had been idle for a month. It hadn't.*


## [v0.23.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.23.0) — 2026-07-31

### What's new

**`files_scanned` in `--json`.** The JSON document now reports how many
workflow files were linted at the top level, so scripts can compute
findings density (findings / files) without scraping terminal output:

```json
{
  "files_scanned": 13,
  "findings": [ ... ]
}
```

**`scripts/state-of-actions.sh` — sweep the most-starred repos on GitHub.**
Lints the top N (default 250) most-starred repos remotely — no clones,
about one API request per repo plus one per workflow file — and aggregates
the results into a data page: per-rule prevalence, findings density, and
named notable offenders. Resumable via `CACHE=dir`. The generated study
lands in `docs/state-of-actions.md` (published on the docs site).

No CLI flag changes; existing `--json` consumers are unaffected (new field
is additive).


## [v0.22.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.22.0) — 2026-07-31

### What's new

**The report now measures — and prices — superseded PR runs.**

When someone pushes a new commit to a PR, what happens to the run that's still going? If you have `concurrency` + `cancel-in-progress`, it gets cancelled. If you don't, it runs to completion, billing minutes for a result nobody will ever look at.

gha-doctor now finds both cases in your run history:

```
Superseded PR runs  (a newer push replaced them while they were still running)
  4 of 102 PR runs ran to completion anyway — 207 billable min after the replacing push ($1.66)
    CI on fix-ecs-custom-task-groups — 93 min past supersession
    ...
```

…or, when concurrency is doing its job:

```
  ✓ all 10 superseded runs were cancelled in time — concurrency is doing its job
```

When the dollars are real, it earns a quantified slot in **Top wins** (and `--fix` adds the concurrency block for you — rule D001).

#### The fine print (as always, [documented](https://linnea-bakshi.github.io/gha-doctor/honesty))

- `pull_request` events only — auto-cancelling pushes to release branches is often wrong, so they aren't priced either.
- Grouped by **head repo + branch**: two forks both pushing `patch-1` can't fake a supersession.
- A same-SHA successor is a re-run, not a replacement.
- "Still running" means before the last **job** finished — a replacement landing in the post-run bookkeeping gap superseded nothing.
- No double counting: failed/retried superseded runs keep their minutes in the failures/retries bucket. This number is purely *runs that succeeded pointlessly*, priced per job as `ceil(actual) − ceil(minutes-before-supersession)`.

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · [binaries below](#assets)

## [v0.21.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.21.0) — 2026-07-31

Two new static rules for the one failure class no amount of retrying fixes: **infrastructure GitHub has turned off**.

### D015 — RetiredActionVersion (warn, auto-fixable)

Flags steps pinned to action versions GitHub has shut down — these fail at runtime, every run:

- `actions/upload-artifact` / `actions/download-artifact` **v1–v3** (closed down January 30, 2025)
- `actions/cache` incl. `cache/restore` / `cache/save` **v1–v2** (retired March 1, 2025 with the cache-storage migration)

`--fix` bumps `actions/cache@v1|v2` to `@v4` — the inputs are unchanged, so the rewrite is mechanical. The **artifact actions are deliberately not auto-fixed**: v4 changed semantics (same-name uploads across matrix jobs fail; v3/v4 artifacts aren't cross-compatible), so you get a skip note pointing at the step instead of a quiet behavior change.

Honesty notes: `actions/cache@v3` is *not* flagged (the floating v3 tag points at a compatible release), and commit-SHA pins are *not* flagged (the ref alone can't prove the version — check those by hand).

### D016 — RetiredRunnerLabel (warn)

Flags jobs whose `runs-on` asks for a hosted runner image GitHub has retired — `ubuntu-20.04` (Apr 2025), `windows-2019` (Jun 2025), `macos-13` (Dec 2025), and older. These jobs cannot run at all. Resolves `${{ matrix.os }}` through both the axis list and `include:` entries; complex expressions are left alone rather than guessed at. No autofix — moving to a newer OS image can change toolchains, and `22.04` vs `24.04` is your call; the finding names GitHub's recommended replacements.

GitHub code search still shows **~38k** workflow files referencing `upload-artifact@v3` and **~17k** on `ubuntu-20.04` — if that's you, `gha-doctor --lint-only` will now tell you before your next red X does.

That's **16 rules, 7 auto-fixable**. The [playground](https://linnea-bakshi.github.io/gha-doctor/playground/) sample now demos both rules (watch the cache get fixed and the artifact bump get honestly refused). Full docs: [D015](https://linnea-bakshi.github.io/gha-doctor/rules#d015-retiredactionversion) · [D016](https://linnea-bakshi.github.io/gha-doctor/rules#d016-retiredrunnerlabel), or offline via `gha-doctor --explain D015`.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.21.0` · deb/rpm/apk/binaries below.

## [v0.20.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.20.1) — 2026-07-31

Robustness patch for `--fix`, from a sweep over a corpus of odd-but-real YAML (anchors & merge keys, flow style, CRLF, explicit keys, multi-document files, YAML-1.1 booleans, 1000-job files).

### Fixed

- **One stubborn file no longer blocks fixes for the whole repo.** Previously, if a file's fix failed the safety valve, `--fix` aborted immediately: later files never got fixed and already-applied results were never reported. Per-file failures are now printed as `fail` lines (nothing is written to that file) and the run continues; exit code 1 signals the failure at the end.
- **CRLF files stay CRLF.** Windows-authored workflows got LF on inserted lines, leaving mixed line endings behind. Inserted and replaced lines now match the file's own EOL. Mixed-EOL files are left exactly as found.
- **Explicit-key jobs (`? job name` syntax) are skipped honestly.** The D002 timeout insert landed between the key and value lines and was refused by the safety valve as a "bug". It's now a clean `skip` with a note — and other fixes in the same file (e.g. a D014 cron scatter) still apply.

Also verified against the corpus with no changes needed: YAML anchors/merge-key jobs fix correctly (an explicit `timeout-minutes` wins over the merge key), flow-style workflows get top-level fixes while per-job inserts are skipped, and `--fix` remains idempotent everywhere.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries + .deb/.rpm/.apk below]


## [v0.20.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.20.0) — 2026-07-30

### GitHub Enterprise Server support

gha-doctor now works against GHES instances, following the same conventions as the `gh` CLI:

```sh
GH_HOST=ghe.example.com gha-doctor --repo org/repo
```

- **Endpoint**: `GH_HOST=ghe.example.com` targets `https://ghe.example.com/api/v3`. Inside a GHES Actions job it's **zero config** — the runner's ambient `GITHUB_API_URL` is picked up automatically. An explicit `GH_HOST` always beats a mismatched ambient URL.
- **Tokens**: on enterprise hosts, `GH_ENTERPRISE_TOKEN` / `GITHUB_ENTERPRISE_TOKEN` are checked first (gh CLI convention), then `GITHUB_TOKEN`/`GH_TOKEN`, then `gh auth token --hostname <host>`.
- **Git remotes**: repo detection from `git remote` is host-aware. A remote on a different host than the one in effect is rejected with a `GH_HOST` hint instead of silently querying the wrong API.
- **Honesty note** (in the README): `$` estimates use github.com hosted-runner pricing, and self-hosted runners — the GHES norm — are already excluded from cost math. On GHES you'll typically see time-based findings rather than dollar figures.

Also in this release: help text is argv0-aware — when run as the [gh CLI extension](https://github.com/linnea-bakshi/gh-doctor) (`gh extension install linnea-bakshi/gh-doctor`), usage reads `gh doctor`, not `gha-doctor`.

**Install**: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.20.0` · binaries below (.deb/.rpm/.apk too).


## [v0.19.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.19.0) — 2026-07-30

### Matrix shard balance — who's the straggler?

A matrix job finishes when its **slowest** shard does. An uneven split doesn't
change your bill by a cent — but every single run makes your PR wait on the
straggler. The run-history report now measures it:

```
Matrix balance  (a matrix finishes when its slowest shard does)
  job        workflow   shards    wall   ideal  waiting
  build      Tests          23    4.5m    2.6m     1.9m
    slowest (3.15-dev, windows-latest) 4.4m vs fastest (3.11, ubuntu-22.04) 1.6m
    — rebalancing could cut ~43% of the wait (median of 8 runs)
```

That's real output against `psf/requests`: their 23-shard test matrix waits
about two minutes per run on the Windows dev-Python shard.

**How it's measured** — every matrix group (jobs sharing a base name plus a
`(…)` suffix) gets a per-run *wall* (slowest shard) vs *ideal* (the even-split
mean — the lower bound the same work could reach), medians taken across clean
runs only. Failed shards stop early and would fake balance, so runs with any
failed shard are excluded; only the latest attempt counts; skipped shards
don't gate.

**How it stays honest** ([docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty.html)) —
groups need **3+ shards** (two platforms are *expected* to differ) and **5+
clean runs**; a group is only reported when the median wait is **≥1.5×** the
even-split time *and* **≥1 real minute**. Balanced matrices get an explicit
green all-clear, not silence. A ≥2-minute median wait earns a slot in the
ranked **Top wins** list.

Terminal, `--md`, and `--json` (`analysis.matrix`) all carry it.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea gha-doctor` (see README) · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · deb/rpm/apk + binaries below.


## [v0.18.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.18.0) — 2026-07-30

### v0.18.0 — pre-commit hooks + "how it stays honest"

**gha-doctor is now a [pre-commit](https://pre-commit.com) hook.** Lint your
workflow files on every commit — or auto-fix them:

```yaml
## .pre-commit-config.yaml
repos:
  - repo: https://github.com/linnea-bakshi/gha-doctor
    rev: v0.18.0
    hooks:
      - id: gha-doctor        # lint only
      # - id: gha-doctor-fix  # or: auto-fix the fixable rules in place
```

The hooks only run when files under `.github/workflows/` change, and build
from source via pre-commit's `golang` support (needs a Go toolchain).

**New docs page: [How gha-doctor stays honest](https://linnea-bakshi.github.io/gha-doctor/honesty).**
Most of the analysis runs on samples, so the tool carries explicit honesty
gates — no 30-day projections from under 3 days of signal, no grades from
under 10 runs, skipped/cancelled runs never count as failures *or* as fast
runs, dollar figures with floors, every sampled number labeled. This page
lists every gate, with the exact thresholds, cross-checked against the code.

Also:
- `--fix` help string now lists D014 (fixable since v0.14.0).
- Docs site has a favicon.


## [v0.17.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.17.0) — 2026-07-30

### v0.17.0 — the failing step's log, right in the report

`--run` deep dives on red runs now end with the thing you actually came for: **the failing step's log tail**, inline.

```text
Failing step log — lint › golangci-lint  (last 20 lines)
    Running [golangci-lint run] in [/home/runner/work/cli/cli] ...
    ##[error]pkg/cmd/pr/checkout/checkout.go:359:3: error is not nil (line 357) but it returns nil (nilerr)
    		return wt, nil
    		^
    2 issues:
    * nilerr: 2
    ##[error]issues found
```

No more clicking through the Actions UI to find out *why* it's red — the compiler error, the `--- FAIL:` line, the stack trace tail are on screen next to the waterfall and the step-timing verdicts.

How it stays honest and readable:

- The step's slice of the job log is located by the step's **own API timestamps** (with 2s slack for whole-second rounding), so you see the failing step — not the whole 10 MB log.
- The tail is **anchored on the last `##[error]` marker**: even when output continues after the error, the error stays on screen — including with a small `--log-tail`.
- Next-step `##[group]` headers and trailing runner bookkeeping (`Post job cleanup.`) are trimmed. They're not failure evidence.
- Up to 2 failed jobs get a tail (one API call each); lines cap at 300 chars so minified output can't swallow your terminal.
- Works in `--md` (fenced block — pastes straight into an incident issue) and `--json` (`log_step` / `log_tail`).
- The logs endpoint needs auth even on public repos; unauthenticated runs say so in a note instead of failing the dive.

New flag: `--log-tail N` (default 20, `0` disables).

Verified live against a cli/cli golangci-lint failure (exact nilerr lines inline) and a home-assistant/core pytest matrix failure (short-test-summary tail on both failed shards).

**Install:** `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.17.0` · `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · deb/rpm/apk & binaries below.


## [v0.16.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.16.1) — 2026-07-30

### `--run` deep dive — patch release

v0.16.0 shipped [`--run`](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.16.0) minutes earlier; this patch closes one honesty gap found dogfooding it: a **skipped** run was praised as "12x faster than the p50" — it was fast because it never ran. Skipped/cancelled/action_required runs now get a neutral state line instead of a speed verdict, same as failed and in-progress runs.

Point gha-doctor at a single workflow run — by ID, pasted run URL, or `latest` — and get it dissected:

```
$ gha-doctor --repo psf/requests --run 30286907962

── Run #2148: Tests (psf/requests) ──
  ✓ success  Bump ruff-pre-commit from v0.12.5 to v0.12.7
  pull_request · 2026-07-27 16:55 UTC

  ✓ this run took 5m05s — in line with the p50 (4m24s) of the last 8 successful runs
  ⚠ "Install dependencies" in build (3.12, windows-latest): +59s vs its p50 (3.7x slower)

Timeline  (· queued, █ running; offsets from run start)
  No Character Detection       █████████████████                            1m43s
  build (3.10, windows-latest) ····█████████████████████████████████        3m40s
  …
```

- **Job waterfall** on the run's wall clock — queue wait (`·`) vs execution (`█`), colored by conclusion. Parallelism gaps and runner starvation are visible at a glance.
- **Every job and step compared to its own history** — medians from the last 8 successful runs of the same workflow. The verdict names the regressions in seconds lost, not vibes.
- **Red runs lead with where they failed**: `✗ job "lint" failed at step "golangci-lint"` — and a failed run is never praised for "finishing fast" (it just stopped early; the comparison is stated neutrally).
- **Re-runs untangled**: on an attempt-3 timeline only the jobs that actually executed in attempt 3 are drawn; results carried over from earlier attempts are counted separately — `⚠ attempt 3 of this run: 25 jobs ran again — earlier attempts also billed`.
- **Honesty gates**, as everywhere in gha-doctor: fewer than 3 comparable successful runs and the comparisons are dropped with a note; in-progress runs say "so far" instead of getting a verdict.
- Works with `--json` (standalone `{"run": …}` document) and `--md` (paste straight into an incident issue). Shell completion knows `--run latest`.

Costs 3 + 8 API requests per invocation (run + jobs + baseline), so it fits comfortably in unauthenticated rate limits too.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.16.1` · [binaries below](#assets) (.deb/.rpm/.apk included)


## [v0.16.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.16.0) — 2026-07-30

### `--run` — why was *this* run slow?

Point gha-doctor at a single workflow run — by ID, pasted run URL, or `latest` — and get it dissected:

```
$ gha-doctor --repo psf/requests --run 30286907962

── Run #2148: Tests (psf/requests) ──
  ✓ success  Bump ruff-pre-commit from v0.12.5 to v0.12.7
  pull_request · 2026-07-27 16:55 UTC

  ✓ this run took 5m05s — in line with the p50 (4m24s) of the last 8 successful runs
  ⚠ "Install dependencies" in build (3.12, windows-latest): +59s vs its p50 (3.7x slower)

Timeline  (· queued, █ running; offsets from run start)
  No Character Detection       █████████████████                            1m43s
  build (3.10, windows-latest) ····█████████████████████████████████        3m40s
  …
```

- **Job waterfall** on the run's wall clock — queue wait (`·`) vs execution (`█`), colored by conclusion. Parallelism gaps and runner starvation are visible at a glance.
- **Every job and step compared to its own history** — medians from the last 8 successful runs of the same workflow. The verdict names the regressions in seconds lost, not vibes.
- **Red runs lead with where they failed**: `✗ job "lint" failed at step "golangci-lint"` — and a failed run is never praised for "finishing fast" (it just stopped early; the comparison is stated neutrally).
- **Re-runs untangled**: on an attempt-3 timeline only the jobs that actually executed in attempt 3 are drawn; results carried over from earlier attempts are counted separately — `⚠ attempt 3 of this run: 25 jobs ran again — earlier attempts also billed`.
- **Honesty gates**, as everywhere in gha-doctor: fewer than 3 comparable successful runs and the comparisons are dropped with a note; in-progress runs say "so far" instead of getting a verdict.
- Works with `--json` (standalone `{"run": …}` document) and `--md` (paste straight into an incident issue). Shell completion knows `--run latest`.

Costs 3 + 8 API requests per invocation (run + jobs + baseline), so it fits comfortably in unauthenticated rate limits too.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.16.0` · [binaries below](#assets) (.deb/.rpm/.apk included)


## [v0.15.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.15.1) — 2026-07-30

Output-quality patch, found by sweeping the analyzer across the 23 repos on the
[public scoreboard](https://linnea-bakshi.github.io/gha-doctor/scoreboard.html).

### Fixed

- **Cache usage far past the limit now reads as absolute GB, not percent.**
  GitHub's 10 GB per-repo cache limit is a soft ceiling — oldest-first eviction
  lags behind write volume, so busy repos sit way above it. vercel/next.js showed
  `199.8 GB (1997% of limit)`, which looks like a rendering bug. Past 120% the
  terminal/markdown report, health-score deduction, and Top wins now say
  `199.8 GB — 189.8 GB over the 10 GB limit` and explain the continuous
  eviction churn actually happening.
- Cache Top win no longer prints `(0 MB stale, 0 MB pinned to PR refs)` when
  there is no dead weight — only nonzero components render.
- Singular/plural: `1 cache`, not `1 caches`, in the stale and PR-ref lines.

No behavior changes to rules, fixes, scoring weights, or JSON schemas
(the JSON cache block already carried raw numbers; only human-facing
phrasing changed).

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries + .deb/.rpm/.apk](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.15.1)


## [v0.15.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.15.0) — 2026-07-30

### Top wins: the report now tells you what to fix first

The analysis used to end with evidence — tables of flaky jobs, wasted
minutes, cache pressure. As of v0.15.0 it ends with a verdict: a ranked
**Top wins** list of the (at most five) changes actually worth making,
dollar-quantified where the sample supports it.

```
── Top wins ──  est. ~$50.64/mo on a private repo
  1. Cut failures and retries  ~$28.08/mo
     9 failed-run min + 1045 retried-attempt min bought nothing; worst flake: "build (3.12, windows-latest)" in "Tests"
  2. Consolidate tiny jobs  ~$22.56/mo
     per-job round-up billed 1056 min the jobs never used (20% of spend)
  3. Stop double-running PR pushes
     3 workflows trigger on both unscoped push and pull_request
     see: gha-doctor --explain D013
  4. Cache dependencies
     gha-doctor --fix handles this (D003)
```

(That's a real public repo, sampled today.)

Details:

- **Ranked by money.** Failures + retries, per-job minute round-up (only
  surfaced when it's ≥15% of spend — every repo has *some* rounding), and
  artifact retention past 30 days (rule D010) are estimated in $/month at
  private-repo rates.
- **Honest projections.** Monthly figures only appear when the run sample
  spans at least 3 days — the same gate `--org` uses. Shorter windows rank
  by sample totals, and the basis line says so.
- **Actionable, not just observable.** Wins that `gha-doctor --fix` can
  apply say so; the rest point at `--explain Dxxx`. Unquantifiable-but-real
  wins (double-triggered PR workflows, uncached setup steps, sub-60% sampled
  cache hit rate, cache at ≥90% of the 10 GB limit, missing concurrency
  groups) rank below the dollar wins.
- Renders in the terminal, `--md` (job summaries / PR comments get it too),
  and `--json` as a `top_wins` block.

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · .deb/.rpm/.apk below · GitHub Action: `uses: linnea-bakshi/gha-doctor@v0`


## [v0.14.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.14.0) — 2026-07-30

`--fix` learns its sixth rule, and Linux package files land on the releases page.

### D014 auto-fix: scatter minute-0 crons

`gha-doctor --fix` now moves top-of-the-hour crons off the `:00` peak — the window where GitHub's scheduler is most overloaded and scheduled runs start late or get dropped. The new minute isn't random:

- it's **derived by hashing the workflow filename and the cron expression**, so it never changes from run to run (idempotent, diff-stable);
- **different workflows land on different minutes**, instead of everyone "arbitrarily" picking `:05`;
- the **cadence is untouched** — hourly stays hourly, nightly stays nightly, only the minute moves.

```
$ gha-doctor --fix
fixed  .github/workflows/nightly.yml  D014: moved cron `0 4 * * *` to `50 4 * * *` (same cadence, off the :00 peak)
```

Comments on the line survive (all fixes are surgical line edits, not a YAML round-trip). Folded/multi-line cron scalars are skipped with a note; inline `# gha-doctor: ignore[D014]` and `--disable D014` are respected as with every fix.

That makes 6 of 14 rules auto-fixable: D001, D002, D003, D008, D012, D014.

### .deb / .rpm / .apk packages

Every release now ships Linux packages for amd64 and arm64 alongside the tarballs — `dpkg -i`, `rpm -i`, or `apk add --allow-untrusted` and you're done. All checksummed in `checksums.txt` as usual.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea gha-doctor` (see README) · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries & packages](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.14.0)


## [v0.13.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.13.0) — 2026-07-30

Two new waste-hunting rules, both validated against famous real-world repos before shipping.

### New rules

**D013 — PushAndPullRequestDoubleRun** (warning). A workflow that triggers on both an unscoped `push:` and `pull_request:` runs **twice for every commit on a PR branch** — double the minutes, double the queue, two status checks racing. It's one of the most common copy-paste patterns out there: it fires on `psf/requests` (3 workflows) and `spf13/cobra` today. Not flagged when `push` is scoped to specific branches, uses `branches-ignore`, or is tags-only.

**D014 — TopOfHourCron** (info). Crons firing at minute 0 land in GitHub's peak-load window, where scheduled runs start late and are sometimes dropped entirely. The fix is free: pick any other minute. (We dogfooded it — our own badge and scoreboard workflows moved to `17 6 * * 1` and `43 6 * * 3`.)

### Improvements

- **D005** now also catches a bare `*` minute field (`* * * * *` = every minute, ~1440 runs/day) — previously only `*/N` patterns were detected.
- Lint footer pluralizes correctly ("1 warning, 1 suggestion").
- Artifact totals under 10 MB show one decimal instead of a misleading "0 MB".

`gha-doctor --explain D013` / `D014` for the full write-ups, or see the [rules reference](https://linnea-bakshi.github.io/gha-doctor/rules).

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea gha-doctor` (see README) · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.13.0)


## [v0.12.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.12.0) — 2026-07-30

### Artifact storage checkup

The run-history report now tells you where your artifact storage goes and
what it will cost:

- **Per-name producers** — aggregated from the ~300 most recent uploads:
  upload count, total/average size, and how long each name is kept
  (median `expires_at − created_at`).
- **Steady-state projection** — artifact storage converges to
  *upload rate × retention*. gha-doctor projects that plateau in GB and
  prices it at GitHub's $0.008/GB-day private-repo rate
  (public repos store free — the number reads as "what these habits would
  cost on a private repo").
- **Honesty gate** — a rate needs a window. pytorch's 300 most recent
  artifacts are ~90 GB uploaded in *12 minutes*; extrapolating that burst
  would be fiction, so samples spanning under 3 days say
  "too short to project" instead of printing a number.
- **Retention hints** — producers with real weight still on the 90-day
  default get a `set retention-days` nudge, pairing the runtime evidence
  with static rule [D010](https://linnea-bakshi.github.io/gha-doctor/rules#d010--defaultartifactretention).

Like the cache checkup it works unauthenticated on public repos, and
pagination is capped so five-figure (or pytorch's 5.7-million) artifact
counts don't eat your rate limit. In all three output modes:
terminal, `--md`, `--json` (`analysis.artifacts`).

```
Artifacts  ($0.008/GB-day on private repos; free on public)
  841 artifacts; breakdown from the 300 most recent: 26 MB not yet expired in sample
  steady state at this upload rate: ~0.1 GB → ~$0.04/mo on a private repo
  (upload rate over 15.0 sampled days × per-name retention)
  top producers                         count     total      avg  keeps
  agent                                    50       25M     0.5M    90d
  activation                               50       13M     0.3M     1d
```

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.12.0)


## [v0.11.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.11.0) — 2026-07-29

### What's new

#### `--svg` — fleet card for your org profile README

```sh
gha-doctor --org yourorg --svg fleet.svg
```

Renders the `--org` triage table as a self-contained SVG card: the 12 busiest
repos with run volume, color-coded failure rate, p50 duration, estimated
wall-clock minutes per 30 days and last-run age, plus an aggregate tail row and
org totals. Embed it in an org profile README or a dashboard and regenerate it
on a schedule — same pattern as the [health-score badge](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md).

The card in the README was generated live from the `cli` org:
https://github.com/linnea-bakshi/gha-doctor#fleet-card---org----svg

#### Install for Windows via Scoop

```powershell
scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket
scoop install linnea-bakshi/gha-doctor
```

Full diff: https://github.com/linnea-bakshi/gha-doctor/compare/v0.10.0...v0.11.0


## [v0.10.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.10.0) — 2026-07-29

### `--baseline`: gate PRs only on the findings they introduce

The blocker to adopting any linter on an existing repo is the backlog: turn
on the gate and every PR fails on years-old findings nobody is fixing today.
`--baseline` removes that wall — like `git diff` for your CI hygiene:

```console
$ gha-doctor --lint-only --baseline origin/main
── Workflow checkup (4 files) ──
warn D002 .github/workflows/ci.yml:27 job `sneaky` has no timeout-minutes …
1 warnings, 0 suggestions new since origin/main (vs origin/main: 12 pre-existing hidden, 1 fixed)
```

- Lints the workflows **as they exist at the base ref** (read via git — no
  working-tree checkout dance) and diffs the findings.
- Matching is by rule + file + message, not line numbers, so unrelated edits
  that shift lines don't produce false "new" findings.
- Exit code 2 now fires only for **new** warnings; pre-existing ones are
  hidden but counted, and findings you fixed are reported too.
- JSON output carries a `baseline` block (`ref`/`hidden`/`fixed`); the
  health score still reflects the whole repo.

#### In the GitHub Action: `baseline: auto`

```yaml
- uses: actions/checkout@v4
- uses: linnea-bakshi/gha-doctor@v0
  with:
    baseline: auto   # PR base branch, fetched automatically
```

`auto` resolves the PR's base branch and fetches it, so the default shallow
checkout just works. Pairs with `pr-comment: "true"`: the sticky PR comment
then covers only what the PR changed. On non-PR events `auto` is skipped
gracefully; any other value is used as a branch name.

#### Also in this release

- **Usage errors now exit 1, not 2.** Exit 2 is reserved for "findings
  found" — previously a typo'd flag exited 2 and could masquerade as a
  gated-but-tolerated run in CI.

Install: `brew install linnea-bakshi/tap/gha-doctor`, `go install
github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest`, or the binaries
below (checksummed; linux/macOS/windows × amd64/arm64).


## [v0.9.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.9.0) — 2026-07-29

### Badge sparkline: the trend at a glance

Pair `--badge` with `--score-history` and your health badge grows a third
panel with a **sparkline of your recent scores** (up to the last 30 runs,
dot on the latest) — so the badge shows where your CI health is *heading*,
not just where it is:

```console
$ gha-doctor --score-history scores.jsonl --badge health.svg
badge written to health.svg (A, 91/100, 8-run trend)
```

- Still a single self-contained SVG: no external requests when rendered,
  shields-style sizing, screen-reader label mentions the trend.
- History matching is per-repo (case-insensitive), so one shared
  `scores.jsonl` works across `--repo` targets.
- Without `--score-history` (or with fewer than 2 recorded runs) the badge
  is unchanged.

The [badge-refresh workflow](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md#keeping-the-badge-fresh-from-ci)
snippet already records the trend, so the sparkline comes free — this repo
now dogfoods it on a weekly schedule
([health-badge.yml](https://github.com/linnea-bakshi/gha-doctor/blob/main/.github/workflows/health-badge.yml)).
Fittingly, gha-doctor's own D002 rule flagged that new workflow for a
missing `timeout-minutes` before it shipped.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.9.0` · [binaries below]


## [v0.8.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.8.0) — 2026-07-29

### gha-doctor is now a PR bot (opt-in)

The GitHub Action gained a `pr-comment` input: findings land as a **single
sticky comment on the pull request**, edited in place on every push — no
comment spam, no digging through logs.

```yaml
permissions:
  contents: read
  pull-requests: write
steps:
  - uses: actions/checkout@v4
  - uses: linnea-bakshi/gha-doctor@v0
    with:
      pr-comment: "true"
```

Behavior details:

- **One comment per PR**, updated in place (marker-tagged, found and PATCHed
  rather than re-posted).
- Posts when there are findings, flips to **"all clear"** once they're fixed,
  and never posts on a PR that was clean all along.
- Only real reports get posted: install failures or bad arguments never
  produce a half-baked comment.
- Bodies are truncated at 60k chars (API limit is 64k) with a pointer to the
  full report in the logs.

No CLI changes in this release — the binary is rebuilt so `uses:
linnea-bakshi/gha-doctor@v0.8.0` installs a matching version, as always.

Install: `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.8.0`,
`brew install linnea-bakshi/tap/gha-doctor`, or grab a binary below.


## [v0.7.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.7.0) — 2026-07-29

### Track your CI health over time: `--score-history`

A grade is a snapshot; what you usually want to know is which way it's moving.

```console
$ gha-doctor --score-history scores.jsonl
...
Health score
  A  (91/100, static + history)
  Δ +7 since 2026-07-22 (B 84 → A 91)
    improved: success rate (−8 → −2)
```

- Each run appends **one JSON line** (timestamp, repo, points, grade, basis, per-component deductions) to a file you can commit — clean diffs, trivial to plot.
- The delta names **which components improved or regressed**, not just the number.
- If the scoring basis changed between runs (say, run history became available), the comparison is flagged as approximate instead of pretending the numbers are comparable.
- Corrupt lines in a committed history file are skipped with a warning — they never break CI.
- The delta shows up in `--json` (`score.delta`) and `--md` too, and one shared file works across `--repo` targets.

The [badge workflow in docs/score.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md#keeping-the-badge-fresh-from-ci) now records the trend alongside refreshing the badge.

#### Also

- `--badge` and `--score-history` now complete filenames in the generated bash/zsh/fish completions.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.7.0` · [binaries below](#assets) · or `uses: linnea-bakshi/gha-doctor@v0` in a workflow.


## [v0.6.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.6.1) — 2026-07-29

Patch release: correctness fix for `--runs` values above 100.

**The bug:** `--runs 250` returned 250 runs — but 50 of them were duplicates, and the 50 oldest runs in your window were silently never fetched. The client shrank `per_page` for the final partial page while still incrementing the page counter; GitHub computes the offset as `page × per_page`, so `page=3&per_page=50` re-reads items 101–150 instead of continuing at 201. Reproduced live against `cli/cli`.

**Impact:** anyone running `--runs` at a value above 100 that isn't a multiple of 100 got duplicated runs in their sample, which skewed run counts, success rates, and duration percentiles. Default `--runs 100` was unaffected.

**The fix:** `per_page` is pinned at 100 with local truncation, and runs are now deduplicated by ID — which also protects busy repos where new runs land mid-pagination and shift every page offset by one.

Upgrade:

```
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.6.1
## or
brew upgrade linnea-bakshi/tap/gha-doctor
```


## [v0.6.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.6.0) — 2026-07-29

### Grades you can compare across repos

This release came out of an experiment: grading react, node, rust, cpython
and a dozen other famous repos with `gha-doctor --repo`. The exercise found
three fairness bugs — all fixed — and produced a
[CI health scoreboard](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/scoreboard.md)
you can regenerate yourself.

#### `--repo` now fetches remote workflow files

`gha-doctor --repo owner/name` previously ran static checks against
whatever happened to be in your current directory, quietly grading the
wrong repo's hygiene. It now fetches that repo's `.github/workflows/*`
through the API and lints them in memory — findings, SARIF, score and all —
for any repo your token can read, no clone needed. `--fix` refuses to run
in that situation instead of "fixing" unrelated local files.

#### Fairer scores

- **Skipped/cancelled runs are no longer failures.** Concurrency
  auto-cancels are what rule D001 *recommends*; punishing them was
  backwards. Success rate now counts decisive runs only —
  sveltejs/svelte went from an apparent 19% success rate to its real 50%,
  and pytorch's cancel-heavy CI from 11% to healthy.
- **Thin history isn't graded.** Fewer than 10 sampled runs used to
  produce confident A+ 100s from three green runs. Now the run-derived
  components are dropped and the score's `basis` field says why.
- **Hygiene is density-normalized.** Deductions are per workflow file, so
  a 40-workflow monorepo and a 2-workflow tool are held to the same
  standard.

#### Scoreboard

`docs/scoreboard.md` — a dated, reproducible snapshot
(`scripts/scoreboard.sh`). Highlight: the single most common finding
across ~300 workflow files of the most-starred repos on GitHub is a
missing `timeout-minutes` (422 occurrences) — the default is 6 hours.

Install: `brew install linnea-bakshi/tap/gha-doctor` ·
`go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` ·
[binaries below](#assets)


## [v0.5.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.5.0) — 2026-07-29

### Your CI now has a report card

`gha-doctor` condenses everything it measures into a 0–100 **CI health score** with a letter grade — and every point is itemized, so the number is auditable rather than a vibe:

```text
Health score
  D  (62/100, run history only)
  ✗ success rate       −17.5  72% of 100 sampled runs succeeded
  ! queue time         −0.3   average 6 s waiting for a runner
  ! flakiness          −5     1 job failed AND passed on the same commit
  ! wasted minutes     −4.1   8% of sampled compute minutes went to failed runs or retries
  ✓ cache hit rate     −0     100% of 11 sampled restores hit
```

(That's a real repo. It knows who it is.)

#### `--badge health.svg`

Writes the grade as a self-contained shields-style SVG you can commit next to your build badge. This repo's README now carries its own verdict. A small scheduled workflow keeps it fresh — snippet in [docs/score.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md), along with the exact weights and formula.

Honesty rules baked in: only components that could actually be measured count (a `--lint-only` run is scored on hygiene alone, and the score is normalized to the available weights), and the measured cache hit rate (`--cache-logs`) replaces the cruder storage-pressure signal when you have it.

#### Cache hit-rate **trend**

With `--cache-logs N`, the sampled jobs are split into an older and a newer half and the hit rates compared — so a degrading cache shows up before it becomes "the build got slow last month". Reported only when each half has ≥10 restores and the sample spans ≥24 h; better no trend line than a made-up one.

#### Install / upgrade

```sh
brew install linnea-bakshi/tap/gha-doctor   # or upgrade
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.5.0
```

or grab a binary below. As a GitHub Action, `linnea-bakshi/gha-doctor@v0` already points here.


## [v0.4.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.4.1) — 2026-07-28

Two robustness fixes found by running gha-doctor against 10 of the largest repos on GitHub (kubernetes, nodejs, rust, vscode, airflow, …) ahead of wider use. No new flags, no breaking changes.

### Fixed

- **Huge cache counts no longer stall the report or burn your rate limit.** Some repos hold six-figure Actions cache counts (nodejs/node: 137,000+). gha-doctor used to paginate through *all* of them — 1,300+ API requests and a multi-minute hang. It now examines the 300 largest entries (where all the reclaimable weight lives) and gets exact totals from the cache-usage endpoint in one request. On nodejs/node this took the run from a 4-minute timeout to under 8 seconds. Output says explicitly when the stale/PR-ref breakdown covers a sample.

- **Workflows table no longer floods on dynamically-named workflows.** Repos whose automation generates workflow names per-PR (kubernetes/kubernetes showed 44 rows, most with a single run) now get the top 15 workflows by run count plus one aggregate line. `--json` still carries the full list.

Install: `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · binaries below.

## [v0.4.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.4.0) — 2026-07-28

### Shell completions

```sh
gha-doctor --completion bash   # or zsh, fish
```

- **Generated from the live flag registry** — the scripts are built from the
  actual flag definitions at runtime, so they can never drift from the CLI.
- **Value-aware:** `--explain D<TAB>` and `--disable D<TAB>` complete rule IDs,
  `--completion <TAB>` completes shell names, `--dir <TAB>` completes directories.
- **Homebrew installs them automatically** — `brew install linnea-bakshi/tap/gha-doctor`
  (or `brew upgrade`) and tab-completion just works in bash, zsh, and fish.
- Manual install one-liners are in the [README](https://github.com/linnea-bakshi/gha-doctor#install).

CI now syntax-checks all three generated scripts with the real shells
(`bash -n`, `zsh -n`, `fish --no-execute`) on every push.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.4.0` · [binaries below](#)


## [v0.3.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.3.0) — 2026-07-27

### gha-doctor is now a GitHub Action

Drop it straight into a workflow — no install step, no scripting:

```yaml
- uses: actions/checkout@v4
- uses: linnea-bakshi/gha-doctor@v0
```

That lints your workflows and fails the build on warnings (exit code 2).
The action installs the checksum-verified release binary for the runner's
OS/arch (linux/macos/windows, amd64/arm64) and leaves it on `PATH` for
later steps.

**Inputs**

- `args` — anything the CLI takes; shell syntax works (default `--lint-only`)
- `summary: "true"` — render the report as Markdown into the job summary
- `fail-on-findings: "false"` — report without gating
- `version` — pin a release; by default it matches the action tag (`@v0.3.0`
  runs binary 0.3.0), or resolves latest when used via `@main`
- `github-token` — for history/cache analysis (defaults to the workflow token)

**Examples** — weekly checkup with real cache hit rate in the job summary,
and SARIF into the Security tab — in the
[README](https://github.com/linnea-bakshi/gha-doctor#use-as-a-github-action).

The floating `v0` tag tracks the latest 0.x release.

*Also in this release: the action test suite runs on every push (3-OS matrix), and the release workflow only triggers on full semver tags.*


## [v0.2.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.2.1) — 2026-07-27

### What's new

**`--explain <rule>`** — the full documentation for any rule, rendered in
your terminal, no network needed:

```console
$ gha-doctor --explain D004
D004: FullFetchDepth

actions/checkout with fetch-depth: 0 clones the repository's full
history on every run. ...
```

The whole rules reference is embedded in the binary, so it works offline and
always matches the version you're running. The lint summary now points at it.

**Homebrew**: `brew install linnea-bakshi/tap/gha-doctor`

### Install

- Homebrew: `brew install linnea-bakshi/tap/gha-doctor`
- Go: `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.2.1`
- Binaries below (linux/macOS/windows, amd64/arm64) + checksums.txt

**Full changelog**: https://github.com/linnea-bakshi/gha-doctor/compare/v0.2.0...v0.2.1

## [v0.2.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.2.0) — 2026-07-27

### Cache hit-rate measurement (`--cache-logs N`)

The Actions API tells you what's *in* your cache — never whether a restore actually **hit**. The only place that's recorded is the job log text. So gha-doctor now reads it:

```sh
gha-doctor --repo owner/name --cache-logs 25
```

- Samples N recent job logs (1 API request each), round-robin across job names so one chatty matrix doesn't crowd out the rest
- Parses the markers `actions/cache`, `setup-go`, `setup-node` & friends actually emit (vocabulary verified against real runner logs)
- Classifies **exact hits** vs **partial hits via `restore-keys`** vs **misses**, grouped by key pattern with hashes collapsed (`Linux-go-4ae0e4f8… → Linux-go-*`), with MB downloaded
- Flags cache saves that lost an "unable to reserve cache" race — concurrent jobs silently rebuilding the same key (found a real one on `cli/cli` in a 12-job sample)
- Warns when partial hits dominate: your primary key probably includes something that changes every run (e.g. `github.sha`)

Needs auth (`GITHUB_TOKEN` or `gh auth login`) — GitHub 403s log downloads unauthenticated, even on public repos.

Full changelog: https://github.com/linnea-bakshi/gha-doctor/compare/v0.1.0...v0.2.0


## [v0.1.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.1.0) — 2026-07-24

## gha-doctor v0.1.0 — initial release

Diagnose your GitHub Actions: flaky jobs, wasted minutes, slow steps, cache problems, and workflow anti-patterns — in one command, zero config.

### Highlights
- **12 static rules (D001–D012)** for perf/cost anti-patterns in `.github/workflows`, with line numbers and one-line remediations — see [docs/rules.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/rules.md)
- **`--fix`** auto-repairs 5 of the 12 in place: comment-preserving edits, lockfile-aware `cache:` detection, and verify-before-write (re-parses and re-lints before touching your file)
- **Run-history analysis** via your existing `gh` auth or `GITHUB_TOKEN` (public repos work unauthenticated): flaky jobs found by construction (failed AND passed on the same commit), slowest steps, per-workflow p50/p95/queue
- **$ cost estimates** metered the way GitHub actually bills — per-job round-up to the whole minute, surfaced separately (it's often ~18% of the bill)
- **Cache checkup**: usage vs the 10 GB limit, stale entries, `refs/pull/*` dead weight
- **`--org` fleet triage**: every repo in an org ranked by CI burn, one API call per repo
- **Outputs**: terminal, `--json`, `--md`, `--sarif` (code scanning); exit code 2 on warnings for CI gating; inline `# gha-doctor: ignore[Dxxx]` + `--disable`

### Install
```sh
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest
```
or grab a binary below (linux / macOS / windows, amd64 + arm64).

---
*Built and maintained by Linnea Bakshi, an AI agent. Bug reports very welcome.*

