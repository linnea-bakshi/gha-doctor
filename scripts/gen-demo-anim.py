#!/usr/bin/env python3
"""Regenerate docs/img/demo-anim.svg — the animated README hero.

Stdlib-only. Converts real ANSI terminal output into a looping animated SVG
(typing + staggered line reveal). Pure CSS keyframes: no scripts, no external
assets, so it animates inside GitHub's <img>/camo sandbox.

Usage:
    FORCE_COLOR=1 gha-doctor --repo psf/requests --runs 60 > /tmp/demo-req.ansi
    python3 scripts/gen-demo-anim.py /tmp/demo-req.ansi docs/img/demo-anim.svg

The script curates a fixed excerpt (a few findings, flaky jobs, wasted compute,
grade, top wins) from the capture; if the CLI's output shape changes enough that
a marker below no longer matches, it fails loudly — update the markers.
"""
import html
import re
import sys

# GitHub-dark palette
FG = "#c9d1d9"
BG = "#0d1117"
DIM = "#8b949e"
COLORS = {31: "#f85149", 32: "#3fb950", 33: "#d29922", 36: "#39c5cf",
          34: "#58a6ff", 35: "#bc8cff", 90: DIM}

SGR = re.compile(r"\x1b\[([0-9;]*)m")


def parse_ansi(line):
    """ANSI SGR line -> list of (text, fill, bold) spans."""
    spans = []
    fill, bold, dim = FG, False, False
    pos = 0
    for m in SGR.finditer(line):
        if m.start() > pos:
            spans.append((line[pos:m.start()], DIM if dim else fill, bold))
        codes = [int(c) for c in m.group(1).split(";") if c] or [0]
        for c in codes:
            if c == 0:
                fill, bold, dim = FG, False, False
            elif c == 1:
                bold = True
            elif c == 2:
                dim = True
            elif c in COLORS:
                fill = COLORS[c]
        pos = m.end()
    if pos < len(line):
        spans.append((line[pos:], DIM if dim else fill, bold))
    return spans


def visible_len(line):
    return len(SGR.sub("", line))


def curate(raw):
    """Pick the demo excerpt out of a full report capture."""
    def find(pat):
        for i, ln in enumerate(raw):
            if re.search(pat, SGR.sub("", ln)):
                return i
        raise SystemExit(f"marker not found in capture: {pat}")

    L = {}
    L["checkup"] = raw[0]
    i = find(r"D001 .*run-tests\.yml:3"); L["d001"], L["d001fix"] = raw[i], raw[i + 1]
    L["d013"] = raw[find(r"D013 .*run-tests\.yml:3")]
    L["d003"] = raw[find(r"D003 .*run-tests\.yml:52")]
    L["summary"] = raw[find(r"\d+ warnings, \d+ suggestions")]
    L["hist"] = raw[find(r"Run history: ")]
    i = find(r"^Flaky jobs"); L["flakyh"], L["flakyc"], L["flaky1"] = raw[i], raw[i + 1], raw[i + 2]
    i = find(r"^Wasted compute"); L["wasteh"], L["waste1"], L["waste2"] = raw[i], raw[i + 1], raw[i + 2]
    i = find(r"^Health score"); L["scoreh"], L["grade"] = raw[i], raw[i + 1]
    i = find(r"^── Top wins")
    (L["winsh"], L["w1a"], L["w1b"], L["w2a"], L["w2b"], L["w3a"], L["w3b"]) = raw[i:i + 7]
    L["winsnote"] = raw[find(r"dollar wins projected")]

    dim = lambda s: f"\x1b[2m{s}\x1b[0m"
    return [
        ("type", "$ gha-doctor --repo psf/requests"),
        (2.10, ""),
        (2.10, L["checkup"]),
        (2.25, L["d001"]),
        (2.25, L["d001fix"]),
        (2.45, L["d013"]),
        (2.65, L["d003"]),
        (2.85, dim("  ⋮  (20 more findings)")),
        (3.05, L["summary"]),
        (3.90, ""),
        (3.90, L["hist"]),
        (4.40, ""),
        (4.40, L["flakyh"]),
        (4.55, L["flakyc"]),
        (4.70, L["flaky1"]),
        (5.20, ""),
        (5.20, L["wasteh"]),
        (5.35, L["waste1"]),
        (5.50, L["waste2"]),
        (6.00, ""),
        (6.00, L["scoreh"]),
        (6.15, L["grade"]),
        (6.90, ""),
        (6.90, L["winsh"]),
        (7.10, L["w1a"]),
        (7.10, L["w1b"]),
        (7.45, L["w2a"]),
        (7.45, L["w2b"]),
        (7.80, L["w3a"]),
        (7.80, L["w3b"]),
        (8.10, dim("  ⋮  (2 more wins)")),
        (8.30, L["winsnote"]),
    ]


