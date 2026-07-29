package report

import (
	"fmt"
	"io"
)

// badge colors follow the shields.io convention so the badge reads
// naturally next to build/coverage badges.
func badgeColor(grade string) string {
	switch grade {
	case "A+", "A":
		return "#4c1" // brightgreen
	case "B":
		return "#97ca00" // green
	case "C":
		return "#dfb317" // yellow
	case "D":
		return "#fe7d37" // orange
	case "F":
		return "#e05d44" // red
	}
	return "#9f9f9f" // lightgrey (nothing to score)
}

// Badge writes a flat SVG badge, e.g. [ci health | A (96/100)].
// The SVG is self-contained (no external fonts fetched at render time —
// it names Verdana/DejaVu with sans-serif fallback, like shields.io) and
// uses textLength so the text fits even if the viewer's font metrics
// differ from our width estimate.
func Badge(w io.Writer, sc Score) error {
	label := "ci health"
	msg := fmt.Sprintf("%s (%d/100)", sc.Grade, sc.Points)
	if sc.Grade == "–" {
		msg = "unknown"
	}
	color := badgeColor(sc.Grade)

	const pad = 10 // 5px each side
	lw := textWidth(label) + pad
	mw := textWidth(msg) + pad
	total := lw + mw

	_, err := fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
  <title>%s: %s — scored by gha-doctor</title>
  <linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
  <clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="%d" height="20" fill="#555"/>
    <rect x="%d" width="%d" height="20" fill="%s"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,DejaVu Sans,sans-serif" font-size="11">
    <text x="%d" y="15" fill="#010101" fill-opacity=".3" textLength="%d">%s</text>
    <text x="%d" y="14" textLength="%d">%s</text>
    <text x="%d" y="15" fill="#010101" fill-opacity=".3" textLength="%d">%s</text>
    <text x="%d" y="14" textLength="%d">%s</text>
  </g>
</svg>
`,
		total, label, msg,
		label, msg,
		total,
		lw,
		lw, mw, color,
		total,
		lw/2, lw-pad, label,
		lw/2, lw-pad, label,
		lw+mw/2, mw-pad, msg,
		lw+mw/2, mw-pad, msg,
	)
	return err
}

// textWidth estimates rendered width of s at Verdana 11px. Rough
// per-class widths are fine: textLength makes the renderer stretch or
// squeeze the run to exactly the width we claim.
func textWidth(s string) int {
	w := 0.0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			w += 7
		case r >= 'A' && r <= 'Z':
			w += 8
		case r == 'i' || r == 'l' || r == 'j' || r == 't' || r == 'f' || r == 'I':
			w += 3.5
		case r == 'm' || r == 'w':
			w += 10
		case r == ' ':
			w += 3.5
		case r == '(' || r == ')' || r == '/':
			w += 4.5
		case r == '+':
			w += 7.5
		default:
			w += 6.5
		}
	}
	return int(w + 0.5)
}
