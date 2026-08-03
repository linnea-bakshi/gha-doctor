#!/usr/bin/env bash
# Merged test coverage: unit -coverprofile + integration binary coverage.
#
# Integration tests in cmd/gha-doctor exec the built binary, so a plain
# `go test -coverprofile` sees ~2% of package main even though the tests
# exercise most of it. Phase 2 rebuilds the binary with -cover
# (GHA_DOCTOR_TEST_BINCOVER=1, see testBinary in main_test.go) and lets
# the exec'd runs write counters to GOCOVERDIR. NB: phase 2 must NOT use
# `go test -cover` — the go tool then overrides GOCOVERDIR for its own
# collection and the children's data is silently discarded (measured).
#
# Usage: scripts/coverage.sh [out.profile]
# Prints per-package + total coverage; writes the merged profile.
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:-/tmp/gha-doctor-cover.out}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "== phase 1: unit coverage" >&2
go test -count=1 -coverprofile="$work/unit.out" -covermode=atomic ./... >&2

echo "== phase 2: integration binary coverage (GOCOVERDIR)" >&2
mkdir -p "$work/covdir"
GHA_DOCTOR_TEST_BINCOVER=1 GOCOVERDIR="$work/covdir" \
  go test -count=1 ./cmd/gha-doctor >&2
go tool covdata textfmt -i="$work/covdir" -o "$work/bin.out"

echo "== merge" >&2
python3 scripts/merge-cover.py "$work/unit.out" "$work/bin.out" > "$out"
echo "== merged profile: $out" >&2
go tool cover -func="$out" | tail -1
