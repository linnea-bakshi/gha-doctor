package report

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func TestScoreCleanStaticOnly(t *testing.T) {
	sc := ComputeScore(nil, 3, nil)
	if sc.Points != 100 || sc.Grade != "A+" {
		t.Errorf("clean static-only repo should be 100/A+, got %d/%s", sc.Points, sc.Grade)
	}
	if sc.Basis != "static checks only" {
		t.Errorf("basis = %q", sc.Basis)
	}
	if len(sc.Components) != 1 {
		t.Fatalf("want 1 component, got %d", len(sc.Components))
	}
}

func TestScoreStaticDeductions(t *testing.T) {
	// Density-normalized: (2 warnings × 4 + 1 info) / 1 file = 9 of the
	// full-deduction density 12 → 22.5 of 30 deducted → 25/100.
	fs := []lint.Finding{
		{Rule: "D001", Severity: lint.Warn},
		{Rule: "D002", Severity: lint.Warn},
		{Rule: "D005", Severity: lint.Info},
	}
	sc := ComputeScore(fs, 1, nil)
	if sc.Points != 25 || sc.Grade != "F" {
		t.Errorf("got %d/%s, want 25/F", sc.Points, sc.Grade)
	}
	// The same findings spread across 3 files are a third of the density.
	sc = ComputeScore(fs, 3, nil)
	if sc.Points != 75 {
		t.Errorf("3-file density: got %d, want 75", sc.Points)
	}
}

func TestScoreStaticCap(t *testing.T) {
	var fs []lint.Finding
	for i := 0; i < 50; i++ {
		fs = append(fs, lint.Finding{Rule: "D001", Severity: lint.Warn})
	}
	sc := ComputeScore(fs, 1, nil)
	if sc.Points != 0 || sc.Grade != "F" {
		t.Errorf("50 warnings static-only should floor at 0/F, got %d/%s", sc.Points, sc.Grade)
	}
	if sc.Components[0].Deducted != 30 {
		t.Errorf("hygiene deduction should cap at 30, got %v", sc.Components[0].Deducted)
	}
}

func TestScoreWithHistory(t *testing.T) {
	sc := ComputeScore(sampleFindings(), 2, sampleAnalysis())
	if sc.Basis != "static checks + run history" {
		t.Errorf("basis = %q", sc.Basis)
	}
	// sampleAnalysis: 75% success, 1 flaky job, 133/900 wasted, cache at
	// 92.8% with 84% dead bytes, 12 s queue. All six components present.
	names := map[string]ScoreComponent{}
	for _, c := range sc.Components {
		names[c.Name] = c
	}
	for _, want := range []string{"workflow hygiene", "success rate", "queue time", "flakiness", "wasted minutes", "cache pressure"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing component %q (have %v)", want, sc.Components)
		}
	}
	if c := names["success rate"]; c.Deducted != 15.6 {
		t.Errorf("success deduction = %v, want 15.6 (25%% fail of 40%% scale)", c.Deducted)
	}
	if c := names["flakiness"]; c.Deducted != 5 {
		t.Errorf("flaky deduction = %v, want 5", c.Deducted)
	}
	if c := names["cache pressure"]; c.Deducted != 10 {
		t.Errorf("cache deduction = %v, want 10 (over limit + dead bytes)", c.Deducted)
	}
	if sc.Points < 0 || sc.Points > 100 {
		t.Errorf("points out of range: %d", sc.Points)
	}
	if sc.Grade != gradeFor(sc.Points) {
		t.Errorf("grade %s does not match points %d", sc.Grade, sc.Points)
	}
}

func TestScoreCacheLogsPreferredOverPressure(t *testing.T) {
	a := sampleAnalysis()
	a.CacheLogs = &api.CacheLogStats{Available: true, Restores: 20, Hits: 15, PartialHits: 3, Misses: 2, HitRate: 90}
	sc := ComputeScore(nil, 1, a)
	for _, c := range sc.Components {
		if c.Name == "cache pressure" {
			t.Error("cache pressure should be replaced by measured hit rate when --cache-logs ran")
		}
	}
	found := false
	for _, c := range sc.Components {
		if c.Name == "cache hit rate" {
			found = true
			if c.Deducted != 2 { // 10% miss × 2 × 10
				t.Errorf("hit-rate deduction = %v, want 2", c.Deducted)
			}
		}
	}
	if !found {
		t.Error("missing cache hit rate component")
	}
}

func TestScoreNothing(t *testing.T) {
	sc := ComputeScore(nil, 0, nil)
	if sc.Grade != "–" || len(sc.Components) != 0 {
		t.Errorf("nothing to score should be grade –, got %+v", sc)
	}
}

