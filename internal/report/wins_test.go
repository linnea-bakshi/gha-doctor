package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func winsAnalysis(windowDays float64, now time.Time) *api.Analysis {
	return &api.Analysis{
		Repo:        "o/r",
		RunsSampled: 100,
		Since:       now.Add(-time.Duration(windowDays * 24 * float64(time.Hour))),
		Waste:       api.WasteStats{FailedRunMinutes: 300, RetryMinutes: 100, TotalMinutes: 400, ComputeMinutes: 2000},
		Cost:        api.CostStats{BillableMinutes: 2200, EstimatedUSD: 17.60, WastedUSD: 3.20, RoundingMinutes: 500, RoundingUSD: 4.00},
		FlakyJobs:   []api.FlakyJob{{Workflow: "CI", Job: "test (windows)", WastedMinutes: 90}},
	}
}

func TestComputeWinsProjection(t *testing.T) {
	now := time.Now()
	ws := ComputeWins(nil, winsAnalysis(15, now), now)
	if ws == nil {
		t.Fatal("expected wins")
	}
	if !ws.Projected {
		t.Fatalf("15-day window should project; basis=%q", ws.Basis)
	}
	// factor = 30/15 = 2: wasted 3.20 -> 6.40, rounding 4.00 -> 8.00.
	if len(ws.Items) < 2 {
		t.Fatalf("want >=2 wins, got %+v", ws.Items)
	}
	// Rounding (8.00) should outrank waste (6.40).
	if ws.Items[0].Title != "Consolidate tiny jobs" || ws.Items[0].USDPerMo != 8.00 {
		t.Fatalf("top win = %+v", ws.Items[0])
	}
	if ws.Items[1].USDPerMo != 6.40 {
		t.Fatalf("waste win = %+v", ws.Items[1])
	}
	if !strings.Contains(ws.Items[1].Detail, "test (windows)") {
		t.Fatalf("waste detail should name the worst flake: %q", ws.Items[1].Detail)
	}
}

func TestComputeWinsShortWindowNoProjection(t *testing.T) {
	now := time.Now()
	ws := ComputeWins(nil, winsAnalysis(1, now), now)
	if ws == nil {
		t.Fatal("expected wins")
	}
	if ws.Projected {
		t.Fatal("1-day window must not project")
	}
	if !strings.Contains(ws.Basis, "too short") {
		t.Fatalf("basis should admit the short window: %q", ws.Basis)
	}
	// Sample totals, not doubled.
	for _, w := range ws.Items {
		if w.Title == "Cut failures and retries" && w.USDPerMo != 3.20 {
			t.Fatalf("short window should keep sample total, got %+v", w)
		}
	}
}

func TestComputeWinsRoundingShareGate(t *testing.T) {
	now := time.Now()
	a := winsAnalysis(15, now)
	a.Cost.RoundingUSD = 1.00 // under 15% of 17.60
	ws := ComputeWins(nil, a, now)
	for _, w := range ws.Items {
		if w.Title == "Consolidate tiny jobs" {
			t.Fatal("rounding under 15% of spend should not be a win")
		}
	}
}

