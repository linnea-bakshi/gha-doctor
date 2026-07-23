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

Warnings make `gha-doctor` exit with code 2 (so you can gate CI on them);
info findings don't affect the exit code.

## Suppressing findings

Every rule is a heuristic; your workflow may be the exception. Two ways to
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

## parse: UnparseableWorkflow

Emitted (as a warning) when a workflow file isn't valid YAML. `gha-doctor`
won't guess at broken files; fix the syntax (actionlint gives precise
YAML errors) and re-run.
