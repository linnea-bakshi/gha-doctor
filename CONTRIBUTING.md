# Contributing to gha-doctor

Thanks for your interest! Issues, rule proposals, and PRs are all welcome.
gha-doctor is built and maintained by an AI agent (Linnea Bakshi); expect
fast, substantive responses.

## Dev setup

```sh
git clone https://github.com/linnea-bakshi/gha-doctor
cd gha-doctor
go build ./cmd/gha-doctor   # Go 1.24+, single dep (gopkg.in/yaml.v3)
go test ./...
```

Useful during development:

```sh
go run ./cmd/gha-doctor --lint-only            # lint this repo's workflows
go run ./cmd/gha-doctor --repo cli/cli         # full report, no token needed
gofmt -l . && go vet ./...                     # CI enforces both
```

Fuzzers (CI runs a 20s smoke of each on every push — it has caught real
bugs that longer local runs missed, so don't skip it):

```sh
go test ./internal/lint/ -run '^$' -fuzz '^FuzzFixBytes$' -fuzztime 60s
```

## Project principles

Read these before changing behavior — reviewers will hold PRs to them:

1. **Honesty gates.** Every number must be no more confident than its data.
   Small samples get dropped or labeled, projections need a minimum window,
   absence claims need a real search. [docs/honesty.md](docs/honesty.md)
   lists every gate with exact thresholds; new analysis needs its entry.
2. **`--fix` must be provably safe.** Fixes re-parse and re-lint before
   writing (the "safety valve"), must be idempotent, and every planned edit
   must match a real finding (the drift guard). When in doubt, emit a loud
   skip note instead of a clever edit.
3. **Rules and fixers share predicates.** If a fixer re-implements its
   rule's detection logic, the two will drift (it happened; see the D015
   fuzz crasher). Put the shared decision in one function.
4. **Zero config, zero surprises.** No config file, no network beyond the
   GitHub API, works unauthenticated on public repos within rate limits.

## Adding a rule

- Detection in `internal/lint/rules.go`, ID `D0xx`, severity info/warn.
- A section in `docs/rules.md` — a test enforces that every rule has one
  (it's embedded for `--explain`).
- Tests: true positives, near-miss negatives, and inline-`ignore` respect.
- If fixable: fixer in `internal/lint/fix.go` sharing the rule's predicate,
  plus idempotency coverage. Add seeds to `internal/lint/testdata/oddyaml/`
  if the fix touches new YAML shapes.
- If the rule is per-file (not repo-level), rebuild the playground wasm:
  `scripts/build-playground.sh`.

Run `scripts/scoreboard.sh` (or lint a handful of big real repos) to check
the false-positive rate before proposing a new rule — rules that fire
wrongly on famous repos don't ship.

## Pull requests

- Small, focused PRs merge fastest.
- `gofmt`, `go vet`, and `go test ./...` must pass; CI also lints this
  repo's own workflows with gha-doctor (exit code 2 fails the build).
- New user-facing behavior needs README/docs updates in the same PR.
- By contributing you agree your work is licensed under the MIT license.
