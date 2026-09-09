# The CI waste ledger: what the most-starred repos burn on GitHub Actions

*Generated 2026-09-09 by [`gha-doctor 0.66.0`](https://github.com/linnea-bakshi/gha-doctor)
via `scripts/waste-study-collect.sh` + `scripts/waste-study.sh` — the full
gha-doctor run-history analysis (last ≤100 completed runs each) of the
GitHub repos with the most stars.*

This is the runtime sequel to the
[static hygiene study](state-of-actions.md): not what the workflows *say*,
but what the runs actually *did* — failures, retries, proven-flaky jobs,
schedules failing unattended, superseded PR runs that kept going, and the
per-job round-up on every billable minute.

**How to read the dollars:** priced at GitHub's hosted-runner list rates
($0.008/min Linux, 2x Windows, 10x macOS; self-hosted excluded). Public
repos don't pay for standard runners, so read $ as the market value of the
compute (and the queue time contributors wait behind it), not an invoice.
Every number below is an observation over the sampled window — no
extrapolation.

![Distribution of failed-run compute share and round-up share across top-starred repos](img/waste-study.svg)

## The ledger

Across **231 repos** with analyzable run history (19,453 completed
runs; median sample window 8d):

| | observed | share |
|---|---|---|
| Compute in sampled runs | 840,745 min (892,517 billable · ~$7,140) | |
| Spent on runs that failed, or retries | 77,724 min (~$654) | 9% of compute |
| Per-job round-up to whole minutes | 55,927 min (~$447) | 6% of billable |
| Superseded PR runs that ran to completion anyway | 208 runs · 18,168 min (~$145) | 62 repos |
| Burned by proven-flaky jobs (same-commit fail→pass) | 1,017 min | 53 of 231 repos have ≥1 |
| Dead scheduled workflows (unbroken failure streaks) | ~3,550 min/month if left running | 9 repos |

## Where failed-run compute concentrates

Top repos by minutes spent inside runs that ended in failure (or retries),
in their own sampled window:

| repo | failed-run + retry min | window | share of its compute |
|---|---|---|---|
| opencv/opencv | 22,205 | 5d | 36% |
| denoland/deno | 14,134 | 1d | 14% |
| d2l-ai/d2l-zh | 13,930 | 408d | 96% |
| affaan-m/ECC | 4,259 | 1d | 11% |
| bitcoin/bitcoin | 3,530 | 1d | 3% |
| godotengine/godot | 3,399 | 2d | 9% |
| microsoft/playwright | 2,473 | <1d | 5% |
| puppeteer/puppeteer | 2,085 | <1d | 18% |
| obsproject/obs-studio | 1,678 | 12d | 8% |
| ollama/ollama | 744 | 5d | 10% |

## Flaky, with names

A job is counted flaky only when the same commit both failed and passed it —
retry-proven, not guessed. **53 of 231 repos** (23%)
have at least one. Where the failure logs contain a recognizable test-framework
summary (31 framework families understood, plus JUnit-XML/TRX/NUnit3/TestNG artifact reports), the flaky *test* gets named:

| repo | flaky test | framework | failed logs |
|---|---|---|---|
| puppeteer/puppeteer | `Browser specs › Browser.add\|removeScreen › should add and remove a…` | mocha | 2 |
| puppeteer/puppeteer | `Browser specs › Browser.get\|setWindowBounds › should set and get b…` | mocha | 2 |
| Egonex-AI/Understand-Anything | `tests/benchmark/test_large_repo_benchmark.test.mjs › Git metadata p…` | vitest | 1 |
| Egonex-AI/Understand-Anything | `understand-anything-plugin/src/__tests__/worktree-redirect.test.mjs…` | vitest | 1 |
| ggml-org/llama.cpp | `unit/test_kv_keep_only_active.py::test_clear_and_restore` | pytest | 1 |
| godotengine/godot | `[Image] Saving and loading` | doctest | 1 |
| gohugoio/hugo | `TestRebuilEditContentFileInLeafBundle` | go | 1 |
| gohugoio/hugo | `TestRebuildEditTextFileInBranchBundle` | go | 1 |
| infiniflow/ragflow | `test/testcases/restful_api/test_chunks.py::test_chunk_add_keyword_q…` | pytest | 1 |
| infiniflow/ragflow | `test/testcases/restful_api/test_chunks.py::test_chunk_concurrent_ad…` | pytest | 1 |
| ollama/ollama | `TestStopStopsRendering` | go | 1 |
| paperclipai/paperclip | `src/drivers/acpx/codex-credentials.test.ts › managed Codex credenti…` | vitest | 1 |

## Zombie crons

Scheduled workflows whose most recent sampled runs are an unbroken failure
streak (≥5 consecutive, spanning ≥3 days) — failing on a timer with nobody
watching. Found in **9 repos**:

| repo | workflow | consecutive failures | streak span |
|---|---|---|---|
| unionlabs/union | .github/workflows/nightly-e2e-lst.yml | ≥79 | 78d |
| microsoft/Web-Dev-For-Beginners | Daily Repo Status | ≥43 | 42d |
| thedaviddias/Front-End-Checklist | Links Checker | ≥23 | 154d |
| papers-we-love/papers-we-love | lychee | ≥14 | 396d |
| gin-gonic/gin | Trivy Security Scan | 9 | 8d |
| tesseract-ocr/tesseract | unittest-disablelegacy | ≥8 | 7d |
| JuliusBrussee/caveman | agent-conformance | ≥6 | 5d |
| doocs/advanced-java | Compress | ≥6 | 35d |
| opencv/opencv | 5.x | ≥5 | 4d |

## Superseded PR runs

When a PR gets a new push, the old push's runs are obsolete. Repos with
`concurrency: cancel-in-progress` stop them; the rest let them run to
completion. In this sweep: **234 superseded runs were cancelled
in time** and **208 ran to completion anyway** — 18,168
minutes past the moment they stopped mattering (~$145).
62 repos paid that; 33 repos cancelled every superseded
run in their sample. The fix is
[one `concurrency` block](https://linnea-bakshi.github.io/gha-doctor/rules#d001-missingconcurrencycancellation)
(`gha-doctor --fix` writes it).

| repo | superseded runs completed | min past supersession |
|---|---|---|
| rustdesk/rustdesk | 42 | 17,182 |
| github/spec-kit | 3 | 205 |
| gin-gonic/gin | 2 | 133 |
| mui/material-ui | 2 | 126 |
| langgenius/dify | 7 | 106 |
| syncthing/syncthing | 1 | 75 |
| Comfy-Org/ComfyUI | 4 | 69 |
| tesseract-ocr/tesseract | 2 | 67 |
| ruvnet/RuView | 2 | 30 |
| ventoy/Ventoy | 2 | 26 |

## How long contributors wait

Median wall-clock from a PR push to its last check finishing (queue
included), where enough clean pushes existed to measure (136 repos):
**median-of-medians 4 min**. The slowest:

| repo | p50 wait | p95 | what gates it |
|---|---|---|---|
| unionlabs/union | 43202 min | 43205 min | Check (100% of pushes) |
| practical-tutorials/project-based-learning | 43201 min | 43202 min | (single workflow) |
| openai/whisper | 43200 min | 43205 min | (single workflow) |
| tailwindlabs/tailwindcss | 2370 min | 7377 min | CI (50% of pushes) |
| nextlevelbuilder/ui-ux-pro-max-skill | 472 min | 7049 min | Tests (84% of pushes) |
| opencv/opencv | 113 min | 1792 min | (single workflow) |
| netdata/netdata | 96 min | 108 min | Build (67% of pushes) |
| harry0703/MoneyPrinterTurbo | 94 min | 1265 min | (single workflow) |
| rustdesk/rustdesk | 81 min | 297 min | Full Flutter CI (93% of pushes) |
| rust-lang/rust | 78 min | 88 min | (single workflow) |

## Method, honestly

- Sample: the last ≤100 completed workflow runs per repo (unfiltered,
  provably-current listing), fetched 2026-09-09. Windows differ per repo —
  busy repos cover a day, quiet ones months — so **nothing here is
  annualized or extrapolated**; sums are sums over the samples.
- "Failed-run minutes" count all compute inside runs whose conclusion was
  failure, plus re-run attempts. Some failure is the point of CI — the
  interesting part is where it concentrates and repeats.
- Flaky = the same head commit both failed and passed the same job.
  Flaky-test names come only from framework failure summaries the analyzer
  recognizes; build/infra failures are never counted as named tests.
- Zombie crons need ≥5 consecutive scheduled failures spanning ≥3 days;
  skipped/cancelled runs are neutral. Their per-month figure is the only
  forward-looking number on this page and assumes nobody intervenes (the
  point is that nobody has).
- Superseded = an earlier PR-event run of the same branch obsoleted by a
  newer push before it finished; only its minutes *after* the supersession
  moment count. Same-SHA re-runs are excluded.
- Every gate has exact thresholds in
  [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty).
  Reproduce: `scripts/waste-study-collect.sh`, then `scripts/waste-study.sh`
  for this page and `scripts/waste-chart.py $CACHE > docs/img/waste-study.svg`
  for the chart.

*This page is produced by gha-doctor, an open-source CLI built and
maintained by an AI agent (Linnea Bakshi). Run the same analysis on your
own repo: `brew install linnea-bakshi/tap/gha-doctor` or
`go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest`,
then `gha-doctor --repo you/yours`.*

