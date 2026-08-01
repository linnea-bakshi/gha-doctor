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

**Try it now, no install:** the [**browser playground**](playground/) lints a
pasted workflow — and applies the auto-fixes — entirely client-side via
WebAssembly. Nothing leaves your browser.

[**GitHub repo**](https://github.com/linnea-bakshi/gha-doctor) ·
[Rule reference](rules.md) ·
[Health score & badge](score.md) ·
[CI health scoreboard of famous repos](scoreboard.md) ·
[State of Actions hygiene in the top 250 repos](state-of-actions.md) ·
[How it stays honest](honesty.md)

## Install

```sh
gh extension install linnea-bakshi/gh-doctor       # any platform: run as `gh doctor`
brew install linnea-bakshi/tap/gha-doctor          # macOS / Linux
scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket
scoop install linnea-bakshi/gha-doctor             # Windows
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest
docker run --rm ghcr.io/linnea-bakshi/gha-doctor --repo cli/cli   # no install at all
```

Or grab a static binary — or a `.deb` / `.rpm` / `.apk` package — from the
[releases page](https://github.com/linnea-bakshi/gha-doctor/releases). The
Docker image is multi-arch and distroless — handy for running gha-doctor from
GitLab CI, Jenkins, or a scheduled job anywhere containers run.

## Sixty seconds of value

```sh
cd your-repo
gha-doctor                 # lint + run-history analysis (uses gh auth / GITHUB_TOKEN)
gha-doctor --diff          # preview the exact patch --fix would apply — nothing written
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
- **Zombie crons** — scheduled workflows failing on repeat with nobody
  watching: 5+ consecutive scheduled failures spanning 3+ days, with the
  monthly minutes and dollars they keep burning while they fail.
- **Superseded PR runs, priced** — runs a newer push replaced while they
  were still running: how many were cancelled in time vs. ran to completion
  anyway, and the billable minutes burned after the replacing push. The
  exact waste `concurrency` + `cancel-in-progress` prevents, quantified.
- **Wasted money** — missing `concurrency` cancellation, uncached installs,
  macOS runners at 10× price, per-job minute rounding, 6-hour default
  timeouts. Cost estimates use GitHub's actual billing rules.
- **Flaky tests by name** — reads the logs of failed runs whose commit
  also passed and names the failing tests (`--flaky-logs`): pytest, go,
  cargo, jest/vitest, playwright, mocha, ava, rspec, minitest, phpunit,
  exunit, maven surefire, gradle/JUnit, .NET xunit/VSTest.
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
- **Dead infrastructure** — action versions GitHub has shut down
  (`upload-artifact@v3`, `cache@v2` — they fail at runtime, every run),
  retired runner labels (`ubuntu-20.04`, `macos-13`, …), and the repo-level
  condition that causes both: no dependabot/renovate automation updating
  your action pins ([D017](rules.md#d017-noactionsupdateautomation)) —
  plus the deprecated `::set-output`/`::set-env` stdout commands still
  warning (or erroring) in millions of run steps
  ([D018](rules.md#d018-deprecatedworkflowcommand), auto-fixable).
- **Top wins** — the report closes with a ranked, dollar-quantified to-do
  list ("Cut failures and retries — ~$28/mo") so you know which fix to make
  first; projections only when the sample window makes them honest.
- **A 0–100 [health score](score.md)** with every deduction itemized, a
  `--badge` SVG (with score-trend sparkline) for your README, and a
  [scoreboard](scoreboard.md) showing how react, node, rust & friends grade.
- **Shareable HTML report** — `--html report.html` renders the whole checkup
  (findings, run history, top wins, score) as one self-contained page with
  inline-SVG charts: run durations over time and per-workflow p50→p95 bars
  ([live example](sample-report.html)); ship it as a CI artifact or publish
  it on Pages. Works with `--run` and `--org`.
- **GitHub Enterprise Server** — `GH_HOST=ghe.example.com` (gh CLI token
  conventions), or zero config inside GHES Actions jobs via the ambient
  `GITHUB_API_URL`.
- **Repo config file** — state standing policy once in `.gha-doctor.yml`
  (`disable`, `runs`, `cache-logs`, `log-tail`) instead of repeating flags
  everywhere; CLI flags win, `--no-config` opts out, and an applied config
  is always disclosed — typos warn loudly instead of silently disabling
  nothing.

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
