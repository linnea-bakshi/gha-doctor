# gha-doctor

**Diagnose your GitHub Actions: flaky jobs, wasted billable minutes, slow
steps, real cache hit rates, and fixable workflow anti-patterns — in one
command, with zero config.**

[actionlint](https://github.com/rhysd/actionlint) checks your workflows for
*correctness*. [zizmor](https://github.com/zizmorcore/zizmor) checks them for
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
[The CI waste ledger](waste-study.md) ·
[Flaky-test frameworks](flaky-frameworks.md) ·
[vs actionlint & zizmor](comparison.md) ·
[MCP server](mcp.md) ·
[How it stays honest](honesty.md) ·
[JSON schemas](schema.md) ·
[FAQ](faq.md)

## Install

```sh
gh extension install linnea-bakshi/gh-doctor       # any platform: run as `gh doctor`
brew install linnea-bakshi/tap/gha-doctor          # macOS / Linux
scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket
scoop install linnea-bakshi/gha-doctor             # Windows
aqua g -i linnea-bakshi/gha-doctor && aqua i       # aqua (standard registry)
asdf plugin add gha-doctor https://github.com/linnea-bakshi/asdf-gha-doctor.git
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
- **Duration trend** — is the build getting slower? Per-workflow p50 of
  successful runs, older vs newer half of the sample; sharp slowdowns get
  an "investigate" slot in the top wins.
- **Superseded PR runs, priced** — runs a newer push replaced while they
  were still running: how many were cancelled in time vs. ran to completion
  anyway, and the billable minutes burned after the replacing push. The
  exact waste `concurrency` + `cancel-in-progress` prevents, quantified.
- **PR feedback time** — median and p95 wait from a PR push to the last
  check finishing (queue time included), plus the critical-path workflow:
  the one that finishes last on most pushes and the median gap it adds
  after everything else. Only pushes whose full verdict arrived count.
- **Wasted money** — missing `concurrency` cancellation, uncached installs,
  macOS runners at 10× price, per-job minute rounding, 6-hour default
  timeouts. Cost estimates use GitHub's actual billing rules.
- **Flaky tests by name** — reads the logs of failed runs whose commit
  also passed and names the failing tests (`--flaky-logs`):
  [23 framework families](flaky-frameworks.md) — pytest, unittest,
  go, cargo, jest, vitest, playwright, mocha, ava, rspec, minitest,
  phpunit, exunit, maven surefire, gradle/JUnit, .NET xunit/VSTest,
  XCTest/xcodebuild, swift-testing, LLVM lit, meson, GoogleTest,
  CTest, bazel — even inside `docker build` output.
- **Real cache hit rates** — sampled straight from job logs
  (`--cache-logs`), split exact vs. prefix restores, plus stale-cache and
  10 GB-limit checkups.
- **Artifact storage checkup** — per-name producers, retention, and the
  steady-state GB (and $/month on private repos) your upload rate converges
  to under the default 90-day retention.
- **Single-run deep dives** (`--run <id|url|latest>`) — "why was this run
  slow?": a job waterfall (queue wait vs execution), every step compared to
  its own median in recent successful runs, failing step named first on red
  runs — with the failing step's log tail inlined and the failing tests
  named via the same 23 framework extractors as `--flaky-logs`
  (authenticated runs) — re-run attempts untangled.
- **Dead infrastructure** — action versions GitHub has shut down
  (`upload-artifact@v3`, `cache@v2` — they fail at runtime, every run),
  retired runner labels (`ubuntu-20.04`, `macos-13`, …), and the repo-level
  condition that causes both: no dependabot/renovate automation updating
  your action pins ([D017](rules.md#d017-noactionsupdateautomation)) —
  plus the deprecated `::set-output`/`::set-env` stdout commands still
  warning (or erroring) in millions of run steps
  ([D018](rules.md#d018-deprecatedworkflowcommand), auto-fixable).
  The actions you *publish* are checked too: `action.yml` manifests
  declaring the removed `node12`/`node16` runtimes — or `node20`, which
  GitHub removes from runners in fall 2026
  ([D019](rules.md#d019-deprecatedactionruntime)); composite-action steps
  get the retired-pin and deprecated-command checks as well. And *dying*
  infrastructure gets a countdown: runner labels with an announced
  retirement — `ubuntu-22.04` (gone April 2027, brownouts from September
  2026) and `macos-14` (gone November 2026) — are flagged while you can
  still migrate on your own schedule
  ([D020](rules.md#d020-deprecatingrunnerlabel), ubuntu auto-fixable).
  Scheduled workflows without a `github.repository` guard get flagged
  too — fork owners who enable Actions inherit your crons, secrets
  failures and bot spam included
  ([D021](rules.md#d021-unguardedcron)).
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
- **Prometheus / Grafana** — `--prom ci-health.prom` exports every measured
  aggregate (score, success ratios, p50/p95 durations, wasted compute in
  seconds and USD, flaky jobs, cache pressure, PR feedback time) in the
  Prometheus text format; run it on a schedule and CI health becomes a
  dashboard with real history. Unmeasured sections emit no series at all —
  a gap is the truth, a zero-fill would be a lie.
- **GitHub Enterprise Server** — `GH_HOST=ghe.example.com` (gh CLI token
  conventions), or zero config inside GHES Actions jobs via the ambient
  `GITHUB_API_URL`.
- **Single-workflow scope** — `--workflow ci.yml` (file, path, or display
  name) restricts the run sample and the static findings to one workflow:
  its flakes, its cost, its shard balance. Cache/artifact figures stay
  repo-wide (those APIs have no per-workflow view) and the report says so.
- **Repo config file** — state standing policy once in `.gha-doctor.yml`
  (`disable`, `runs`, `cache-logs`, `log-tail`, `fail-on`) instead of repeating flags
  everywhere; CLI flags win, `--no-config` opts out, and an applied config
  is always disclosed — typos warn loudly instead of silently disabling
  nothing.

- **[MCP server](mcp.md)** — `gha-doctor --mcp` exposes six read-only tools
  (analyze, lint, fix preview, run deep-dive, org triage, rule docs) to
  Claude Code, Cursor, and any other Model Context Protocol client, so an
  AI agent can diagnose your CI mid-conversation. It never writes:
  applying fixes stays an explicit `--fix` in your shell. Listed in the
  official MCP Registry as `io.github.linnea-bakshi/gha-doctor`.

## Use it as a GitHub Action

`gha-doctor --init` scaffolds the whole thing, ready to commit. Or by hand:

```yaml
- uses: linnea-bakshi/gha-doctor@v0
  with:
    baseline: auto      # gate PRs only on findings they introduce
    pr-comment: "true"  # sticky PR comment with the report
```

Findings also land as inline `::warning` annotations on the PR diff by
default — no code-scanning setup needed (`annotate: "false"` opts out).

See the [README](https://github.com/linnea-bakshi/gha-doctor#readme) for the
full flag reference, JSON/Markdown/SARIF output, suppression comments, and
CI gating patterns.
