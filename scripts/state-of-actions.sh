#!/usr/bin/env bash
# Emits docs/state-of-actions.md to stdout: aggregate gha-doctor lint
# findings across the N most-starred repos on GitHub (default 250).
#
#   scripts/state-of-actions.sh > docs/state-of-actions.md
#   N=100 scripts/state-of-actions.sh > /tmp/small.md
#
# Static lint only (--lint-only): ~1 API request per repo + 1 per workflow
# file (60-file cap/repo), so the whole sweep fits in one authenticated
# rate-limit hour. Run-history analysis at this scale would cost ~100x more
# requests, so it's deliberately out of scope here — see docs/scoreboard.md
# for deep grades of a smaller set.
#
# Requires: gha-doctor on PATH, gh auth, jq, python3.
# Set CACHE=dir to reuse per-repo JSON between runs (resumable).
set -u

N=${N:-250}
CACHE=${CACHE:-$(mktemp -d)}
mkdir -p "$CACHE"

# ---- 1. Top-N most-starred repos (search API, 100/page) -------------------
repos_file="$CACHE/repos.txt"
if [ ! -s "$repos_file" ]; then
  pages=$(( (N + 99) / 100 ))
  for p in $(seq 1 "$pages"); do
    gh api "search/repositories?q=stars:%3E10000&sort=stars&order=desc&per_page=100&page=$p" \
      --jq '.items[].full_name'
    sleep 2 # search API secondary limit
  done | head -n "$N" >"$repos_file"
fi
total=$(wc -l <"$repos_file")
echo "sweeping $total repos (cache: $CACHE)" >&2

# ---- 2. Lint each repo remotely (no clone) --------------------------------
i=0
while read -r r; do
  i=$((i+1))
  f="$CACHE/$(echo "$r" | tr '/' '=').json"
  [ -s "$f" ] && continue
  err="$CACHE/$(echo "$r" | tr '/' '=').err"
  gha-doctor --repo "$r" --no-config --lint-only --json >"$f" 2>"$err"
  rc=$?
  case $rc in
    0|2) rm -f "$err" ;;
    1) # commonly "no .github/workflows" — record and move on
       echo '{"no_workflows": true}' >"$f"
       grep -q "no .github/workflows" "$err" || \
         { echo "warn: $r: $(tail -1 "$err")" >&2; echo '{"error": true}' >"$f"; }
       rm -f "$err" ;;
    *) echo "warn: $r failed (exit $rc): $(tail -1 "$err")" >&2
       echo '{"error": true}' >"$f" ;;
  esac
  [ $((i % 25)) -eq 0 ] && echo "  [$i/$total]" >&2
done <"$repos_file"

# ---- 3. Aggregate → markdown ---------------------------------------------
VERSION=$(gha-doctor --version 2>/dev/null | head -1) \
python3 - "$CACHE" <<'EOF'
import collections, datetime, glob, json, os, statistics, sys

cache = sys.argv[1]
repos_scanned = with_wf = no_wf = errors = files_total = clean = 0
rule_repos = collections.Counter()   # rule -> repos with >=1 hit
rule_hits = collections.Counter()    # rule -> total findings
per_repo = []                        # (repo, files, findings)
clean_repos = []
examples = collections.defaultdict(list)  # rule -> [(count, repo)]

for f in sorted(glob.glob(cache + '/*=*.json')):
    repo = os.path.basename(f)[:-5].replace('=', '/')
    d = json.load(open(f))
    repos_scanned += 1
    if d.get('no_workflows'):
        no_wf += 1
        continue
    if d.get('error'):
        errors += 1
        continue
    files = d.get('files_scanned', 0)
    finds = d.get('findings') or []
    if files == 0:
        no_wf += 1
        continue
    with_wf += 1
    files_total += files
    per_repo.append((repo, files, len(finds)))
    if not finds:
        clean += 1
        clean_repos.append(repo)
    c = collections.Counter(x['rule'] for x in finds)
    for rule, n in c.items():
        rule_repos[rule] += 1
        rule_hits[rule] += n
        examples[rule].append((n, repo))

