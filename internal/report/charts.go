package report

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

// Charts renders the inline-SVG chart section of the --html report. Charts
// are generated only from run history (nil analysis = no charts) and only
// when the sample can honestly carry them:
//
//   - the duration scatter needs at least chartMinScatter decisive runs —
//     a trend line through three dots is decoration, not information;
//   - a workflow's p50→p95 range bar needs at least chartMinWFRuns decisive
//     runs of that workflow, the same reason percentiles of two runs are
//     noise.
//
// Everything is self-contained SVG: no scripts, no external assets, all
// user-controlled strings (workflow names) HTML-escaped.
const (
	chartMinScatter = 10
	chartMinWFRuns  = 5
	chartMaxWF      = 10

	chartGreen = "#3fb950"
	chartRed   = "#f85149"
	chartGrid  = "#21262d"
	chartAxis  = "#8b949e"
	chartText  = "#e6edf3"
	chartP95   = "#3d444d"
	chartP50   = "#58a6ff"
)

// Charts returns zero or more SVG snippets, in render order.
func Charts(a *api.Analysis) []string {
	if a == nil {
		return nil
	}
	var out []string
	if s := durationScatterSVG(a); s != "" {
		out = append(out, s)
	}
	if s := workflowRangeSVG(a); s != "" {
		out = append(out, s)
	}
	return out
}

// niceCeil rounds v up to a 1/2/5×10^k "nice" bound for an axis maximum.
func niceCeil(v float64) float64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if v <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

