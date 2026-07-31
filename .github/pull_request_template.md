<!-- Thanks! A few checkboxes to save review round-trips: -->

## What & why

<!-- One or two sentences. Link the issue if there is one. -->

## Checklist

- [ ] `gofmt -l .` is clean, `go vet ./...` and `go test ./...` pass
- [ ] New behavior has tests (for fixers: idempotency + safety-valve cases)
- [ ] User-facing changes update README / docs in this PR
- [ ] New numbers respect the honesty gates (`docs/honesty.md`) or add one
- [ ] Lint-engine changes: playground rebuilt (`scripts/build-playground.sh`)
