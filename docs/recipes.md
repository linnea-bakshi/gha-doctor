# Recipes

Copy-paste setups for the ways teams actually run gha-doctor: a PR gate, a
weekly report, a README badge, code scanning, Grafana, a whole-org fleet
view. Each recipe is a complete workflow — grab the one that matches your
situation and change the obvious placeholders (`OWNER/REPO`, org name).

**Every workflow on this page lints clean under gha-doctor itself.** A test
in the repo extracts each snippet and fails the build if any of our own
rules flag it — so the recipes can't quietly rot into bad examples.

Notes that apply to all of them:

- Scheduled workflows carry an `if: github.repository == 'OWNER/REPO'`
  guard so forks skip cleanly instead of failing on missing secrets
  ([D021](rules.md#d021-unguardedcron)). Keep it, with your slug.
- Cron minutes are deliberately off the hour ([D014](rules.md#d014-topofhourcron)).
- History analysis reads the Actions API: the job needs `actions: read`
  (plus `contents: read`) on its token. See the
  [FAQ](faq.md) for private-repo token scopes.

## The PR gate

One command scaffolds it:

```sh
gha-doctor --init
```

That writes `.github/workflows/gha-doctor.yml`:

```yaml
name: gha-doctor

on:
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: gha-doctor-${{ github.ref }}
  cancel-in-progress: true

jobs:
  doctor:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    permissions:
      contents: read
      pull-requests: write # sticky PR comment (remove with pr-comment below)
    steps:
      - uses: actions/checkout@v7
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          baseline: auto # gate only on findings this PR introduces
          pr-comment: "true"
          summary: "true"
```

`baseline: auto` is what makes this adoptable on a repo with existing
findings: the PR fails only on findings **it introduces**, never on debt
that was already there. Pay the debt down separately at your own pace —
`gha-doctor --diff` previews the auto-fixable part as a patch, `--fix`
applies it.

## The weekly health report

Static lint is only a third of what gha-doctor does. This runs the full
history analysis — flaky jobs *and the tests behind them*, wasted billable
minutes, cache hit rate, zombie crons, duration trends, $-quantified top
wins — every Monday, into the job summary, with a self-contained HTML
report attached as an artifact:

```yaml
name: CI health report

on:
  schedule: [{cron: "23 7 * * 1"}] # Mondays 07:23 UTC
  workflow_dispatch:

permissions:
  contents: read
  actions: read

jobs:
  report:
    if: github.repository == 'OWNER/REPO' # forks: skip cleanly
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          args: --runs 100 --flaky-logs 4 --cache-logs 10 --html report.html
          summary: "true"
          fail-on: never # report-only: a red report shouldn't be a red build
      - uses: actions/upload-artifact@v4
        with:
          name: ci-health-report
          path: report.html
          retention-days: 30
```

`fail-on: never` makes it report-only — a dashboard job should inform, not
gate (gating is the PR workflow's job, above).

## The README badge

A health-score badge that regenerates weekly and carries a sparkline of
recent scores (this is exactly how the badge on our own README works — see
[Health score & badge](score.md)):

```yaml
name: CI health badge

on:
  schedule: [{cron: "17 6 * * 1"}] # Mondays 06:17 UTC
  workflow_dispatch:

permissions:
  contents: write
  actions: read

jobs:
  badge:
    if: github.repository == 'OWNER/REPO' # forks: skip cleanly
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v7
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          args: --badge docs/img/health.svg --score-history docs/scores.jsonl
          fail-on-findings: "false"
      - name: Commit if changed
        run: |
          git config user.name github-actions
          git config user.email github-actions@github.com
          git add docs/img/health.svg docs/scores.jsonl
          git diff --cached --quiet || { git commit -m "chore: update CI health badge" && git push; }
```

Then embed it:

```md
![CI health](docs/img/health.svg)
```

## SARIF → GitHub code scanning

Findings as native code-scanning alerts on the Security tab (static
findings only — history analysis has no file to annotate):

```yaml
name: gha-doctor SARIF

on:
  schedule: [{cron: "31 6 * * 2"}] # Tuesdays 06:31 UTC
  workflow_dispatch:

permissions:
  contents: read
  security-events: write

jobs:
  sarif:
    if: github.repository == 'OWNER/REPO' # forks: skip cleanly
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v7
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          args: --sarif > results.sarif
          fail-on-findings: "false"
      - uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: results.sarif
```

If you just want inline PR annotations, you don't need any of this — the
action emits them by default (`annotate` input), zero setup.

## CI health in Grafana

`--prom` exports every measured aggregate in Prometheus text format. Run it
on a schedule and push to a Pushgateway (or write to a node_exporter
textfile-collector directory on your own runner):

```yaml
name: CI metrics

on:
  schedule: [{cron: "47 */6 * * *"}] # every 6h, off-peak minute
  workflow_dispatch:

permissions:
  contents: read
  actions: read

jobs:
  prom:
    if: github.repository == 'OWNER/REPO' # forks: skip cleanly
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v7
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          args: --prom gha-doctor.prom --runs 100
          fail-on: never
      - name: Push to Pushgateway
        run: curl -sf --data-binary @gha-doctor.prom "$PUSHGATEWAY_URL/metrics/job/gha-doctor/repo/$GITHUB_REPOSITORY"
        env:
          PUSHGATEWAY_URL: ${{ secrets.PUSHGATEWAY_URL }}
```

A ready-made 24-panel dashboard JSON and the full wiring guide (including
the honest-gaps semantics: unmeasured = absent series, not zero) live in
[the Grafana guide](grafana.md).

## The whole-org fleet view

One scan across an org (or user): per-repo success rate, queue, spend —
plus an HTML fleet report you can pass around:

```yaml
name: Fleet report

on:
  schedule: [{cron: "13 5 * * 1"}] # Mondays 05:13 UTC
  workflow_dispatch:

permissions:
  contents: read

jobs:
  fleet:
    if: github.repository == 'OWNER/REPO' # forks: skip cleanly
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          args: --org your-org --max-repos 30 --html fleet.html
          fail-on: never
      - uses: actions/upload-artifact@v4
        with:
          name: fleet-report
          path: fleet.html
          retention-days: 30
```

No checkout step needed — `--org` doesn't read the local repo. The default
workflow token sees the org's public repos; to include private ones, pass a
PAT with Actions read access via `github-token:`. `--svg` instead of
`--html` produces a fleet card you can embed in a profile README.

## Pre-commit hook

Catch workflow findings before they're even committed:

```yaml
repos:
  - repo: https://github.com/linnea-bakshi/gha-doctor
    rev: v0.49.0
    hooks:
      - id: gha-doctor        # lint on changed workflow files
      # - id: gha-doctor-fix  # or: auto-fix the fixable findings in place
```

Requires a Go toolchain (`language: golang`). Runs `--lint-only` — no
network, no token.

## More ways to run it

- **No install at all:** `docker run --rm ghcr.io/linnea-bakshi/gha-doctor
  --repo OWNER/REPO` — multi-arch, distroless; works from GitLab CI,
  Jenkins, or any scheduler that runs containers. Pass `-e GITHUB_TOKEN`
  for history analysis.
- **As a `gh` extension:** `gh extension install linnea-bakshi/gh-doctor`,
  then `gh doctor --repo OWNER/REPO`.
- **From an AI agent:** gha-doctor is an [MCP server](mcp.md) —
  `gha-doctor --mcp` exposes six read-only diagnose tools to Claude, VS
  Code, or any MCP client.
- **In the browser:** the [playground](playground/) lints a pasted workflow
  and previews fixes entirely client-side.

Per-repo defaults (disabled rules, sample size, log budgets) go in
[`.gha-doctor.yml`](faq.md) — with editor autocompletion via the published
[JSON schema](schema.md).
