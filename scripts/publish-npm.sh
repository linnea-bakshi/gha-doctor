#!/usr/bin/env bash
# Publish the npm wrapper package for a released version.
#
#   scripts/publish-npm.sh v0.43.0
#
# Prereqs: the GitHub release for that tag must already exist (the package
# downloads its binaries from it at install time), and `npm whoami` must
# work (npm login or NPM_TOKEN in ~/.npmrc).
#
# After the first successful publish, add the npm/npx install lines to
# README.md and docs/index.md.
set -euo pipefail

tag="${1:?usage: publish-npm.sh vX.Y.Z}"
ver="${tag#v}"
case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "error: tag must look like vX.Y.Z (got '$tag')" >&2; exit 1 ;;
esac

dir="$(cd "$(dirname "$0")/../packaging/npm" && pwd)"
cd "$dir"

# The release must exist — install.js downloads from it.
gh release view "$tag" --repo linnea-bakshi/gha-doctor --json name >/dev/null

npm whoami >/dev/null || { echo "error: not logged in to npm" >&2; exit 1; }

# Pin the package version to the binary version.
npm version --no-git-tag-version --allow-same-version "$ver" >/dev/null

# Local smoke: fresh install from a packed tarball must produce a working
# binary before anything is published.
tarball="$(npm pack --silent)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
(
  cd "$tmp"
  npm init -y >/dev/null 2>&1
  npm install --no-audit --no-fund "$dir/$tarball" >/dev/null
  out="$(./node_modules/.bin/gha-doctor --version)"
  echo "smoke: $out"
  [[ "$out" == "gha-doctor $ver" ]]
)
rm -f "$dir/$tarball"

npm publish
echo "published gha-doctor@$ver to npm"
echo "REMINDER: commit the packaging/npm/package.json version bump."
