#!/usr/bin/env bash
# Builds the docs site into _pages-out/ with Liquid disabled per page.
#
# Why: GitHub Actions expressions (`${{ ... }}`) are Liquid syntax to
# Jekyll — the legacy Pages build silently ate them from every code
# snippet (a copy-pasted workflow from the live recipes page was
# broken). Jekyll 4 supports `render_with_liquid: false`, so we stage a
# copy of docs/ and inject that front matter into every markdown page,
# keeping the committed sources pristine for GitHub's own rendering and
# the go:embed'd rules reference.
#
# Usage: scripts/build-pages.sh   (run from the repo root; needs bundler
# with docs/Gemfile installed — see .github/workflows/pages.yml)
set -euo pipefail
cd "$(dirname "$0")/.."

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
cp -r docs/. "$STAGE/"

python3 - "$STAGE" <<'EOF'
import glob, os, sys
stage = sys.argv[1]
inject = "---\nlayout: default\nrender_with_liquid: false\n---\n\n"
n = 0
for md in glob.glob(stage + "/**/*.md", recursive=True):
    with open(md, encoding="utf-8") as f:
        text = f.read()
    if text.startswith("---\n"):
        end = text.index("\n---\n", 4)
        head, body = text[4:end], text[end + 5:]
        if "render_with_liquid" in head or "layout:" in head:
            raise SystemExit(f"{md}: front matter already sets layout/"
                             "render_with_liquid — resolve by hand")
        text = f"---\nlayout: default\nrender_with_liquid: false\n{head}\n---\n{body}"
        with open(md, "w", encoding="utf-8") as f:
            f.write(text)
    else:
        with open(md, "w", encoding="utf-8") as f:
            f.write(inject + text)
    n += 1
print(f"injected front matter into {n} pages", file=sys.stderr)
EOF

BUNDLE_GEMFILE=docs/Gemfile bundle exec jekyll build \
  --source "$STAGE" --destination _pages-out
echo "built _pages-out/"
