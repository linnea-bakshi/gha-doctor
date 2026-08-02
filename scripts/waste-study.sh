#!/usr/bin/env bash
# Aggregates the per-repo history analyses collected by
# scripts/waste-study-collect.sh into docs/waste-study.md.
#
# Usage:
#   scripts/waste-study-collect.sh          # (hours; paced, resumable)
#   scripts/waste-study.sh > docs/waste-study.md
set -u
CACHE="${CACHE:-/tmp/waste-cache}"
[ -d "$CACHE" ] || { echo "cache dir $CACHE missing; run waste-study-collect.sh first" >&2; exit 1; }

VERSION=$(gha-doctor --version 2>/dev/null | head -1) \
python3 - "$CACHE" <<'EOF'
import collections, datetime, glob, json, os, statistics, sys

cache = sys.argv[1]

def num(x):
    return x if isinstance(x, (int, float)) else 0

repos = []          # per-repo dict of extracted numbers
errored = 0
for f in sorted(glob.glob(cache + '/*=*.json')):
    name = os.path.basename(f)[:-5].replace('=', '/')
    try:
        d = json.load(open(f))
    except Exception:
        errored += 1
        continue
    a = d.get('analysis')
    if not a or not a.get('runs_sampled'):
        errored += 1
        continue
    since = a.get('since')
    window_days = None
    if since:
        try:
            t = datetime.datetime.fromisoformat(since.replace('Z', '+00:00'))
            window_days = (datetime.datetime.now(datetime.timezone.utc) - t).total_seconds() / 86400
        except ValueError:
            pass
    cost = a.get('cost') or {}
    waste = a.get('waste') or {}
    sup = a.get('superseded') or {}
    r = {
        'repo': name,
        'runs': a['runs_sampled'],
        'window_days': window_days,
        'billable_min': num(cost.get('billable_minutes')),
        'est_usd': num(cost.get('estimated_usd')),
        'wasted_usd': num(cost.get('wasted_usd')),
        'rounding_min': num(cost.get('rounding_minutes')),
        'rounding_usd': num(cost.get('rounding_usd')),
        'self_hosted_jobs': num(cost.get('self_hosted_jobs')),
        'waste_min': num(waste.get('total_minutes')),
        'compute_min': num(waste.get('compute_minutes')),
        'sup_pr_runs': num(sup.get('pr_runs')),
        'sup_completed': num(sup.get('completed')),
        'sup_cancelled': num(sup.get('cancelled')),
        'sup_min': num(sup.get('wasted_minutes')),
        'sup_usd': num(sup.get('wasted_usd')),
        'flaky_jobs': a.get('flaky_jobs') or [],
        'flaky_tests': (a.get('flaky_tests') or {}).get('tests') or [],
        'zombies': a.get('zombie_crons') or [],
        'pr_feedback': a.get('pr_feedback'),
    }
    repos.append(r)

n = len(repos)
if n == 0:
    sys.exit('no analyzable repos in cache')

tot_runs = sum(r['runs'] for r in repos)
tot_billable = sum(r['billable_min'] for r in repos)
tot_usd = sum(r['est_usd'] for r in repos)
tot_waste_min = sum(r['waste_min'] for r in repos)
tot_wasted_usd = sum(r['wasted_usd'] for r in repos)
tot_round_min = sum(r['rounding_min'] for r in repos)
tot_round_usd = sum(r['rounding_usd'] for r in repos)
tot_compute = sum(r['compute_min'] for r in repos)
windows = [r['window_days'] for r in repos if r['window_days']]
med_window = statistics.median(windows)

sup_completed = sum(r['sup_completed'] for r in repos)
sup_cancelled = sum(r['sup_cancelled'] for r in repos)
sup_min = sum(r['sup_min'] for r in repos)
sup_usd = sum(r['sup_usd'] for r in repos)
sup_repos = [r for r in repos if r['sup_completed'] > 0]

flaky_repos = [r for r in repos if r['flaky_jobs']]
flaky_waste_min = sum(num(j.get('wasted_minutes')) for r in repos for j in r['flaky_jobs'])
named_repos = [r for r in repos if r['flaky_tests']]

zombie_repos = [r for r in repos if r['zombies']]
zombie_min_mo = sum(num(z.get('est_min_per_month')) for r in repos for z in r['zombies'])

fb = [r for r in repos if r['pr_feedback']]

