package lint

import "testing"

func bf(rule, file, msg string) Finding {
	return Finding{Rule: rule, Severity: Warn, SevStr: "warning", File: file, Line: 1, Message: msg}
}

func TestDiffFindingsBasenameAndLineDrift(t *testing.T) {
	// Same finding, different directory prefix and different line: hidden.
	cur := []Finding{
		{Rule: "D002", File: "repo/.github/workflows/ci.yml", Line: 40, Message: "job `build` has no timeout-minutes"},
		{Rule: "D003", File: "repo/.github/workflows/ci.yml", Line: 12, Message: "setup-node without `cache:`"},
	}
	base := []Finding{
		{Rule: "D002", File: ".github/workflows/ci.yml", Line: 8, Message: "job `build` has no timeout-minutes"},
	}
	newF, hidden, fixed := DiffFindings(cur, base)
	if len(newF) != 1 || newF[0].Rule != "D003" {
		t.Fatalf("new = %+v, want only D003", newF)
	}
	if hidden != 1 || fixed != 0 {
		t.Fatalf("hidden=%d fixed=%d, want 1,0", hidden, fixed)
	}
}

func TestDiffFindingsMultiset(t *testing.T) {
	// Two identical findings now, one at baseline: exactly one is new.
	cur := []Finding{
		bf("D002", "ci.yml", "job `a` has no timeout-minutes"),
		bf("D002", "ci.yml", "job `a` has no timeout-minutes"),
	}
	base := []Finding{bf("D002", "ci.yml", "job `a` has no timeout-minutes")}
	newF, hidden, fixed := DiffFindings(cur, base)
	if len(newF) != 1 || hidden != 1 || fixed != 0 {
		t.Fatalf("new=%d hidden=%d fixed=%d, want 1,1,0", len(newF), hidden, fixed)
	}
}

func TestDiffFindingsFixedAndDeletedFile(t *testing.T) {
	cur := []Finding{}
	base := []Finding{
		bf("D001", "ci.yml", "no concurrency group"),
		bf("D012", "old.yml", "`npm install` in CI"),
	}
	newF, hidden, fixed := DiffFindings(cur, base)
	if len(newF) != 0 || hidden != 0 || fixed != 2 {
		t.Fatalf("new=%d hidden=%d fixed=%d, want 0,0,2", len(newF), hidden, fixed)
	}
	if newF == nil {
		t.Fatal("newFindings must be non-nil for JSON output")
	}
}

func TestDiffFindingsAllNewOnEmptyBaseline(t *testing.T) {
	cur := []Finding{bf("D001", "new.yml", "no concurrency group")}
	newF, hidden, fixed := DiffFindings(cur, nil)
	if len(newF) != 1 || hidden != 0 || fixed != 0 {
		t.Fatalf("new=%d hidden=%d fixed=%d, want 1,0,0", len(newF), hidden, fixed)
	}
}