func TestComputeWinsArtifactRetention(t *testing.T) {
	now := time.Now()
	a := winsAnalysis(15, now)
	a.Artifacts = api.ArtifactStats{
		Available: true, EstStorageGB: 40, WindowDays: 10,
		Producers: []api.ArtifactProducer{
			// 10 GB over 10 days kept 90d: rate 1 GB/d, saving 60 GB steady
			// state -> 60*0.008*30 = $14.40/mo.
			{Name: "wheels", TotalMB: 10240, RetentionDays: 90},
			{Name: "logs", TotalMB: 100, RetentionDays: 7}, // under 30d: no saving
		},
	}
	ws := ComputeWins(nil, a, now)
	var found *Win
	for i := range ws.Items {
		if ws.Items[i].Rule == "D010" {
			found = &ws.Items[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a D010 retention win: %+v", ws.Items)
	}
	if found.USDPerMo < 14.3 || found.USDPerMo > 14.5 {
		t.Fatalf("retention saving = %v, want ~14.40", found.USDPerMo)
	}
	if !strings.Contains(found.Detail, "wheels") || strings.Contains(found.Detail, "logs") {
		t.Fatalf("detail should name only >30d producers: %q", found.Detail)
	}
}

func TestComputeWinsUnquantifiedAndCap(t *testing.T) {
	now := time.Now()
	a := winsAnalysis(15, now)
	a.Cache = api.CacheStats{Available: true, LimitPct: 95, StaleMB: 800, PRRefMB: 1200}
	hr := 40.0
	a.CacheLogs = &api.CacheLogStats{Available: true, Restores: 20, HitRate: hr}
	findings := []lint.Finding{
		{Rule: "D013", File: "a.yml"},
		{Rule: "D003", File: "a.yml"},
		{Rule: "D003", File: "b.yml"},
		{Rule: "D001", File: "a.yml"},
	}
	ws := ComputeWins(findings, a, now)
	if len(ws.Items) != winsMax {
		t.Fatalf("want cap at %d, got %d", winsMax, len(ws.Items))
	}
	// Quantified first, then D013, then D003 (fixable).
	if ws.Items[2].Rule != "D013" {
		t.Fatalf("item 3 = %+v, want D013", ws.Items[2])
	}
	if ws.Items[3].Rule != "D003" || !ws.Items[3].Fixable {
		t.Fatalf("item 4 = %+v, want fixable D003", ws.Items[3])
	}
	if !strings.Contains(ws.Items[3].Detail, "2 setup steps download") {
		t.Fatalf("D003 detail = %q", ws.Items[3].Detail)
	}
}

func TestComputeWinsCacheDeadWeightPhrasing(t *testing.T) {
	now := time.Now()
	findCache := func(ws *Wins) *Win {
		if ws == nil {
			t.Fatal("no wins")
		}
		for i := range ws.Items {
			if ws.Items[i].Title == "Free cache space before evictions" {
				return &ws.Items[i]
			}
		}
		t.Fatal("no cache win")
		return nil
	}

	a := winsAnalysis(15, now)
	a.Cache = api.CacheStats{Available: true, LimitPct: 100, TotalMB: 10240}
	w := findCache(ComputeWins(nil, a, now))
	if strings.Contains(w.Detail, "0 MB") || strings.Contains(w.Detail, "(") {
		t.Errorf("zero dead weight must omit the parenthetical: %q", w.Detail)
	}

	a.Cache.PRRefMB = 1200
	w = findCache(ComputeWins(nil, a, now))
	if !strings.Contains(w.Detail, "(1200 MB pinned to PR refs)") || strings.Contains(w.Detail, "stale") {
		t.Errorf("only nonzero components should render: %q", w.Detail)
	}
}

func TestComputeWinsNilWhenNothing(t *testing.T) {
	now := time.Now()
	a := &api.Analysis{Repo: "o/r", Since: now.Add(-10 * 24 * time.Hour)}
	if ws := ComputeWins(nil, a, now); ws != nil {
		t.Fatalf("clean repo should have no wins section, got %+v", ws)
	}
	if ws := ComputeWins(nil, nil, now); ws != nil {
		t.Fatal("nil analysis must yield nil wins")
	}
}

func TestWinsRenderers(t *testing.T) {
	now := time.Now()
	ws := ComputeWins([]lint.Finding{{Rule: "D003", File: "a.yml"}}, winsAnalysis(15, now), now)

	var term bytes.Buffer
	WinsSection(&term, Style{Plain: true}, ws)
	out := term.String()
	for _, want := range []string{"Top wins", "~$8.00/mo", "Consolidate tiny jobs", "gha-doctor --fix handles this (D003)", "projected to 30 days"} {
		if !strings.Contains(out, want) {
			t.Fatalf("terminal output missing %q:\n%s", want, out)
		}
	}
	// Headline total = 8.00 + 6.40 = 14.40.
	if !strings.Contains(out, "est. ~$14.40/mo") {
		t.Fatalf("headline total missing:\n%s", out)
	}

	var md bytes.Buffer
	WinsMarkdown(&md, ws)
	if !strings.Contains(md.String(), "### Top wins") || !strings.Contains(md.String(), "**~$8.00/mo**") {
		t.Fatalf("markdown output:\n%s", md.String())
	}
	// nil renders nothing.
	var empty bytes.Buffer
	WinsSection(&empty, Style{Plain: true}, nil)
	WinsMarkdown(&empty, nil)
	if empty.Len() != 0 {
		t.Fatal("nil wins must render nothing")
	}
}

func TestWinsInJSON(t *testing.T) {
	now := time.Now()
	ws := ComputeWins(nil, winsAnalysis(15, now), now)
	var buf bytes.Buffer
	if err := JSON(&buf, nil, nil, nil, nil, ws); err != nil {
		t.Fatal(err)
	}
	var got struct {
		TopWins *Wins `json:"top_wins"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TopWins == nil || len(got.TopWins.Items) == 0 {
		t.Fatalf("top_wins missing from JSON: %s", buf.String())
	}
}

func TestComputeWinsMatrixImbalance(t *testing.T) {
	now := time.Now()
	a := winsAnalysis(15, now)
	a.Matrix = &api.MatrixStats{GroupsMeasured: 1, Imbalanced: []api.MatrixGroup{{
		Workflow: "CI", Job: "test", Shards: 8, RunsMeasured: 12,
		P50WallMin: 10, P50IdealMin: 4, P50SavingMin: 6, Ratio: 2.5,
		SlowestShard: "(windows-latest, 3.12)", SlowestP50: 10, FastestShard: "(ubuntu-latest, 3.11)", FastestP50: 2,
	}}}
	ws := ComputeWins(nil, a, now)
	found := false
	for _, w := range ws.Items {
		if w.Title == "Rebalance matrix shards" {
			found = true
			if w.USDPerMo != 0 {
				t.Errorf("matrix win must be unquantified (latency, not dollars): %+v", w)
			}
			if !strings.Contains(w.Detail, "(windows-latest, 3.12)") || !strings.Contains(w.Detail, "~6m") {
				t.Errorf("detail missing shard/saving: %q", w.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("6m median straggler wait should earn a win slot: %+v", ws.Items)
	}
}

func TestComputeWinsMatrixBelowThresholdSkipped(t *testing.T) {
	now := time.Now()
	a := winsAnalysis(15, now)
	a.Matrix = &api.MatrixStats{GroupsMeasured: 1, Imbalanced: []api.MatrixGroup{{
		Workflow: "CI", Job: "test", Shards: 4, RunsMeasured: 12,
		P50WallMin: 3, P50IdealMin: 1.5, P50SavingMin: 1.5, Ratio: 2.0,
	}}}
	for _, w := range ComputeWins(nil, a, now).Items {
		if w.Title == "Rebalance matrix shards" {
			t.Fatalf("1.5m saving is below the 2m win threshold: %+v", w)
		}
	}
}
