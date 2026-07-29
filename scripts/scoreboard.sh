#!/usr/bin/env bash
# Regenerates docs/scoreboard.md: gha-doctor grades for a set of well-known
# repos. Usage: scripts/scoreboard.sh [repo ...]  (defaults to the list below)
# Requires: gha-doctor on PATH, gh auth (or GITHUB_TOKEN), python3, ~20 API
# requests per repo.
set -u

REPOS=${@:-"python/cpython pytorch/pytorch rust-lang/rust nodejs/node \
cli/cli microsoft/typescript microsoft/vscode home-assistant/core \
apache/airflow sveltejs/svelte prometheus/prometheus facebook/react \
vitejs/vite vercel/next.js denoland/deno"}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for r in $REPOS; do
  f="$tmp/$(echo "$r" | tr '/' '_').json"
  gha-doctor --repo "$r" --json >"$f" 2>"$f.err"
  rc=$?
  if [ $rc -gt 2 ]; then # exit 2 just means findings; >2 is a real failure
    echo "warn: $r failed (exit $rc): $(tail -1 "$f.err")" >&2
    rm -f "$f"
  fi
done

python3 - "$tmp" <<'EOF'
import json, glob, sys, datetime
rows = []
for f in sorted(glob.glob(sys.argv[1] + '/*.json')):
    d = json.load(open(f))
    repo = f.split('/')[-1][:-5].replace('_', '/', 1)
    s = d.get('score')
    if not s or not s.get('components'):
        continue
    top = max(s['components'], key=lambda c: c['deducted'])
    rows.append((s['points'], s['grade'], repo, top))
rows.sort(key=lambda r: -r[0])
print(f"_Snapshot: {datetime.date.today().isoformat()}, last 100 completed runs each._\n")
print("| Repo | Grade | Score | Biggest deduction |")
print("|---|---|---|---|")
for p, g, repo, top in rows:
    print(f"| [{repo}](https://github.com/{repo}) | **{g}** | {p}/100 "
          f"| {top['name']} (−{top['deducted']:g}): {top['detail']} |")
EOF
