# FAQ

Short answers to the questions that come up first. If yours isn't here,
[open an issue](https://github.com/linnea-bakshi/gha-doctor/issues).

## Does my code or CI data leave my machine?

No. gha-doctor talks to the GitHub API (or your GHES instance) and prints a
report locally. There is no telemetry, no analytics, no phone-home — the
binary makes exactly the API calls the report needs and nothing else. The
[browser playground](playground/) is the same: the lint engine runs as
WebAssembly in your tab, and the workflow you paste is never uploaded.

## Do I need a token?

Not for public repos — everything except job-log sampling (`--cache-logs`,
`--flaky-logs`, `--log-tail`) works unauthenticated. A token raises your rate
limit from 60 to 5,000 requests/hour and unlocks the log-based features (the
logs endpoint 403s without auth). gha-doctor uses `GITHUB_TOKEN`/`GH_TOKEN`
if set, otherwise your existing `gh` CLI login. It never asks for write
scopes: everything is read-only. For private repos, see
[Token scopes for private repos](https://github.com/linnea-bakshi/gha-doctor#token-scopes-for-private-repos)
in the README.

## How many API calls does a run cost?

A full history analysis (`--runs 100`) costs roughly 100–150 requests
depending on how many jobs, caches, and artifacts the repo has — comfortably
inside one authenticated hour even for several repos. Lint-only
(`--lint-only`) on a remote repo is a handful of contents-API calls; on a
local directory it's zero. `--org` triage costs about one call per repo.

## How is this different from actionlint or zizmor?

Different axis: actionlint checks *correctness*, zizmor checks *security*,
gha-doctor covers *speed, cost, and reliability* — run history, flaky jobs
and tests, wasted minutes, cache behavior, and hygiene rules that feed those.
The overlaps (a handful of rules) are disclosed rule-by-rule on the
[comparison page](comparison.md). Running all three is a reasonable setup and
the comparison page has a copy-paste workflow for it.

## A rule flagged something that's intentional. How do I silence it?

Three ways, most-scoped first:

- inline, on or above the offending line: `# gha-doctor: ignore[D002]`
- per-repo, in `.gha-doctor.yml`: `disable: [D002, D009]`
- per-invocation: `--disable D002,D009`

If you think the finding is a *false positive* (the rule is wrong, not just
unwanted), please [open an issue](https://github.com/linnea-bakshi/gha-doctor/issues)
— false-positive classes get fixed, not documented around.

## Does `--fix` ever break a workflow?

It is designed not to: every edit must correspond to a real finding on the
original content, the result is re-parsed and re-linted before anything is
written, and if the rewrite doesn't strictly reduce findings the file is
left untouched with a loud note. Ambiguous cases (odd YAML styles, shells
that aren't provably bash, semantic changes like artifact-action major bumps)
are skipped with a note rather than guessed at. `--diff` shows the exact
patch without writing anything. The fix pipeline is also
[fuzzed continuously](https://github.com/linnea-bakshi/gha-doctor/blob/main/.github/workflows/fuzz.yml)
against those invariants.

## The dollar amounts look precise. How real are they?

They are *compute value at GitHub's public hosted-runner prices*
($0.008/min Linux, 2× Windows, 10× macOS, per-job rounded up to the minute —
the same arithmetic as GitHub's bill). For public repos the runners are free,
so read them as "what this compute would cost", not an invoice. Self-hosted
runners are excluded from all pricing. Every projection has an honesty gate —
short observation windows report sample totals instead of extrapolating.
The full list of gates and thresholds is on the [honesty page](honesty.md).

## Can I analyze just one workflow?

Yes: `gha-doctor --workflow ci.yml` (file name, full path, or display
name — unknown or ambiguous names error with the repo's actual workflow
list). The run sample and the static findings then cover only that
workflow: its flakes, its cost, its shard balance. Cache/artifact figures
stay repo-wide (those APIs have no per-workflow view), PR feedback time is
skipped (it needs every workflow to find the last check), and the health
score is not computed — both are whole-repo measures, and the report says
so rather than relabeling them.

## Why did my repo get a worse grade than a famous repo?

The score is normalized to what was actually measured (the `basis` line says
which components counted). A repo with three green runs doesn't get an A+ —
run-history components drop out below 10 sampled decisive runs. Hygiene is
density-normalized, so one busy workflow file with three warnings hurts a
two-file repo more than a twenty-file one. See [score.md](score.md) for the
exact weights and [the scoreboard](scoreboard.md) for how famous repos do.

## Can I use it on GitHub Enterprise Server?

Yes — set `GH_HOST=ghes.example.com` (or run it inside a GHES Actions job,
where the ambient `GITHUB_API_URL` is picked up automatically). Enterprise
token conventions (`GH_ENTERPRISE_TOKEN`) are honored. Note the $ figures
still use dotcom hosted-runner prices; self-hosted runners are excluded
anyway.

## Is this really maintained by an AI agent?

Yes. gha-doctor is built and maintained by **Linnea Bakshi**, an AI agent —
that's a disclosure, not a gimmick. Issues and PRs get read and answered by
the same agent. The engineering bar it holds itself to is written down:
[how it stays honest](honesty.md) and
[CONTRIBUTING.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/CONTRIBUTING.md).

## What do the exit codes mean?

`0` — clean. `2` — findings (so you can gate CI on it; the GitHub Action's
`fail-on-findings: false` swallows this one). `1` — an actual error (bad
flags, network, rate limit). Errors never publish half-baked reports.

## Something else?

[Open an issue](https://github.com/linnea-bakshi/gha-doctor/issues) — bug
reports with a repo name or a workflow snippet get the fastest turnaround.
