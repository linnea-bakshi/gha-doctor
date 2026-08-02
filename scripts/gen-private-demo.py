#!/usr/bin/env python3
"""Regenerate docs/img/private-repo.svg — the README private-repo screenshot.

Stdlib-only. Renders a *static* SVG of a curated excerpt from a real capture
against the private testbed repo (linnea-bakshi/gd-private-testbed): flaky job
+ named flaky test (from private job logs), log-measured cache hit rate, and
the health score — the parts of the pipeline that need Actions:read on a
private repo. Shares the ANSI parser + palette with gen-demo-anim.py.

Usage:
    FORCE_COLOR=1 gha-doctor --repo linnea-bakshi/gd-private-testbed \
        --flaky-logs 4 --cache-logs 10 > /tmp/priv-demo.ansi
    python3 scripts/gen-private-demo.py /tmp/priv-demo.ansi docs/img/private-repo.svg

If the CLI's output shape changes enough that a marker below no longer
matches, this fails loudly — update the markers.
"""
import html
import importlib.util
import os
import re
import sys

_spec = importlib.util.spec_from_file_location(
    "demoanim", os.path.join(os.path.dirname(os.path.abspath(__file__)), "gen-demo-anim.py"))
_anim = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_anim)
parse_ansi, visible_len = _anim.parse_ansi, _anim.visible_len
FG, BG, DIM, SGR = _anim.FG, _anim.BG, _anim.DIM, _anim.SGR


def curate(raw):
    def find(pat):
        for i, ln in enumerate(raw):
            if re.search(pat, SGR.sub("", ln)):
                return i
        raise SystemExit(f"marker not found in capture: {pat}")

    out = ["\x1b[2m$\x1b[0m \x1b[1mgha-doctor --repo linnea-bakshi/gd-private-testbed"
           " --flaky-logs 4 --cache-logs 10\x1b[0m", ""]
    i = find(r"^Flaky jobs")
    out += raw[i:i + 3] + [""]
    i = find(r"^Flaky tests")
    out += raw[i:i + 3] + [""]
    i = find(r"^Cache hit rate")
    out += raw[i:i + 2] + [""]
    i = find(r"^Health score")
    out += raw[i:i + 2]
    # sanity: the excerpt must actually demo the private-log features
    joined = SGR.sub("", "\n".join(out))
    for must in ("test_flaky.py::test_sometimes_fails", "hit rate", "sampled job logs"):
        if must not in joined:
            raise SystemExit(f"expected content missing from excerpt: {must}")
    return out


def build(lines, out, font_size=12.5):
    cols = max(visible_len(l) for l in lines)
    cw = font_size * 0.602
    lh = font_size * 1.55
    pad_x, pad_top, pad_bot = 18, 44, 16
    width = round(pad_x * 2 + cols * cw)
    height = round(pad_top + pad_bot + len(lines) * lh)

    body = [
        f'<rect width="{width}" height="{height}" rx="9" fill="{BG}"/>',
        f'<rect width="{width}" height="{height}" rx="9" fill="none" stroke="#30363d"/>',
    ]
    for cx, col in [(31, "#ff5f57"), (51, "#febc2e"), (71, "#28c840")]:
        body.append(f'<circle cx="{cx}" cy="22" r="6" fill="{col}"/>')
    body.append(f'<text x="{width / 2}" y="26" text-anchor="middle" class="t" fill="{DIM}">'
                f'gha-doctor · private repo</text>')
    for idx, line in enumerate(lines):
        y = pad_top + (idx + 0.8) * lh
        tspans = "".join(
            f'<tspan fill="{f}"{" class=\"b\"" if b else ""}>{html.escape(txt)}</tspan>'
            for txt, f, b in parse_ansi(line))
        body.append(f'<text x="{pad_x}" y="{y:.1f}" class="t">{tspans}</text>')

    css = (f".t {{ font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, "
           f"'Liberation Mono', monospace; font-size: {font_size}px; white-space: pre; }}\n"
           f".b {{ font-weight: 600; }}")
    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" '
           f'font-size="{font_size}" role="img" '
           f'aria-label="gha-doctor analyzing a private repo: flaky job and flaky test named '
           f'from private job logs, cache hit rate, health score">\n'
           f"<style>{css}</style>\n" + "\n".join(body) + "\n</svg>\n")
    with open(out, "w") as f:
        f.write(svg)
    print(f"wrote {out}: {width}x{height}, {len(lines)} lines, {len(svg)} bytes")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    build(curate(open(sys.argv[1]).read().split("\n")), sys.argv[2])
