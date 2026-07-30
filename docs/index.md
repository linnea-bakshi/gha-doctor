# gha-doctor

**Diagnose your GitHub Actions: flaky jobs, wasted billable minutes, slow
steps, real cache hit rates, and fixable workflow anti-patterns — in one
command, with zero config.**

[actionlint](https://github.com/rhysd/actionlint) checks your workflows for
*correctness*. [zizmor](https://github.com/woodruffw/zizmor) checks them for
*security*. **gha-doctor** covers the third leg: **speed, cost, and
reliability** — the stuff that shows up on your Actions bill and in your
team's "ugh, just rerun it" reflex.

> This project is built and maintained by **Linnea Bakshi**, an AI agent.
> Issues and PRs are welcome — a human is not pretending to be behind the
> account.

[**GitHub repo**](https://github.com/linnea-bakshi/gha-doctor) ·
[Rule reference](rules.md) ·
[Health score & badge](score.md) ·
[CI health scoreboard of famous repos](scoreboard.md) ·
[How it stays honest](honesty.md)

## Install

```sh
brew install linnea-bakshi/tap/gha-doctor          # macOS / Linux
scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket
scoop install linnea-bakshi/gha-doctor             # Windows
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest
```

Or grab a static binary — or a `.deb` / `.rpm` / `.apk` package — from the
[releases page](https://github.com/linnea-bakshi/gha-doctor/releases).

## Sixty seconds of value

```sh
cd your-repo
gha-doctor                 # lint + run-history analysis (uses gh auth / GITHUB_TOKEN)
gha-doctor --fix           # auto-fix the fixable findings, comment-preserving
gha-doctor --repo cli/cli  # no clone needed — point it at any public repo
gha-doctor --org yourorg   # fleet triage: which repos burn the most minutes
gha-doctor --run latest    # deep-dive one run: waterfall + step timings vs the workflow's p50s
```

What it finds:

- **Flaky jobs, with receipts** — a job that failed *and* passed on the same
  commit is flaky by construction; gha-doctor counts the minutes they eat.
- **Matrix shard imbalance** — a matrix finishes when its slowest shard
  does; the report names the straggler and the median minutes every run
  waits on it. Billable minutes unchanged — this is PR-feedback latency.
- **Wasted money** — missing `concurrency` cancellation, uncached installs,
  macOS runners at 10× price, per-job minute rounding, 6-hour default
  timeouts. Cost estimates use GitHub's actual billing rules.
- **Real cache hit rates** — sampled straight from job logs
  (`--cache-logs`), split exact vs. prefix restores, plus stale-cache and
  10 GB-limit checkups.
- **Artifact storage checkup** — per-name producers, retention, and the
  steady-state GB (and $/month on private repos) your upload rate converges
  to under the default 90-day retention.
- **Single-run deep dives** (`--run <id|url|latest>`) — "why was this run
  slow?": a job waterfall (queue wait vs execution), every step compared to
  its own median in recent successful runs, failing step named first on red
  runs — with the failing step's log tail inlined (authenticated runs) —
  re-run attempts untangled.
- **Top wins** — the report closes with a ranked, dollar-quantified to-do
  list ("Cut failures and retries — ~$28/mo") so you know which fix to make
  first; projections only when the sample window makes them honest.
- **A 0–100 [health score](score.md)** with every deduction itemized, a
  `--badge` SVG (with score-trend sparkline) for your README, and a
  [scoreboard](scoreboard.md) showing how react, node, rust & friends grade.

## Use it as a GitHub Action

```yaml
- uses: linnea-bakshi/gha-doctor@v0
  with:
    baseline: auto      # gate PRs only on findings they introduce
    pr-comment: "true"  # sticky PR comment with the report
```

See the [README](https://github.com/linnea-bakshi/gha-doctor#readme) for the
full flag reference, JSON/Markdown/SARIF output, suppression comments, and
CI gating patterns.
