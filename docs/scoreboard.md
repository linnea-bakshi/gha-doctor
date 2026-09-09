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

_Snapshot: 2026-09-09 · gha-doctor 0.65.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **B** | 87/100 | success rate (−6.3): 90% of 60 decisive runs succeeded (skipped/cancelled not counted) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **B** | 85/100 | workflow hygiene (−11.3): 4 warning(s), 2 info finding(s) across 4 file(s) |
| [django/django](https://github.com/django/django) | **B** | 84/100 | workflow hygiene (−5.6): 7 warning(s), 10 info finding(s) across 17 file(s) |
| [python/cpython](https://github.com/python/cpython) | **C** | 75/100 | workflow hygiene (−9.9): 20 warning(s), 11 info finding(s) across 23 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **C** | 74/100 | workflow hygiene (−13.3): 74 warning(s), 34 info finding(s) across 62 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 72/100 | workflow hygiene (−18): 127 warning(s), 31 info finding(s) across 75 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **C** | 70/100 | workflow hygiene (−14.2): 4 warning(s), 1 info finding(s) across 3 file(s) |
| [facebook/react](https://github.com/facebook/react) | **D** | 69/100 | workflow hygiene (−30): 113 warning(s), 57 info finding(s) across 26 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 68/100 | workflow hygiene (−16.9): 14 warning(s), 5 info finding(s) across 9 file(s) |
| [cli/cli](https://github.com/cli/cli) | **D** | 63/100 | workflow hygiene (−26.5): 29 warning(s), 22 info finding(s) across 13 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 61/100 | workflow hygiene (−29.5): 40 warning(s), 17 info finding(s) across 15 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **D** | 60/100 | workflow hygiene (−21.1): 28 warning(s), 6 info finding(s) across 14 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 57/100 | workflow hygiene (−29.3): 158 warning(s), 47 info finding(s) across 58 file(s) |
| [angular/angular](https://github.com/angular/angular) | **F** | 53/100 | workflow hygiene (−26.2): 34 warning(s), 0 info finding(s) across 13 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **F** | 53/100 | workflow hygiene (−22.6): 103 warning(s), 21 info finding(s) across 48 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 53/100 | workflow hygiene (−30): 44 warning(s), 4 info finding(s) across 15 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 52/100 | workflow hygiene (−30): 45 warning(s), 33 info finding(s) across 16 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **F** | 51/100 | workflow hygiene (−27.4): 52 warning(s), 11 info finding(s) across 20 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 50/100 | workflow hygiene (−18.9): 66 warning(s), 68 info finding(s) across 44 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 45/100 | workflow hygiene (−30): 41 warning(s), 8 info finding(s) across 11 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **F** | 45/100 | workflow hygiene (−20.9): 80 warning(s), 40 info finding(s) across 43 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 39/100 | workflow hygiene (−29.9): 49 warning(s), 19 info finding(s) across 18 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 25/100 | workflow hygiene (−30): 37 warning(s), 54 info finding(s) across 11 file(s) |

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
  the most common were D002 ×762, D003 ×261, D010 ×205 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

