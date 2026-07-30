#!/usr/bin/env bash
# Builds the browser playground's WebAssembly module into docs/playground/.
#
# Output is committed to the repo (GitHub Pages serves main:/docs directly),
# so run this whenever the lint engine changes and commit the result:
#
#   ./scripts/build-playground.sh && git add docs/playground && git commit ...
#
# The module ships gzipped (~1.2 MB vs ~4.4 MB raw); the page inflates it
# with DecompressionStream, available in every browser that runs WASM today.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=$(git describe --tags --exclude v0 --always 2>/dev/null || echo dev)
OUT=docs/playground

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

GOOS=js GOARCH=wasm go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$TMP/playground.wasm" ./cmd/playground

gzip -9 -c "$TMP/playground.wasm" > "$OUT/playground.wasm.gz"

# Go's JS support shim, taken from the exact toolchain that built the module
# (it is version-coupled). BSD-licensed by the Go Authors; header retained.
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$OUT/wasm_exec.js"

echo "built $OUT/playground.wasm.gz ($(du -h "$OUT/playground.wasm.gz" | cut -f1)) version=$VERSION"
