# CI health scoreboard

How do the workflows of some of the most-starred repos on GitHub grade
under `gha-doctor`? A point-in-time snapshot, generated with
[`scripts/scoreboard.sh`](../scripts/scoreboard.sh) — every number is
reproducible with one command against public data:

```console
$ gha-doctor --repo facebook/react
```

_Snapshot: 2026-07-29 · gha-doctor v0.6.0 · last 100 completed runs per repo._

| Repo | Grade | Score | Biggest deduction |
|---|---|---|---|
| [pytorch/pytorch](https://github.com/pytorch/pytorch) | **A** | 93/100 | workflow hygiene (−6.9): 37 warning(s), 17 info finding(s) across 60 file(s) |
| [python/cpython](https://github.com/python/cpython) | **B** | 84/100 | workflow hygiene (−8.1): 15 warning(s), 11 info finding(s) across 22 file(s) |
| [rust-lang/rust](https://github.com/rust-lang/rust) | **D** | 68/100 | workflow hygiene (−17.5): 7 warning(s), 0 info finding(s) across 4 file(s) |
| [nodejs/node](https://github.com/nodejs/node) | **D** | 66/100 | workflow hygiene (−24.4): 99 warning(s), 14 info finding(s) across 42 file(s) |
| [apache/airflow](https://github.com/apache/airflow) | **D** | 63/100 | success rate (−18.1): 71% of 76 decisive runs succeeded (skipped/cancelled not counted) |
| [sveltejs/svelte](https://github.com/sveltejs/svelte) | **D** | 63/100 | success rate (−25): 50% of 38 decisive runs succeeded (skipped/cancelled not counted) |
| [cli/cli](https://github.com/cli/cli) | **D** | 62/100 | workflow hygiene (−24): 30 warning(s), 5 info finding(s) across 13 file(s) |
| [microsoft/typescript](https://github.com/microsoft/typescript) | **F** | 59/100 | workflow hygiene (−30): 55 warning(s), 11 info finding(s) across 17 file(s) |
| [microsoft/vscode](https://github.com/microsoft/vscode) | **F** | 56/100 | workflow hygiene (−30): 50 warning(s), 16 info finding(s) across 16 file(s) |
| [home-assistant/core](https://github.com/home-assistant/core) | **F** | 55/100 | workflow hygiene (−30): 47 warning(s), 25 info finding(s) across 13 file(s) |
| [prometheus/prometheus](https://github.com/prometheus/prometheus) | **F** | 50/100 | workflow hygiene (−28.3): 42 warning(s), 2 info finding(s) across 15 file(s) |
| [facebook/react](https://github.com/facebook/react) | **F** | 42/100 | workflow hygiene (−30): 104 warning(s), 44 info finding(s) across 22 file(s) |
| [vitejs/vite](https://github.com/vitejs/vite) | **F** | 40/100 | workflow hygiene (−15.8): 19 warning(s), 0 info finding(s) across 12 file(s) |
| [vercel/next.js](https://github.com/vercel/next.js) | **F** | 37/100 | workflow hygiene (−25.4): 86 warning(s), 22 info finding(s) across 36 file(s) |
| [denoland/deno](https://github.com/denoland/deno) | **F** | 36/100 | workflow hygiene (−30): 35 warning(s), 51 info finding(s) across 11 file(s) |

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
- Most hygiene warnings here are the boring, fixable kind: jobs without
  `timeout-minutes` (D002, the single most common finding — 422 across
  these repos), setup actions without dependency caching (D003), and
  artifacts kept 90 days by default (D010). `gha-doctor --fix` cleans up
  several of these automatically.

Want your repo's grade in your README? See the
[badge docs](score.md#badge).
