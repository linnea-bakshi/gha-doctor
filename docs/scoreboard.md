# CI health scoreboard

How do the workflows of some of the most-starred repos on GitHub grade
under [`gha-doctor`](https://github.com/linnea-bakshi/gha-doctor)? A
point-in-time snapshot, regenerated weekly by
[`scoreboard.yml`](https://github.com/linnea-bakshi/gha-doctor/blob/main/.github/workflows/scoreboard.yml)
running
[`scripts/scoreboard.sh`](https://github.com/linnea-bakshi/gha-doctor/blob/main/scripts/scoreboard.sh)
— every number is reproducible with one command against public data, no
clone needed:

```console
$ gha-doctor --repo facebook/react
```

_Snapshot: 2026-10-14 · gha-doctor 0.66.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [django/django](https://github.com/django/django) | **B** | 89/100 | cache pressure (−5): 99% of cache bytes are stale or pinned to PR refs |
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **B** | 85/100 | cache pressure (−5): 52% of cache bytes are stale or pinned to PR refs |
| [python/cpython](https://github.com/python/cpython) | **B** | 81/100 | workflow hygiene (−9.9): 20 warning(s), 11 info finding(s) across 23 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **C** | 72/100 | workflow hygiene (−17.5): 5 warning(s), 1 info finding(s) across 3 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 71/100 | workflow hygiene (−17.8): 126 warning(s), 30 info finding(s) across 75 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **C** | 71/100 | workflow hygiene (−22.3): 102 warning(s), 20 info finding(s) across 48 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **D** | 69/100 | workflow hygiene (−11.1): 63 warning(s), 36 info finding(s) across 65 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 69/100 | workflow hygiene (−21.1): 70 warning(s), 32 info finding(s) across 37 file(s) |
| [facebook/react](https://github.com/facebook/react) | **D** | 68/100 | workflow hygiene (−30): 113 warning(s), 57 info finding(s) across 26 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **D** | 64/100 | success rate (−22.1): 65% of 34 decisive runs succeeded (skipped/cancelled not counted) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 61/100 | workflow hygiene (−30): 46 warning(s), 18 info finding(s) across 16 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 61/100 | workflow hygiene (−17.5): 13 warning(s), 4 info finding(s) across 8 file(s) |
| [cli/cli](https://github.com/cli/cli) | **D** | 60/100 | workflow hygiene (−25.4): 30 warning(s), 22 info finding(s) across 14 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 59/100 | workflow hygiene (−16.6): 61 warning(s), 74 info finding(s) across 48 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **F** | 59/100 | workflow hygiene (−27.2): 50 warning(s), 7 info finding(s) across 19 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 58/100 | workflow hygiene (−29.9): 52 warning(s), 19 info finding(s) across 19 file(s) |
| [angular/angular](https://github.com/angular/angular) | **F** | 56/100 | workflow hygiene (−26.7): 32 warning(s), 0 info finding(s) across 12 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 56/100 | workflow hygiene (−30): 50 warning(s), 38 info finding(s) across 18 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 54/100 | workflow hygiene (−30): 44 warning(s), 4 info finding(s) across 15 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 53/100 | workflow hygiene (−30): 163 warning(s), 45 info finding(s) across 58 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 49/100 | workflow hygiene (−30): 50 warning(s), 11 info finding(s) across 14 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 45/100 | workflow hygiene (−17.5): 23 warning(s), 6 info finding(s) across 14 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 40/100 | workflow hygiene (−30): 37 warning(s), 54 info finding(s) across 11 file(s) |

**This is not a quality ranking of these projects.** It grades one narrow
thing: how their GitHub Actions setup scores on hygiene, reliability, and
efficiency signals, [formula here](score.md). A few honest caveats:

- **A snapshot, not a trend.** Success/flakiness/waste come from the last
  100 completed runs at generation time; a bad day moves the grade.
- **Skipped and cancelled runs are not failures.** Concurrency
  auto-cancels are good practice (rule D001 recommends them) and carry no
  verdict, so they're excluded from the success rate.
- **Hygiene is density-normalized** (per workflow file), so a
  40-workflow monorepo isn't penalized for sheer volume.
- Several famous repos are *absent* because their real CI isn't GitHub
  Actions: `golang/go` (LUCI), `kubernetes/kubernetes` (Prow),
  `ansible/ansible` (Azure Pipelines). Grading their incidental Actions
  runs would be misleading.
- Most findings here are the boring, fixable kind — across these repos
  the most common were D002 ×767, D003 ×262, D010 ×215 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

