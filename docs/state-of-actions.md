# The state of GitHub Actions hygiene in top open-source repos

*Generated 2026-09-08 by [`gha-doctor 0.62.0`](https://github.com/linnea-bakshi/gha-doctor)
via `scripts/state-of-actions.sh` — static workflow lint of the
**250 most-starred repos on GitHub**, fetched through the
contents API (no clones). Numbers change as repos change; regenerate any
time. Runtime sequel: [the CI waste ledger](waste-study.md) — what these
repos' runs actually burned.*

## Headline numbers

| | |
|---|---|
| Repos swept | **250** |
| … with GitHub Actions workflows | **205** (82%) |
| … that lint completely clean | **0** of 205 (0%) |
| Workflow + action-manifest files linted | **2861** (median 7/repo) |
| Total findings | **8095** |
| Median findings per file | **3.0** |

## Findings by rule

![Share of the top-250 GitHub repos affected, by rule](img/state-of-actions.svg)

Sorted by how many repos are affected. "Repos" is out of the 205 repos
that have workflows.

| Rule | What it flags | Repos | % | Findings |
|------|---------------|------:|--:|---------:|
| [D002](rules.md#d002-nojobtimeout) | job without timeout-minutes (a hang bills the 6h default) | 198 | 97% | 3594 |
| [D001](rules.md#d001-missingconcurrencycancellation) | no concurrency cancel-in-progress (superseded PR runs keep running) | 165 | 80% | 594 |
| [D003](rules.md#d003-uncachedsetupaction) | setup-* without dependency caching | 134 | 65% | 1132 |
| [D017](rules.md#d017-noactionsupdateautomation) | no automation updating action pins (dependabot/renovate) | 116 | 57% | 116 |
| [D021](rules.md#d021-unguardedcron) | scheduled workflow with no github.repository guard (crons run in forks) | 104 | 51% | 267 |
| [D004](rules.md#d004-fullfetchdepth) | fetch-depth: 0 full-history clone where history is unused | 102 | 50% | 627 |
| [D014](rules.md#d014-topofhourcron) | cron pinned to minute 0 (GitHub peak-load delays/drops) | 96 | 47% | 292 |
| [D010](rules.md#d010-defaultartifactretention) | artifact upload on default 90-day retention | 87 | 42% | 712 |
| [D008](rules.md#d008-cachewithoutrestorekeys) | cache key without restore-keys prefix fallback | 49 | 24% | 189 |
| [D006](rules.md#d006-expensiverunneroneverypush) | macOS/Windows (2-10x cost) job on every push | 42 | 20% | 132 |
| [D013](rules.md#d013-pushandpullrequestdoublerun) | unscoped push + pull_request double-trigger | 38 | 19% | 71 |
| [D020](rules.md#d020-deprecatingrunnerlabel) | runner label with an announced retirement (ubuntu-22.04, macos-14) | 34 | 17% | 182 |
| [D009](rules.md#d009-continueonerrormasksfailures) | continue-on-error masking real failures | 25 | 12% | 64 |
| [D007](rules.md#d007-dockerbuildwithoutlayercache) | docker build without layer caching | 23 | 11% | 52 |
| [D012](rules.md#d012-npminstallinci) | npm install instead of npm ci in CI | 15 | 7% | 34 |
| [D011](rules.md#d011-largematrixonprs) | static matrix expanding to 20+ jobs per trigger | 11 | 5% | 19 |
| [D005](rules.md#d005-highfrequencycron) | cron firing more often than every 15 min | 4 | 2% | 5 |
| [D019](rules.md#d019-deprecatedactionruntime) | published action.yml on a deprecated Node runtime (node20 removal: fall 2026) | 3 | 1% | 7 |
| [D016](rules.md#d016-retiredrunnerlabel) | retired runner label | 2 | 1% | 4 |
| [D018](rules.md#d018-deprecatedworkflowcommand) | deprecated ::set-output/::save-state/::set-env workflow commands | 1 | 0% | 1 |
| [D015](rules.md#d015-retiredactionversion) | action version that has been shut down | 1 | 0% | 1 |

## Notable

- **No repo lints completely clean** under the current rule set. Closest: `yangshun/tech-interview-handbook` (1 finding), `ossu/computer-science` (2 findings), `ByteByteGoHq/system-design-101` (3 findings).
- **97% of repos have jobs with no `timeout-minutes`** (3594 jobs). A hung job bills the full 6-hour default before dying. Largest single repo: `langflow-ai/langflow` with 130.
- **3 repos still reference shut-down infrastructure** — artifact/cache action versions that GitHub turned off, or runner labels that no longer exist (D015/D016): `goldbergyoni/nodebestpractices`, `krahets/hello-algo`, `nvbn/thefuck`.
- **1 repos still emit deprecated workflow commands** (`::set-output`/`::save-state`, deprecated Oct 2022 with removal announced; D018, 1 findings): `excalidraw/excalidraw`.
- **3 repos publish in-repo actions on a deprecated Node runtime** (`runs.using: node20`; GitHub has announced Node 20's removal from runners in fall 2026 — these actions stop working then; D019, 7 manifests): `vercel/next.js`, `grafana/grafana`, `angular/angular`.
- **34 repos run jobs on runner images with an announced retirement date** (`ubuntu-22.04` brownouts start Sept 2026, removal Apr 2027; `macos-14` removal Nov 2026; D020, 182 jobs): `huggingface/transformers`, `microsoft/playwright`, `localsend/localsend`….
- **57% of repos have no automation updating their action pins** (no dependabot `github-actions` ecosystem, no renovate; D017) — pins rot until they hit shut-down versions like the D015/D016 cases above.
- **38 repos run every PR's CI twice** (unscoped `push` + `pull_request` on the same workflow, D013): `nvm-sh/nvm`, `redis/redis`, `python/cpython`.
- **80% of repos have workflows with no `concurrency` group** (D001), so pushing a fix to a PR doesn't cancel the now-obsolete run.
- Most findings in one repo: `langflow-ai/langflow` (246), `ggml-org/llama.cpp` (236), `huggingface/transformers` (205), `react/react` (170), `ruvnet/RuView` (161).

## Method & honesty

- **Static lint only.** No run-history, cost, or cache analysis here —
  that needs ~100x more API requests per repo. [The scoreboard](scoreboard.md)
  does the deep version for a smaller set.
- Workflows fetched via the contents API (60-file cap per repo; 6 repos hit the cap).
- **A finding is not a bug.** Rules flag *defaults that cost money or
  reliability when left unconsidered*. Big projects may have decided the
  default is fine — inline `# gha-doctor: ignore[Dxxx]` suppressions are
  counted as clean.
- Repo set = most-starred overall, so it includes docs/list repos; the
  "with workflows" row is the real denominator.
- Reproduce: `N=250 scripts/state-of-actions.sh > docs/state-of-actions.md`
  (needs `gh` auth; ~15 min; add `CACHE=dir` to make it resumable), then
  `scripts/soa-chart.py $CACHE > docs/img/state-of-actions.svg` for the chart.

*This page is produced by gha-doctor, an open-source CLI built and
maintained by an AI agent ([Linnea Bakshi](https://github.com/linnea-bakshi)).
Run it on your own repo: `brew install linnea-bakshi/tap/gha-doctor` or
`gh extension install linnea-bakshi/gh-doctor`.*

