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

_Snapshot: 2026-10-21 · gha-doctor 0.66.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **B** | 87/100 | workflow hygiene (−11.3): 4 warning(s), 2 info finding(s) across 4 file(s) |
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **B** | 84/100 | success rate (−10.4): 83% of 78 decisive runs succeeded (skipped/cancelled not counted) |
| [django/django](https://github.com/django/django) | **B** | 81/100 | workflow hygiene (−5.1): 6 warning(s), 11 info finding(s) across 17 file(s) |
| [python/cpython](https://github.com/python/cpython) | **C** | 73/100 | workflow hygiene (−9.8): 20 warning(s), 10 info finding(s) across 23 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **C** | 73/100 | workflow hygiene (−17.5): 5 warning(s), 1 info finding(s) across 3 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 71/100 | workflow hygiene (−17.8): 126 warning(s), 30 info finding(s) across 75 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 68/100 | workflow hygiene (−26.7): 32 warning(s), 0 info finding(s) across 12 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **D** | 68/100 | workflow hygiene (−22.3): 102 warning(s), 20 info finding(s) across 48 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **D** | 67/100 | success rate (−12.3): 80% of 76 decisive runs succeeded (skipped/cancelled not counted) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 66/100 | workflow hygiene (−17.5): 13 warning(s), 4 info finding(s) across 8 file(s) |
| [cli/cli](https://github.com/cli/cli) | **D** | 64/100 | workflow hygiene (−26.3): 33 warning(s), 26 info finding(s) across 15 file(s) |
| [facebook/react](https://github.com/facebook/react) | **D** | 64/100 | workflow hygiene (−30): 113 warning(s), 57 info finding(s) across 26 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **D** | 63/100 | workflow hygiene (−25.8): 57 warning(s), 9 info finding(s) across 23 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **D** | 61/100 | workflow hygiene (−30): 50 warning(s), 11 info finding(s) across 14 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 61/100 | workflow hygiene (−30): 47 warning(s), 18 info finding(s) across 17 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 61/100 | workflow hygiene (−21): 71 warning(s), 35 info finding(s) across 38 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 56/100 | workflow hygiene (−30): 51 warning(s), 38 info finding(s) across 19 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 55/100 | workflow hygiene (−30): 165 warning(s), 45 info finding(s) across 58 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 54/100 | workflow hygiene (−25.5): 54 warning(s), 19 info finding(s) across 23 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 44/100 | workflow hygiene (−30): 37 warning(s), 52 info finding(s) across 11 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 43/100 | workflow hygiene (−16.6): 61 warning(s), 74 info finding(s) across 48 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 42/100 | workflow hygiene (−28.8): 45 warning(s), 4 info finding(s) across 16 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 38/100 | workflow hygiene (−17.5): 23 warning(s), 6 info finding(s) across 14 file(s) |

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
  the most common were D002 ×781, D003 ×266, D010 ×221 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

