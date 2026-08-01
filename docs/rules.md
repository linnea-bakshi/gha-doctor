# Rules reference

Every static rule `gha-doctor` checks, why it matters, and how to fix or
silence it. Rules target **speed, cost, and reliability** — for syntax
correctness use [actionlint](https://github.com/rhysd/actionlint), for
security use [zizmor](https://github.com/woodruffw/zizmor).

| ID | Name | Severity | `--fix` |
|----|------|----------|:-------:|
| [D001](#d001-missingconcurrencycancellation) | MissingConcurrencyCancellation | warning | ✅ |
| [D002](#d002-nojobtimeout) | NoJobTimeout | warning | ✅ |
| [D003](#d003-uncachedsetupaction) | UncachedSetupAction | warning | ✅ |
| [D004](#d004-fullfetchdepth) | FullFetchDepth | info | — |
| [D005](#d005-highfrequencycron) | HighFrequencyCron | warning | — |
| [D006](#d006-expensiverunneroneverypush) | ExpensiveRunnerOnEveryPush | info | — |
| [D007](#d007-dockerbuildwithoutlayercache) | DockerBuildWithoutLayerCache | warning | — |
| [D008](#d008-cachewithoutrestorekeys) | CacheWithoutRestoreKeys | info | ✅ |
| [D009](#d009-continueonerrormasksfailures) | ContinueOnErrorMasksFailures | info | — |
| [D010](#d010-defaultartifactretention) | DefaultArtifactRetention | info | — |
| [D011](#d011-largematrixonprs) | LargeMatrixOnPRs | warning | — |
| [D012](#d012-npminstallinci) | NpmInstallInCI | info | ✅ |
| [D013](#d013-pushandpullrequestdoublerun) | PushAndPullRequestDoubleRun | warning | — |
| [D014](#d014-topofhourcron) | TopOfHourCron | info | ✅ |
| [D015](#d015-retiredactionversion) | RetiredActionVersion | warning | ✅ |
| [D016](#d016-retiredrunnerlabel) | RetiredRunnerLabel | warning | — |
| [D017](#d017-noactionsupdateautomation) | NoActionsUpdateAutomation | info | — |
| [D018](#d018-deprecatedworkflowcommand) | DeprecatedWorkflowCommand | warn | ✓ |

Warnings make `gha-doctor` exit with code 2 (so you can gate CI on them);
info findings don't affect the exit code.

## Suppressing findings

Every rule is a heuristic; your workflow may be the exception. Three ways to
say so:

**Inline, per finding** — a comment on the flagged line, or on its own line
directly above:

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0  # gha-doctor: ignore[D004]  (semantic-release needs history)

# gha-doctor: ignore[D003,D008]
- uses: actions/setup-node@v4
```

A bare `# gha-doctor: ignore` suppresses every rule on that line. Rule IDs
are case-insensitive. `--fix` respects these directives: a suppressed
finding is never auto-fixed.

**Globally, per rule** — the `--disable` flag:

```console
$ gha-doctor --lint-only --disable D004,D009
```

**Repo-wide, as standing policy** — a `.gha-doctor.yml` at the repo root
(or `.github/gha-doctor.yml`):

```yaml
disable: [D004, D009]
```

CLI flags beat the file, `--disable` adds to its list, and `--no-config`
ignores it. An applied config is always disclosed (stderr + the `config`
block in `--json`), and unknown keys or rule IDs warn loudly — a typo must
never silently disable nothing. With `--repo`, the target repo's own config
is fetched and honored.

---

## D001: MissingConcurrencyCancellation

**A `pull_request` workflow has no `concurrency` group with
`cancel-in-progress`.** When someone pushes a new commit to a PR, the run
for the old commit keeps going — burning billable minutes producing a
result nobody will look at. On an active repo this is routinely 10–30% of
all CI minutes.

```yaml
# bad: superseded runs keep running
on: pull_request

# good
on: pull_request
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
```

Also fires (as info) when a `concurrency` group exists but
`cancel-in-progress` is absent or `false` — superseded runs then *queue*
instead of cancel.

**Auto-fix:** inserts the concurrency block above `jobs:`, or adds/flips
`cancel-in-progress` in an existing group.

The run-history report measures this rule's real cost: the **Superseded PR
runs** section counts runs a newer push replaced while they were still
running, and prices the billable minutes the completed ones burned past
that moment.

## D002: NoJobTimeout

**A job has no `timeout-minutes`.** The default is 360: one wedged step —
a hung network call, a deadlocked test — bills six hours of runner time
before GitHub kills it. Set the timeout a little above the job's normal
duration so hangs die in minutes, not hours.

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 15   # normal run is ~8 min
```

Jobs that call reusable workflows (`uses:`) are skipped — the timeout
belongs in the callee.

**Auto-fix:** inserts `timeout-minutes: 30` (deliberately generous — the
point is capping hangs at well under 360, not guessing your build time;
tighten it afterwards).

## D003: UncachedSetupAction

**`actions/setup-node` / `setup-python` / `setup-java` without the
`cache:` input.** These actions have built-in dependency caching, off by
default. Without it every run re-downloads your whole dependency tree.
Enabling it is one line and typically saves 30s–3min per run.

```yaml
- uses: actions/setup-node@v4
  with:
    node-version: 20
    cache: npm          # or yarn / pnpm
```

**Auto-fix:** detects your lockfile (`package-lock.json`, `poetry.lock`,
`pom.xml`, …) and inserts the matching `cache:` value. Skips when the
ecosystem is ambiguous (two lockfiles) rather than guess wrong.

## D004: FullFetchDepth

**`actions/checkout` with `fetch-depth: 0`** clones the repository's full
history on every run. On a large repo that can dominate job time. Most
jobs only need the checked-out commit (the default, depth 1).

Legitimate uses exist — changelog generation, `semantic-release`,
`git describe` — which is why this is info-level and **deliberately not
auto-fixed**: whether a job needs history is a semantic question a linter
can't answer. If yours does:

```yaml
- uses: actions/checkout@v4
  with:
    fetch-depth: 0  # gha-doctor: ignore[D004]  (semantic-release)
```

## D005: HighFrequencyCron

**A `schedule:` cron firing more often than every 15 minutes.** That's
~96+ runs a day of baseline load, and GitHub explicitly deprioritizes
high-frequency schedules — under load they're delayed or silently
dropped, so you pay the minutes *and* can't rely on the timing. Prefer
event-driven triggers (`push`, `workflow_run`, webhooks) or a coarser
interval.

## D006: ExpensiveRunnerOnEveryPush

**A macOS or Windows job triggered on every push or schedule.** macOS
bills at **10×** the Linux rate, Windows at **2×**. A matrix that runs all
three OSes on every push spends 13× a Linux-only run. Common pattern:
run Linux everywhere, and gate macOS/Windows behind a Linux smoke test,
path filters, or release tags.

## D007: DockerBuildWithoutLayerCache

**`docker/build-push-action` without `cache-from`.** Runners are
ephemeral: with no cache source, every run rebuilds every layer from
scratch — routinely 5–20 minutes for nothing.

```yaml
- uses: docker/build-push-action@v6
  with:
    cache-from: type=gha
    cache-to: type=gha,mode=max
```

## D008: CacheWithoutRestoreKeys

**`actions/cache` with a `key` but no `restore-keys`.** The moment your
lockfile changes, the exact key misses and you start from a fully cold
cache — even though yesterday's cache is 95% right. `restore-keys` lets a
stale-but-close cache be restored and updated.

```yaml
- uses: actions/cache@v4
  with:
    path: ~/.npm
    key: npm-${{ runner.os }}-${{ hashFiles('package-lock.json') }}
    restore-keys: |
      npm-${{ runner.os }}-
```

**Auto-fix:** when the key ends in `${{ hashFiles(...) }}`, derives the
prefix mechanically (everything before the hash). Other key shapes are
skipped with a note.

## D009: ContinueOnErrorMasksFailures

**`continue-on-error: true` at the job level.** The job shows green no
matter what happens, so real breakage accumulates unseen — the usual
story is "that job's been failing for six weeks." For known-flaky matrix
legs, prefer excluding them from `strategy.matrix` or surfacing a
separate non-required status check.

## D010: DefaultArtifactRetention

**`actions/upload-artifact` without `retention-days`.** Artifacts default
to 90-day retention and count against your storage quota; on a busy repo
debug logs and build outputs quietly pile up into gigabytes you pay for.
Most artifacts are looked at within days:

```yaml
- uses: actions/upload-artifact@v4
  with:
    name: test-logs
    path: logs/
    retention-days: 7
```

## D011: LargeMatrixOnPRs

**A static `strategy.matrix` that expands to 20+ jobs per trigger.**
Every push multiplies into that many jobs — queue pressure for everyone
and a big minutes bill. Common pattern: a reduced matrix on PRs, the full
matrix on `main`/release:

```yaml
strategy:
  matrix:
    os: ${{ github.event_name == 'pull_request' && fromJSON('["ubuntu-latest"]') || fromJSON('["ubuntu-latest","macos-latest","windows-latest"]') }}
```

(or two workflows: a slim `pr.yml` and a full `main.yml`).

## D012: NpmInstallInCI

**`npm install` in a `run:` step.** In CI you want `npm ci`: it installs
exactly what the lockfile says (no drift), deletes `node_modules` first
(reproducible), and is faster. `npm install` can *modify* the lockfile
mid-build.

**Auto-fix:** rewrites bare `npm install` → `npm ci`. Lines with package
arguments are skipped with a note (`npm ci` takes no package args, so a
mechanical rewrite could change behavior).

## D013: PushAndPullRequestDoubleRun

**`on:` lists both an unscoped `push` and `pull_request`.** A commit
pushed to a PR branch in the same repository matches *both* triggers, so
the whole workflow runs twice — double the minutes, double the queue
pressure, and two status checks racing each other. This is one of the most
common (and most expensive) copy-paste patterns in the wild.

Scope `push` to the branches that aren't covered by PRs:

```yaml
on:
  push:
    branches: [main]   # post-merge runs
  pull_request:        # PR runs
```

Not flagged when `push` is limited to specific branches, uses
`branches-ignore`, or is tags-only (`push: {tags: [...]}` never fires for
branch pushes).

## D014: TopOfHourCron

**A `schedule:` cron firing at minute 0.** Everyone's crons fire at the
top of the hour, so that's when GitHub's scheduler is most overloaded —
runs regularly start many minutes late, and under heavy load [scheduled
runs can be dropped entirely](https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#schedule).
The fix costs nothing: pick an arbitrary minute.

```yaml
on:
  schedule:
    - cron: "23 4 * * *"   # not "0 4 * * *"
```

**Auto-fix:** rewrites the minute field to a stable value in 1–59, picked
by hashing the workflow filename and the expression — so the choice never
changes between runs, different workflows scatter across the hour instead
of all moving to the same "arbitrary" minute, and the cadence (hourly,
daily, weekly…) is untouched. Folded or multi-line cron scalars are
skipped with a note.

## D015: RetiredActionVersion

**A step `uses:` an action version GitHub has shut down.** Unlike every
other rule, this isn't about waste — these steps *hard-fail at runtime,
every run*:

- `actions/upload-artifact` / `actions/download-artifact` **v1–v3** —
  [closed down January 30, 2025](https://github.blog/changelog/2024-04-16-deprecation-notice-v3-of-the-artifact-actions/)
- `actions/cache` (incl. `cache/restore`, `cache/save`) **v1–v2** —
  [retired March 1, 2025](https://github.blog/changelog/2024-09-16-notice-of-upcoming-deprecations-and-changes-in-github-actions-services/)
  when cache storage moved to its new architecture

`actions/cache@v3` is *not* flagged: the floating `v3` tag was updated to
a compatible release. Commit-SHA pins are also not flagged — the SHA alone
can't prove which version it is, and gha-doctor doesn't report what it
can't verify (a SHA pin of a retired build will still fail; check it by
hand).

**Auto-fix:** bumps `actions/cache@v1|v2` (and `restore`/`save`
subpaths) to `@v4` — the inputs (`path`, `key`, `restore-keys`) are
unchanged, so the rewrite is mechanical. The **artifact actions are
deliberately not auto-fixed**: v4 changed semantics (same-name uploads
across matrix jobs fail; v3/v4 artifacts aren't cross-compatible), so a
mechanical bump could trade a loud failure for a quiet wrong result. You
get a skip note pointing at the step instead.

## D016: RetiredRunnerLabel

**A job requests a hosted runner label GitHub has retired.** The job
cannot run — it fails immediately or sits queued until the timeout. As of
this release the retired labels are:

| label | retired |
|-------|---------|
| `ubuntu-20.04` | [April 15, 2025](https://github.blog/changelog/2025-01-15-github-actions-ubuntu-20-runner-image-brownout-dates-and-other-breaking-changes/) |
| `windows-2019` | [June 30, 2025](https://github.com/actions/runner-images/issues/12045) |
| `macos-13` (+ `-large`/`-xlarge`) | [December 4, 2025](https://github.blog/changelog/2025-09-19-github-actions-macos-13-runner-image-is-closing-down/) |
| `macos-12`, `macos-11`, `macos-10.15` | Dec 2024 / Jun 2024 / Sep 2022 |
| `ubuntu-18.04`, `ubuntu-16.04` | Apr 2023 / Sep 2021 |
| `windows-2016` | June 2022 |

Checked on scalar `runs-on:`, label lists, and `${{ matrix.KEY }}`
indirection (both the axis list and `include:` entries). Complex
expressions (`${{ matrix.os || '…' }}`) aren't resolved — no guessing.

**No auto-fix**, deliberately: moving to a newer OS image can change
toolchain versions and break builds, and the right target (`22.04` vs
`24.04`, `macos-14` vs `15`) is your call. The finding names GitHub's
recommended replacements.

## D017: NoActionsUpdateAutomation

**Nothing in the repo updates its action pins.** Workflow `uses:` pins
only move when something moves them. Without automation they rot in place
for years — until they hit a version GitHub has shut down (D015) or a
retired runner image (D016) and CI breaks on an otherwise-normal Tuesday.
A GitHub code search finds tens of thousands of workflows still pinned to
`actions/upload-artifact@v3`, which stopped working in January 2025 —
that's what "nobody updates action pins by hand" looks like at scale.

The check is satisfied by either:

- **dependabot** with the `github-actions` package ecosystem:

  ```yaml
  # .github/dependabot.yml
  version: 2
  updates:
    - package-ecosystem: github-actions
      directory: /
      schedule:
        interval: weekly
  ```

- **renovate** — any config file (`renovate.json`, `renovate.json5`,
  `.renovaterc`(.json/.json5), or the `.github/` variants). Renovate's
  `github-actions` manager is enabled by default, so presence of a config
  counts; gha-doctor doesn't inspect it further.

If a dependabot config exists but lists other ecosystems only, the finding
points at its `updates:` block instead.

This is a **repo-level** rule — the evidence lives outside
`.github/workflows/` — with a few deliberate differences from the per-file
rules:

- It's absent from `--sarif` output: when the finding is that a file is
  *missing*, there is no file for code scanning to annotate.
- `--baseline` mode omits it: a missing dependabot config wasn't
  "introduced since REF" by anyone's PR (it still counts toward the health
  score, which grades the whole repo).
- It doesn't run in the [browser playground](../playground/), which lints
  pasted workflow snippets and can't see your repo.
- Silence it with `--disable D017`, or — when a dependabot config exists —
  an inline `# gha-doctor: ignore[D017]` comment above its `updates:` key.

**No auto-fix**: creating a `.github/dependabot.yml` decides your update
cadence and PR volume for you; that's your call. The snippet above is the
whole fix.

## D018: DeprecatedWorkflowCommand

**A `run:` step writes a deprecated stdout workflow command.** Two
generations of breakage in one rule:

- `::set-env` and `::add-path` were
  [disabled November 16, 2020](https://github.blog/changelog/2020-11-09-github-actions-removing-set-env-and-add-path-commands-on-november-16/)
  after a command-injection vulnerability — a step that emits them
  **errors at runtime, every run**.
- `::set-output` and `::save-state` were
  [deprecated in October 2022](https://github.blog/changelog/2022-10-11-github-actions-deprecating-save-state-and-set-output-commands/);
  they still work, but GitHub prints a deprecation-warning annotation on
  **every single run** and has announced their removal (the original
  May 2023 cut-off was
  [postponed](https://github.blog/changelog/2023-07-24-github-actions-update-on-save-state-and-set-output-commands/),
  not cancelled).

The replacement is the environment-file syntax, and it's mechanical:

```bash
# before                                      # after
echo "::set-output name=sha::$SHA"            echo "sha=$SHA"   >> "$GITHUB_OUTPUT"
echo "::save-state name=pid::$PID"            echo "pid=$PID"   >> "$GITHUB_STATE"
echo "::set-env name=MODE::release"           echo "MODE=release" >> "$GITHUB_ENV"
echo "::add-path::$HOME/.local/bin"           echo "$HOME/.local/bin" >> "$GITHUB_PATH"
```

Detection scans every `run:` script (comment lines don't count) and fires
once per command per step, whatever the shell — a pwsh `Write-Output
"::set-output …"` is just as deprecated.

**Auto-fix:** rewrites plain single-`echo` lines to the environment-file
form, preserving your quoting and everything else on the line — but only
when the step provably runs under a bash-compatible shell (explicit
`shell: bash`/`sh`, or the runner default on non-Windows runners,
resolved through `${{ matrix.KEY }}` like D016). Everything else is a
loud skip note instead of a guess: Windows/pwsh/cmd steps (`>>
"$GITHUB_OUTPUT"` means something else there), `printf`/piped/compound
lines, values using the `%0A`/`%0D`/`%25` command escapes (environment
files express those with heredocs), and expression-valued runners. The
fix is all-or-nothing per step and command: if one of three `::set-output`
lines can't be rewritten, none are — a half-fixed step would still warn.

## D019: DeprecatedActionRuntime

**Severity: warning.** An `action.yml` / `action.yaml` manifest declares
`runs.using: node12`, `node16`, or `node20`.

This is the one rule that lints the actions a repository *publishes*
rather than the workflows it runs. GitHub retires Node runtimes on its
runners on a schedule:

- **node12** — removed in 2023. **node16** — removed in 2024. Actions
  declaring either are force-migrated onto a newer Node at runtime: the
  manifest is a lie today, and the runtime actually used is on its own
  clock (see below).
- **node20** — deprecated September 2025; Node 24 became the default
  runtime on June 2, 2026, and GitHub has announced Node 20's removal
  from runners in fall 2026. An action still declaring `node20` stops
  working when that lands — for every repository that uses it.

The fix is to declare `runs.using: node24` **and verify the bundled
`dist/` code actually runs on Node 24** (native modules and long-frozen
bundles are the usual casualties). Because that verification is a real
test, not a text edit, `--fix` deliberately does not rewrite this one.

`gha-doctor` finds manifests at the conventional places: `action.yml` at
the repository root, in shallow subdirectories (monorepos like
`actions/cache` keep `restore/action.yml` and `save/action.yml`), and
anywhere under `.github/actions/`. Dependency and build trees
(`node_modules`, `vendor`, `dist`, …) are never scanned — the vendored
copies of other people's actions are not yours to fix. Composite-action
steps in these manifests also get the D015 (retired action versions) and
D018 (deprecated workflow commands) checks, driven by the same tables as
their workflow-file counterparts.

## parse: UnparseableWorkflow

Emitted (as a warning) when a workflow file isn't valid YAML. `gha-doctor`
won't guess at broken files; fix the syntax (actionlint gives precise
YAML errors) and re-run.