date = datetime.date.today().isoformat()
ver = os.environ.get('VERSION', 'gha-doctor')

def rw(k):
    return f"{k} repo" + ("" if k == 1 else "s")

def pct(a, b):
    return f"{100*a/b:.0f}%" if b else "–"

def days(x):
    return f"{x:.0f}d" if x and x >= 1 else "<1d"

print(f"""# The CI waste ledger: what the most-starred repos burn on GitHub Actions

*Generated {date} by [`{ver}`](https://github.com/linnea-bakshi/gha-doctor)
via `scripts/waste-study-collect.sh` + `scripts/waste-study.sh` — the full
gha-doctor run-history analysis (last ≤100 completed runs each) of the
GitHub repos with the most stars.*

This is the runtime sequel to the
[static hygiene study](state-of-actions.md): not what the workflows *say*,
but what the runs actually *did* — failures, retries, proven-flaky jobs,
schedules failing unattended, superseded PR runs that kept going, and the
per-job round-up on every billable minute.

**How to read the dollars:** priced at GitHub's hosted-runner list rates
($0.008/min Linux, 2x Windows, 10x macOS; self-hosted excluded). Public
repos don't pay for standard runners, so read $ as the market value of the
compute (and the queue time contributors wait behind it), not an invoice.
Every number below is an observation over the sampled window — no
extrapolation.

## The ledger

Across **{n} repos** with analyzable run history ({tot_runs:,} completed
runs; median sample window {days(med_window)}):

| | observed | share |
|---|---|---|
| Compute in sampled runs | {tot_compute:,.0f} min ({tot_billable:,.0f} billable · ~${tot_usd:,.0f}) | |
| Spent on runs that failed, or retries | {tot_waste_min:,.0f} min (~${tot_wasted_usd:,.0f}) | {pct(tot_waste_min, tot_compute)} of compute |
| Per-job round-up to whole minutes | {tot_round_min:,.0f} min (~${tot_round_usd:,.0f}) | {pct(tot_round_min, tot_billable)} of billable |
| Superseded PR runs that ran to completion anyway | {sup_completed:,} runs · {sup_min:,.0f} min (~${sup_usd:,.0f}) | {rw(len(sup_repos))} |
| Burned by proven-flaky jobs (same-commit fail→pass) | {flaky_waste_min:,.0f} min | {len(flaky_repos)} of {n} repos have ≥1 |
| Dead scheduled workflows (unbroken failure streaks) | ~{zombie_min_mo:,.0f} min/month if left running | {rw(len(zombie_repos))} |
""")

# ---- failure waste top list ----------------------------------------------
top_waste = sorted(repos, key=lambda r: -r['waste_min'])[:10]
print("""## Where failed-run compute concentrates

Top repos by minutes spent inside runs that ended in failure (or retries),
in their own sampled window:

| repo | failed-run + retry min | window | share of its compute |
|---|---|---|---|""")
for r in top_waste:
    print(f"| {r['repo']} | {r['waste_min']:,.0f} | {days(r['window_days'])} | {pct(r['waste_min'], r['compute_min'])} |")

# ---- flaky ---------------------------------------------------------------
print(f"""
## Flaky, with names

A job is counted flaky only when the same commit both failed and passed it —
retry-proven, not guessed. **{len(flaky_repos)} of {n} repos** ({pct(len(flaky_repos), n)})
have at least one. Where the failure logs contain a recognizable test-framework
summary (23 framework families understood), the flaky *test* gets named:
""")
showcase = []
for r in named_repos:
    for t in r['flaky_tests'][:2]:
        showcase.append((t.get('failures', 0), r['repo'], t))
showcase.sort(key=lambda x: -x[0])
if showcase:
    print("| repo | flaky test | framework | failed logs |")
    print("|---|---|---|---|")
    for fails, repo, t in showcase[:12]:
        name = t.get('name', '?').replace('|', '\\|')
        if len(name) > 70:
            name = name[:67] + '…'
        print(f"| {repo} | `{name}` | {t.get('framework','?')} | {fails} |")
else:
    print("*(no flaky failures produced recognizable test summaries in this sweep)*")

# ---- zombies -------------------------------------------------------------
print(f"""
## Zombie crons

Scheduled workflows whose most recent sampled runs are an unbroken failure
streak (≥5 consecutive, spanning ≥3 days) — failing on a timer with nobody
watching. Found in **{rw(len(zombie_repos))}**:

| repo | workflow | consecutive failures | streak span |
|---|---|---|---|""")
zrows = []
for r in zombie_repos:
    for z in r['zombies']:
        zrows.append((num(z.get('consecutive_failures')), r['repo'], z))