// fmtTick renders an axis value without trailing noise (2 not 2.0, 0.5 kept).
func fmtTick(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

// durationScatterSVG plots every decisive run: x = start time, y = wall-clock
// minutes, green = success, red = failure/timeout. Failures are drawn last so
// a red dot is never buried under green neighbors.
func durationScatterSVG(a *api.Analysis) string {
	pts := a.RunPoints
	if len(pts) < chartMinScatter {
		return ""
	}
	const (
		W, H                     = 860, 250
		mLeft, mRight, mTop, mBt = 46, 14, 30, 36
	)
	plotW, plotH := float64(W-mLeft-mRight), float64(H-mTop-mBt)

	tmin, tmax := pts[0].Start, pts[0].Start
	ymax := 0.0
	for _, p := range pts {
		if p.Start.Before(tmin) {
			tmin = p.Start
		}
		if p.Start.After(tmax) {
			tmax = p.Start
		}
		if p.Minutes > ymax {
			ymax = p.Minutes
		}
	}
	if ymax <= 0 {
		return ""
	}
	span := tmax.Sub(tmin)
	if span <= 0 {
		span = time.Minute
	}
	ymax = niceCeil(ymax)

	xOf := func(t time.Time) float64 {
		return float64(mLeft) + plotW*float64(t.Sub(tmin))/float64(span)
	}
	yOf := func(m float64) float64 {
		return float64(mTop) + plotH*(1-m/ymax)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" role="img" aria-label="Run durations over time">`+"\n", W, H, W)
	fmt.Fprintf(&b, `<text x="%d" y="16" font-size="13" font-weight="600" fill="%s">Run durations — %d decisive runs</text>`+"\n",
		mLeft, chartText, len(pts))
	// Legend, top right.
	lx := W - mRight - 150
	fmt.Fprintf(&b, `<circle cx="%d" cy="12" r="4" fill="%s"/><text x="%d" y="16" font-size="11" fill="%s">success</text>`+"\n", lx, chartGreen, lx+8, chartAxis)
	fmt.Fprintf(&b, `<circle cx="%d" cy="12" r="4" fill="%s"/><text x="%d" y="16" font-size="11" fill="%s">failure</text>`+"\n", lx+70, chartRed, lx+78, chartAxis)

	// Horizontal grid + y ticks (4 divisions).
	for i := 0; i <= 4; i++ {
		v := ymax * float64(i) / 4
		y := yOf(v)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s"/>`+"\n", mLeft, y, W-mRight, y, chartGrid)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="11" fill="%s" text-anchor="end">%s</text>`+"\n", mLeft-6, y+4, chartAxis, fmtTick(v))
	}
	fmt.Fprintf(&b, `<text x="12" y="%d" font-size="11" fill="%s">min</text>`+"\n", mTop-8, chartAxis)

	// X ticks: 4 evenly spaced timestamps; format depends on the span.
	tf := "Jan 2"
	if span < 48*time.Hour {
		tf = "Jan 2 15:04"
	}
	for i := 0; i <= 3; i++ {
		t := tmin.Add(span * time.Duration(i) / 3)
		x := xOf(t)
		anchor := "middle"
		if i == 0 {
			anchor = "start"
		} else if i == 3 {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="%s"/>`+"\n", x, mTop, x, H-mBt, chartGrid)
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" font-size="11" fill="%s" text-anchor="%s">%s</text>`+"\n",
			x, H-mBt+16, chartAxis, anchor, t.UTC().Format(tf))
	}

	// Points: successes first, failures on top.
	for _, wantSuccess := range []bool{true, false} {
		for _, p := range pts {
			if p.Success != wantSuccess {
				continue
			}
			color, verdict := chartGreen, "success"
			if !p.Success {
				color, verdict = chartRed, "failure"
			}
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="%s" fill-opacity="0.85"><title>%s · %.1f min · %s · %s</title></circle>`+"\n",
				xOf(p.Start), yOf(p.Minutes), color,
				html.EscapeString(p.Workflow), p.Minutes, verdict, p.Start.UTC().Format("2006-01-02 15:04 UTC"))
		}
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="%s">wall-clock duration per run; skipped/cancelled runs excluded</text>`+"\n",
		mLeft, H-4, chartAxis)
	b.WriteString("</svg>")
	return b.String()
}

// workflowRangeSVG draws, per workflow, a light bar to p95 with a solid bar
// to p50 — "typical vs bad day" at a glance. Only workflows with enough
// decisive runs to make percentiles meaningful get a row.
func workflowRangeSVG(a *api.Analysis) string {
	type row struct {
		name     string
		p50, p95 float64
		decisive int
	}
	var rows []row
	xmax := 0.0
	for _, w := range a.Workflows {
		if w.Decisive < chartMinWFRuns || w.P95Minutes <= 0 {
			continue
		}
		rows = append(rows, row{w.Name, w.P50Minutes, w.P95Minutes, w.Decisive})
		if w.P95Minutes > xmax {
			xmax = w.P95Minutes
		}
		if len(rows) == chartMaxWF {
			break
		}
	}
	if len(rows) == 0 || xmax <= 0 {
		return ""
	}
	xmax = niceCeil(xmax)

	const (
		W      = 860
		mLeft  = 230 // workflow name column
		mRight = 96  // "p50 / p95" text column
		mTop   = 30
		rowH   = 24
		barH   = 12
		axisH  = 26
	)
	H := mTop + rowH*len(rows) + axisH
	plotW := float64(W - mLeft - mRight)
	xOf := func(v float64) float64 { return float64(mLeft) + plotW*v/xmax }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" role="img" aria-label="Workflow duration percentiles">`+"\n", W, H, W)
	fmt.Fprintf(&b, `<text x="14" y="16" font-size="13" font-weight="600" fill="%s">Workflow durations — typical (p50) vs bad day (p95), decisive runs only</text>`+"\n", chartText)

	// Vertical grid + x ticks.
	for i := 0; i <= 4; i++ {
		v := xmax * float64(i) / 4
		x := xOf(v)
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="%s"/>`+"\n", x, mTop, x, H-axisH, chartGrid)
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" font-size="11" fill="%s" text-anchor="middle">%s</text>`+"\n", x, H-axisH+16, chartAxis, fmtTick(v))
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="%s" text-anchor="end">min</text>`+"\n", W-14, H-axisH+16, chartAxis)

	for i, r := range rows {
		y := float64(mTop + i*rowH)
		cy := y + float64(rowH-barH)/2
		name := html.EscapeString(trunc(r.name, 30))
		full := html.EscapeString(r.name)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="12" fill="%s" text-anchor="end"><title>%s</title>%s</text>`+"\n",
			mLeft-10, y+float64(rowH)/2+4, chartText, full, name)
		fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%d" rx="2" fill="%s"><title>%s · p95 %.1f min (%d runs)</title></rect>`+"\n",
			mLeft, cy, xOf(r.p95)-float64(mLeft), barH, chartP95, full, r.p95, r.decisive)
		fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%d" rx="2" fill="%s"><title>%s · p50 %.1f min (%d runs)</title></rect>`+"\n",
			mLeft, cy, xOf(r.p50)-float64(mLeft), barH, chartP50, full, r.p50, r.decisive)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="11" fill="%s">%s / %s</text>`+"\n",
			W-mRight+8, y+float64(rowH)/2+4, chartAxis, fmtTick(round1(r.p50)), fmtTick(round1(r.p95)))
	}
	b.WriteString("</svg>")
	return b.String()
}