func TestGradeBands(t *testing.T) {
	for _, tc := range []struct {
		pts  int
		want string
	}{{100, "A+"}, {97, "A+"}, {96, "A"}, {90, "A"}, {89, "B"}, {80, "B"}, {79, "C"}, {70, "C"}, {69, "D"}, {60, "D"}, {59, "F"}, {0, "F"}} {
		if got := gradeFor(tc.pts); got != tc.want {
			t.Errorf("gradeFor(%d) = %s, want %s", tc.pts, got, tc.want)
		}
	}
}

func TestBadgeSVGWellFormed(t *testing.T) {
	for _, sc := range []Score{
		{Points: 96, Grade: "A"},
		{Points: 71, Grade: "C"},
		{Points: 12, Grade: "F"},
		{Grade: "–"},
	} {
		var buf bytes.Buffer
		if err := Badge(&buf, sc); err != nil {
			t.Fatal(err)
		}
		// Must be valid XML all the way through.
		dec := xml.NewDecoder(bytes.NewReader(buf.Bytes()))
		for {
			if _, err := dec.Token(); err != nil {
				if err.Error() == "EOF" {
					break
				}
				t.Fatalf("badge for %s is not well-formed XML: %v\n%s", sc.Grade, err, buf.String())
			}
		}
		out := buf.String()
		if !strings.Contains(out, "ci health") {
			t.Error("badge missing label")
		}
		if sc.Grade == "–" {
			if !strings.Contains(out, "unknown") {
				t.Error("unknown badge should say unknown")
			}
		} else if !strings.Contains(out, sc.Grade+" (") {
			t.Errorf("badge missing grade %s: %s", sc.Grade, out)
		}
		if !strings.Contains(out, `textLength=`) {
			t.Error("badge should pin text width with textLength")
		}
		if !strings.Contains(out, badgeColor(sc.Grade)) {
			t.Errorf("badge missing color %s", badgeColor(sc.Grade))
		}
	}
}

func TestScoreSectionAndMarkdown(t *testing.T) {
	sc := ComputeScore(sampleFindings(), 2, sampleAnalysis())
	var term bytes.Buffer
	ScoreSection(&term, Style{Plain: true}, sc)
	if !strings.Contains(term.String(), "Health score") || !strings.Contains(term.String(), sc.Grade) {
		t.Errorf("terminal score output incomplete:\n%s", term.String())
	}
	var md bytes.Buffer
	ScoreMarkdown(&md, sc)
	if !strings.Contains(md.String(), "## Health score") || !strings.Contains(md.String(), "| success rate |") {
		t.Errorf("markdown score output incomplete:\n%s", md.String())
	}
}

func TestScoreThinHistoryNotGraded(t *testing.T) {
	a := sampleAnalysis()
	a.RunsSampled = 3 // below minRunsToGrade
	sc := ComputeScore(nil, 0, a)
	for _, c := range sc.Components {
		switch c.Name {
		case "success rate", "queue time", "flakiness", "wasted minutes":
			t.Errorf("component %q graded from only 3 sampled runs", c.Name)
		}
	}
	// Cache data comes from the caches API, not the run sample — still graded.
	found := false
	for _, c := range sc.Components {
		if c.Name == "cache pressure" || c.Name == "cache hit rate" {
			found = true
		}
	}
	if !found {
		t.Errorf("cache component should survive the thin-history guard (have %v)", sc.Components)
	}
	if !strings.Contains(sc.Basis, "only 3 run(s) sampled") {
		t.Errorf("basis should explain the guard, got %q", sc.Basis)
	}
}

func TestScoreThinHistoryWithStatic(t *testing.T) {
	a := sampleAnalysis()
	a.RunsSampled = 9
	sc := ComputeScore(sampleFindings(), 2, a)
	if !strings.HasPrefix(sc.Basis, "static checks only") || !strings.Contains(sc.Basis, "need 10") {
		t.Errorf("basis = %q", sc.Basis)
	}
}

func TestScoreExactlyMinRunsGraded(t *testing.T) {
	a := sampleAnalysis()
	a.RunsSampled = 10
	sc := ComputeScore(nil, 0, a)
	if sc.Basis != "run history only" {
		t.Errorf("basis = %q", sc.Basis)
	}
	found := false
	for _, c := range sc.Components {
		if c.Name == "success rate" {
			found = true
		}
	}
	if !found {
		t.Error("10 sampled runs should be graded")
	}
}
