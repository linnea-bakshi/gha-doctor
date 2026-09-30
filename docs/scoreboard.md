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

_Snapshot: 2026-09-30 · gha-doctor 0.66.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [django/django](https://github.com/django/django) | **A** | 94/100 | workflow hygiene (−5): 6 warning(s), 10 info finding(s) across 17 file(s) |
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 90/100 | workflow hygiene (−5.1): 38 warning(s), 22 info finding(s) across 85 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **B** | 88/100 | workflow hygiene (−11.3): 4 warning(s), 2 info finding(s) across 4 file(s) |
| [python/cpython](https://github.com/python/cpython) | **B** | 82/100 | workflow hygiene (−9.9): 20 warning(s), 11 info finding(s) across 23 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **C** | 77/100 | workflow hygiene (−11.3): 63 warning(s), 38 info finding(s) across 64 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 72/100 | workflow hygiene (−17.8): 126 warning(s), 30 info finding(s) across 75 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **C** | 72/100 | workflow hygiene (−22.6): 103 warning(s), 21 info finding(s) across 48 file(s) |
| [cli/cli](https://github.com/cli/cli) | **D** | 69/100 | workflow hygiene (−25.4): 30 warning(s), 22 info finding(s) across 14 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 68/100 | workflow hygiene (−17.5): 13 warning(s), 4 info finding(s) across 8 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 67/100 | workflow hygiene (−21.4): 71 warning(s), 33 info finding(s) across 37 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **D** | 66/100 | workflow hygiene (−27.8): 51 warning(s), 7 info finding(s) across 19 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **D** | 64/100 | success rate (−17.2): 72% of 87 decisive runs succeeded (skipped/cancelled not counted) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **D** | 62/100 | workflow hygiene (−30): 162 warning(s), 47 info finding(s) across 58 file(s) |
| [angular/angular](https://github.com/angular/angular) | **F** | 59/100 | workflow hygiene (−26.7): 32 warning(s), 0 info finding(s) across 12 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 54/100 | workflow hygiene (−30): 50 warning(s), 11 info finding(s) across 14 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 53/100 | workflow hygiene (−30): 46 warning(s), 33 info finding(s) across 16 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 51/100 | workflow hygiene (−30): 44 warning(s), 4 info finding(s) across 15 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 46/100 | workflow hygiene (−17.5): 23 warning(s), 6 info finding(s) across 14 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **F** | 44/100 | workflow hygiene (−30): 42 warning(s), 17 info finding(s) across 15 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 43/100 | workflow hygiene (−29.9): 52 warning(s), 19 info finding(s) across 19 file(s) |
| [facebook/react](https://github.com/facebook/react) | **F** | 38/100 | workflow hygiene (−30): 113 warning(s), 57 info finding(s) across 26 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 33/100 | workflow hygiene (−17.4): 65 warning(s), 74 info finding(s) across 48 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 29/100 | workflow hygiene (−30): 37 warning(s), 54 info finding(s) across 11 file(s) |

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
  the most common were D002 ×764, D003 ×260, D010 ×211 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

