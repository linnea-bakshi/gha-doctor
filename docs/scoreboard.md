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

_Snapshot: 2026-11-11 · gha-doctor 0.66.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 90/100 | workflow hygiene (−5): 37 warning(s), 22 info finding(s) across 85 file(s) |
| [django/django](https://github.com/django/django) | **B** | 89/100 | workflow hygiene (−5.1): 6 warning(s), 13 info finding(s) across 18 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **B** | 88/100 | workflow hygiene (−7): 3 warning(s), 2 info finding(s) across 5 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **B** | 86/100 | workflow hygiene (−8.2): 54 warning(s), 22 info finding(s) across 73 file(s) |
| [python/cpython](https://github.com/python/cpython) | **B** | 82/100 | workflow hygiene (−9.5): 19 warning(s), 11 info finding(s) across 23 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 73/100 | workflow hygiene (−15.4): 110 warning(s), 29 info finding(s) across 76 file(s) |
| [cli/cli](https://github.com/cli/cli) | **C** | 72/100 | workflow hygiene (−22.5): 33 warning(s), 12 info finding(s) across 16 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 69/100 | workflow hygiene (−17.5): 13 warning(s), 4 info finding(s) across 8 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **D** | 68/100 | workflow hygiene (−17.5): 5 warning(s), 1 info finding(s) across 3 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 66/100 | workflow hygiene (−28.6): 41 warning(s), 19 info finding(s) across 16 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 66/100 | workflow hygiene (−22.1): 73 warning(s), 35 info finding(s) across 37 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 63/100 | workflow hygiene (−26.7): 32 warning(s), 0 info finding(s) across 12 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **D** | 60/100 | workflow hygiene (−25.8): 57 warning(s), 9 info finding(s) across 23 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 59/100 | workflow hygiene (−30): 61 warning(s), 11 info finding(s) across 15 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 58/100 | workflow hygiene (−30): 51 warning(s), 38 info finding(s) across 19 file(s) |
| [facebook/react](https://github.com/facebook/react) | **F** | 57/100 | workflow hygiene (−30): 114 warning(s), 57 info finding(s) across 26 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **F** | 56/100 | workflow hygiene (−22.1): 101 warning(s), 20 info finding(s) across 48 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 55/100 | workflow hygiene (−30): 90 warning(s), 22 info finding(s) across 26 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 51/100 | workflow hygiene (−30): 169 warning(s), 48 info finding(s) across 60 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 48/100 | workflow hygiene (−17.5): 23 warning(s), 6 info finding(s) across 14 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 46/100 | success rate (−16.4): 74% of 61 decisive runs succeeded (skipped/cancelled not counted) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 46/100 | workflow hygiene (−28.8): 45 warning(s), 4 info finding(s) across 16 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 27/100 | workflow hygiene (−30): 37 warning(s), 40 info finding(s) across 11 file(s) |

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
  the most common were D002 ×811, D003 ×266, D010 ×211 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

