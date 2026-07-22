package report

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func sampleFindings() []lint.Finding {
	return []lint.Finding{
		{Rule: "D001", Severity: lint.Warn, SevStr: "warn", File: ".github/workflows/ci.yml",
			Line: 3, Message: "no concurrency group", Advice: "add concurrency: with cancel-in-progress"},
		{Rule: "D005", Severity: lint.Info, SevStr: "info", File: ".github/workflows/ci.yml",
			Line: 12, Message: "setup-node without cache"},
	}
}

func sampleAnalysis() *api.Analysis {
	return &api.Analysis{
		Repo:        "o/r",
		RunsSampled: 42,
		Since:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Workflows: []api.WorkflowStats{
			{Name: "CI", Runs: 40, SuccessRate: 0.75, P50Minutes: 8.2, P95Minutes: 14.9, AvgQueueSec: 12},
		},
		FlakyJobs: []api.FlakyJob{
			{Workflow: "CI", Job: "test", FlakyCommits: 3, Failures: 4, Runs: 40, FlakeRate: 0.1, WastedMinutes: 33},
		},
		SlowSteps: []api.StepStats{
			{Job: "test", Step: "Build image", Count: 40, P50Minutes: 4.4, TotalMin: 176},
		},
		Waste: api.WasteStats{FailedRunMinutes: 100, RetryMinutes: 33, TotalMinutes: 133, ComputeMinutes: 900},
		Cache: api.CacheStats{
			Available: true, Count: 7, TotalMB: 9500, LimitPct: 92.8,
			StaleCount: 2, StaleMB: 5500, PRRefCount: 1, PRRefMB: 2500,
			Largest: []api.CacheEntry{{Key: "go-build-linux", Ref: "refs/heads/main", SizeMB: 4000}},
		},
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleFindings(), sampleAnalysis()); err != nil {
		t.Fatal(err)
	}
	var doc Combined
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(doc.Findings) != 2 || doc.Analysis == nil || doc.Analysis.Repo != "o/r" {
		t.Errorf("round trip lost data: %+v", doc)
	}
}

func TestJSONNilFindingsIsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"findings": null`) {
		t.Errorf("nil findings must serialize as [], got: %s", buf.String())
	}
}

func TestFindingsTerminal(t *testing.T) {
	var buf bytes.Buffer
	Findings(&buf, Style{Plain: true}, sampleFindings(), 2)
	out := buf.String()
	for _, want := range []string{"D001", "ci.yml:3", "fix:", "1 warnings, 1 suggestions"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("Plain style must not emit ANSI escapes")
	}
}

func TestFindingsEmptySkipsSection(t *testing.T) {
	var buf bytes.Buffer
	Findings(&buf, Style{Plain: true}, nil, 0)
	if buf.Len() != 0 {
		t.Errorf("no files scanned should print nothing, got: %q", buf.String())
	}
}

func TestAnalysisTerminal(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, sampleAnalysis())
	out := buf.String()
	for _, want := range []string{"o/r", "CI", "75%", "test", "Build image"} {
		if !strings.Contains(out, want) {
			t.Errorf("analysis output missing %q:\n%s", want, out)
		}
	}
}

func TestMarkdown(t *testing.T) {
	var buf bytes.Buffer
	Markdown(&buf, sampleFindings(), 2, sampleAnalysis())
	out := buf.String()
	for _, want := range []string{"| D001", "| CI", "test"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}

func TestSARIF(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, "v0.1.0-test", "", sampleFindings()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("SARIF output is not valid JSON: %v", err)
	}
	if doc["version"] != "2.1.0" {
		t.Errorf("version = %v, want 2.1.0", doc["version"])
	}
	out := buf.String()
	for _, want := range []string{`"ruleId": "D001"`, `"level": "warning"`, `"level": "note"`,
		`"uri": ".github/workflows/ci.yml"`, `"startLine": 3`, "MissingConcurrencyCancellation"} {
		if !strings.Contains(out, want) {
			t.Errorf("SARIF output missing %q", want)
		}
	}
}

func TestSARIFEmptyFindings(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, "dev", "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"results": null`) {
		t.Error("empty findings must serialize results as [], not null")
	}
}

func TestAnalysisTerminalCacheSection(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, sampleAnalysis())
	out := buf.String()
	for _, want := range []string{"7 caches", "93% of limit", "stale (unused 7+ days): 2 caches, 5500 MB",
		"on PR refs: 1 caches, 2500 MB", "go-build-linux"} {
		if !strings.Contains(out, want) {
			t.Errorf("cache section missing %q:\n%s", want, out)
		}
	}
}

func TestAnalysisTerminalCacheUnavailable(t *testing.T) {
	a := sampleAnalysis()
	a.Cache = api.CacheStats{Available: false, Note: "cache data unavailable (needs a token)"}
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	if !strings.Contains(buf.String(), "cache data unavailable") {
		t.Error("missing unavailable note")
	}
}

func TestAutoStyleForceColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")

	t.Setenv("FORCE_COLOR", "1")
	if s := AutoStyle(); s.Plain {
		t.Error("FORCE_COLOR=1 should enable color even without a TTY")
	}
	t.Setenv("FORCE_COLOR", "0")
	if s := AutoStyle(); !s.Plain {
		t.Error("FORCE_COLOR=0 should not force color")
	}
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	if s := AutoStyle(); s.Plain {
		t.Error("CLICOLOR_FORCE=1 should enable color even without a TTY")
	}
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "1")
	if s := AutoStyle(); !s.Plain {
		t.Error("NO_COLOR wins over FORCE_COLOR")
	}
}

func TestAnalysisColorAlignmentMatchesPlain(t *testing.T) {
	var plain, colored bytes.Buffer
	Analysis(&plain, Style{Plain: true}, sampleAnalysis())
	Analysis(&colored, Style{}, sampleAnalysis())
	re := regexp.MustCompile("\x1b\\[[0-9;]*m")
	stripped := re.ReplaceAllString(colored.String(), "")
	if stripped != plain.String() {
		t.Errorf("colored output (ANSI stripped) differs from plain output:\n--- plain ---\n%s\n--- stripped ---\n%s", plain.String(), stripped)
	}
}
