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

_Snapshot: 2026-07-30 · gha-doctor 0.15.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **B** | 88/100 | workflow hygiene (−7): 37 warning(s), 21 info finding(s) across 60 file(s) |
| [django/django](https://github.com/django/django) | **B** | 86/100 | success rate (−6.9): 89% of 45 decisive runs succeeded (skipped/cancelled not counted) |
| [python/cpython](https://github.com/python/cpython) | **B** | 84/100 | workflow hygiene (−10.5): 20 warning(s), 12 info finding(s) across 22 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **C** | 76/100 | workflow hygiene (−10.1): 42 warning(s), 22 info finding(s) across 47 file(s) |
| [cli/cli](https://github.com/cli/cli) | **C** | 70/100 | workflow hygiene (−25.2): 30 warning(s), 11 info finding(s) across 13 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 69/100 | workflow hygiene (−26.3): 34 warning(s), 1 info finding(s) across 13 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 68/100 | workflow hygiene (−16.9): 14 warning(s), 5 info finding(s) across 9 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **D** | 63/100 | workflow hygiene (−22.9): 131 warning(s), 26 info finding(s) across 60 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **D** | 63/100 | success rate (−25): 59% of 22 decisive runs succeeded (skipped/cancelled not counted) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **D** | 62/100 | workflow hygiene (−18.8): 7 warning(s), 2 info finding(s) across 4 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 61/100 | workflow hygiene (−30): 35 warning(s), 17 info finding(s) across 12 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 59/100 | workflow hygiene (−21.4): 64 warning(s), 52 info finding(s) across 36 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 59/100 | workflow hygiene (−30): 55 warning(s), 14 info finding(s) across 17 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **F** | 59/100 | workflow hygiene (−24.8): 99 warning(s), 21 info finding(s) across 42 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **F** | 58/100 | workflow hygiene (−26.2): 86 warning(s), 33 info finding(s) across 36 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 56/100 | workflow hygiene (−19.3): 102 warning(s), 32 info finding(s) across 57 file(s) |
| [facebook/react](https://github.com/facebook/react) | **F** | 55/100 | workflow hygiene (−30): 104 warning(s), 48 info finding(s) across 22 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 55/100 | workflow hygiene (−28.3): 42 warning(s), 2 info finding(s) across 15 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 50/100 | workflow hygiene (−30): 35 warning(s), 53 info finding(s) across 11 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 49/100 | workflow hygiene (−30): 47 warning(s), 29 info finding(s) across 13 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 48/100 | workflow hygiene (−30): 51 warning(s), 16 info finding(s) across 16 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **F** | 45/100 | workflow hygiene (−27.3): 52 warning(s), 10 info finding(s) across 20 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 40/100 | workflow hygiene (−17.3): 20 warning(s), 3 info finding(s) across 12 file(s) |

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
  the most common were D002 ×731, D003 ×280, D010 ×182 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

