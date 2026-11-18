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

_Snapshot: 2026-11-18 · gha-doctor 0.66.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 90/100 | workflow hygiene (−5): 37 warning(s), 22 info finding(s) across 85 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **B** | 82/100 | workflow hygiene (−8): 54 warning(s), 21 info finding(s) across 74 file(s) |
| [django/django](https://github.com/django/django) | **C** | 79/100 | success rate (−6.7): 89% of 56 decisive runs succeeded (skipped/cancelled not counted) |
| [python/cpython](https://github.com/python/cpython) | **C** | 78/100 | workflow hygiene (−9.5): 19 warning(s), 11 info finding(s) across 23 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 75/100 | workflow hygiene (−14.9): 108 warning(s), 28 info finding(s) across 77 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **C** | 72/100 | workflow hygiene (−17.5): 5 warning(s), 1 info finding(s) across 3 file(s) |
| [cli/cli](https://github.com/cli/cli) | **C** | 71/100 | workflow hygiene (−22.5): 33 warning(s), 12 info finding(s) across 16 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **D** | 68/100 | workflow hygiene (−22): 103 warning(s), 20 info finding(s) across 49 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 67/100 | workflow hygiene (−22.1): 73 warning(s), 35 info finding(s) across 37 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 66/100 | workflow hygiene (−26.7): 32 warning(s), 0 info finding(s) across 12 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 64/100 | workflow hygiene (−17.5): 13 warning(s), 4 info finding(s) across 8 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 63/100 | workflow hygiene (−28.6): 41 warning(s), 19 info finding(s) across 16 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **D** | 63/100 | success rate (−22.2): 64% of 45 decisive runs succeeded (skipped/cancelled not counted) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **D** | 61/100 | workflow hygiene (−30): 91 warning(s), 23 info finding(s) across 27 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **F** | 59/100 | workflow hygiene (−29): 64 warning(s), 11 info finding(s) across 23 file(s) |
| [facebook/react](https://github.com/facebook/react) | **F** | 58/100 | workflow hygiene (−30): 114 warning(s), 57 info finding(s) across 26 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 57/100 | workflow hygiene (−30): 51 warning(s), 38 info finding(s) across 19 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 56/100 | workflow hygiene (−16.1): 62 warning(s), 74 info finding(s) across 50 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 55/100 | workflow hygiene (−30): 171 warning(s), 48 info finding(s) across 60 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 50/100 | workflow hygiene (−30): 61 warning(s), 11 info finding(s) across 15 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 44/100 | workflow hygiene (−28.6): 45 warning(s), 3 info finding(s) across 16 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 41/100 | workflow hygiene (−30): 34 warning(s), 40 info finding(s) across 11 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 41/100 | workflow hygiene (−17.5): 23 warning(s), 6 info finding(s) across 14 file(s) |

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
  the most common were D002 ×815, D003 ×270, D010 ×214 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

