package report

import (
	"fmt"
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func TestAnnotationsBasic(t *testing.T) {
	findings := []lint.Finding{
		{Rule: "D002", Severity: lint.Warn, File: "repo/.github/workflows/ci.yml", Line: 12,
			Message: "job build has no timeout-minutes", Advice: "add timeout-minutes"},
		{Rule: "D009", Severity: lint.Info, File: "repo/.github/workflows/ci.yml", Line: 3,
			Message: "informational thing"},
		// Defensive: a finding with no file has nothing to attach to.
		// (In practice even repo-level D017 carries a path.)
		{Rule: "D999", Severity: lint.Info, File: "", Line: 0,
			Message: "hypothetical fileless finding"},
	}
	var b strings.Builder
	Annotations(&b, []string{"repo"}, findings)
	out := b.String()

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 annotation lines (repo-level skipped), got %d:\n%s", len(lines), out)
	}
	// ":" is escaped only in properties (title/file), not in message data.
	want := "::warning file=.github/workflows/ci.yml,line=12,title=gha-doctor D002 NoJobTimeout::job build has no timeout-minutes — fix: add timeout-minutes"
	if lines[0] != want {
		t.Errorf("warning line:\n got %q\nwant %q", lines[0], want)
	}
	if !strings.HasPrefix(lines[1], "::notice file=.github/workflows/ci.yml,line=3,") {
		t.Errorf("info finding should be a ::notice, got %q", lines[1])
	}
	if strings.Contains(out, "D999") {
		t.Errorf("fileless finding must not be annotated:\n%s", out)
	}
}

func TestAnnotationsEscaping(t *testing.T) {
	findings := []lint.Finding{
		{Rule: "parse", Severity: lint.Warn, File: "a,b:c.yml", Line: 1,
			Message: "line1\nline2 with 100% and \r return"},
	}
	var b strings.Builder
	Annotations(&b, nil, findings)
	out := strings.TrimSpace(b.String())

	if strings.Count(out, "\n") != 0 {
		t.Fatalf("annotation must be a single line, got:\n%s", out)
	}
	if !strings.Contains(out, "file=a%2Cb%3Ac.yml") {
		t.Errorf("property escaping (, and :) missing: %s", out)
	}
	if !strings.Contains(out, "line1%0Aline2 with 100%25 and %0D return") {
		t.Errorf("data escaping (%%, CR, LF) wrong: %s", out)
	}
	// % must be escaped first or %0A would double-escape to %250A.
	if strings.Contains(out, "%250A") || strings.Contains(out, "%250D") {
		t.Errorf("double-escaped newline markers: %s", out)
	}
}

func TestAnnotationsCap(t *testing.T) {
	var findings []lint.Finding
	for i := 0; i < 15; i++ {
		findings = append(findings, lint.Finding{
			Rule: "D002", Severity: lint.Warn, File: "wf.yml", Line: i + 1, Message: fmt.Sprintf("w%d", i)})
	}
	for i := 0; i < 12; i++ {
		findings = append(findings, lint.Finding{
			Rule: "D009", Severity: lint.Info, File: "wf.yml", Line: i + 1, Message: fmt.Sprintf("i%d", i)})
	}
	var b strings.Builder
	Annotations(&b, nil, findings)
	out := b.String()

	warns := strings.Count(out, "::warning ")
	if warns != annotationsPerTypeCap {
		t.Errorf("want %d ::warning lines, got %d", annotationsPerTypeCap, warns)
	}
	// 10 info notices + 1 summary notice for the 5+2 skipped.
	notices := strings.Count(out, "::notice ")
	if notices != annotationsPerTypeCap+1 {
		t.Errorf("want %d ::notice lines (cap + summary), got %d", annotationsPerTypeCap+1, notices)
	}
	if !strings.Contains(out, "7 more finding(s)") {
		t.Errorf("summary notice should count 7 skipped findings:\n%s", out)
	}
}

func TestAnnotationsWorkspaceRelative(t *testing.T) {
	// `--dir sub/checkout` scans a repo below the CWD: the annotation path
	// must stay workspace-relative (sub/checkout/.github/...), because the
	// runner resolves it against the workspace root — the first base (".")
	// wins over the scan dir.
	findings := []lint.Finding{
		{Rule: "D002", Severity: lint.Warn, File: "sub/checkout/.github/workflows/ci.yml", Line: 2, Message: "m"},
	}
	var b strings.Builder
	Annotations(&b, []string{".", "sub/checkout"}, findings)
	if !strings.Contains(b.String(), "file=sub/checkout/.github/workflows/ci.yml,") {
		t.Errorf("want workspace-relative path, got: %s", b.String())
	}

	// An absolute scan dir outside the CWD can't be CWD-relative; the scan
	// dir fallback still yields a repo-relative path.
	findings[0].File = "/abs/elsewhere/.github/workflows/ci.yml"
	b.Reset()
	Annotations(&b, []string{".", "/abs/elsewhere"}, findings)
	if !strings.Contains(b.String(), "file=.github/workflows/ci.yml,") {
		t.Errorf("want scan-dir-relative fallback, got: %s", b.String())
	}
}

func TestAnnotationsLineFloor(t *testing.T) {
	findings := []lint.Finding{
		{Rule: "D001", Severity: lint.Warn, File: "wf.yml", Line: 0, Message: "m"},
	}
	var b strings.Builder
	Annotations(&b, nil, findings)
	if !strings.Contains(b.String(), "line=1,") {
		t.Errorf("line 0 should floor to 1: %s", b.String())
	}
}
