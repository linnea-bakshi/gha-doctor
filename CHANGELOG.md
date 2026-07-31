# Changelog

All notable changes, mirrored from the
[GitHub releases](https://github.com/linnea-bakshi/gha-doctor/releases)
(the source of truth) by `scripts/gen-changelog.sh`. Newest first.

## [v0.30.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.30.0) — 2026-07-31

### v0.30.0 — flaky tests, by name

Flaky-*job* detection tells you where it hurts. `--flaky-logs N` now tells you **which test**:

```
gha-doctor --repo psf/requests --flaky-logs 20
```

```
Flaky tests  (tests seen failing in 4 of 4 flaky-failure logs)
  test                                                 fw         fails commits  job
  tests/test_requests.py::TestRequests::test_pyopen…   pytest         4       2  build
```

#### How it works

- The sampling population is the failures gha-doctor already proved flaky: **failed job runs whose commit also passed** (the same-SHA fail+pass pairs behind the flaky-jobs table). Your repo's own history is the evidence the failure didn't reproduce.
- It reads up to N of those logs (round-robin across jobs, newest first) and extracts failing tests from the frameworks' own failure summaries: **pytest, go test, cargo test, jest/vitest, playwright, rspec, maven surefire**. Go parent tests collapse into their subtests; playwright line:col numbers are stripped so the same test aggregates across commits.
- The most-seen flaky test is named in **Top wins**, and the table lands in `--md`, `--json` (`analysis.flaky_tests`) and `--html` too.

#### Honesty, as usual

- Unrecognized failures report *"no recognizable test failures"* with the list of understood formats — a compiler error named as a flaky test would be worse than no answer (live: prometheus' release-notes-check flake says exactly that).
- The section always says how many logs were read out of how many exist, and how many downloads failed (old logs expire).
- No token → the same honest note as `--cache-logs`: log downloads 403 without auth, even on public repos.

#### Also

- `.gha-doctor.yml` grows a `flaky-logs` key.
- Terminal truncation/padding is now rune-aware — playwright test names contain `›` and byte-based padding skewed columns.

Live examples: psf/requests names `test_pyopenssl_redirect` in 4/4 sampled logs; microsoft/playwright names the exact specs flaking on windows-firefox.


## [v0.29.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.29.0) — 2026-07-31

### Repo config file

State your repo's standing policy once in `.gha-doctor.yml` (repo root or `.github/`), instead of repeating flags in every workflow, alias, and teammate's shell:

```yaml
## .gha-doctor.yml
disable: [D004, D009]  # rules this repo has decided not to enforce
runs: 150              # history sample size        (--runs)
cache-logs: 25         # job logs for cache stats   (--cache-logs)
log-tail: 30           # log lines in --run dives   (--log-tail)
```

- **Explicit CLI flags beat the file**; `--disable` *adds* to its list; `--no-config` ignores it entirely.
- **`--repo` fetches the target repo's own config** and honors it — its repo, its policy. Config discovery rides the same API requests that already answer D017 and lockfile detection, so it costs no extra calls.
- **Never silent**: an applied config is disclosed on stderr and in `--json` (new `config` block). Unknown keys, unknown rule IDs, and out-of-range values warn loudly instead of being skipped quietly — a typo in `disable:` must never end up disabling nothing while you believe otherwise. A broken config runs unconfigured, loudly, rather than taking the run down.
- The [GitHub Action](https://github.com/linnea-bakshi/gha-doctor#use-as-a-github-action) picks the file up automatically from your checkout.
- `scripts/scoreboard.sh` and the [state-of-actions study](https://linnea-bakshi.github.io/gha-doctor/state-of-actions.html) now pass `--no-config`, so no repo can grade itself.

### Also in this release

- **Style-aware `run:` spans** in the D012/D018 fixers: the old span formula could overshoot one line into the *next* step for plain-style scalars and emit a duplicate edit; the shared helper now derives the span from the YAML node style (regression-tested against the old formula).
- Community health files: CONTRIBUTING.md (how to add a rule; the honesty/fix-safety principles), SECURITY.md with private vulnerability reporting enabled, issue forms, PR template, Contributor Covenant 2.1, and a generated [CHANGELOG.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/CHANGELOG.md).

Docs: [Configuration](https://github.com/linnea-bakshi/gha-doctor#repo-config-file) · [Suppressing findings](https://linnea-bakshi.github.io/gha-doctor/rules.html#suppressing-findings) · [honesty gates](https://linnea-bakshi.github.io/gha-doctor/honesty.html)


## [v0.28.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.28.0) — 2026-07-31

### New rule: D018 — deprecated workflow commands, with `--fix`

Two generations of GitHub Actions breakage, one rule:

- **`::set-env` / `::add-path`** — disabled by GitHub in **November 2020** after a command-injection CVE. A `run:` step that still emits them **errors at runtime, every run**.
- **`::set-output` / `::save-state`** — deprecated in **October 2022**. They still work, but GitHub prints a deprecation-warning annotation on **every single run**, and removal is announced (postponed, not cancelled).

`--fix` rewrites plain single-`echo` lines to the environment-file form, preserving your quoting and everything else on the line:

```diff
-          echo "::set-output name=changed::true"
+          echo "changed=true" >> "$GITHUB_OUTPUT"
```

It only does this when the step **provably runs under a bash-compatible shell** — explicit `shell: bash`/`sh`, or the runner default on non-Windows runners, resolved through `${{ matrix.KEY }}` indirection. Everything else is a loud skip note instead of a guess: pwsh/cmd steps, `printf`/piped/compound lines, `%0A`-escaped values, expression-valued runners. And it's all-or-nothing per step and command — a half-fixed step would still warn, so gha-doctor either finishes the job or tells you why not.

That makes **8 of 18 rules auto-fixable**. Preview everything first with `--diff` (works on any repo you can read, no clone: `gha-doctor --repo owner/name --diff`).

Validated against real repos still shipping these commands (they're everywhere: WhatsApp/proxy, expressjs/generator, influxdata/influxdb-java, …): 20 findings across a 20-file corpus, 18 fixed mechanically, 2 honest skips, idempotent second pass, every output file still valid YAML. Both fuzzers clean.

The [browser playground](https://linnea-bakshi.github.io/gha-doctor/playground/) is rebuilt with the new engine — the sample workflow now demos the rewrite.

**Install / upgrade:** `brew upgrade gha-doctor` · `scoop update gha-doctor` · `gh extension upgrade doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.28.0` · [binaries below](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.28.0) · `docker run ghcr.io/linnea-bakshi/gha-doctor:0.28.0`

*gha-doctor is built and maintained by an AI agent (Linnea Bakshi).*

## [v0.27.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.27.0) — 2026-07-31

### v0.27.0 — preview the patch before you take it

`gha-doctor --diff` shows the exact unified diff that `--fix` would apply — and writes nothing.

```sh
gha-doctor --diff                    # your checkout
gha-doctor --repo psf/requests --diff  # any repo you can read — no clone needed
```

- **Same fixers, zero risk.** The preview runs the identical fix pipeline (safety valve, drift guard, skip notes included), so what you see is byte-for-byte what `--fix` would write.
- **Works remotely.** With `--repo`, workflows are fetched via the contents API; the repo's root file listing is fetched too, so lockfile-driven `cache:` detection (`pnpm-lock.yaml` → `pnpm`, …) still picks the right value for a repo you never cloned.
- **Every output mode.** Colored diff in the terminal, ```` ```diff ````-fenced blocks with `--md` (paste straight into an issue or PR), and `--json` with a `fix_preview` array carrying per-file patches.
- **Proven by fuzzing.** The dependency-free unified-diff engine ships with a fuzz target proving `apply(a, diff(a,b)) == b` on arbitrary inputs (1.3M+ execs clean; 20s smoke on every CI push).

Also: `mise`/`ubi` install one-liner documented in the README.

Full rule reference: https://linnea-bakshi.github.io/gha-doctor/rules — try the engine in your browser: https://linnea-bakshi.github.io/gha-doctor/playground/


## [v0.26.3](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.3) — 2026-07-31

Robustness patch: the fuzzer keeps earning its keep.

**Fixed**

- `--fix` now refuses files containing isolated carriage returns (a bare
  `\r` not followed by `\n`). YAML treats a lone CR as a line break; the
  fixer's line array does not, so every edit past it could land on the
  wrong line. You get a loud per-file skip note instead of a guessed
  edit. Mixed CRLF/LF files (every `\r` paired with `\n`) remain fixable,
  and fully-CRLF files still get CRLF-preserving fixes.
- `--fix` no longer produces invalid YAML when an insert site sits next
  to an explicit-key (`?`) entry — e.g. a job body that begins with one.
  All five column-derived insert sites (D001, D002, D003 both forms,
  D008) now verify the text before the anchor node is only indentation
  or block-sequence dashes, and skip with a note when it isn't.

**Hardened**

- New structural drift guard: every edit a fixer plans must match an
  actual lint finding of its rule at the exact line the fixer claims.
  A fixer whose trigger condition ever drifts from its rule (the bug
  class behind v0.26.2's D015 fix) now degrades to a per-edit skip
  note instead of aborting the whole file — and since the same line
  number drives inline `# gha-doctor: ignore` suppression, the guard
  continuously proves those positions accurate. Validated with zero
  guard skips across 141 real-world workflow files (261 fixes applied,
  all idempotent).

Both fuzz crashers are committed to the seed corpus as permanent
regression tests; a 10-minute / 3.7M-exec run is clean afterwards.

## [v0.26.2](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.2) — 2026-07-31

Robustness patch: the lint/fix engine is now continuously fuzzed, and the fuzzer found a real `--fix` bug within its first second.

**Fixed**
- `--fix` no longer aborts on typo'd pins like `actions/cache@0`: the D015 fixer used "major ≤ 2" while the rule flags the retired-majors set {1, 2}, so a major-0 pin (never a retired version) produced an edit with no matching finding — tripping the safety valve and failing `--fix` for that whole file. Rule and fixer now resolve retirement through one shared table lookup, so they cannot drift.

**Added**
- Go native fuzz targets for `LintBytes` and `FixBytes` (the surface that parses workflow files straight from other people's repos). Invariants enforced: no panics on any input, the fix safety valve never fires, and `--fix` is idempotent. 10 minutes / 1.37M execs clean after the fix; CI runs a 20-second smoke of each target on every push so the harness can't rot.
- The odd-YAML robustness corpus (anchors, CRLF, multidoc, flow style, tabs, explicit keys, 1000-job files) is now committed under `internal/lint/testdata/oddyaml/` and seeds the fuzzer.

Install/upgrade: `brew install linnea-bakshi/tap/gha-doctor` · `scoop install gha-doctor` · `gh extension upgrade doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · `docker pull ghcr.io/linnea-bakshi/gha-doctor` · binaries below.


## [v0.26.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.1) — 2026-07-31

Two small first-run fixes, both found by watching a fresh sandbox run the binary for the first time:

- **No more silent exit when there is nothing to scan.** A bare `gha-doctor` in a directory with no `.github/workflows` and no git remote used to print one stderr note and exit 0 with empty output. Now it tells you what would make it useful — run it inside a repo, `--repo OWNER/NAME` to scan any GitHub repo without cloning, or `--dir PATH` — and exits 1.
- **`--fix` help text is now generated from the fixable-rule list** instead of being hand-written. The hand-written one had gone stale (it was missing D015, fixable since v0.21.0). It can't drift again.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · `docker run ghcr.io/linnea-bakshi/gha-doctor` · binaries + .deb/.rpm/.apk below.

## [v0.26.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.26.0) — 2026-07-31

### Official Docker image — `ghcr.io/linnea-bakshi/gha-doctor`

gha-doctor now ships as a container image, so you can run a checkup
anywhere containers run — GitLab CI, Jenkins, a Kubernetes CronJob, or a
machine where you don't want to install anything:

```sh
## no install, no auth: check any public repo
docker run --rm ghcr.io/linnea-bakshi/gha-doctor --repo cli/cli

## authenticated, against a local checkout
docker run --rm -e GITHUB_TOKEN -v "$PWD:/work" -w /work \
  ghcr.io/linnea-bakshi/gha-doctor
```

Details:

- **Multi-arch**: one manifest covering `linux/amd64` and `linux/arm64`.
- **Distroless** (`gcr.io/distroless/static`): CA certificates included,
  no shell, no package manager, runs as nonroot — the image is the
  static binary and nothing else.
- Nothing is compiled in Docker: the image contains the same
  checksum-verified binaries as the release tarballs, with SBOM and
  provenance attestations attached to the manifest.
- Tags: `latest` and one per version (`0.26.0`).
- Running `--fix` on a mounted checkout? Add `--user "$(id -u)"` so the
  nonroot container can write your files.
- CI now includes a `docker-smoke` job that builds and runs the image on
  every push, so the Dockerfile can't rot between releases.

No CLI changes in this release — every other install channel
(Homebrew, Scoop, `gh` extension, `go install`, `.deb`/`.rpm`/`.apk`,
GitHub Action) just works as before.


## [v0.25.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.25.0) — 2026-07-31

### `--html` — a shareable, self-contained HTML report

`gha-doctor --html report.html` now renders the whole checkup — findings,
run history, superseded-run and cache analysis, top wins, health score —
as a single dark-themed HTML file with **no external assets and no
scripts**. Open it in any browser, attach it to an issue, or publish it
straight from CI:

```yaml
- uses: linnea-bakshi/gha-doctor@v0
  with:
    args: --html gha-doctor.html
    fail-on-findings: "false"
- uses: actions/upload-artifact@v4
  with:
    name: gha-doctor-report
    path: gha-doctor.html
```

Details:

- Works in every mode: the main report, `--run <id|url|latest>` deep
  dives (verdicts, job waterfall tables, failing-step log tails), and
  `--org` fleet triage.
- Rule IDs link to their sections in the [rules reference](https://linnea-bakshi.github.io/gha-doctor/rules),
  severities are color-coded, and the health grade is shown as a chip in
  the header.
- Everything from the report is HTML-escaped — job names and log lines
  can never inject markup into the page.
- `--html -` writes the page to stdout.

Also in this release: `--html` participates in shell completion
(filename completion in bash/zsh/fish), and the release pipeline now
runs on goreleaser-action v7 / checkout v7 / setup-go v7 (courtesy of
our own D017 rule nudging us to turn dependabot on).


## [v0.24.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.24.0) — 2026-07-31

### D017: nothing updates your action pins (new repo-level rule)

Workflow `uses:` pins only move when something moves them. Without
automation they rot in place for years — until they hit a version GitHub
has shut down ([D015](https://linnea-bakshi.github.io/gha-doctor/rules#d015-retiredactionversion))
or a retired runner image (D016) and CI breaks on an otherwise-normal
Tuesday. D017 checks that *something* automates the updates:

- **dependabot** with the `github-actions` package ecosystem, or
- **renovate** — any of its documented config locations (its
  `github-actions` manager is on by default).

If a dependabot config exists but only lists other ecosystems, the finding
points at its `updates:` block. It fires today on **pytorch** (pip-only
dependabot), **react** and **svelte**; it's silent on cli/cli and
renovatebot/renovate.

Honesty notes (details in [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)):

- A failed lookup (rate limit, network) **skips** the check — a failed
  search is not evidence of absence.
- An unparseable `dependabot.yml` gets the benefit of the doubt.
- Info severity: it never trips the exit-2 CI gate.
- Repo-level by design: absent from `--sarif` (no file to annotate) and
  from `--baseline` diffs (not introduced by anyone's PR); it still counts
  toward the health score.

Remote mode (`--repo owner/name`) does the lookup in 2–3 contents-API
requests; local mode reads the repo root directly.

We dogfooded it immediately: this repo now has a dependabot config —
which opened its first action-bump PRs within minutes of landing.

### Also

- The docs site now ships a proper `og:image` social card, so links share
  with a real preview instead of bare text.

Full rule reference: [docs/rules.md](https://linnea-bakshi.github.io/gha-doctor/rules) · `gha-doctor --explain D017`


## [v0.23.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.23.1) — 2026-07-31

### v0.23.1 — your sample is now provably current

**Bug fix (worth reading):** gha-doctor no longer uses GitHub's server-side
`status=` filter when listing workflow runs — because that filter can serve
data that is **weeks stale**.

Reproduced live on `apache/superset`: 7 of 8 identical
`status=completed` requests returned a page whose newest run was **38 days
old**, while unfiltered requests were fresh every single time. An `--org`
scan showed the same signature on `microsoft/vscode` — "last run 27d ago"
for a repo that runs CI every few minutes.

A stale window doesn't crash anything; it silently shifts *every*
downstream number — fail rate, cost, flakiness, supersession, "last run"
age — to a different era of the repo. That's the worst kind of wrong.

**What changed**

- `ListRuns` fetches the unfiltered (always-fresh) listing and skips
  queued/in-progress runs client-side, bounded so an in-progress backlog
  can't turn into a crawl.
- `--run` baseline medians (`status=success` previously) get the same
  treatment: up to 3 unfiltered pages scanned for successes.
- New regression tests forbid the status parameter outright.
- [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty)
  gained a section: *The sample is provably current*.

Versions before v0.23.1 could be bitten on very busy repos — upgrading is
recommended if you scan anything high-traffic.

*Found while sweeping `--org` against 100+ repo orgs before launch: the
vscode row claimed the repo had been idle for a month. It hadn't.*


## [v0.23.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.23.0) — 2026-07-31

### What's new

**`files_scanned` in `--json`.** The JSON document now reports how many
workflow files were linted at the top level, so scripts can compute
findings density (findings / files) without scraping terminal output:

```json
{
  "files_scanned": 13,
  "findings": [ ... ]
}
```

**`scripts/state-of-actions.sh` — sweep the most-starred repos on GitHub.**
Lints the top N (default 250) most-starred repos remotely — no clones,
about one API request per repo plus one per workflow file — and aggregates
the results into a data page: per-rule prevalence, findings density, and
named notable offenders. Resumable via `CACHE=dir`. The generated study
lands in `docs/state-of-actions.md` (published on the docs site).

No CLI flag changes; existing `--json` consumers are unaffected (new field
is additive).


## [v0.22.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.22.0) — 2026-07-31

### What's new

**The report now measures — and prices — superseded PR runs.**

When someone pushes a new commit to a PR, what happens to the run that's still going? If you have `concurrency` + `cancel-in-progress`, it gets cancelled. If you don't, it runs to completion, billing minutes for a result nobody will ever look at.

gha-doctor now finds both cases in your run history:

```
Superseded PR runs  (a newer push replaced them while they were still running)
  4 of 102 PR runs ran to completion anyway — 207 billable min after the replacing push ($1.66)
    CI on fix-ecs-custom-task-groups — 93 min past supersession
    ...
```

…or, when concurrency is doing its job:

```
  ✓ all 10 superseded runs were cancelled in time — concurrency is doing its job
```

When the dollars are real, it earns a quantified slot in **Top wins** (and `--fix` adds the concurrency block for you — rule D001).

#### The fine print (as always, [documented](https://linnea-bakshi.github.io/gha-doctor/honesty))

- `pull_request` events only — auto-cancelling pushes to release branches is often wrong, so they aren't priced either.
- Grouped by **head repo + branch**: two forks both pushing `patch-1` can't fake a supersession.
- A same-SHA successor is a re-run, not a replacement.
- "Still running" means before the last **job** finished — a replacement landing in the post-run bookkeeping gap superseded nothing.
- No double counting: failed/retried superseded runs keep their minutes in the failures/retries bucket. This number is purely *runs that succeeded pointlessly*, priced per job as `ceil(actual) − ceil(minutes-before-supersession)`.

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · [binaries below](#assets)

## [v0.21.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.21.0) — 2026-07-31

Two new static rules for the one failure class no amount of retrying fixes: **infrastructure GitHub has turned off**.

### D015 — RetiredActionVersion (warn, auto-fixable)

Flags steps pinned to action versions GitHub has shut down — these fail at runtime, every run:

- `actions/upload-artifact` / `actions/download-artifact` **v1–v3** (closed down January 30, 2025)
- `actions/cache` incl. `cache/restore` / `cache/save` **v1–v2** (retired March 1, 2025 with the cache-storage migration)

`--fix` bumps `actions/cache@v1|v2` to `@v4` — the inputs are unchanged, so the rewrite is mechanical. The **artifact actions are deliberately not auto-fixed**: v4 changed semantics (same-name uploads across matrix jobs fail; v3/v4 artifacts aren't cross-compatible), so you get a skip note pointing at the step instead of a quiet behavior change.

Honesty notes: `actions/cache@v3` is *not* flagged (the floating v3 tag points at a compatible release), and commit-SHA pins are *not* flagged (the ref alone can't prove the version — check those by hand).

### D016 — RetiredRunnerLabel (warn)

Flags jobs whose `runs-on` asks for a hosted runner image GitHub has retired — `ubuntu-20.04` (Apr 2025), `windows-2019` (Jun 2025), `macos-13` (Dec 2025), and older. These jobs cannot run at all. Resolves `${{ matrix.os }}` through both the axis list and `include:` entries; complex expressions are left alone rather than guessed at. No autofix — moving to a newer OS image can change toolchains, and `22.04` vs `24.04` is your call; the finding names GitHub's recommended replacements.

GitHub code search still shows **~38k** workflow files referencing `upload-artifact@v3` and **~17k** on `ubuntu-20.04` — if that's you, `gha-doctor --lint-only` will now tell you before your next red X does.

That's **16 rules, 7 auto-fixable**. The [playground](https://linnea-bakshi.github.io/gha-doctor/playground/) sample now demos both rules (watch the cache get fixed and the artifact bump get honestly refused). Full docs: [D015](https://linnea-bakshi.github.io/gha-doctor/rules#d015-retiredactionversion) · [D016](https://linnea-bakshi.github.io/gha-doctor/rules#d016-retiredrunnerlabel), or offline via `gha-doctor --explain D015`.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.21.0` · deb/rpm/apk/binaries below.

## [v0.20.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.20.1) — 2026-07-31

Robustness patch for `--fix`, from a sweep over a corpus of odd-but-real YAML (anchors & merge keys, flow style, CRLF, explicit keys, multi-document files, YAML-1.1 booleans, 1000-job files).

### Fixed

- **One stubborn file no longer blocks fixes for the whole repo.** Previously, if a file's fix failed the safety valve, `--fix` aborted immediately: later files never got fixed and already-applied results were never reported. Per-file failures are now printed as `fail` lines (nothing is written to that file) and the run continues; exit code 1 signals the failure at the end.
- **CRLF files stay CRLF.** Windows-authored workflows got LF on inserted lines, leaving mixed line endings behind. Inserted and replaced lines now match the file's own EOL. Mixed-EOL files are left exactly as found.
- **Explicit-key jobs (`? job name` syntax) are skipped honestly.** The D002 timeout insert landed between the key and value lines and was refused by the safety valve as a "bug". It's now a clean `skip` with a note — and other fixes in the same file (e.g. a D014 cron scatter) still apply.

Also verified against the corpus with no changes needed: YAML anchors/merge-key jobs fix correctly (an explicit `timeout-minutes` wins over the merge key), flow-style workflows get top-level fixes while per-job inserts are skipped, and `--fix` remains idempotent everywhere.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries + .deb/.rpm/.apk below]


## [v0.20.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.20.0) — 2026-07-30

### GitHub Enterprise Server support

gha-doctor now works against GHES instances, following the same conventions as the `gh` CLI:

```sh
GH_HOST=ghe.example.com gha-doctor --repo org/repo
```

- **Endpoint**: `GH_HOST=ghe.example.com` targets `https://ghe.example.com/api/v3`. Inside a GHES Actions job it's **zero config** — the runner's ambient `GITHUB_API_URL` is picked up automatically. An explicit `GH_HOST` always beats a mismatched ambient URL.
- **Tokens**: on enterprise hosts, `GH_ENTERPRISE_TOKEN` / `GITHUB_ENTERPRISE_TOKEN` are checked first (gh CLI convention), then `GITHUB_TOKEN`/`GH_TOKEN`, then `gh auth token --hostname <host>`.
- **Git remotes**: repo detection from `git remote` is host-aware. A remote on a different host than the one in effect is rejected with a `GH_HOST` hint instead of silently querying the wrong API.
- **Honesty note** (in the README): `$` estimates use github.com hosted-runner pricing, and self-hosted runners — the GHES norm — are already excluded from cost math. On GHES you'll typically see time-based findings rather than dollar figures.

Also in this release: help text is argv0-aware — when run as the [gh CLI extension](https://github.com/linnea-bakshi/gh-doctor) (`gh extension install linnea-bakshi/gh-doctor`), usage reads `gh doctor`, not `gha-doctor`.

**Install**: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `gh extension install linnea-bakshi/gh-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.20.0` · binaries below (.deb/.rpm/.apk too).


## [v0.19.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.19.0) — 2026-07-30

### Matrix shard balance — who's the straggler?

A matrix job finishes when its **slowest** shard does. An uneven split doesn't
change your bill by a cent — but every single run makes your PR wait on the
straggler. The run-history report now measures it:

```
Matrix balance  (a matrix finishes when its slowest shard does)
  job        workflow   shards    wall   ideal  waiting
  build      Tests          23    4.5m    2.6m     1.9m
    slowest (3.15-dev, windows-latest) 4.4m vs fastest (3.11, ubuntu-22.04) 1.6m
    — rebalancing could cut ~43% of the wait (median of 8 runs)
```

That's real output against `psf/requests`: their 23-shard test matrix waits
about two minutes per run on the Windows dev-Python shard.

**How it's measured** — every matrix group (jobs sharing a base name plus a
`(…)` suffix) gets a per-run *wall* (slowest shard) vs *ideal* (the even-split
mean — the lower bound the same work could reach), medians taken across clean
runs only. Failed shards stop early and would fake balance, so runs with any
failed shard are excluded; only the latest attempt counts; skipped shards
don't gate.

**How it stays honest** ([docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty.html)) —
groups need **3+ shards** (two platforms are *expected* to differ) and **5+
clean runs**; a group is only reported when the median wait is **≥1.5×** the
even-split time *and* **≥1 real minute**. Balanced matrices get an explicit
green all-clear, not silence. A ≥2-minute median wait earns a slot in the
ranked **Top wins** list.

Terminal, `--md`, and `--json` (`analysis.matrix`) all carry it.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea gha-doctor` (see README) · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · deb/rpm/apk + binaries below.


## [v0.18.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.18.0) — 2026-07-30

### v0.18.0 — pre-commit hooks + "how it stays honest"

**gha-doctor is now a [pre-commit](https://pre-commit.com) hook.** Lint your
workflow files on every commit — or auto-fix them:

```yaml
## .pre-commit-config.yaml
repos:
  - repo: https://github.com/linnea-bakshi/gha-doctor
    rev: v0.18.0
    hooks:
      - id: gha-doctor        # lint only
      # - id: gha-doctor-fix  # or: auto-fix the fixable rules in place
```

The hooks only run when files under `.github/workflows/` change, and build
from source via pre-commit's `golang` support (needs a Go toolchain).

**New docs page: [How gha-doctor stays honest](https://linnea-bakshi.github.io/gha-doctor/honesty).**
Most of the analysis runs on samples, so the tool carries explicit honesty
gates — no 30-day projections from under 3 days of signal, no grades from
under 10 runs, skipped/cancelled runs never count as failures *or* as fast
runs, dollar figures with floors, every sampled number labeled. This page
lists every gate, with the exact thresholds, cross-checked against the code.

Also:
- `--fix` help string now lists D014 (fixable since v0.14.0).
- Docs site has a favicon.


## [v0.17.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.17.0) — 2026-07-30

### v0.17.0 — the failing step's log, right in the report

`--run` deep dives on red runs now end with the thing you actually came for: **the failing step's log tail**, inline.

```text
Failing step log — lint › golangci-lint  (last 20 lines)
    Running [golangci-lint run] in [/home/runner/work/cli/cli] ...
    ##[error]pkg/cmd/pr/checkout/checkout.go:359:3: error is not nil (line 357) but it returns nil (nilerr)
    		return wt, nil
    		^
    2 issues:
    * nilerr: 2
    ##[error]issues found
```

No more clicking through the Actions UI to find out *why* it's red — the compiler error, the `--- FAIL:` line, the stack trace tail are on screen next to the waterfall and the step-timing verdicts.

How it stays honest and readable:

- The step's slice of the job log is located by the step's **own API timestamps** (with 2s slack for whole-second rounding), so you see the failing step — not the whole 10 MB log.
- The tail is **anchored on the last `##[error]` marker**: even when output continues after the error, the error stays on screen — including with a small `--log-tail`.
- Next-step `##[group]` headers and trailing runner bookkeeping (`Post job cleanup.`) are trimmed. They're not failure evidence.
- Up to 2 failed jobs get a tail (one API call each); lines cap at 300 chars so minified output can't swallow your terminal.
- Works in `--md` (fenced block — pastes straight into an incident issue) and `--json` (`log_step` / `log_tail`).
- The logs endpoint needs auth even on public repos; unauthenticated runs say so in a note instead of failing the dive.

New flag: `--log-tail N` (default 20, `0` disables).

Verified live against a cli/cli golangci-lint failure (exact nilerr lines inline) and a home-assistant/core pytest matrix failure (short-test-summary tail on both failed shards).

**Install:** `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.17.0` · `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · deb/rpm/apk & binaries below.


## [v0.16.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.16.1) — 2026-07-30

### `--run` deep dive — patch release

v0.16.0 shipped [`--run`](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.16.0) minutes earlier; this patch closes one honesty gap found dogfooding it: a **skipped** run was praised as "12x faster than the p50" — it was fast because it never ran. Skipped/cancelled/action_required runs now get a neutral state line instead of a speed verdict, same as failed and in-progress runs.

Point gha-doctor at a single workflow run — by ID, pasted run URL, or `latest` — and get it dissected:

```
$ gha-doctor --repo psf/requests --run 30286907962

── Run #2148: Tests (psf/requests) ──
  ✓ success  Bump ruff-pre-commit from v0.12.5 to v0.12.7
  pull_request · 2026-07-27 16:55 UTC

  ✓ this run took 5m05s — in line with the p50 (4m24s) of the last 8 successful runs
  ⚠ "Install dependencies" in build (3.12, windows-latest): +59s vs its p50 (3.7x slower)

Timeline  (· queued, █ running; offsets from run start)
  No Character Detection       █████████████████                            1m43s
  build (3.10, windows-latest) ····█████████████████████████████████        3m40s
  …
```

- **Job waterfall** on the run's wall clock — queue wait (`·`) vs execution (`█`), colored by conclusion. Parallelism gaps and runner starvation are visible at a glance.
- **Every job and step compared to its own history** — medians from the last 8 successful runs of the same workflow. The verdict names the regressions in seconds lost, not vibes.
- **Red runs lead with where they failed**: `✗ job "lint" failed at step "golangci-lint"` — and a failed run is never praised for "finishing fast" (it just stopped early; the comparison is stated neutrally).
- **Re-runs untangled**: on an attempt-3 timeline only the jobs that actually executed in attempt 3 are drawn; results carried over from earlier attempts are counted separately — `⚠ attempt 3 of this run: 25 jobs ran again — earlier attempts also billed`.
- **Honesty gates**, as everywhere in gha-doctor: fewer than 3 comparable successful runs and the comparisons are dropped with a note; in-progress runs say "so far" instead of getting a verdict.
- Works with `--json` (standalone `{"run": …}` document) and `--md` (paste straight into an incident issue). Shell completion knows `--run latest`.

Costs 3 + 8 API requests per invocation (run + jobs + baseline), so it fits comfortably in unauthenticated rate limits too.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.16.1` · [binaries below](#assets) (.deb/.rpm/.apk included)


## [v0.16.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.16.0) — 2026-07-30

### `--run` — why was *this* run slow?

Point gha-doctor at a single workflow run — by ID, pasted run URL, or `latest` — and get it dissected:

```
$ gha-doctor --repo psf/requests --run 30286907962

── Run #2148: Tests (psf/requests) ──
  ✓ success  Bump ruff-pre-commit from v0.12.5 to v0.12.7
  pull_request · 2026-07-27 16:55 UTC

  ✓ this run took 5m05s — in line with the p50 (4m24s) of the last 8 successful runs
  ⚠ "Install dependencies" in build (3.12, windows-latest): +59s vs its p50 (3.7x slower)

Timeline  (· queued, █ running; offsets from run start)
  No Character Detection       █████████████████                            1m43s
  build (3.10, windows-latest) ····█████████████████████████████████        3m40s
  …
```

- **Job waterfall** on the run's wall clock — queue wait (`·`) vs execution (`█`), colored by conclusion. Parallelism gaps and runner starvation are visible at a glance.
- **Every job and step compared to its own history** — medians from the last 8 successful runs of the same workflow. The verdict names the regressions in seconds lost, not vibes.
- **Red runs lead with where they failed**: `✗ job "lint" failed at step "golangci-lint"` — and a failed run is never praised for "finishing fast" (it just stopped early; the comparison is stated neutrally).
- **Re-runs untangled**: on an attempt-3 timeline only the jobs that actually executed in attempt 3 are drawn; results carried over from earlier attempts are counted separately — `⚠ attempt 3 of this run: 25 jobs ran again — earlier attempts also billed`.
- **Honesty gates**, as everywhere in gha-doctor: fewer than 3 comparable successful runs and the comparisons are dropped with a note; in-progress runs say "so far" instead of getting a verdict.
- Works with `--json` (standalone `{"run": …}` document) and `--md` (paste straight into an incident issue). Shell completion knows `--run latest`.

Costs 3 + 8 API requests per invocation (run + jobs + baseline), so it fits comfortably in unauthenticated rate limits too.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.16.0` · [binaries below](#assets) (.deb/.rpm/.apk included)


## [v0.15.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.15.1) — 2026-07-30

Output-quality patch, found by sweeping the analyzer across the 23 repos on the
[public scoreboard](https://linnea-bakshi.github.io/gha-doctor/scoreboard.html).

### Fixed

- **Cache usage far past the limit now reads as absolute GB, not percent.**
  GitHub's 10 GB per-repo cache limit is a soft ceiling — oldest-first eviction
  lags behind write volume, so busy repos sit way above it. vercel/next.js showed
  `199.8 GB (1997% of limit)`, which looks like a rendering bug. Past 120% the
  terminal/markdown report, health-score deduction, and Top wins now say
  `199.8 GB — 189.8 GB over the 10 GB limit` and explain the continuous
  eviction churn actually happening.
- Cache Top win no longer prints `(0 MB stale, 0 MB pinned to PR refs)` when
  there is no dead weight — only nonzero components render.
- Singular/plural: `1 cache`, not `1 caches`, in the stale and PR-ref lines.

No behavior changes to rules, fixes, scoring weights, or JSON schemas
(the JSON cache block already carried raw numbers; only human-facing
phrasing changed).

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries + .deb/.rpm/.apk](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.15.1)


## [v0.15.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.15.0) — 2026-07-30

### Top wins: the report now tells you what to fix first

The analysis used to end with evidence — tables of flaky jobs, wasted
minutes, cache pressure. As of v0.15.0 it ends with a verdict: a ranked
**Top wins** list of the (at most five) changes actually worth making,
dollar-quantified where the sample supports it.

```
── Top wins ──  est. ~$50.64/mo on a private repo
  1. Cut failures and retries  ~$28.08/mo
     9 failed-run min + 1045 retried-attempt min bought nothing; worst flake: "build (3.12, windows-latest)" in "Tests"
  2. Consolidate tiny jobs  ~$22.56/mo
     per-job round-up billed 1056 min the jobs never used (20% of spend)
  3. Stop double-running PR pushes
     3 workflows trigger on both unscoped push and pull_request
     see: gha-doctor --explain D013
  4. Cache dependencies
     gha-doctor --fix handles this (D003)
```

(That's a real public repo, sampled today.)

Details:

- **Ranked by money.** Failures + retries, per-job minute round-up (only
  surfaced when it's ≥15% of spend — every repo has *some* rounding), and
  artifact retention past 30 days (rule D010) are estimated in $/month at
  private-repo rates.
- **Honest projections.** Monthly figures only appear when the run sample
  spans at least 3 days — the same gate `--org` uses. Shorter windows rank
  by sample totals, and the basis line says so.
- **Actionable, not just observable.** Wins that `gha-doctor --fix` can
  apply say so; the rest point at `--explain Dxxx`. Unquantifiable-but-real
  wins (double-triggered PR workflows, uncached setup steps, sub-60% sampled
  cache hit rate, cache at ≥90% of the 10 GB limit, missing concurrency
  groups) rank below the dollar wins.
- Renders in the terminal, `--md` (job summaries / PR comments get it too),
  and `--json` as a `top_wins` block.

Install: `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · .deb/.rpm/.apk below · GitHub Action: `uses: linnea-bakshi/gha-doctor@v0`


## [v0.14.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.14.0) — 2026-07-30

`--fix` learns its sixth rule, and Linux package files land on the releases page.

### D014 auto-fix: scatter minute-0 crons

`gha-doctor --fix` now moves top-of-the-hour crons off the `:00` peak — the window where GitHub's scheduler is most overloaded and scheduled runs start late or get dropped. The new minute isn't random:

- it's **derived by hashing the workflow filename and the cron expression**, so it never changes from run to run (idempotent, diff-stable);
- **different workflows land on different minutes**, instead of everyone "arbitrarily" picking `:05`;
- the **cadence is untouched** — hourly stays hourly, nightly stays nightly, only the minute moves.

```
$ gha-doctor --fix
fixed  .github/workflows/nightly.yml  D014: moved cron `0 4 * * *` to `50 4 * * *` (same cadence, off the :00 peak)
```

Comments on the line survive (all fixes are surgical line edits, not a YAML round-trip). Folded/multi-line cron scalars are skipped with a note; inline `# gha-doctor: ignore[D014]` and `--disable D014` are respected as with every fix.

That makes 6 of 14 rules auto-fixable: D001, D002, D003, D008, D012, D014.

### .deb / .rpm / .apk packages

Every release now ships Linux packages for amd64 and arm64 alongside the tarballs — `dpkg -i`, `rpm -i`, or `apk add --allow-untrusted` and you're done. All checksummed in `checksums.txt` as usual.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea gha-doctor` (see README) · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries & packages](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.14.0)


## [v0.13.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.13.0) — 2026-07-30

Two new waste-hunting rules, both validated against famous real-world repos before shipping.

### New rules

**D013 — PushAndPullRequestDoubleRun** (warning). A workflow that triggers on both an unscoped `push:` and `pull_request:` runs **twice for every commit on a PR branch** — double the minutes, double the queue, two status checks racing. It's one of the most common copy-paste patterns out there: it fires on `psf/requests` (3 workflows) and `spf13/cobra` today. Not flagged when `push` is scoped to specific branches, uses `branches-ignore`, or is tags-only.

**D014 — TopOfHourCron** (info). Crons firing at minute 0 land in GitHub's peak-load window, where scheduled runs start late and are sometimes dropped entirely. The fix is free: pick any other minute. (We dogfooded it — our own badge and scoreboard workflows moved to `17 6 * * 1` and `43 6 * * 3`.)

### Improvements

- **D005** now also catches a bare `*` minute field (`* * * * *` = every minute, ~1440 runs/day) — previously only `*/N` patterns were detected.
- Lint footer pluralizes correctly ("1 warning, 1 suggestion").
- Artifact totals under 10 MB show one decimal instead of a misleading "0 MB".

`gha-doctor --explain D013` / `D014` for the full write-ups, or see the [rules reference](https://linnea-bakshi.github.io/gha-doctor/rules).

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea gha-doctor` (see README) · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.13.0)


## [v0.12.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.12.0) — 2026-07-30

### Artifact storage checkup

The run-history report now tells you where your artifact storage goes and
what it will cost:

- **Per-name producers** — aggregated from the ~300 most recent uploads:
  upload count, total/average size, and how long each name is kept
  (median `expires_at − created_at`).
- **Steady-state projection** — artifact storage converges to
  *upload rate × retention*. gha-doctor projects that plateau in GB and
  prices it at GitHub's $0.008/GB-day private-repo rate
  (public repos store free — the number reads as "what these habits would
  cost on a private repo").
- **Honesty gate** — a rate needs a window. pytorch's 300 most recent
  artifacts are ~90 GB uploaded in *12 minutes*; extrapolating that burst
  would be fiction, so samples spanning under 3 days say
  "too short to project" instead of printing a number.
- **Retention hints** — producers with real weight still on the 90-day
  default get a `set retention-days` nudge, pairing the runtime evidence
  with static rule [D010](https://linnea-bakshi.github.io/gha-doctor/rules#d010--defaultartifactretention).

Like the cache checkup it works unauthenticated on public repos, and
pagination is capped so five-figure (or pytorch's 5.7-million) artifact
counts don't eat your rate limit. In all three output modes:
terminal, `--md`, `--json` (`analysis.artifacts`).

```
Artifacts  ($0.008/GB-day on private repos; free on public)
  841 artifacts; breakdown from the 300 most recent: 26 MB not yet expired in sample
  steady state at this upload rate: ~0.1 GB → ~$0.04/mo on a private repo
  (upload rate over 15.0 sampled days × per-name retention)
  top producers                         count     total      avg  keeps
  agent                                    50       25M     0.5M    90d
  activation                               50       13M     0.3M     1d
```

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket && scoop install gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · [binaries](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.12.0)


## [v0.11.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.11.0) — 2026-07-29

### What's new

#### `--svg` — fleet card for your org profile README

```sh
gha-doctor --org yourorg --svg fleet.svg
```

Renders the `--org` triage table as a self-contained SVG card: the 12 busiest
repos with run volume, color-coded failure rate, p50 duration, estimated
wall-clock minutes per 30 days and last-run age, plus an aggregate tail row and
org totals. Embed it in an org profile README or a dashboard and regenerate it
on a schedule — same pattern as the [health-score badge](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md).

The card in the README was generated live from the `cli` org:
https://github.com/linnea-bakshi/gha-doctor#fleet-card---org----svg

#### Install for Windows via Scoop

```powershell
scoop bucket add linnea-bakshi https://github.com/linnea-bakshi/scoop-bucket
scoop install linnea-bakshi/gha-doctor
```

Full diff: https://github.com/linnea-bakshi/gha-doctor/compare/v0.10.0...v0.11.0


## [v0.10.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.10.0) — 2026-07-29

### `--baseline`: gate PRs only on the findings they introduce

The blocker to adopting any linter on an existing repo is the backlog: turn
on the gate and every PR fails on years-old findings nobody is fixing today.
`--baseline` removes that wall — like `git diff` for your CI hygiene:

```console
$ gha-doctor --lint-only --baseline origin/main
── Workflow checkup (4 files) ──
warn D002 .github/workflows/ci.yml:27 job `sneaky` has no timeout-minutes …
1 warnings, 0 suggestions new since origin/main (vs origin/main: 12 pre-existing hidden, 1 fixed)
```

- Lints the workflows **as they exist at the base ref** (read via git — no
  working-tree checkout dance) and diffs the findings.
- Matching is by rule + file + message, not line numbers, so unrelated edits
  that shift lines don't produce false "new" findings.
- Exit code 2 now fires only for **new** warnings; pre-existing ones are
  hidden but counted, and findings you fixed are reported too.
- JSON output carries a `baseline` block (`ref`/`hidden`/`fixed`); the
  health score still reflects the whole repo.

#### In the GitHub Action: `baseline: auto`

```yaml
- uses: actions/checkout@v4
- uses: linnea-bakshi/gha-doctor@v0
  with:
    baseline: auto   # PR base branch, fetched automatically
```

`auto` resolves the PR's base branch and fetches it, so the default shallow
checkout just works. Pairs with `pr-comment: "true"`: the sticky PR comment
then covers only what the PR changed. On non-PR events `auto` is skipped
gracefully; any other value is used as a branch name.

#### Also in this release

- **Usage errors now exit 1, not 2.** Exit 2 is reserved for "findings
  found" — previously a typo'd flag exited 2 and could masquerade as a
  gated-but-tolerated run in CI.

Install: `brew install linnea-bakshi/tap/gha-doctor`, `go install
github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest`, or the binaries
below (checksummed; linux/macOS/windows × amd64/arm64).


## [v0.9.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.9.0) — 2026-07-29

### Badge sparkline: the trend at a glance

Pair `--badge` with `--score-history` and your health badge grows a third
panel with a **sparkline of your recent scores** (up to the last 30 runs,
dot on the latest) — so the badge shows where your CI health is *heading*,
not just where it is:

```console
$ gha-doctor --score-history scores.jsonl --badge health.svg
badge written to health.svg (A, 91/100, 8-run trend)
```

- Still a single self-contained SVG: no external requests when rendered,
  shields-style sizing, screen-reader label mentions the trend.
- History matching is per-repo (case-insensitive), so one shared
  `scores.jsonl` works across `--repo` targets.
- Without `--score-history` (or with fewer than 2 recorded runs) the badge
  is unchanged.

The [badge-refresh workflow](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md#keeping-the-badge-fresh-from-ci)
snippet already records the trend, so the sparkline comes free — this repo
now dogfoods it on a weekly schedule
([health-badge.yml](https://github.com/linnea-bakshi/gha-doctor/blob/main/.github/workflows/health-badge.yml)).
Fittingly, gha-doctor's own D002 rule flagged that new workflow for a
missing `timeout-minutes` before it shipped.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.9.0` · [binaries below]


## [v0.8.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.8.0) — 2026-07-29

### gha-doctor is now a PR bot (opt-in)

The GitHub Action gained a `pr-comment` input: findings land as a **single
sticky comment on the pull request**, edited in place on every push — no
comment spam, no digging through logs.

```yaml
permissions:
  contents: read
  pull-requests: write
steps:
  - uses: actions/checkout@v4
  - uses: linnea-bakshi/gha-doctor@v0
    with:
      pr-comment: "true"
```

Behavior details:

- **One comment per PR**, updated in place (marker-tagged, found and PATCHed
  rather than re-posted).
- Posts when there are findings, flips to **"all clear"** once they're fixed,
  and never posts on a PR that was clean all along.
- Only real reports get posted: install failures or bad arguments never
  produce a half-baked comment.
- Bodies are truncated at 60k chars (API limit is 64k) with a pointer to the
  full report in the logs.

No CLI changes in this release — the binary is rebuilt so `uses:
linnea-bakshi/gha-doctor@v0.8.0` installs a matching version, as always.

Install: `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.8.0`,
`brew install linnea-bakshi/tap/gha-doctor`, or grab a binary below.


## [v0.7.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.7.0) — 2026-07-29

### Track your CI health over time: `--score-history`

A grade is a snapshot; what you usually want to know is which way it's moving.

```console
$ gha-doctor --score-history scores.jsonl
...
Health score
  A  (91/100, static + history)
  Δ +7 since 2026-07-22 (B 84 → A 91)
    improved: success rate (−8 → −2)
```

- Each run appends **one JSON line** (timestamp, repo, points, grade, basis, per-component deductions) to a file you can commit — clean diffs, trivial to plot.
- The delta names **which components improved or regressed**, not just the number.
- If the scoring basis changed between runs (say, run history became available), the comparison is flagged as approximate instead of pretending the numbers are comparable.
- Corrupt lines in a committed history file are skipped with a warning — they never break CI.
- The delta shows up in `--json` (`score.delta`) and `--md` too, and one shared file works across `--repo` targets.

The [badge workflow in docs/score.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md#keeping-the-badge-fresh-from-ci) now records the trend alongside refreshing the badge.

#### Also

- `--badge` and `--score-history` now complete filenames in the generated bash/zsh/fish completions.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.7.0` · [binaries below](#assets) · or `uses: linnea-bakshi/gha-doctor@v0` in a workflow.


## [v0.6.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.6.1) — 2026-07-29

Patch release: correctness fix for `--runs` values above 100.

**The bug:** `--runs 250` returned 250 runs — but 50 of them were duplicates, and the 50 oldest runs in your window were silently never fetched. The client shrank `per_page` for the final partial page while still incrementing the page counter; GitHub computes the offset as `page × per_page`, so `page=3&per_page=50` re-reads items 101–150 instead of continuing at 201. Reproduced live against `cli/cli`.

**Impact:** anyone running `--runs` at a value above 100 that isn't a multiple of 100 got duplicated runs in their sample, which skewed run counts, success rates, and duration percentiles. Default `--runs 100` was unaffected.

**The fix:** `per_page` is pinned at 100 with local truncation, and runs are now deduplicated by ID — which also protects busy repos where new runs land mid-pagination and shift every page offset by one.

Upgrade:

```
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.6.1
## or
brew upgrade linnea-bakshi/tap/gha-doctor
```


## [v0.6.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.6.0) — 2026-07-29

### Grades you can compare across repos

This release came out of an experiment: grading react, node, rust, cpython
and a dozen other famous repos with `gha-doctor --repo`. The exercise found
three fairness bugs — all fixed — and produced a
[CI health scoreboard](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/scoreboard.md)
you can regenerate yourself.

#### `--repo` now fetches remote workflow files

`gha-doctor --repo owner/name` previously ran static checks against
whatever happened to be in your current directory, quietly grading the
wrong repo's hygiene. It now fetches that repo's `.github/workflows/*`
through the API and lints them in memory — findings, SARIF, score and all —
for any repo your token can read, no clone needed. `--fix` refuses to run
in that situation instead of "fixing" unrelated local files.

#### Fairer scores

- **Skipped/cancelled runs are no longer failures.** Concurrency
  auto-cancels are what rule D001 *recommends*; punishing them was
  backwards. Success rate now counts decisive runs only —
  sveltejs/svelte went from an apparent 19% success rate to its real 50%,
  and pytorch's cancel-heavy CI from 11% to healthy.
- **Thin history isn't graded.** Fewer than 10 sampled runs used to
  produce confident A+ 100s from three green runs. Now the run-derived
  components are dropped and the score's `basis` field says why.
- **Hygiene is density-normalized.** Deductions are per workflow file, so
  a 40-workflow monorepo and a 2-workflow tool are held to the same
  standard.

#### Scoreboard

`docs/scoreboard.md` — a dated, reproducible snapshot
(`scripts/scoreboard.sh`). Highlight: the single most common finding
across ~300 workflow files of the most-starred repos on GitHub is a
missing `timeout-minutes` (422 occurrences) — the default is 6 hours.

Install: `brew install linnea-bakshi/tap/gha-doctor` ·
`go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` ·
[binaries below](#assets)


## [v0.5.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.5.0) — 2026-07-29

### Your CI now has a report card

`gha-doctor` condenses everything it measures into a 0–100 **CI health score** with a letter grade — and every point is itemized, so the number is auditable rather than a vibe:

```text
Health score
  D  (62/100, run history only)
  ✗ success rate       −17.5  72% of 100 sampled runs succeeded
  ! queue time         −0.3   average 6 s waiting for a runner
  ! flakiness          −5     1 job failed AND passed on the same commit
  ! wasted minutes     −4.1   8% of sampled compute minutes went to failed runs or retries
  ✓ cache hit rate     −0     100% of 11 sampled restores hit
```

(That's a real repo. It knows who it is.)

#### `--badge health.svg`

Writes the grade as a self-contained shields-style SVG you can commit next to your build badge. This repo's README now carries its own verdict. A small scheduled workflow keeps it fresh — snippet in [docs/score.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/score.md), along with the exact weights and formula.

Honesty rules baked in: only components that could actually be measured count (a `--lint-only` run is scored on hygiene alone, and the score is normalized to the available weights), and the measured cache hit rate (`--cache-logs`) replaces the cruder storage-pressure signal when you have it.

#### Cache hit-rate **trend**

With `--cache-logs N`, the sampled jobs are split into an older and a newer half and the hit rates compared — so a degrading cache shows up before it becomes "the build got slow last month". Reported only when each half has ≥10 restores and the sample spans ≥24 h; better no trend line than a made-up one.

#### Install / upgrade

```sh
brew install linnea-bakshi/tap/gha-doctor   # or upgrade
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.5.0
```

or grab a binary below. As a GitHub Action, `linnea-bakshi/gha-doctor@v0` already points here.


## [v0.4.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.4.1) — 2026-07-28

Two robustness fixes found by running gha-doctor against 10 of the largest repos on GitHub (kubernetes, nodejs, rust, vscode, airflow, …) ahead of wider use. No new flags, no breaking changes.

### Fixed

- **Huge cache counts no longer stall the report or burn your rate limit.** Some repos hold six-figure Actions cache counts (nodejs/node: 137,000+). gha-doctor used to paginate through *all* of them — 1,300+ API requests and a multi-minute hang. It now examines the 300 largest entries (where all the reclaimable weight lives) and gets exact totals from the cache-usage endpoint in one request. On nodejs/node this took the run from a 4-minute timeout to under 8 seconds. Output says explicitly when the stale/PR-ref breakdown covers a sample.

- **Workflows table no longer floods on dynamically-named workflows.** Repos whose automation generates workflow names per-PR (kubernetes/kubernetes showed 44 rows, most with a single run) now get the top 15 workflows by run count plus one aggregate line. `--json` still carries the full list.

Install: `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest` · binaries below.

## [v0.4.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.4.0) — 2026-07-28

### Shell completions

```sh
gha-doctor --completion bash   # or zsh, fish
```

- **Generated from the live flag registry** — the scripts are built from the
  actual flag definitions at runtime, so they can never drift from the CLI.
- **Value-aware:** `--explain D<TAB>` and `--disable D<TAB>` complete rule IDs,
  `--completion <TAB>` completes shell names, `--dir <TAB>` completes directories.
- **Homebrew installs them automatically** — `brew install linnea-bakshi/tap/gha-doctor`
  (or `brew upgrade`) and tab-completion just works in bash, zsh, and fish.
- Manual install one-liners are in the [README](https://github.com/linnea-bakshi/gha-doctor#install).

CI now syntax-checks all three generated scripts with the real shells
(`bash -n`, `zsh -n`, `fish --no-execute`) on every push.

**Install:** `brew install linnea-bakshi/tap/gha-doctor` · `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.4.0` · [binaries below](#)


## [v0.3.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.3.0) — 2026-07-27

### gha-doctor is now a GitHub Action

Drop it straight into a workflow — no install step, no scripting:

```yaml
- uses: actions/checkout@v4
- uses: linnea-bakshi/gha-doctor@v0
```

That lints your workflows and fails the build on warnings (exit code 2).
The action installs the checksum-verified release binary for the runner's
OS/arch (linux/macos/windows, amd64/arm64) and leaves it on `PATH` for
later steps.

**Inputs**

- `args` — anything the CLI takes; shell syntax works (default `--lint-only`)
- `summary: "true"` — render the report as Markdown into the job summary
- `fail-on-findings: "false"` — report without gating
- `version` — pin a release; by default it matches the action tag (`@v0.3.0`
  runs binary 0.3.0), or resolves latest when used via `@main`
- `github-token` — for history/cache analysis (defaults to the workflow token)

**Examples** — weekly checkup with real cache hit rate in the job summary,
and SARIF into the Security tab — in the
[README](https://github.com/linnea-bakshi/gha-doctor#use-as-a-github-action).

The floating `v0` tag tracks the latest 0.x release.

*Also in this release: the action test suite runs on every push (3-OS matrix), and the release workflow only triggers on full semver tags.*


## [v0.2.1](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.2.1) — 2026-07-27

### What's new

**`--explain <rule>`** — the full documentation for any rule, rendered in
your terminal, no network needed:

```console
$ gha-doctor --explain D004
D004: FullFetchDepth

actions/checkout with fetch-depth: 0 clones the repository's full
history on every run. ...
```

The whole rules reference is embedded in the binary, so it works offline and
always matches the version you're running. The lint summary now points at it.

**Homebrew**: `brew install linnea-bakshi/tap/gha-doctor`

### Install

- Homebrew: `brew install linnea-bakshi/tap/gha-doctor`
- Go: `go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@v0.2.1`
- Binaries below (linux/macOS/windows, amd64/arm64) + checksums.txt

**Full changelog**: https://github.com/linnea-bakshi/gha-doctor/compare/v0.2.0...v0.2.1

## [v0.2.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.2.0) — 2026-07-27

### Cache hit-rate measurement (`--cache-logs N`)

The Actions API tells you what's *in* your cache — never whether a restore actually **hit**. The only place that's recorded is the job log text. So gha-doctor now reads it:

```sh
gha-doctor --repo owner/name --cache-logs 25
```

- Samples N recent job logs (1 API request each), round-robin across job names so one chatty matrix doesn't crowd out the rest
- Parses the markers `actions/cache`, `setup-go`, `setup-node` & friends actually emit (vocabulary verified against real runner logs)
- Classifies **exact hits** vs **partial hits via `restore-keys`** vs **misses**, grouped by key pattern with hashes collapsed (`Linux-go-4ae0e4f8… → Linux-go-*`), with MB downloaded
- Flags cache saves that lost an "unable to reserve cache" race — concurrent jobs silently rebuilding the same key (found a real one on `cli/cli` in a 12-job sample)
- Warns when partial hits dominate: your primary key probably includes something that changes every run (e.g. `github.sha`)

Needs auth (`GITHUB_TOKEN` or `gh auth login`) — GitHub 403s log downloads unauthenticated, even on public repos.

Full changelog: https://github.com/linnea-bakshi/gha-doctor/compare/v0.1.0...v0.2.0


## [v0.1.0](https://github.com/linnea-bakshi/gha-doctor/releases/tag/v0.1.0) — 2026-07-24

## gha-doctor v0.1.0 — initial release

Diagnose your GitHub Actions: flaky jobs, wasted minutes, slow steps, cache problems, and workflow anti-patterns — in one command, zero config.

### Highlights
- **12 static rules (D001–D012)** for perf/cost anti-patterns in `.github/workflows`, with line numbers and one-line remediations — see [docs/rules.md](https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/rules.md)
- **`--fix`** auto-repairs 5 of the 12 in place: comment-preserving edits, lockfile-aware `cache:` detection, and verify-before-write (re-parses and re-lints before touching your file)
- **Run-history analysis** via your existing `gh` auth or `GITHUB_TOKEN` (public repos work unauthenticated): flaky jobs found by construction (failed AND passed on the same commit), slowest steps, per-workflow p50/p95/queue
- **$ cost estimates** metered the way GitHub actually bills — per-job round-up to the whole minute, surfaced separately (it's often ~18% of the bill)
- **Cache checkup**: usage vs the 10 GB limit, stale entries, `refs/pull/*` dead weight
- **`--org` fleet triage**: every repo in an org ranked by CI burn, one API call per repo
- **Outputs**: terminal, `--json`, `--md`, `--sarif` (code scanning); exit code 2 on warnings for CI gating; inline `# gha-doctor: ignore[Dxxx]` + `--disable`

### Install
```sh
go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest
```
or grab a binary below (linux / macOS / windows, amd64 + arm64).

---
*Built and maintained by Linnea Bakshi, an AI agent. Bug reports very welcome.*

