package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

// The fleet card is a self-contained SVG meant for embedding in an org
// profile README: dark GitHub-ish card, monospace table of the busiest
// repos, totals in the footer. Like the score badge, it names common
// fonts with a monospace fallback and pins every text run with
// textLength so alignment survives foreign font metrics.

const (
	cardWidth    = 600
	cardPadX     = 16
	cardRowH     = 17
	cardFontSize = 11
	cardCharW    = 6.6 // DejaVu Sans Mono at 11px
	maxCardRows  = 12  // busiest repos shown individually; rest aggregated
)

// column right edges (numeric, text-anchor=end) / left edges (text).
const (
	colRepoX  = cardPadX
	colRunsX  = 320
	colFailX  = 372
	colP50X   = 434
	colEstX   = 520
	colAgeX   = 530
	repoTrunc = 34
)

func cardFailColor(rate float64) string {
	switch {
	case rate >= 0.30:
		return "#f85149" // red
	case rate >= 0.10:
		return "#d29922" // yellow
	}
	return "#3fb950" // green
}

// svgEsc escapes the five XML special characters. Repo names are the only
// externally sourced strings on the card, but escape everything anyway.
var svgEsc = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
).Replace

// cardText emits one pinned text run. anchor is "start" or "end"; x is the
// corresponding edge.
func cardText(b *strings.Builder, x int, y int, anchor, fill, s string) {
	if s == "" {
		return
	}
	fmt.Fprintf(b, `    <text x="%d" y="%d" text-anchor="%s" fill="%s" textLength="%.0f">%s</text>`+"\n",
		x, y, anchor, fill, float64(len([]rune(s)))*cardCharW, svgEsc(s))
}

// OrgSVG writes the fleet card for an org scan. now is injectable for tests.
func OrgSVG(w io.Writer, oa *api.OrgAnalysis, now time.Time) error {
	rows := oa.Repos
	var tailN int
	var tailMin float64
	if len(rows) > maxCardRows {
		for _, r := range rows[maxCardRows:] {
			tailMin += r.Est30dMinutes
		}
		tailN = len(rows) - maxCardRows
		rows = rows[:maxCardRows]
	}

	nRows := len(rows)
	extraLines := 0
	if tailN > 0 {
		extraLines++
	}
	if nRows == 0 {
		extraLines++ // "no completed runs" line
	}
	// 12 top pad + title(18) + gap(8) + header + rows + extras + gap(6) + rule+footer(22) + 10 bottom pad
	height := 12 + 18 + 8 + cardRowH + (nRows+extraLines)*cardRowH + 6 + 22 + 10

	title := fmt.Sprintf("CI checkup: %s", oa.Org)
	sub := fmt.Sprintf("%d of %d repos scanned", oa.ReposScanned, oa.ReposListed)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s — %s, scanned by gha-doctor">
  <title>%s — %s, scanned by gha-doctor</title>
  <rect x="0.5" y="0.5" width="%d" height="%d" rx="6" fill="#0d1117" stroke="#30363d"/>
  <g font-family="SFMono-Regular,Consolas,DejaVu Sans Mono,monospace" font-size="%d">
`,
		cardWidth, height, cardWidth, height, svgEsc(title), sub,
		svgEsc(title), sub,
		cardWidth-1, height-1,
		cardFontSize)

	y := 12 + 13 // first baseline
	cardText(&b, colRepoX, y, "start", "#e6edf3", title)
	cardText(&b, cardWidth-cardPadX, y, "end", "#8b949e", sub)

	y += 8 + cardRowH
	hdr := "#8b949e"
	cardText(&b, colRepoX, y, "start", hdr, "repo")
	cardText(&b, colRunsX, y, "end", hdr, "runs")
	cardText(&b, colFailX, y, "end", hdr, "fail")
	cardText(&b, colP50X, y, "end", hdr, "p50")
	cardText(&b, colEstX, y, "end", hdr, "~min/30d")
	cardText(&b, colAgeX, y, "start", hdr, "last run")

	if nRows == 0 {
		y += cardRowH
		cardText(&b, colRepoX, y, "start", "#8b949e", "no completed workflow runs in the scanned repos")
	}
	for _, r := range rows {
		y += cardRowH
		est := fmt.Sprintf("%.0f", r.Est30dMinutes)
		switch {
		case r.Extrapolated:
			est += "*"
		case r.Truncated:
			est += "+"
		}
		cardText(&b, colRepoX, y, "start", "#e6edf3", trunc(r.Repo, repoTrunc))
		cardText(&b, colRunsX, y, "end", "#e6edf3", fmt.Sprintf("%d", r.RunsSampled))
		cardText(&b, colFailX, y, "end", cardFailColor(r.FailRate), fmt.Sprintf("%.0f%%", r.FailRate*100))
		cardText(&b, colP50X, y, "end", "#e6edf3", fmt.Sprintf("%.1fm", r.P50Minutes))
		cardText(&b, colEstX, y, "end", "#e6edf3", est)
		cardText(&b, colAgeX, y, "start", "#8b949e", humanAgeAt(r.LastRun, now))
	}
	if tailN > 0 {
		y += cardRowH
		cardText(&b, colRepoX, y, "start", "#8b949e",
			fmt.Sprintf("… and %d more active %s", tailN, plural(tailN, "repo")))
		cardText(&b, colEstX, y, "end", "#8b949e", fmt.Sprintf("%.0f", tailMin))
	}

	y += 6 + 10
	fmt.Fprintf(&b, `    <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#30363d"/>`+"\n",
		cardPadX, y, cardWidth-cardPadX, y)
	y += 15
	foot := fmt.Sprintf("total ~%.0f run min/30d · run-weighted fail %.0f%%", oa.TotalEst30d, oa.TotalFailRate*100)
	if nRows == 0 {
		foot = "no run data"
	}
	cardText(&b, colRepoX, y, "start", "#8b949e", foot)
	cardText(&b, cardWidth-cardPadX, y, "end", "#58a6ff", "gha-doctor")

	b.WriteString("  </g>\n</svg>\n")
	_, err := io.WriteString(w, b.String())
	return err
}