RULES = {
 'D001': 'no concurrency cancel-in-progress (superseded PR runs keep running)',
 'D002': 'job without timeout-minutes (a hang bills the 6h default)',
 'D003': 'setup-* without dependency caching',
 'D004': 'fetch-depth: 0 full-history clone where history is unused',
 'D005': 'cron firing more often than every 15 min',
 'D006': 'macOS/Windows (2-10x cost) job on every push',
 'D007': 'docker build without layer caching',
 'D008': 'cache key without restore-keys prefix fallback',
 'D009': 'continue-on-error masking real failures',
 'D010': 'artifact upload on default 90-day retention',
 'D011': 'static matrix expanding to 20+ jobs per trigger',
 'D012': 'npm install instead of npm ci in CI',
 'D013': 'unscoped push + pull_request double-trigger',
 'D014': 'cron pinned to minute 0 (GitHub peak-load delays/drops)',
 'D015': 'action version that has been shut down',
 'D016': 'retired runner label',
 'D017': 'no automation updating action pins (dependabot/renovate)',
 'D018': 'deprecated ::set-output/::save-state/::set-env workflow commands',
 'D019': 'published action.yml on a deprecated Node runtime (node20 removal: fall 2026)',
 'D020': 'runner label with an announced retirement (ubuntu-22.04, macos-14)',
 'D021': 'scheduled workflow with no github.repository guard (crons run in forks)',
}
NAMES = {
 'D001': 'MissingConcurrencyCancellation', 'D002': 'NoJobTimeout',
 'D003': 'UncachedSetupAction', 'D004': 'FullFetchDepth',
 'D005': 'HighFrequencyCron', 'D006': 'ExpensiveRunnerOnEveryPush',
 'D007': 'DockerBuildWithoutLayerCache', 'D008': 'CacheWithoutRestoreKeys',
 'D009': 'ContinueOnErrorMasksFailures', 'D010': 'DefaultArtifactRetention',
 'D011': 'LargeMatrixOnPRs', 'D012': 'NpmInstallInCI',
 'D013': 'PushAndPullRequestDoubleRun', 'D014': 'TopOfHourCron',
 'D015': 'RetiredActionVersion', 'D016': 'RetiredRunnerLabel',
 'D017': 'NoActionsUpdateAutomation', 'D018': 'DeprecatedWorkflowCommand',
 'D019': 'DeprecatedActionRuntime', 'D020': 'DeprecatingRunnerLabel',
 'D021': 'UnguardedCron',
}
def anchor(rule):
    return (rule + '-' + NAMES.get(rule, '')).lower()
date = datetime.date.today().isoformat()
ver = os.environ.get('VERSION', 'gha-doctor')
densities = [n / f for _, f, n in per_repo]
med_files = statistics.median(f for _, f, _ in per_repo)

print(f"""# The state of GitHub Actions hygiene in top open-source repos

*Generated {date} by [`{ver}`](https://github.com/linnea-bakshi/gha-doctor)
via `scripts/state-of-actions.sh` — static workflow lint of the
**{repos_scanned} most-starred repos on GitHub**, fetched through the
contents API (no clones). Numbers change as repos change; regenerate any
time. Runtime sequel: [the CI waste ledger](waste-study.md) — what these
repos' runs actually burned.*

## Headline numbers

| | |
|---|---|
| Repos swept | **{repos_scanned}** |
| … with GitHub Actions workflows | **{with_wf}** ({with_wf/max(repos_scanned-errors,1):.0%}) |
| … that lint completely clean | **{clean}** of {with_wf} ({clean/max(with_wf,1):.0%}) |
| Workflow + action-manifest files linted | **{files_total}** (median {med_files:.0f}/repo) |
| Total findings | **{sum(rule_hits.values())}** |
| Median findings per file | **{statistics.median(densities):.1f}** |
""")
if errors:
    print(f"({errors} repos skipped: API errors during the sweep)\n")

print("""## Findings by rule

![Share of the top-250 GitHub repos affected, by rule](img/state-of-actions.svg)

Sorted by how many repos are affected. "Repos" is out of the {} repos
that have workflows.

| Rule | What it flags | Repos | % | Findings |
|------|---------------|------:|--:|---------:|""".format(with_wf))
for rule, nrepos in rule_repos.most_common():
    print(f"| [{rule}](rules.md#{anchor(rule)}) | {RULES.get(rule, '')} "
          f"| {nrepos} | {nrepos/max(with_wf,1):.0%} | {rule_hits[rule]} |")

print("\n## Notable\n")
bullets = []
if clean_repos:
    bullets.append(
        f"Only **{len(clean_repos)}** of {with_wf} repos lint completely "
        f"clean: " + ', '.join(f'`{r}`' for r in sorted(clean_repos)[:5]) +
        ('…' if len(clean_repos) > 5 else '') + ".")
else:
    nearest = sorted(per_repo, key=lambda x: x[2])[:3]
    bullets.append(
        f"**No repo lints completely clean** under the current rule set. "
        f"Closest: " + ', '.join(f'`{r}` ({n} finding{"s"*(n!=1)})'
                                 for r, _, n in nearest) + ".")
if rule_repos.get('D002'):
    top = max(examples['D002'])
    bullets.append(
        f"**{rule_repos['D002']/max(with_wf,1):.0%} of repos have jobs with no "
        f"`timeout-minutes`** ({rule_hits['D002']} jobs). A hung job bills the "
        f"full 6-hour default before dying. Largest single repo: `{top[1]}` "
        f"with {top[0]}.")
