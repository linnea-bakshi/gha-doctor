package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func histScore(points int, grade string, comps ...ScoreComponent) Score {
	return Score{Points: points, Grade: grade, Basis: "static checks + run history", Components: comps}
}

func TestHistoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")

	// Missing file is not an error: first run of a new repo.
	entries, bad, err := LoadHistory(path)
	if err != nil || bad != 0 || entries != nil {
		t.Fatalf("missing file: entries=%v bad=%d err=%v", entries, bad, err)
	}

	e1 := EntryFor(histScore(84, "B", ScoreComponent{Name: "success rate", Deducted: 8, Max: 25}), "octo/repo", time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC))
	e2 := EntryFor(histScore(91, "A", ScoreComponent{Name: "success rate", Deducted: 2, Max: 25}), "octo/repo", time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	for _, e := range []ScoreEntry{e1, e2} {
		if err := AppendHistory(path, e); err != nil {
			t.Fatal(err)
		}
	}

	entries, bad, err = LoadHistory(path)
	if err != nil || bad != 0 {
		t.Fatalf("bad=%d err=%v", bad, err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Points != 84 || entries[1].Grade != "A" || entries[1].Repo != "octo/repo" {
		t.Fatalf("round-trip mismatch: %+v", entries)
	}
	if entries[1].Components["success rate"] != 2 {
		t.Fatalf("components not preserved: %+v", entries[1].Components)
	}
}

func TestHistorySkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	content := `{"ts":"2026-07-22T12:00:00Z","points":84,"grade":"B","basis":"x"}
not json at all
{"points":10}
{"ts":"2026-07-29T12:00:00Z","points":91,"grade":"A","basis":"x"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, bad, err := LoadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || bad != 2 {
		t.Fatalf("entries=%d bad=%d, want 2/2 (line without ts must count as corrupt)", len(entries), bad)
	}
}

func TestLatestForRepoMatching(t *testing.T) {
	entries := []ScoreEntry{
		{Time: time.Now(), Repo: "octo/a", Points: 50},
		{Time: time.Now(), Repo: "octo/b", Points: 60},
		{Time: time.Now(), Repo: "", Points: 70}, // untagged: matches anything
	}
	if e, ok := LatestFor(entries, "octo/a"); !ok || e.Points != 70 {
		t.Fatalf("untagged latest should match: %+v %v", e, ok)
	}
	entries = entries[:2]
	if e, ok := LatestFor(entries, "OCTO/A"); !ok || e.Points != 50 {
		t.Fatalf("case-insensitive repo match failed: %+v %v", e, ok)
	}
	if _, ok := LatestFor(entries[:1], "other/repo"); ok {
		t.Fatal("different repo must not match")
	}
	if e, ok := LatestFor(entries, ""); !ok || e.Points != 60 {
		t.Fatalf("unknown current repo takes latest: %+v %v", e, ok)
	}
}

func TestDeltaFrom(t *testing.T) {
	prev := ScoreEntry{
		Time: time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC), Points: 84, Grade: "B",
		Basis: "static checks + run history",
		Components: map[string]float64{
			"success rate": 8, "flakiness": 0, "queue time": 1.2,
		},
	}
	cur := histScore(91, "A",
		ScoreComponent{Name: "success rate", Deducted: 2, Max: 25},   // improved by 6
		ScoreComponent{Name: "flakiness", Deducted: 5, Max: 15},      // regressed by 5
		ScoreComponent{Name: "queue time", Deducted: 1.4, Max: 5},    // moved 0.2 → below threshold
		ScoreComponent{Name: "cache hit rate", Deducted: 3, Max: 10}, // not in prev → ignored
	)
	d := DeltaFrom(prev, cur)
	if d.Change != 7 || d.BasisChanged {
		t.Fatalf("change=%d basisChanged=%v", d.Change, d.BasisChanged)
	}
	if len(d.Improved) != 1 || d.Improved[0].Name != "success rate" || d.Improved[0].From != 8 || d.Improved[0].To != 2 {
		t.Fatalf("improved: %+v", d.Improved)
	}
	if len(d.Regressed) != 1 || d.Regressed[0].Name != "flakiness" {
		t.Fatalf("regressed: %+v", d.Regressed)
	}

	line := deltaLine(d, cur)
	if line != "+7 since 2026-07-22 (B 84 → A 91)" {
		t.Fatalf("deltaLine: %q", line)
	}
	same := DeltaFrom(prev, histScore(84, "B"))
	if got := deltaLine(same, histScore(84, "B")); got != "unchanged since 2026-07-22 (B 84)" {
		t.Fatalf("unchanged line: %q", got)
	}
	down := DeltaFrom(prev, histScore(80, "B"))
	if got := deltaLine(down, histScore(80, "B")); !strings.HasPrefix(got, "-4 since ") {
		t.Fatalf("negative line: %q", got)
	}
}

func TestDeltaBasisChanged(t *testing.T) {
	prev := ScoreEntry{Time: time.Now(), Points: 96, Grade: "A", Basis: "static checks only"}
	cur := histScore(70, "C")
	if d := DeltaFrom(prev, cur); !d.BasisChanged {
		t.Fatal("basis change not flagged")
	}
}

func TestScoreRenderersIncludeDelta(t *testing.T) {
	sc := histScore(91, "A", ScoreComponent{Name: "success rate", Deducted: 2, Max: 25, Detail: "d"})
	sc.Delta = DeltaFrom(ScoreEntry{
		Time: time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC), Points: 84, Grade: "B",
		Basis:      sc.Basis,
		Components: map[string]float64{"success rate": 8},
	}, sc)

	var term strings.Builder
	ScoreSection(&term, Style{Plain: true}, sc)
	out := term.String()
	if !strings.Contains(out, "+7 since 2026-07-22 (B 84 → A 91)") {
		t.Fatalf("terminal delta missing:\n%s", out)
	}
	if !strings.Contains(out, "improved:") || !strings.Contains(out, "success rate (−8 → −2)") {
		t.Fatalf("terminal movers missing:\n%s", out)
	}

	var md strings.Builder
	ScoreMarkdown(&md, sc)
	if !strings.Contains(md.String(), "_Change: +7 since 2026-07-22") {
		t.Fatalf("markdown delta missing:\n%s", md.String())
	}
}

func TestPointsFor(t *testing.T) {
	entries := []ScoreEntry{
		{Repo: "a/b", Points: 40},
		{Repo: "", Points: 55}, // untagged: matches any repo
		{Repo: "c/d", Points: 90},
		{Repo: "A/B", Points: 70}, // case-insensitive match
	}
	got := PointsFor(entries, "a/b")
	want := []int{40, 55, 70}
	if len(got) != len(want) {
		t.Fatalf("PointsFor = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PointsFor = %v, want %v", got, want)
		}
	}
	if pts := PointsFor(entries, ""); len(pts) != 4 {
		t.Errorf("empty repo should match all entries, got %v", pts)
	}
	if pts := PointsFor(nil, "a/b"); pts != nil {
		t.Errorf("no entries should give nil, got %v", pts)
	}
}
