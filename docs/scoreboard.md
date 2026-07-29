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

_Snapshot: 2026-07-29 · gha-doctor v0.11.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 90/100 | workflow hygiene (−6.9): 37 warning(s), 17 info finding(s) across 60 file(s) |
| [django/django](https://github.com/django/django) | **B** | 85/100 | workflow hygiene (−5.4): 7 warning(s), 9 info finding(s) across 17 file(s) |
| [python/cpython](https://github.com/python/cpython) | **B** | 83/100 | workflow hygiene (−8.1): 15 warning(s), 11 info finding(s) across 22 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **C** | 73/100 | workflow hygiene (−9.7): 42 warning(s), 14 info finding(s) across 47 file(s) |
| [cli/cli](https://github.com/cli/cli) | **C** | 71/100 | workflow hygiene (−24): 30 warning(s), 5 info finding(s) across 13 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **C** | 70/100 | workflow hygiene (−15.3): 13 warning(s), 3 info finding(s) across 9 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 65/100 | workflow hygiene (−25.4): 86 warning(s), 22 info finding(s) across 36 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 64/100 | workflow hygiene (−26.2): 34 warning(s), 0 info finding(s) across 13 file(s) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **D** | 63/100 | success rate (−25): 59% of 22 decisive runs succeeded (skipped/cancelled not counted) |
| [grafana/grafana](https://github.com/grafana/grafana) | **D** | 61/100 | workflow hygiene (−22.6): 131 warning(s), 18 info finding(s) across 60 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **D** | 60/100 | workflow hygiene (−24.4): 99 warning(s), 14 info finding(s) across 42 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 59/100 | workflow hygiene (−30): 55 warning(s), 11 info finding(s) across 17 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 58/100 | workflow hygiene (−30): 50 warning(s), 16 info finding(s) across 16 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **F** | 57/100 | workflow hygiene (−17.5): 7 warning(s), 0 info finding(s) across 4 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **F** | 55/100 | workflow hygiene (−27.1): 52 warning(s), 9 info finding(s) across 20 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **F** | 54/100 | workflow hygiene (−30): 34 warning(s), 15 info finding(s) across 12 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 48/100 | workflow hygiene (−21.3): 64 warning(s), 51 info finding(s) across 36 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 48/100 | workflow hygiene (−28.3): 42 warning(s), 2 info finding(s) across 15 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 47/100 | workflow hygiene (−30): 35 warning(s), 51 info finding(s) across 11 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 44/100 | workflow hygiene (−15.8): 19 warning(s), 0 info finding(s) across 12 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 42/100 | workflow hygiene (−30): 47 warning(s), 25 info finding(s) across 13 file(s) |
| [facebook/react](https://github.com/facebook/react) | **F** | 37/100 | workflow hygiene (−30): 104 warning(s), 44 info finding(s) across 22 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 37/100 | success rate (−20.8): 67% of 57 decisive runs succeeded (skipped/cancelled not counted) |

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

