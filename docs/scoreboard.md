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

_Snapshot: 2026-11-04 · gha-doctor 0.66.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 90/100 | cache pressure (−5): 54% of cache bytes are stale or pinned to PR refs |
| [apache/airflow](https://github.com/apache/airflow) | **B** | 85/100 | workflow hygiene (−8.4): 56 warning(s), 22 info finding(s) across 73 file(s) |
| [django/django](https://github.com/django/django) | **B** | 84/100 | cache pressure (−10): cache storage at 100% of the 10 GB limit (evictions likely); 81% of cache bytes are stale or pinned to PR refs |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **C** | 79/100 | workflow hygiene (−7): 3 warning(s), 2 info finding(s) across 5 file(s) |
| [grafana/grafana](https://github.com/grafana/grafana) | **C** | 73/100 | workflow hygiene (−16.5): 116 warning(s), 31 info finding(s) across 75 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **C** | 70/100 | workflow hygiene (−22.1): 101 warning(s), 20 info finding(s) across 48 file(s) |
| [python/cpython](https://github.com/python/cpython) | **C** | 70/100 | workflow hygiene (−9.5): 19 warning(s), 11 info finding(s) across 23 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **D** | 68/100 | workflow hygiene (−22.4): 74 warning(s), 35 info finding(s) across 37 file(s) |
| [pola-rs/polars](https://github.com/pola-rs/polars) | **D** | 66/100 | workflow hygiene (−25.8): 57 warning(s), 9 info finding(s) across 23 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **D** | 66/100 | workflow hygiene (−17.5): 5 warning(s), 1 info finding(s) across 3 file(s) |
| [vuejs/core](https://github.com/vuejs/core) | **D** | 66/100 | workflow hygiene (−17.5): 13 warning(s), 4 info finding(s) across 8 file(s) |
| [pandas-dev/pandas](https://github.com/pandas-dev/pandas) | **D** | 64/100 | workflow hygiene (−28.6): 41 warning(s), 19 info finding(s) across 16 file(s) |
| [angular/angular](https://github.com/angular/angular) | **D** | 62/100 | workflow hygiene (−26.7): 32 warning(s), 0 info finding(s) across 12 file(s) |
| [facebook/react](https://github.com/facebook/react) | **D** | 61/100 | workflow hygiene (−30): 113 warning(s), 57 info finding(s) across 26 file(s) |
| [cli/cli](https://github.com/cli/cli) | **D** | 60/100 | workflow hygiene (−21.9): 32 warning(s), 12 info finding(s) across 16 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 57/100 | workflow hygiene (−30): 52 warning(s), 38 info finding(s) across 19 file(s) |
| [huggingface/transformers](https://github.com/huggingface/transformers) | **F** | 54/100 | workflow hygiene (−29.8): 167 warning(s), 48 info finding(s) across 60 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 54/100 | workflow hygiene (−30): 88 warning(s), 19 info finding(s) across 24 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 53/100 | workflow hygiene (−30): 52 warning(s), 11 info finding(s) across 14 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 52/100 | workflow hygiene (−28.8): 45 warning(s), 4 info finding(s) across 16 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 39/100 | workflow hygiene (−17.5): 23 warning(s), 6 info finding(s) across 14 file(s) |
| [astral-sh/uv](https://github.com/astral-sh/uv) | **F** | 34/100 | success rate (−22.6): 64% of 83 decisive runs succeeded (skipped/cancelled not counted) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 34/100 | workflow hygiene (−30): 37 warning(s), 40 info finding(s) across 11 file(s) |

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
  the most common were D002 ×811, D003 ×259, D010 ×210 ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.

