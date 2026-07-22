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
