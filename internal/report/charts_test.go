package report

import (
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

func chartAnalysis(n int) *api.Analysis {
	a := &api.Analysis{Repo: "o/r", RunsSampled: n}
	t0 := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		a.RunPoints = append(a.RunPoints, api.RunPoint{
			Workflow: "CI <build> & test", // hostile name: must be escaped
			Start:    t0.Add(time.Duration(i) * time.Hour),
			Minutes:  float64(2 + i%7),
			Success:  i%3 != 0,
		})
	}
	a.Workflows = []api.WorkflowStats{
		{Name: "CI <build> & test", Runs: n, Decisive: n, P50Minutes: 4.2, P95Minutes: 8.9},
		{Name: "tiny", Runs: 3, Decisive: 3, P50Minutes: 1, P95Minutes: 2}, // below chartMinWFRuns
	}
	return a
}

func TestChartsGateSmallSamples(t *testing.T) {
	if got := Charts(nil); got != nil {
		t.Fatalf("nil analysis: want no charts, got %d", len(got))
	}
	a := chartAnalysis(chartMinScatter - 1)
	a.Workflows = nil
	if got := Charts(a); got != nil {
		t.Fatalf("small sample + no eligible workflows: want no charts, got %d", len(got))
	}
}

func TestDurationScatter(t *testing.T) {
	a := chartAnalysis(12)
	svg := durationScatterSVG(a)
	if svg == "" {
		t.Fatal("expected a scatter for 12 decisive runs")
	}
	if n := strings.Count(svg, "<circle"); n != 12+2 { // 12 points + 2 legend dots
		t.Fatalf("want 14 circles (12 points + legend), got %d", n)
	}
	if !strings.Contains(svg, chartRed) || !strings.Contains(svg, chartGreen) {
		t.Fatal("expected both success and failure colors")
	}
	if strings.Contains(svg, "<build>") {
		t.Fatal("workflow name not escaped")
	}
	if !strings.Contains(svg, "CI &lt;build&gt; &amp; test") {
		t.Fatal("escaped workflow name missing from tooltips")
	}
	if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
		t.Fatal("SVG contains NaN/Inf coordinates")
	}
	if !strings.Contains(svg, "12 decisive runs") {
		t.Fatal("title should state the sample size")
	}
	if !strings.Contains(svg, "skipped/cancelled runs excluded") {
		t.Fatal("caption should state the exclusion")
	}
}

func TestWorkflowRangeBars(t *testing.T) {
	a := chartAnalysis(12)
	svg := workflowRangeSVG(a)
	if svg == "" {
		t.Fatal("expected range bars")
	}
	// Only the big workflow qualifies (tiny has 3 < chartMinWFRuns decisive).
	if n := strings.Count(svg, "<rect"); n != 2 { // p95 + p50 for one row
		t.Fatalf("want 2 rects (one row), got %d", n)
	}
	if strings.Contains(svg, ">tiny<") {
		t.Fatal("workflow below the decisive-run gate must not get a row")
	}
	if !strings.Contains(svg, "p50 4.2 min") || !strings.Contains(svg, "p95 8.9 min") {
		t.Fatal("tooltips should carry the percentile values")
	}
	if strings.Contains(svg, "NaN") {
		t.Fatal("SVG contains NaN")
	}
}

func TestChartsInHTMLPage(t *testing.T) {
	a := chartAnalysis(12)
	page := string(HTMLPage("## report\n\nbody\n", HTMLMeta{Title: "t", Charts: Charts(a)}))
	if strings.Count(page, "<figure class=\"chart\">") != 2 {
		t.Fatalf("want 2 chart figures in page")
	}
	if !strings.Contains(page, "Run durations") || !strings.Contains(page, "Workflow durations") {
		t.Fatal("both charts should be embedded")
	}
	// Charts must come before the body content.
	if strings.Index(page, "<figure") > strings.Index(page, "<p>body</p>") {
		t.Fatal("charts should render before the report body")
	}
}

func TestNiceCeil(t *testing.T) {
	cases := map[float64]float64{0.3: 0.5, 1: 1, 3: 5, 7: 10, 12: 20, 47: 50, 96: 100, 130: 200}
	for in, want := range cases {
		if got := niceCeil(in); got != want {
			t.Errorf("niceCeil(%v) = %v, want %v", in, got, want)
		}
	}
	if got := niceCeil(0); got != 1 {
		t.Errorf("niceCeil(0) = %v, want 1", got)
	}
}