if rule_repos.get('D015') or rule_repos.get('D016'):
    dead = sorted(set(r for _, r in examples['D015'] + examples['D016']))
    bullets.append(
        f"**{len(dead)} repos still reference shut-down infrastructure** — "
        f"artifact/cache action versions that GitHub turned off, or runner "
        f"labels that no longer exist (D015/D016): " +
        ', '.join(f'`{r}`' for r in dead[:8]) + ('…' if len(dead) > 8 else '') + ".")
if rule_repos.get('D018'):
    ex = sorted(examples['D018'], reverse=True)[:3]
    bullets.append(
        f"**{rule_repos['D018']} repos still emit deprecated workflow "
        f"commands** (`::set-output`/`::save-state`, deprecated Oct 2022 with "
        f"removal announced; D018, {rule_hits['D018']} findings): " +
        ', '.join(f'`{r}`' for _, r in ex) +
        ('…' if rule_repos['D018'] > 3 else '') + ".")
if rule_repos.get('D019'):
    ex = sorted(examples['D019'], reverse=True)[:3]
    bullets.append(
        f"**{rule_repos['D019']} repos publish in-repo actions on a "
        f"deprecated Node runtime** (`runs.using: node20`; GitHub has "
        f"announced Node 20's removal from runners in fall 2026 — these "
        f"actions stop working then; D019, {rule_hits['D019']} manifests): " +
        ', '.join(f'`{r}`' for _, r in ex) +
        ('…' if rule_repos['D019'] > 3 else '') + ".")
if rule_repos.get('D020'):
    ex = sorted(examples['D020'], reverse=True)[:3]
    bullets.append(
        f"**{rule_repos['D020']} repos run jobs on runner images with an "
        f"announced retirement date** (`ubuntu-22.04` brownouts start "
        f"Sept 2026, removal Apr 2027; `macos-14` removal Nov 2026; D020, "
        f"{rule_hits['D020']} jobs): " +
        ', '.join(f'`{r}`' for _, r in ex) +
        ('…' if rule_repos['D020'] > 3 else '') + ".")
if rule_repos.get('D017'):
    bullets.append(
        f"**{rule_repos['D017']/max(with_wf,1):.0%} of repos have no "
        f"automation updating their action pins** (no dependabot "
        f"`github-actions` ecosystem, no renovate; D017) — pins rot until "
        f"they hit shut-down versions like the D015/D016 cases above.")
if rule_repos.get('D013'):
    ex = sorted(examples['D013'], reverse=True)[:3]
    bullets.append(
        f"**{rule_repos['D013']} repos run every PR's CI twice** (unscoped "
        f"`push` + `pull_request` on the same workflow, D013): " +
        ', '.join(f'`{r}`' for _, r in ex) + ".")
if rule_repos.get('D001'):
    bullets.append(
        f"**{rule_repos['D001']/max(with_wf,1):.0%} of repos have workflows "
        f"with no `concurrency` group** (D001), so pushing a fix to a PR "
        f"doesn't cancel the now-obsolete run.")
worst = sorted(per_repo, key=lambda x: -x[2])[:5]
if worst:
    bullets.append("Most findings in one repo: " +
        ', '.join(f'`{r}` ({n})' for r, f, n in worst) + ".")
for b in bullets:
    print(f"- {b}")

print(f"""
## Method & honesty

- **Static lint only.** No run-history, cost, or cache analysis here —
  that needs ~100x more API requests per repo. [The scoreboard](scoreboard.md)
  does the deep version for a smaller set.
- Workflows fetched via the contents API (60-file cap per repo; {sum(1 for _, f, _ in per_repo if f >= 60)} repos hit the cap).
- **A finding is not a bug.** Rules flag *defaults that cost money or
  reliability when left unconsidered*. Big projects may have decided the
  default is fine — inline `# gha-doctor: ignore[Dxxx]` suppressions are
  counted as clean.
- Repo set = most-starred overall, so it includes docs/list repos; the
  "with workflows" row is the real denominator.
- Reproduce: `N={repos_scanned} scripts/state-of-actions.sh > docs/state-of-actions.md`
  (needs `gh` auth; ~15 min; add `CACHE=dir` to make it resumable), then
  `scripts/soa-chart.py $CACHE > docs/img/state-of-actions.svg` for the chart.

*This page is produced by gha-doctor, an open-source CLI built and
maintained by an AI agent ([Linnea Bakshi](https://github.com/linnea-bakshi)).
Run it on your own repo: `brew install linnea-bakshi/tap/gha-doctor` or
`gh extension install linnea-bakshi/gh-doctor`.*
""")
EOF
