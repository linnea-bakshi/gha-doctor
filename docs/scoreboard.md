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

_Snapshot: 2026-08-05 · gha-doctor 0.61.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 91/100 | workflow hygiene (−5.4): 40 warning(s), 22 info finding(s) across 85 file(s) |
| [python/cpython](https://github.com/python/cpython) | **B** | 84/100 | workflow hygiene (−10.3): 20 warning(s), 11 info finding(s) across 22 file(s) |
| [django/django](https://github.com/django/django) | **C** | 79/100 | workflow hygiene (−5.6): 7 warning(s), 10 info finding(s) across 17 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **C** | 73/100 | workflow hygiene (−11.3): 4 warning(s), 2 info finding(s) across 4 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **C** | 71/100 | workflow hygiene (−12.6): 66 warning(s), 33 info finding(s) across 59 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 71/100 | workflow hygiene (−18.4): 124 warning(s), 33 info finding(s) across 72 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **C** | 70/100 | workflow hygiene (−16.9): 14 warning(s), 5 info finding(s) across 9 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **D** | 67/100 | workflow hygiene (−18.8): 7 warning(s), 2 info finding(s) across 4 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **D** | 66/100 | workflow hygiene (−27.4): 52 warning(s), 11 info finding(s) across 20 file(s) |
| [cli/cli](https://github.com/cli/cli) | **D** | 65/100 | workflow hygiene (−30): 34 warning(s), 20 info finding(s) across 13 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **D** | 65/100 | workflow hygiene (−30): 51 warning(s), 18 info finding(s) across 18 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 63/100 | workflow hygiene (−25.4): 35 warning(s), 2 info finding(s) across 14 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **D** | 63/100 | workflow hygiene (−30): 55 warning(s), 12 info finding(s) across 17 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **D** | 63/100 | workflow hygiene (−23.2): 99 warning(s), 21 info finding(s) across 45 file(s) |
| [facebook/react](https://github.com/facebook/react) | **D** | 60/100 | workflow hygiene (−30): 104 warning(s), 55 info finding(s) across 22 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 59/100 | workflow hygiene (−28.7): 42 warning(s), 4 info finding(s) across 15 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **F** | 57/100 | workflow hygiene (−28): 35 warning(s), 17 info finding(s) across 14 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 52/100 | workflow hygiene (−30): 46 warning(s), 30 info finding(s) across 16 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 52/100 | workflow hygiene (−26.2): 139 warning(s), 42 info finding(s) across 57 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **F** | 52/100 | workflow hygiene (−22.3): 90 warning(s), 42 info finding(s) across 45 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 48/100 | workflow hygiene (−21.9): 68 warning(s), 52 info finding(s) across 37 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 37/100 | workflow hygiene (−16.9): 21 warning(s), 4 info finding(s) across 13 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 34/100 | workflow hygiene (−30): 38 warning(s), 54 info finding(s) across 11 file(s) |

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
  the most common were D002 ×733, D003 ×282, D010 ×187 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

