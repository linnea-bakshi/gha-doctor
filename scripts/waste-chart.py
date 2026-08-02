#!/usr/bin/env python3
"""Render docs/img/waste-study.svg from a waste-study-collect.sh cache dir.

Usage: scripts/waste-chart.py CACHE_DIR > docs/img/waste-study.svg

Reads the same per-repo JSON cache that scripts/waste-study.sh aggregates,
so the chart and the page always describe the same sweep. Repos with under
30 sampled compute-minutes are excluded (shares would be noise). Stdlib
only — no matplotlib, runs anywhere Python 3 does.
"""
import datetime
import glob
import json
import os
import statistics
import sys

MIN_COMPUTE_MIN = 30


def collect(cache):
    fail_share, round_share = [], []
    for f in sorted(glob.glob(cache + '/*=*.json')):
        try:
            d = json.load(open(f))
        except Exception:
            continue
        a = d.get('analysis') or {}
        cost = a.get('cost') or {}
        waste = a.get('waste') or {}
        compute = waste.get('compute_minutes') or 0
        billable = cost.get('billable_minutes') or 0
        if compute < MIN_COMPUTE_MIN or not billable:
            continue
        fail_share.append(100.0 * (waste.get('total_minutes') or 0) / compute)
        round_share.append(100.0 * (cost.get('rounding_minutes') or 0) / billable)
    return fail_share, round_share


def buckets(vals, width=10, cap=60):
    """[0,10) [10,20) … [cap,100] buckets of percentage values."""
    n = cap // width + 1
    out = [0] * n
    for v in vals:
        out[min(int(v // width), n - 1)] += 1
    return out


def panel(out, x0, y0, title, vals, accent):
    W, H = 400, 210          # panel plot area incl. axis labels
    bs = buckets(vals)
    top = max(bs) or 1
    bw = (W - 30) // len(bs)
    med = statistics.median(vals)
    out.append(f'<text x="{x0}" y="{y0}" class="pt">{title}</text>')
    out.append(f'<text x="{x0}" y="{y0 + 19}" class="s">median '
               f'{med:.0f}% \u00b7 {len(vals)} repos</text>')
    base = y0 + H - 26
    scale = (H - 78) / top
    for i, b in enumerate(bs):
        bx = x0 + i * bw
        bh = max(2, round(b * scale)) if b else 0
        if b:
            out.append(f'<rect x="{bx}" y="{base - bh}" width="{bw - 6}" '
                       f'height="{bh}" rx="2" fill="{accent}" '
                       f'fill-opacity="0.85"/>')
            out.append(f'<text x="{bx + (bw - 6) // 2}" y="{base - bh - 5}" '
                       f'class="n" text-anchor="middle">{b}</text>')
        lab = f'{i * 10}' if i < len(bs) - 1 else '60+'
        out.append(f'<text x="{bx}" y="{base + 16}" class="n">{lab}</text>')
    out.append(f'<line x1="{x0}" y1="{base}" x2="{x0 + W - 30}" y2="{base}" '
               f'stroke="#d0d7de"/>')
    out.append(f'<text x="{x0}" y="{base + 32}" class="n">% \u2192</text>')


def main():
    if len(sys.argv) != 2 or not os.path.isdir(sys.argv[1]):
        sys.exit(__doc__.strip())
    fail_share, round_share = collect(sys.argv[1])
    if not fail_share:
        sys.exit('no usable repo JSON in cache dir')

    W, H = 900, 330
    date = datetime.date.today().isoformat()
    out = []
    out.append(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" '
               f'height="{H}" viewBox="0 0 {W} {H}" role="img" '
               f'aria-label="Distribution of CI waste across top-starred '
               f'GitHub repos">')
    out.append('<style>text{font-family:-apple-system,"Segoe UI",Helvetica,'
               'Arial,sans-serif}.t{font-size:19px;font-weight:600;'
               'fill:#24292f}.pt{font-size:14px;font-weight:600;fill:#24292f}'
               '.s{font-size:12.5px;fill:#57606a}'
               '.n{font-size:11.5px;fill:#57606a}</style>')
    out.append(f'<rect width="{W}" height="{H}" fill="#ffffff"/>')
    out.append('<text x="24" y="32" class="t">How much of top-repo CI '
               'compute is waste?</text>')
    out.append(f'<text x="24" y="54" class="s">last \u2264100 completed runs '
               f'per repo \u00b7 repos with \u2265{MIN_COMPUTE_MIN} sampled '
               f'compute-minutes \u00b7 {date}</text>')
    panel(out, 40, 100, 'compute spent in failed runs + retries',
          fail_share, '#c9463d')
    panel(out, 490, 100, 'billable minutes created by per-job round-up',
          round_share, '#157878')
    out.append(f'<text x="24" y="{H - 10}" class="n">repo count per 10-point '
               'bucket \u00b7 github.com/linnea-bakshi/gha-doctor \u00b7 '
               'method: linnea-bakshi.github.io/gha-doctor/waste-study</text>')
    out.append('</svg>')
    print('\n'.join(out))


if __name__ == '__main__':
    main()
