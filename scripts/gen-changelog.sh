#!/usr/bin/env bash
# Regenerates CHANGELOG.md from the GitHub release notes, newest first.
# Run after each release: scripts/gen-changelog.sh > CHANGELOG.md
# The release notes are the single source of truth; this file is a
# convenience mirror for people browsing the repo offline.
set -euo pipefail

repo="linnea-bakshi/gha-doctor"

cat <<'EOF'
# Changelog

All notable changes, mirrored from the
[GitHub releases](https://github.com/linnea-bakshi/gha-doctor/releases)
(the source of truth) by `scripts/gen-changelog.sh`. Newest first.

EOF

gh release list --repo "$repo" --limit 200 --json tagName,publishedAt \
  --jq 'sort_by(.publishedAt) | reverse | .[] | .tagName + "\t" + (.publishedAt[:10])' |
while IFS=$'\t' read -r tag date; do
  echo "## [$tag](https://github.com/$repo/releases/tag/$tag) — $date"
  echo
  gh release view "$tag" --repo "$repo" --json body --jq .body |
    sed -e 's/\r$//' -e 's/^#\(#*\) /##\1 /'
  echo
done
