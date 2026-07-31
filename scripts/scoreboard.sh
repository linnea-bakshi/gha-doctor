#!/usr/bin/env bash
# Emits docs/scoreboard.md to stdout: gha-doctor grades for a set of
# well-known repos whose primary CI runs on GitHub Actions.
#   scripts/scoreboard.sh > docs/scoreboard.md
#   scripts/scoreboard.sh owner/repo other/repo > /tmp/custom.md
# Requires: gha-doctor on PATH, gh auth (or GITHUB_TOKEN), python3, ~20 API
# requests per repo. Regenerated weekly by .github/workflows/scoreboard.yml.
#
# Deliberately absent: golang/go (LUCI), kubernetes/kubernetes (Prow),
# ansible/ansible (Azure Pipelines) — their real CI isn't GitHub Actions,
# so grading their incidental Actions runs would be misleading.
set -u

REPOS=${@:-"python/cpython pytorch/pytorch rust-lang/rust nodejs/node \
cli/cli microsoft/typescript microsoft/vscode home-assistant/core \
apache/airflow sveltejs/svelte prometheus/prometheus facebook/react \
vitejs/vite vercel/next.js denoland/deno grafana/grafana vuejs/core \
angular/angular huggingface/transformers pandas-dev/pandas django/django \
astral-sh/uv pola-rs/polars"}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for r in $REPOS; do
  f="$tmp/$(echo "$r" | tr '/' '=').json"
  gha-doctor --repo "$r" --no-config --json >"$f" 2>"$f.err"
  rc=$?
  if [ $rc -gt 2 ]; then # exit 2 just means findings; >2 is a real failure
    echo "warn: $r failed (exit $rc): $(tail -1 "$f.err")" >&2
    rm -f "$f"
  fi
done

VERSION=$(gha-doctor --version 2>/dev/null | head -1) \
python3 - "$tmp" <<'EOF'
import collections, datetime, glob, json, os, sys

rows, rule_counts = [], collections.Counter()
for f in sorted(glob.glob(sys.argv[1] + '/*.json')):
    d = json.load(open(f))
    repo = f.split('/')[-1][:-5].replace('=', '/')
    for fi in d.get('findings') or []:
        rule_counts[fi['rule']] += 1
    s = d.get('score')
    if not s or not s.get('components'):
        continue
    top = max(s['components'], key=lambda c: c['deducted'])
    rows.append((s['points'], s['grade'], repo, top))
rows.sort(key=lambda r: (-r[0], r[2]))

print("""# CI health scoreboard

How do the workflows of some of the most-starred repos on GitHub grade
under [`gha-doctor`](https://github.com/linnea-bakshi/gha-doctor)? A
point-in-time snapshot, regenerated weekly by
[`scoreboard.yml`](https://github.com/linnea-bakshi/gha-doctor/blob/main/.github/workflows/scoreboard.yml)
running
[`scripts/scoreboard.sh`](https://github.com/linnea-bakshi/gha-doctor/blob/main/scripts/scoreboard.sh)
— every number is reproducible with one command against public data, no
clone needed:

```console
$ gha-doctor --repo facebook/react
```""")
ver = os.environ.get('VERSION') or 'unknown version'
print(f"\n_Snapshot: {datetime.date.today().isoformat()} · {ver} · last 100"
      " completed runs per repo._\n")
print("| Repo | Grade | Score | Biggest deduction |")
print("|---|---|---|---|")
for p, g, repo, top in rows:
    print(f"| [{repo}](https://github.com/{repo}) | **{g}** | {p}/100 "
          f"| {top['name']} (−{top['deducted']:g}): {top['detail']} |")

common = ", ".join(f"{r} ×{n}" for r, n in rule_counts.most_common(3))
print(f"""
**This is not a quality ranking of these projects.** It grades one narrow
thing: how their GitHub Actions setup scores on hygiene, reliability, and
efficiency signals, [formula here](score.md). A few honest caveats:

- **A snapshot, not a trend.** Success/flakiness/waste come from the last
  100 completed runs at generation time; a bad day moves the grade.
- **Skipped and cancelled runs are not failures.** Concurrency
  auto-cancels are good practice (rule D001 recommends them) and carry no
  verdict, so they're excluded from the success rate.
- **Hygiene is density-normalized** (per workflow file), so a
  40-workflow monorepo isn't penalized for sheer volume.
- Several famous repos are *absent* because their real CI isn't GitHub
  Actions: `golang/go` (LUCI), `kubernetes/kubernetes` (Prow),
  `ansible/ansible` (Azure Pipelines). Grading their incidental Actions
  runs would be misleading.
- Most findings here are the boring, fixable kind — across these repos
  the most common were {common} ([rule reference](rules.md)). `gha-doctor
  --fix` cleans up several of these automatically.

Want the itemized deductions behind any grade?
`gha-doctor --repo owner/repo --json | jq .score` — and see the
[badge docs](score.md#badge) to put your own repo's grade in its README.
""")
EOF
