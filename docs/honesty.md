# How gha-doctor stays honest

A diagnostics tool is only useful if you can trust its numbers. Most of
gha-doctor's analysis is built on *samples* — recent runs, recent artifacts,
the largest caches — and samples can lie when they're too small, too short,
or too bursty. So the tool carries explicit **honesty gates**: rules about
when a number is allowed to be reported, extrapolated, or turned into a
dollar figure. When a gate fires, the output says so instead of printing a
confident guess.

This page lists every gate, so you know exactly what a gha-doctor number
means — and what it refuses to mean.

## Projections need a real window

Monthly projections are only made when the sampled runs span **at least 3
days**. A repo that did 300 runs in 12 minutes of a release stampede would
"project" to a six-figure monthly burn — that's noise, not signal.

- `--org` triage: repos with less than 3 days of run signal show a **lower
  bound** marked `+` (sample totals, no extrapolation). Repos with 3+ days
  get an extrapolated figure marked `*`. The legend explains both.
- **Top wins**: 30-day dollar projections only appear when the run window is
  3+ days; otherwise you get sample totals and the basis line says so.
- **Artifact storage**: the steady-state projection (upload rate × retention
  × price) is skipped with a "window too short to project" note when the
  ~300 most recent uploads span less than 3 days. (pytorch's last 300
  artifacts covered 12 minutes — this gate exists because that's real.)

## Small samples don't get graded

- The [health score](score.md) drops its run-history components (success
  rate, flakiness, waste, queue) entirely when **fewer than 10 runs** were
  sampled — 3 green runs are not an A+. The `basis` field always says which
  components the grade actually stands on.
- `--run` compares each step against the median of the last 8 successful
  runs of the same workflow — but with **fewer than 3 baseline runs** it
  prints a note instead of a fake baseline.
- The cache **hit-rate trend** (older half vs newer half of sampled jobs) is
  only reported when each half has **10+ restores** and the halves span
  **24+ hours** — otherwise "trend" would just be jitter.
- The badge sparkline needs **2+ recorded scores**; with fewer, the badge is
  unchanged rather than drawing a one-point "trend".
- **Matrix balance** only measures groups with **3+ shards** (two shards on
  different platforms are *expected* to differ — that's a matrix, not an
  imbalance) and **5+ clean runs** (every non-skipped shard succeeded; failed
  shards stop early and would fake imbalance). A group is only *reported* when
  the median slowest-shard wait is at least **1.5×** the even-split time *and*
  at least **1 real minute** — a 2× ratio on a 20-second job is noise. The
  "top wins" slot needs a **2+ minute** median wait.

## Only decisive runs count

Skipped, cancelled, and `action_required` runs are **excluded** from success
rates and duration percentiles. Counting them as failures made svelte look
like 19% success when its decisive rate was 50%; counting them as fast runs
made p50s absurd (0.1 minutes). The output reports how many runs were
decisive.

The same principle drives `--run` verdicts: a run that failed, was
cancelled, or skipped is **never praised for being fast** — stopping early
is not speed. Failed runs lead with the failing job and step instead.

## Dollar figures are floors, and say what they include

- Cost estimates use GitHub's actual billing semantics: per-job rounding up
  to whole minutes, $0.008/min Linux, 2× Windows, 10× macOS. Self-hosted
  runners are excluded (they don't bill minutes).
- Round-up overhead is reported separately, and only surfaces as a "top win"
  when it's **15%+ of spend**.
- Waste from failures and retries only becomes a dollar figure when there's
  **at least 1 wasted billable minute** in the sample.
- Top wins below **$0.25/month** aren't listed — a to-do list of pennies is
  noise.

## A zombie cron has to be both long-dead and provably dead

A scheduled workflow is only called out as a failing-on-repeat "zombie cron"
when its newest sampled scheduled runs are an unbroken failure streak of
**5+ consecutive failures** spanning **3+ days**:

- One broken nightly is a bad day, not a zombie — hence the streak floor.
- Five failures of an every-10-minutes cron is under an hour of breakage
  the owner may already be fixing — hence the span floor. The 3-day span
  doubles as the projection honesty gate, so the "$/month while it keeps
  failing" figure never extrapolates from less than 3 days of signal.
- A success ends the streak; skipped/cancelled runs neither break nor
  extend it; in-flight runs are ignored (their verdict isn't known).
  `timed_out` and `startup_failure` count as failures.
- When every sampled scheduled run of the workflow failed, the report says
  `≥ N` — the real streak may predate the sample.
- Its minutes are **already inside the waste bucket**; the zombie-cron
  entry never adds dollars to the top-wins total (that would double-count).
  The cadence for the monthly projection is `span / (failures − 1)`.

## Supersession has to be provable

A run only counts as superseded when a **different commit's** run of the
same workflow, from the **same head repo and branch**, was created before
the first run's last job finished:

- Scope is `pull_request`/`pull_request_target` events only. Auto-cancelling
  in-flight pushes to a release branch is often the wrong call, and D001
  deliberately doesn't recommend it — so pushes aren't priced here either.
- Grouping keys on head repo + branch, not branch alone: two forks both
  pushing a branch named `patch-1` must not fake a supersession.
- A same-SHA successor is a re-run, not a replacement.
- "Still running" means before the **last job completed**, not before the
  run record's `updated_at` — a replacement landing in the post-run
  bookkeeping gap superseded nothing.
- In-flight runs are left unclassified; their verdict isn't known yet.
- No double counting: failed superseded runs and superseded earlier
  attempts keep their minutes in the failures/retries bucket. The
  superseded figure is purely "runs that succeeded pointlessly", priced
  per job as `ceil(actual) − ceil(minutes-before-supersession)`.

## PR feedback time only counts full verdicts

The "push → last check finishes" percentiles need at least **5 qualifying
pushes**, and a push only qualifies when the wait it describes actually
happened, end to end:

- Every run of the push completed, and none concluded `cancelled`,
  `action_required`, or `stale` — a superseded push, or a fork PR waiting
  for a maintainer's approval, is not a wait anyone sat through. (This is
  why fork-heavy repos that gate CI on approval may not get the section at
  all: most of their "waits" are human approval time, not pipeline time.)
- Nothing was re-run later: a manual re-run three days after the push would
  fake a three-day wait, so any run or job with `run_attempt > 1`
  disqualifies the whole push.
- Skipped (path-filtered) runs neither disqualify nor extend the wait.
- A push is the runs created within **5 minutes** of the group's earliest
  run. PR events like `labeled` or `ready_for_review` re-trigger workflows
  on the same SHA hours later; counting those would fake an hours-long wait
  (seen live: a label sweep re-ran a check 15 hours after the push, on
  every open PR). Later-burst runs are ignored, not disqualifying — the
  push's own verdict already arrived with the first burst.
- The wait starts at the earliest run's creation — queue time is included,
  because the contributor waits through it too — and ends at the last job
  completion, not the run record's `updated_at` bookkeeping.
- The critical-path list is omitted entirely when only one workflow ran:
  "the critical path is your only workflow" is zero information. The
  top-wins slot additionally requires a median wait ≥ 15 min, one workflow
  gating ≥ 50% of pushes, and a median gap ≥ 2 min to the next-latest
  check — otherwise there is no single thing to speed up.

## Sampling is labeled

- The cache checkup inspects the **300 largest** caches (the API can't
  return everything on huge repos — nodejs/node has 137k entries), but the
  usage total comes from the exact `/actions/cache/usage` endpoint. Sampled
  sections say "sampled".
- Artifact analysis reads the **~300 most recent** uploads — newest-first,
  because an upload *rate* is what a projection needs.
- Cache hit rates come from **sampled job logs** (`--cache-logs N`),
  round-robin across jobs so one chatty matrix doesn't dominate.
- Flaky test names (`--flaky-logs N`) come **only from failed runs whose
  commit also passed** — the project's own history is the evidence the
  failure didn't reproduce. Extraction anchors on the test frameworks' own
  failure-summary formats ([25 framework families](flaky-frameworks.md):
  pytest, Python unittest, go test, cargo test, jest, vitest, cypress,
  playwright, mocha, ava, rspec, minitest, phpunit, exunit, maven
  surefire, gradle/JUnit, .NET xunit/VSTest, XCTest/xcodebuild,
  swift-testing, LLVM lit, meson test, GoogleTest, CTest, bazel, node-core test.py);
  anything else reports "no
  recognizable test failures" instead of guessing, because a compiler
  error named as a flaky test would be worse than no answer. The section
  always says how many logs were read out of how many exist.
- `--run` deep dives name a red run's failing tests with the **same
  extractors** — and the same restraint: a job whose log shows no
  recognized test-failure output gets its log tail and nothing more.
  Absence of names is a statement about the log's shape, not proof no
  test failed.
- When no failed job's log named any tests, `--run` falls back to **JUnit
  XML test reports uploaded as run artifacts**. Two honesty rules govern
  that source: artifacts are *run*-scoped, so names from them are reported
  in their own run-level section (with the source artifact named) and are
  never pinned to a specific failed job; and a report that records zero
  failures produces a note saying exactly that — the failing shard may
  simply not have uploaded its report, so "no failures recorded" is not
  "no test failed". Only direct `<failure>`/`<error>` entries count:
  surefire's `<flakyFailure>`/`<rerunFailure>` mark retries that passed,
  and `<skipped>` is not a failure.

## Diffs that survive line drift

`--baseline REF` reports only findings introduced since a git ref. The diff
is a **multiset over (rule, file basename, message)** — not line numbers —
so reformatting or an unrelated edit above a finding doesn't produce a
false "new issue". Fixed and hidden findings are counted and reported.

## Failure modes are loud, not silent

- No token? Endpoints that require auth (job logs, for example) produce an
  explicit note in the output — not a silent zero or a fake "all clear".
- Flag-parse errors and API failures exit **1**; findings exit **2**; clean
  exits **0**. CI gates can tell "broken" from "found something".
- `--fix` re-parses and re-lints every file before writing; if a fix would
  make things worse, it **refuses to write** and says why. Fixes are
  idempotent — a second pass changes nothing.
- The score's `basis` field, the `+`/`*` markers, and every "sampled" label
  exist so a number is never more confident than its data.
- If the per-job fetches fail partway through a history analysis — the
  classic case is the unauthenticated 60-requests/hour limit running out
  mid-sample — the report says **exactly how many runs lack job data**
  (`job_data_missing`/`job_data_note` in `--json`, a ⚠ line under the
  history header everywhere else), because queue, cost, waste and
  flakiness figures cover only the runs that have it. The health score's
  basis repeats the caveat: with job data missing, flakiness/waste
  deductions can only be too generous. If **no** sampled run has job
  data, that's an error (exit 1), not a report full of zeros.

## Absence claims need a real search

D017 ("nothing updates your action pins") only fires after actually
looking: the repo root and `.github/` are checked for every config
location renovate documents, plus dependabot's two. If the lookup fails
(rate limit, network), the check is **skipped** — a failed search is not
evidence of absence. An unparseable `dependabot.yml` gets the benefit of
the doubt: D017 is about missing automation, not YAML syntax. And any
renovate config counts as covered — its `github-actions` manager is on by
default, and gha-doctor won't accuse a config it didn't fully parse.

## Flakiness needs proof

A job is only called **flaky** when it both failed *and* passed on the same
commit SHA — flaky by construction, not by vibes. No heuristic guessing
from failure rates.

## The sample is provably current

The run sample is always taken from GitHub's *unfiltered* run listing, and
completed runs are selected client-side. That's deliberate: the API's
`status=` filtered listings are served from a separate index whose replicas
can lag by **weeks** — observed live (2026-07-31) on `apache/superset`,
where 7 of 8 identical `status=completed` requests returned a page whose
newest run was 38 days old, while unfiltered requests were fresh 8/8. A
stale window would silently shift every downstream number (fail rate, cost,
"last run" age) to a different era of the repo, which is worse than any
single wrong stat. Versions before v0.23.1 could be bitten by this on very
busy repos.

## Charts don't decorate thin data

The `--html` report's charts follow the same rules as the numbers. The
run-duration scatter only draws with **10+ decisive runs** (the same
`minRunsToGrade` bar the health score uses) — a trend through three dots is
decoration, not information. A workflow only gets a p50→p95 range bar with
**5+ decisive runs** of that workflow, because percentiles of two runs are
noise. Skipped/cancelled runs are excluded from both, exactly as they are
from success rates and percentiles, and the scatter says so in its caption
along with the sample size.

## A trend needs both runs and time

The duration-trend section compares the p50 of a workflow's **successful**
runs in the older half of the sample against the newer half — failures
stop early or get retried, so mixing them in would make any "trend" an
artifact of the failure mix. A workflow is only measured with **12+
successful runs spanning 24+ hours** (halves of a two-hour burst are
noise, not a trend), and a measured change is only reported past **both**
a 20% and a 1-minute p50 shift — percent alone would flag 0.1m → 0.2m,
minutes alone would flag 60m → 61.5m. The report states the window and the
per-half run counts on every line; measured-but-stable workflows are
counted out loud ("no significant change across N measured workflows")
instead of disappearing. The "investigate the slowdown" to-do slot holds a
higher bar still (30%+ slower), and carries no dollar figure: the extra
minutes are already inside the cost totals, and pricing them twice would
inflate the ledger.

## A scoped sample never wears whole-repo labels

`--workflow` restricts the run sample to one workflow, and everything that
would be wrong at that scope steps aside rather than quietly reporting a
different number. The health score is not computed (its hygiene and success
components grade the whole repo; a one-workflow score would be an unlabeled
different metric, and `--badge`/`--score-history` refuse the combination
outright). PR feedback time is skipped — it measures the wait until the
*last* check across all workflows, so a scoped sample would understate
every wait. Cache, artifact and storage figures have no per-workflow API
view; they stay repo-wide and the report header says so on the spot.

## An absent series is not a zero

The `--prom` export follows the same rule as `--json`'s omitted fields:
a section the run did not measure — no history sample, cache API
unavailable, too few qualifying PR pushes — emits **no series at all**,
so the dashboard shows a gap instead of a fabricated flat line at zero.
A measured zero (zero flaky jobs across a sampled window) IS emitted:
"we looked, and it's zero" is information. A success ratio or duration
percentile for a workflow with no decisive runs is undefined, so that
series is absent rather than 0 or 1. Every value is a gauge describing
the sampled window; `gha_doctor_sample_since_timestamp_seconds` states
how far back it reaches, and `gha_doctor_runs_missing_job_data` says
loudly when job-derived gauges understate. The shipped
[Grafana dashboard](grafana.md) keeps the same posture: panels for
unmeasured aggregates show "No data" rather than a zero-fill.

## Config is never silent

A `.gha-doctor.yml` changes what the doctor reports, so applying one is
always disclosed: a stderr note naming the file and every setting it
contributed, plus a `config` block in `--json`. Unknown keys and unknown
rule IDs in the file warn loudly instead of being skipped quietly — a typo
in `disable:` must never end up disabling nothing while the author believes
otherwise. `--no-config` shows what the report would say unconfigured, and
the [scoreboard](scoreboard.md)/state-of-actions numbers are collected with
it so no repo can grade itself.

---

If you catch a number that's more confident than its data, that's a bug —
[open an issue](https://github.com/linnea-bakshi/gha-doctor/issues).