zrows.sort(key=lambda x: -x[0])
for cf, repo, z in zrows[:12]:
    edge = '≥' if z.get('streak_reaches_sample_edge') else ''
    print(f"| {repo} | {z.get('workflow','?')} | {edge}{cf} | {num(z.get('span_days')):.0f}d |")

# ---- superseded ----------------------------------------------------------
adopters = sum(1 for r in repos if r['sup_cancelled'] > 0 and r['sup_completed'] == 0)
print(f"""
## Superseded PR runs

When a PR gets a new push, the old push's runs are obsolete. Repos with
`concurrency: cancel-in-progress` stop them; the rest let them run to
completion. In this sweep: **{sup_cancelled:,} superseded runs were cancelled
in time** and **{sup_completed:,} ran to completion anyway** — {sup_min:,.0f}
minutes past the moment they stopped mattering (~${sup_usd:,.0f}).
{rw(len(sup_repos))} paid that; {rw(adopters)} cancelled every superseded
run in their sample. The fix is
[one `concurrency` block](https://linnea-bakshi.github.io/gha-doctor/rules#d001-missingconcurrencycancellation)
(`gha-doctor --fix` writes it).
""")
if sup_repos:
    print("| repo | superseded runs completed | min past supersession |")
    print("|---|---|---|")
    for r in sorted(sup_repos, key=lambda r: -r['sup_min'])[:10]:
        print(f"| {r['repo']} | {r['sup_completed']} | {r['sup_min']:,.0f} |")

# ---- PR feedback ---------------------------------------------------------
if fb:
    p50s = sorted(fb, key=lambda r: -num(r['pr_feedback'].get('p50_minutes')))
    med_p50 = statistics.median(num(r['pr_feedback'].get('p50_minutes')) for r in fb)
    print(f"""
## How long contributors wait

Median wall-clock from a PR push to its last check finishing (queue
included), where enough clean pushes existed to measure ({len(fb)} repos):
**median-of-medians {med_p50:.0f} min**. The slowest:

| repo | p50 wait | p95 | what gates it |
|---|---|---|---|""")
    for r in p50s[:10]:
        p = r['pr_feedback']
        gate = ''
        gw = p.get('gating_workflows') or []
        if gw:
            g = gw[0]
            gate = f"{g.get('workflow','?')} ({pct(num(g.get('share')), 1)} of pushes)"
        print(f"| {r['repo']} | {num(p.get('p50_minutes')):.0f} min | {num(p.get('p95_minutes')):.0f} min | {gate} |")

print(f"""
## Method, honestly

- Sample: the last ≤100 completed workflow runs per repo (unfiltered,
  provably-current listing), fetched {date}. Windows differ per repo —
  busy repos cover a day, quiet ones months — so **nothing here is
  annualized or extrapolated**; sums are sums over the samples.
- "Failed-run minutes" count all compute inside runs whose conclusion was
  failure, plus re-run attempts. Some failure is the point of CI — the
  interesting part is where it concentrates and repeats.
- Flaky = the same head commit both failed and passed the same job.
  Flaky-test names come only from framework failure summaries the analyzer
  recognizes; build/infra failures are never counted as named tests.
- Zombie crons need ≥5 consecutive scheduled failures spanning ≥3 days;
  skipped/cancelled runs are neutral. Their per-month figure is the only
  forward-looking number on this page and assumes nobody intervenes (the
  point is that nobody has).
- Superseded = an earlier PR-event run of the same branch obsoleted by a
  newer push before it finished; only its minutes *after* the supersession
  moment count. Same-SHA re-runs are excluded.
- Every gate has exact thresholds in
  [docs/honesty.md](https://linnea-bakshi.github.io/gha-doctor/honesty).
  Reproduce: `scripts/waste-study-collect.sh` then `scripts/waste-study.sh`.

*This page is produced by gha-doctor, an open-source CLI built and
maintained by an AI agent (Linnea Bakshi). Run the same analysis on your
own repo: `brew install linnea-bakshi/tap/gha-doctor` or
`go install github.com/linnea-bakshi/gha-doctor/cmd/gha-doctor@latest`,
then `gha-doctor --repo you/yours`.*
""")
EOF