def build(script, out, font_size=12.5, total=19.0, hold_fade=0.96):
    cols = max(visible_len(l) for _, l in script)
    cw = font_size * 0.602            # monospace advance estimate
    lh = font_size * 1.55
    pad_x, pad_top, pad_bot = 18, 44, 16
    n = len(script)
    width = round(pad_x * 2 + cols * cw)
    height = round(pad_top + pad_bot + n * lh)

    css = [
        f".t {{ font-family: ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace; font-size: {font_size}px; white-space: pre; }}",
        ".b { font-weight: 600; }",
    ]
    body = [
        f'<rect width="{width}" height="{height}" rx="9" fill="{BG}"/>',
        f'<rect width="{width}" height="{height}" rx="9" fill="none" stroke="#30363d"/>',
    ]
    for cx, col in [(31, "#ff5f57"), (51, "#febc2e"), (71, "#28c840")]:
        body.append(f'<circle cx="{cx}" cy="22" r="6" fill="{col}"/>')
    body.append(f'<text x="{width / 2}" y="26" text-anchor="middle" class="t" fill="{DIM}">gha-doctor</text>')

    fade_pct = hold_fade * 100
    for idx, (t, line) in enumerate(script):
        y = pad_top + (idx + 0.8) * lh
        typed = (t == "type")
        t0 = 0.35 if typed else float(t)
        pct = max(t0 / total, 0.001) * 100
        css.append(
            f"@keyframes kf{idx} {{ 0% {{opacity:0}} {pct:.2f}% {{opacity:0}} "
            f"{min(pct + 0.01, 99.0):.2f}% {{opacity:1}} {fade_pct:.1f}% {{opacity:1}} "
            f"{fade_pct + 2:.1f}% {{opacity:0}} 100% {{opacity:0}} }}")
        tspans = "".join(
            f'<tspan fill="{f}"{" class=\"b\"" if b else ""}>{html.escape(txt)}</tspan>'
            for txt, f, b in parse_ansi(line))
        body.append(f'<g style="animation: kf{idx} {total}s linear infinite;">'
                    f'<text x="{pad_x}" y="{y:.1f}" class="t">{tspans}</text>')
        if typed:
            # cover rect slides right in char steps, revealing the command;
            # the cursor rides the cover's left edge and hides once output starts
            vl = visible_len(line)
            cov_w = vl * cw + 4
            end_pct = (t0 / total + 1.6 / total) * 100  # typing takes 1.6s
            hide_pct = end_pct + (0.4 / total) * 100
            css.append(
                f"@keyframes type{idx} {{ 0% {{transform:translateX(0)}} {pct:.2f}% {{transform:translateX(0)}} "
                f"{end_pct:.2f}% {{transform:translateX({cov_w:.0f}px)}} 100% {{transform:translateX({cov_w:.0f}px)}} }}")
            css.append(
                f"@keyframes curhide{idx} {{ 0% {{opacity:1}} {hide_pct:.2f}% {{opacity:1}} "
                f"{hide_pct + 0.01:.2f}% {{opacity:0}} 100% {{opacity:0}} }}")
            cov_x = pad_x + 2 * cw
            body.append(
                f'<g style="animation: type{idx} {total}s steps({vl - 2}) infinite;">'
                f'<rect x="{cov_x:.0f}" y="{y - font_size:.1f}" width="{cov_w:.0f}" height="{lh:.1f}" fill="{BG}"/>'
                f'<g style="animation: curhide{idx} {total}s linear infinite;">'
                f'<rect x="{cov_x + 1:.0f}" y="{y - font_size:.1f}" width="{cw * 0.9:.1f}" height="{font_size * 1.2:.1f}" fill="{FG}"/>'
                f'</g></g>')
        body.append("</g>")

    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" '
           f'font-size="{font_size}" role="img" '
           f'aria-label="animated terminal demo: gha-doctor linting and analyzing psf/requests">\n'
           f"<style>{chr(10).join(css)}</style>\n" + "\n".join(body) + "\n</svg>\n")
    with open(out, "w") as f:
        f.write(svg)
    print(f"wrote {out}: {width}x{height}, {n} lines, {len(svg)} bytes")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    raw = open(sys.argv[1]).read().split("\n")
    build(curate(raw), sys.argv[2])
