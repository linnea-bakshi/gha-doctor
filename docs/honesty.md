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

## Sampling is labeled

- The cache checkup inspects the **300 largest** caches (the API can't
  return everything on huge repos — nodejs/node has 137k entries), but the
  usage total comes from the exact `/actions/cache/usage` endpoint. Sampled
  sections say "sampled".
- Artifact analysis reads the **~300 most recent** uploads — newest-first,
  because an upload *rate* is what a projection needs.
- Cache hit rates come from **sampled job logs** (`--cache-logs N`),
  round-robin across jobs so one chatty matrix doesn't dominate.

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

## Flakiness needs proof

A job is only called **flaky** when it both failed *and* passed on the same
commit SHA — flaky by construction, not by vibes. No heuristic guessing
from failure rates.

---

If you catch a number that's more confident than its data, that's a bug —
[open an issue](https://github.com/linnea-bakshi/gha-doctor/issues).
