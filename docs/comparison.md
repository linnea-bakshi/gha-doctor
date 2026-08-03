# gha-doctor vs actionlint vs zizmor

The GitHub Actions tooling space has three open-source linters/analyzers that
sound similar and get compared constantly. They are **not** competitors — they
cover three different failure modes of the same YAML:

| | [actionlint](https://github.com/rhysd/actionlint) | [zizmor](https://github.com/zizmorcore/zizmor) | [gha-doctor](https://github.com/linnea-bakshi/gha-doctor) |
|---|---|---|---|
| **Question it answers** | "Will this workflow *run correctly*?" | "Can this workflow *be exploited*?" | "Is this workflow *wasting time and money*?" |
| Focus | correctness | security | speed / cost / reliability |
| Language | Go | Rust | Go |

**Short version: run all three.** They overlap on a handful of checks (listed
honestly [below](#where-they-overlap)), but each one's core competency is
something the other two don't attempt. This page is maintained by the
gha-doctor project, so read it with that in mind — but every claim about the
other tools was checked against their current documentation, and we'd rather
be corrected than flattering (facts checked 2026-08; actionlint v1.7.x,
zizmor v1.2x).

## What each tool uniquely does

### actionlint: the correctness checker

actionlint parses your workflow files against the full workflow-syntax schema
and *type-checks* `${{ }}` expressions. It's the only tool of the three that
will tell you:

- an expression references an undefined context property, misspelled step
  output, or wrong-typed matrix value — before the run fails at 3 a.m.
- your `run:` script has a bug, via embedded **shellcheck** (and pyflakes for
  `shell: python`)
- your cron line, glob filter, webhook event name, or `workflow_dispatch`
  input definition is invalid
- a `needs:` graph has a cycle, or a `with:` input doesn't exist on a popular
  action

gha-doctor deliberately does **none** of this. If a workflow is syntactically
broken, actionlint is the right tool, full stop.

### zizmor: the security auditor

zizmor runs ~40 audits focused on exploitability: template injection,
`pull_request_target` and other dangerous triggers, credential persistence
(`artipacked`), cache poisoning in release workflows, excessive `GITHUB_TOKEN`
permissions, unpinned or impostor `uses:` refs, known-vulnerable action
versions, secrets over-exposure, and more. It audits workflows, action
manifests, Dependabot config, and pre-commit config, has severity/persona
levels, offline mode, and auto-fixes for several audits.

gha-doctor deliberately does **none** of this either. If you want to know
whether a fork PR can steal your tokens, use zizmor.

### gha-doctor: the speed/cost/reliability profiler

The other two tools read your YAML. gha-doctor's core is reading your **run
history and logs** via the GitHub API — things that cannot be determined
statically at all:

- **Flaky jobs with receipts** — same commit failed *and* passed — plus the
  **names of the flaky tests**, extracted from failure logs across
  [23 test-framework families](flaky-frameworks.md)
- **Wasted compute, in dollars** — failed/retried runs, the per-job
  round-up tax, superseded PR runs that ran to completion anyway
- **Real cache hit rates** measured from job logs, cache-storage pressure
  vs the 10 GB limit, artifact-storage growth projections
- **PR feedback time** (push → last check done) and which workflow gates it
- **Zombie crons** — scheduled workflows failing unbroken for weeks
- **Matrix shard imbalance**, queue times, per-workflow p50/p95 durations,
  and a single-run deep dive (`--run`) for "why was this run slow?"
- A 0–100 [health score / badge](score.md) condensing all of it

Its ~21 static rules exist to serve that goal (missing `concurrency`
cancellation, uncached setup steps, retired runners/actions, unscoped
double-triggers…), and 10 of them are auto-fixable with a
verify-before-write safety valve. Every measurement has an explicit
[honesty gate](honesty.md): if the sample is too small or too recent, it
says so instead of extrapolating.

## Where they overlap

Being honest about the seams (this is where "just pick one" goes wrong —
the overlaps are small and the emphasis differs):

| Check | actionlint | zizmor | gha-doctor |
|---|---|---|---|
| Missing `concurrency` cancellation | — | `concurrency-limits` (waste-as-attack-vector) | D001 (fixable) + **measures the $ actually lost** to superseded runs in your history |
| Deprecated workflow commands (`::set-output` etc.) | flags them | `insecure-commands` flags the *opt-in* (`ACTIONS_ALLOW_UNSECURE_COMMANDS`) | D018, with an **auto-fix** that rewrites to env-file writes when the shell is provably bash-compatible |
| Outdated/retired actions | flags popular actions on removed runners | `known-vulnerable-actions` (CVEs), `archived-uses` | D015 flags shut-down `upload-artifact@v1–3` / `cache@v1–2` (cache auto-fixed), D019 flags `node20` and older runtimes in *your own* action manifests |
| Retired runner labels | unknown labels error (list kept current) | — | D016, incl. resolution through `${{ matrix.* }}` |
| Cron checks | syntax, timezone, ≥5-min interval | — | D005 (too-frequent), D014 (minute-0 peak-load, auto-fix scatters it) — and detects crons that are *dead in practice* from history |
| Action manifests (`action.yml`) | syntax validation | audited | linted (D015/D018/D019 surface) |

If one of these fires in two tools on your repo, that's not redundancy —
it's two independent reasons to fix it.

## What "run all three" looks like

```yaml
jobs:
  lint-workflows:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v7
      - uses: docker://rhysd/actionlint:latest
      - uses: zizmorcore/zizmor-action@v0.6.2  # check for newer
      - uses: linnea-bakshi/gha-doctor@v0   # or `gha-doctor --init` for the PR-gate setup
```

All three are single static binaries, run offline-or-cheap, and exit nonzero
for CI gating. Total added CI time is seconds.

## Adjacent tools (different jobs entirely)

- [pinact](https://github.com/suzuki-shunsuke/pinact) / [ratchet](https://github.com/sethvargo/ratchet) — pin `uses:` refs to commit SHAs (a remediation
  tool for what zizmor's `unpinned-uses` flags)
- [yamllint](https://github.com/adrienverge/yamllint) — generic YAML style; actionlint explicitly punts style to it
- Flaky-test / CI-analytics SaaS (BuildPulse, Trunk, Datadog CI Visibility…)
  — the commercial neighborhood of gha-doctor's history analysis, with
  dashboards and cross-CI support; gha-doctor is the zero-config, local,
  open-source slice of that

## Corrections welcome

If anything above misrepresents actionlint or zizmor, please
[open an issue](https://github.com/linnea-bakshi/gha-doctor/issues) — this
page's value depends on being fair to the other two tools.
