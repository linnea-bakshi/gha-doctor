# The health score

`gha-doctor` condenses everything it measured into a 0–100 **CI health
score** with a letter grade. It appears at the end of the terminal report,
in the `score` object of `--json` output, at the end of `--md` output, and
`--badge health.svg` renders it as a shields-style SVG for your README.

The score is deliberately boring: fixed weights, linear deductions,
every point itemized. If your grade drops, the report tells you exactly
which component took the points and why.

## Formula

Each component has a maximum weight. Deductions are linear inside the
component and capped at its weight:

| Component | Weight | Full deduction when… |
|---|---|---|
| workflow hygiene | 30 | static findings: 4 pts per warning, 1 per info |
| success rate | 25 | 40% of sampled runs failed |
| flakiness | 15 | 3+ jobs failed *and* passed on the same commit (5 pts each) |
| wasted minutes | 15 | 30% of compute minutes went to failed runs or retries |
| cache | 10 | measured miss rate ≥ 50% (`--cache-logs`), else storage-pressure signals |
| queue time | 5 | runs average ≥ 120 s waiting for a runner |

Only components that could actually be measured count. The final score is
normalized to the available weights:

```
score = round(100 × (Σ max − Σ deducted) / Σ max)
```

So a `--lint-only` run is scored on hygiene alone, and a `--repo` run
against a repo you haven't cloned is scored on history alone. The `basis`
field says which ("static checks + run history", "static checks only",
"run history only").

For the cache component the measured hit rate (`--cache-logs N`) is
preferred when available; otherwise storage pressure is used: 5 pts when
usage is ≥ 90% of the 10 GB per-repo limit (evictions imminent), 5 pts
when more than half the cached bytes are stale (unused 7+ days) or pinned
to PR refs.

## Grades

| Grade | Points |
|---|---|
| A+ | 97–100 |
| A | 90–96 |
| B | 80–89 |
| C | 70–79 |
| D | 60–69 |
| F | < 60 |

## Badge

```console
$ gha-doctor --badge health.svg
badge written to health.svg (A, 96/100)
```

The SVG is self-contained (no external requests when rendered) and sized
like a shields.io badge, so it sits naturally next to your build badge:

```markdown
![CI health](health.svg)
```

### Keeping the badge fresh from CI

A small scheduled workflow can regenerate the badge and commit it when it
changes:

```yaml
name: CI health badge
on:
  schedule: [{cron: "0 6 * * 1"}]
  workflow_dispatch:
permissions:
  contents: write
  actions: read
jobs:
  badge:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          args: --badge docs/ci-health.svg
          fail-on-findings: false
      - name: Commit if changed
        run: |
          git config user.name github-actions
          git config user.email github-actions@github.com
          git add docs/ci-health.svg
          git diff --cached --quiet || git commit -m "chore: update CI health badge" && git push
```

## Interpreting a bad grade

The score is a snapshot of the sampled window (default: last 100 runs).
A repo mid-incident will grade badly and recover on its own; a repo that
grades F for weeks has a real problem. Run with `--json` and diff the
`score.components` over time if you want to track it properly.
