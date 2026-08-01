#!/usr/bin/env python3
"""Render docs/img/state-of-actions.svg from a state-of-actions.sh cache dir.

Usage: scripts/soa-chart.py CACHE_DIR > docs/img/state-of-actions.svg

Reads the same per-repo JSON cache that scripts/state-of-actions.sh writes
(CACHE=dir), so the chart and the page always describe the same sweep.
Stdlib only — no matplotlib, runs anywhere Python 3 does.
"""
import collections
import datetime
import glob
import html
import json
import os
import sys

# Terse per-rule labels for chart rows. The page's table carries the full
# wording; these just need to be readable at a glance.
LABELS = {
    'D001': 'no cancel-in-progress',
    'D002': 'no job timeout',
    'D003': 'uncached setup-*',
    'D004': 'fetch-depth: 0',
    'D005': 'high-frequency cron',
    'D006': 'macOS/Windows on every push',
    'D007': 'uncached docker build',
    'D008': 'cache without restore-keys',
    'D009': 'continue-on-error masking',
    'D010': '90-day artifact retention',
    'D011': '20+ job matrix',
    'D012': 'npm install in CI',
    'D013': 'push + PR double-run',
    'D014': 'cron at minute 0',
    'D015': 'shut-down action version',
    'D016': 'retired runner label',
    'D017': 'no action update automation',
    'D018': 'deprecated ::set-output etc.',
    'D019': 'deprecated action Node runtime',
}


def main():
    if len(sys.argv) != 2 or not os.path.isdir(sys.argv[1]):
        sys.exit(__doc__.strip())
    cache = sys.argv[1]

    with_wf = 0
    rule_repos = collections.Counter()
    rule_hits = collections.Counter()
    for f in sorted(glob.glob(cache + '/*=*.json')):
        d = json.load(open(f))
        if d.get('no_workflows') or d.get('error') or not d.get('files_scanned'):
            continue
        with_wf += 1
        finds = d.get('findings') or []
        c = collections.Counter(x['rule'] for x in finds)
        for rule, n in c.items():
            rule_repos[rule] += 1
            rule_hits[rule] += n
    if not with_wf:
        sys.exit('no usable repo JSON in cache dir')

    rows = sorted(rule_repos, key=lambda r: (-rule_repos[r], r))

    # Layout.
    W = 900
    LEFT = 300           # label column
    BAR_MAX = 430        # widest bar (100%)
    ROW_H = 30
    TOP = 78
    H = TOP + len(rows) * ROW_H + 34
    ACCENT = '#157878'   # cayman theme green
    date = datetime.date.today().isoformat()

    out = []
    out.append(
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
        f'viewBox="0 0 {W} {H}" role="img" aria-label="Share of top-250 '
        f'GitHub repos affected, by gha-doctor rule">')
    style = ('<style>text{font-family:-apple-system,"Segoe UI",Helvetica,'
             'Arial,sans-serif}.t{font-size:19px;font-weight:600;fill:#24292f}'
             '.s{font-size:12.5px;fill:#57606a}'
             '.id{font-size:13px;font-weight:600;fill:ACCENT}'
             '.lb{font-size:13px;fill:#24292f}'
             '.pc{font-size:13px;font-weight:600;fill:#24292f}'
             '.n{font-size:12px;fill:#57606a}</style>')
    out.append(style.replace('ACCENT', ACCENT))
    out.append(f'<rect width="{W}" height="{H}" fill="#ffffff"/>')
    out.append('<text x="24" y="32" class="t">What the top 250 GitHub repos '
               'get wrong about Actions</text>')
    out.append(f'<text x="24" y="54" class="s">share of the {with_wf} repos '
               f'with workflows that each rule flags \u00b7 '
               f'gha-doctor static lint \u00b7 {date}</text>')

    y = TOP
    for rule in rows:
        n_repos = rule_repos[rule]
        pct = 100.0 * n_repos / with_wf
        bar = max(2, round(BAR_MAX * n_repos / with_wf))
        label = html.escape(LABELS.get(rule, ''))
        cy = y + ROW_H // 2
        out.append(f'<text x="{LEFT - 12}" y="{cy + 4}" text-anchor="end">'
                   f'<tspan class="id">{rule}</tspan>'
                   f'<tspan class="lb"> {label}</tspan></text>')
        out.append(f'<rect x="{LEFT}" y="{y + 6}" width="{bar}" '
                   f'height="{ROW_H - 12}" rx="3" fill="{ACCENT}" '
                   f'fill-opacity="{0.35 + 0.65 * n_repos / with_wf:.2f}"/>')
        ptxt = f'{pct:.0f}%' if pct >= 1 else '&lt;1%'
        out.append(f'<text x="{LEFT + bar + 8}" y="{cy + 4}">'
                   f'<tspan class="pc">{ptxt}</tspan>'
                   f'<tspan class="n"> \u00b7 {rule_hits[rule]:,} finding'
                   f'{"s" if rule_hits[rule] != 1 else ""}</tspan></text>')
        y += ROW_H

    out.append(f'<text x="24" y="{H - 12}" class="n">github.com/linnea-bakshi/'
               'gha-doctor \u00b7 rule details: linnea-bakshi.github.io/'
               'gha-doctor/rules \u00b7 repos without workflows excluded</text>')
    out.append('</svg>')
    print('\n'.join(out))


if __name__ == '__main__':
    main()
