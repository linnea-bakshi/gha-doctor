package report

import (
	"bytes"
	"encoding/json"
	"fmt"
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
			{Name: "CI", Runs: 40, Decisive: 40, SuccessRate: 0.75, P50Minutes: 8.2, P95Minutes: 14.9, AvgQueueSec: 12},
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
		Artifacts: api.ArtifactStats{
			Available: true, Count: 841, Sampled: true, SampleCount: 300,
			WindowDays: 12.5, ActiveCount: 250, ActiveMB: 4200,
			EstStorageGB: 18.4, EstUSDPerMo: 4.42,
			EstimateBasis: "upload rate over 12.5 sampled days × per-name retention",
			Producers: []api.ArtifactProducer{
				{Name: "test-results", Count: 120, TotalMB: 3100, AvgMB: 26, RetentionDays: 90, SteadyGB: 17.1},
				{Name: "coverage", Count: 80, TotalMB: 900, AvgMB: 11, RetentionDays: 7, SteadyGB: 1.3},
			},
		},
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleFindings(), 2, nil, nil, sampleAnalysis(), nil, nil); err != nil {
		t.Fatal(err)
	}
	var doc Combined
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(doc.Findings) != 2 || doc.Analysis == nil || doc.Analysis.Repo != "o/r" {
		t.Errorf("round trip lost data: %+v", doc)
	}
	if doc.FilesScanned != 2 {
		t.Errorf("files_scanned = %d, want 2", doc.FilesScanned)
	}
}

func TestJSONNilFindingsIsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, nil, 0, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"findings": null`) {
		t.Errorf("nil findings must serialize as [], got: %s", buf.String())
	}
}

func TestFindingsTerminal(t *testing.T) {
	var buf bytes.Buffer
	Findings(&buf, Style{Plain: true}, sampleFindings(), 2, nil)
	out := buf.String()
	for _, want := range []string{"D001", "ci.yml:3", "fix:", "1 warning, 1 suggestion"} {
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
	Findings(&buf, Style{Plain: true}, nil, 0, nil)
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
	Markdown(&buf, sampleFindings(), 2, nil, sampleAnalysis(), nil, nil)
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
		"on PR refs: 1 cache, 2500 MB", "go-build-linux"} {
		if !strings.Contains(out, want) {
			t.Errorf("cache section missing %q:\n%s", want, out)
		}
	}
}

func TestAnalysisTerminalCacheOverLimit(t *testing.T) {
	// vercel/next.js sat at ~200 GB — "1997% of limit" reads like a bug.
	// Past overLimitPct the section must switch to absolute terms.
	a := sampleAnalysis()
	a.Cache.TotalMB = 204800 // 200 GB
	a.Cache.LimitPct = 2000
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	for _, want := range []string{"200.0 GB", "190.0 GB over the 10 GB limit", "eviction churn"} {
		if !strings.Contains(out, want) {
			t.Errorf("over-limit cache section missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "% of limit") {
		t.Errorf("over-limit cache section must not print %% of limit:\n%s", out)
	}

	var md bytes.Buffer
	Markdown(&md, nil, 0, nil, a, nil, nil)
	if !strings.Contains(md.String(), "over the 10 GB limit") {
		t.Errorf("markdown over-limit cache line missing:\n%s", md.String())
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

func TestWorkflowTailAggregation(t *testing.T) {
	mk := func(n int) []api.WorkflowStats {
		wfs := make([]api.WorkflowStats, n)
		for i := range wfs {
			wfs[i] = api.WorkflowStats{Name: fmt.Sprintf("wf-%02d", i), Runs: 1, EstUSD: 0.5}
		}
		return wfs
	}

	// At or one past the cap: no tail (a one-row summary saves nothing).
	for _, n := range []int{0, 1, maxWorkflowRows, maxWorkflowRows + 1} {
		shown, rest := splitWorkflowTail(mk(n))
		if len(shown) != n || rest.Count != 0 {
			t.Errorf("n=%d: want all %d shown and no tail, got %d shown, tail %+v", n, n, len(shown), rest)
		}
	}

	// Well past the cap: tail aggregates count, runs, and est$.
	shown, rest := splitWorkflowTail(mk(maxWorkflowRows + 10))
	if len(shown) != maxWorkflowRows {
		t.Fatalf("want %d shown, got %d", maxWorkflowRows, len(shown))
	}
	if rest.Count != 10 || rest.Runs != 10 || rest.EstUSD != 5.0 {
		t.Errorf("tail = %+v, want Count=10 Runs=10 EstUSD=5.0", rest)
	}
}

func TestAnalysisTerminalWorkflowTailRow(t *testing.T) {
	a := sampleAnalysis()
	for i := 0; i < maxWorkflowRows+5; i++ {
		a.Workflows = append(a.Workflows, api.WorkflowStats{Name: fmt.Sprintf("generated-%d", i), Runs: 1})
	}
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	if !strings.Contains(out, "more workflows") {
		t.Errorf("terminal output missing tail summary row:\n%s", out)
	}
	if strings.Contains(out, "generated-20") {
		t.Errorf("tail workflow leaked into terminal table:\n%s", out)
	}

	var md bytes.Buffer
	Markdown(&md, nil, 0, nil, a, nil, nil)
	if !strings.Contains(md.String(), "more workflows") {
		t.Errorf("markdown output missing tail summary row:\n%s", md.String())
	}
}

func TestBaselineRendering(t *testing.T) {
	b := &lint.Baseline{Ref: "origin/main", Hidden: 3, Fixed: 1}

	// Markdown, no new findings: says "no new issues" + comparison note.
	var md bytes.Buffer
	Markdown(&md, nil, 2, b, nil, nil, nil)
	if !strings.Contains(md.String(), "No new issues since `origin/main`") {
		t.Errorf("md missing no-new-issues line:\n%s", md.String())
	}
	if !strings.Contains(md.String(), "3 pre-existing finding(s) hidden, 1 fixed") {
		t.Errorf("md missing comparison note:\n%s", md.String())
	}

	// Terminal, with findings: summary line mentions the baseline.
	var buf bytes.Buffer
	Findings(&buf, Style{Plain: true}, sampleFindings(), 2, b)
	if !strings.Contains(buf.String(), "new since origin/main") {
		t.Errorf("terminal summary missing baseline note:\n%s", buf.String())
	}

	// Terminal, clean: still renders the checkup header with the note even
	// with zero findings.
	buf.Reset()
	Findings(&buf, Style{Plain: true}, nil, 2, b)
	if !strings.Contains(buf.String(), "no new issues since origin/main") {
		t.Errorf("terminal clean output missing baseline wording:\n%s", buf.String())
	}

	// JSON carries the baseline block.
	var js bytes.Buffer
	if err := JSON(&js, nil, 1, nil, b, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"ref": "origin/main"`) || !strings.Contains(js.String(), `"hidden": 3`) {
		t.Errorf("json missing baseline block:\n%s", js.String())
	}
}

func TestAnalysisTerminalArtifactSection(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, sampleAnalysis())
	out := buf.String()
	for _, want := range []string{
		"841 artifacts; breakdown from the 300 most recent",
		"4.1 GB not yet expired",
		"~18.4 GB → ~$4.42/mo",
		"test-results",
		"← default 90d retention; set retention-days (D010)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("artifact section missing %q:\n%s", want, out)
		}
	}
	// 7-day producer must NOT carry the retention hint on its row.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "coverage") && strings.Contains(line, "D010") {
			t.Errorf("coverage keeps 7d; must not get the retention hint: %q", line)
		}
	}
}

func TestAnalysisTerminalArtifactShortWindow(t *testing.T) {
	a := sampleAnalysis()
	a.Artifacts.EstStorageGB = 0
	a.Artifacts.EstUSDPerMo = 0
	a.Artifacts.WindowDays = 0.4
	a.Artifacts.EstimateBasis = "sample spans only 0.4 days — too short to project steady-state storage"
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	if strings.Contains(out, "steady state") {
		t.Error("short window must not print a steady-state estimate")
	}
	if !strings.Contains(out, "too short to project") {
		t.Errorf("want the honesty note:\n%s", out)
	}
}

func TestMarkdownArtifactSection(t *testing.T) {
	var buf bytes.Buffer
	Markdown(&buf, nil, 0, nil, sampleAnalysis(), nil, nil)
	out := buf.String()
	for _, want := range []string{"**Artifacts:** 841 total", "~18.4 GB", "`test-results` (120 uploads"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown artifact section missing %q:\n%s", want, out)
		}
	}
}

func matrixAnalysis() *api.Analysis {
	return &api.Analysis{
		Repo:        "o/r",
		RunsSampled: 50,
		Matrix: &api.MatrixStats{
			GroupsMeasured: 2,
			Imbalanced: []api.MatrixGroup{{
				Workflow: "CI", Job: "test", Shards: 8, RunsMeasured: 20,
				P50WallMin: 12.0, P50IdealMin: 4.0, P50SavingMin: 8.0, Ratio: 3.0,
				SlowestShard: "(windows-latest, 3.12)", SlowestP50: 12.0,
				FastestShard: "(ubuntu-latest, 3.11)", FastestP50: 2.0,
			}},
		},
	}
}

func TestAnalysisRendersMatrixBalance(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, matrixAnalysis())
	out := buf.String()
	for _, want := range []string{"Matrix balance", "(windows-latest, 3.12)", "PR feedback latency", "waiting"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}
}

func TestAnalysisRendersMatrixBalancedGreen(t *testing.T) {
	a := matrixAnalysis()
	a.Matrix.Imbalanced = nil
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	if !strings.Contains(buf.String(), "shards look balanced across 2 measured groups") {
		t.Errorf("balanced case should render green line:\n%s", buf.String())
	}
}

func TestMarkdownRendersMatrixBalance(t *testing.T) {
	var buf bytes.Buffer
	Markdown(&buf, nil, 0, nil, matrixAnalysis(), nil, nil)
	out := buf.String()
	for _, want := range []string{"**Matrix balance**", "| test | CI | 8 | 12.0m | 4.0m | 8.0m |", "`(windows-latest, 3.12)`"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestFlakyTestNamesTerminalAndMarkdown(t *testing.T) {
	a := sampleAnalysis()
	a.FlakyTests = &api.FlakyTestStats{
		Available: true, LogsTotal: 14, LogsSampled: 12, JobsSkipped: 2,
		Tests: []api.FlakyTest{
			{Name: "tests/test_net.py::test_timeout|edge", Framework: "pytest", Failures: 3, Commits: 2, Jobs: []string{"test"}},
		},
	}
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	for _, want := range []string{"Flaky tests", "12 of 14", "tests/test_net.py::test_timeout|edge", "pytest", "did not reproduce", "2 log downloads could not be fetched"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	Markdown(&buf, nil, 0, nil, a, nil, nil)
	md := buf.String()
	// Pipe in the test name must be escaped so the table stays intact.
	for _, want := range []string{"**Flaky tests**", "`tests/test_net.py::test_timeout\\|edge`", "| pytest | 3 | 2 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown output missing %q:\n%s", want, md)
		}
	}
}

func TestFlakyTestNamesHintWhenNotSampled(t *testing.T) {
	a := sampleAnalysis()
	a.FlakyTests = nil
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	if len(a.FlakyJobs) > 0 && !strings.Contains(buf.String(), "--flaky-logs") {
		t.Errorf("expected --flaky-logs hint when flaky jobs exist and sampling was off:\n%s", buf.String())
	}
}

func TestFlakyTestNamesUnavailableNote(t *testing.T) {
	var buf bytes.Buffer
	FlakyTestNames(&buf, Style{Plain: true}, &api.FlakyTestStats{Available: false, Note: "needs auth"})
	if !strings.Contains(buf.String(), "needs auth") {
		t.Errorf("note not rendered: %q", buf.String())
	}
}

func TestZombieCronRendering(t *testing.T) {
	a := sampleAnalysis()
	a.ZombieCrons = []api.ZombieCron{
		{Workflow: "Nightly", URL: "https://github.com/o/r/actions/runs/9", Fails: 12,
			SpanDays: 14.1, LastFailedAt: time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC),
			MedianMinutes: 8, EstMinPerMo: 240, EstUSDPerMo: 1.92},
		{Workflow: "Stale sweep", URL: "https://github.com/o/r/actions/runs/8", Fails: 6,
			StreakOpen: true, SpanDays: 5, LastFailedAt: time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)},
	}

	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	for _, want := range []string{
		"Failing scheduled workflows",
		"Nightly — 12 consecutive scheduled failures over 14 days",
		"$1.92/mo",
		"≥ 6 consecutive", // open streak marker on the second entry
		"waste bucket",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}
	// The zero-estimate entry must not render a $0.00 figure.
	if strings.Contains(out, "$0.00/mo") {
		t.Errorf("terminal output renders a zero estimate:\n%s", out)
	}

	buf.Reset()
	Markdown(&buf, nil, 0, nil, a, nil, nil)
	md := buf.String()
	for _, want := range []string{
		"**Failing scheduled workflows**",
		"[Nightly](https://github.com/o/r/actions/runs/9) — 12 consecutive scheduled failures over 14 days, last 2026-07-30 (~240 min/mo, $1.92/mo",
		"- [Stale sweep](https://github.com/o/r/actions/runs/8) — ≥ 6 consecutive",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown output missing %q:\n%s", want, md)
		}
	}
}

func TestZombieCronAbsentSectionSilent(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, sampleAnalysis())
	if strings.Contains(buf.String(), "Failing scheduled") {
		t.Error("zombie section rendered with no zombie crons")
	}
}

func TestFeedbackRendering(t *testing.T) {
	a := sampleAnalysis()
	a.Feedback = &api.FeedbackStats{
		Pushes: 24, PRRuns: 60, P50Minutes: 18.4, P95Minutes: 47.0,
		Gaters: []api.GatingWorkflow{
			{Workflow: "Integration tests", Count: 18, Share: 0.75, SlackP50Minutes: 6.2},
			{Workflow: "Lint", Count: 2, Share: 2.0 / 24.0, SlackP50Minutes: 0.4}, // < 15% share: noise, hidden
		},
	}

	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	for _, want := range []string{
		"PR feedback time",
		"24 pushes with a full verdict",
		"median 18.4m, p95 47.0m",
		"critical path: Integration tests — last to finish on 75% of pushes (median 6.2m after the next-latest check)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Lint") {
		t.Errorf("terminal output renders a sub-15%%-share gater:\n%s", out)
	}

	buf.Reset()
	Markdown(&buf, nil, 0, nil, a, nil, nil)
	md := buf.String()
	for _, want := range []string{
		"**PR feedback time** (push → last check finishes; 24 pushes with a full verdict): median 18.4m, p95 47.0m.",
		"_Critical path: `Integration tests` — last to finish on 75% of pushes (median 6.2m after the next-latest check)._",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown output missing %q:\n%s", want, md)
		}
	}
}

func TestFeedbackAbsentSectionSilent(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, sampleAnalysis())
	if strings.Contains(buf.String(), "PR feedback time") {
		t.Error("feedback section rendered with no feedback stats")
	}
}

func TestAnalysisScopedHeaderAndNote(t *testing.T) {
	a := sampleAnalysis()
	a.Scope = &api.WorkflowScope{Name: "CI", Path: ".github/workflows/ci.yml"}
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	out := buf.String()
	for _, want := range []string{"workflow CI", ".github/workflows/ci.yml", "repo-wide", "PR feedback"} {
		if !strings.Contains(out, want) {
			t.Errorf("scoped terminal output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	Markdown(&buf, nil, 0, nil, a, nil, nil)
	out = buf.String()
	for _, want := range []string{"workflow CI", "`.github/workflows/ci.yml`", "repo-wide"} {
		if !strings.Contains(out, want) {
			t.Errorf("scoped markdown output missing %q:\n%s", want, out)
		}
	}

	// Unscoped output must not mention a scope.
	buf.Reset()
	Analysis(&buf, Style{Plain: true}, sampleAnalysis())
	if strings.Contains(buf.String(), "scoped") {
		t.Errorf("unscoped output mentions a scope:\n%s", buf.String())
	}
}

func trendAnalysis() *api.Analysis {
	return &api.Analysis{
		Repo:        "o/r",
		RunsSampled: 60,
		DurationTrends: &api.DurationTrends{
			Significant: []api.DurationTrend{{
				Workflow: "CI", OlderP50: 10.0, NewerP50: 16.0,
				OlderRuns: 14, NewerRuns: 14, ChangePct: 60, SpanHours: 156,
			}},
			MeasuredStable: 2,
		},
	}
}

func TestAnalysisRendersDurationTrend(t *testing.T) {
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, trendAnalysis())
	out := buf.String()
	for _, want := range []string{"Duration trend", "▲", "10.0m → 16.0m", "+60%", "7d", "14 vs 14 runs",
		"2 other measured workflows show no significant change"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}
}

func TestAnalysisRendersDurationTrendStableGreen(t *testing.T) {
	a := trendAnalysis()
	a.DurationTrends.Significant = nil
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	if !strings.Contains(buf.String(), "no significant p50 change across 2 measured workflows") {
		t.Errorf("stable case should render green line:\n%s", buf.String())
	}
}

func TestAnalysisSkipsDurationTrendWhenUnmeasured(t *testing.T) {
	a := trendAnalysis()
	a.DurationTrends = nil
	var buf bytes.Buffer
	Analysis(&buf, Style{Plain: true}, a)
	if strings.Contains(buf.String(), "Duration trend") {
		t.Errorf("unmeasured trend must not render a section:\n%s", buf.String())
	}
}

func TestMarkdownRendersDurationTrend(t *testing.T) {
	var buf bytes.Buffer
	Markdown(&buf, nil, 0, nil, trendAnalysis(), nil, nil)
	out := buf.String()
	for _, want := range []string{"**Duration trend**", "| CI | 10.0m | 16.0m | +60% | 7d | 14 → 14 |",
		"_2 other measured workflows show no significant change._"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestSpanStr(t *testing.T) {
	cases := map[float64]string{30: "30h", 47.9: "48h", 48: "2d", 156: "7d"}
	for in, want := range cases {
		if got := spanStr(in); got != want {
			t.Errorf("spanStr(%v) = %q, want %q", in, got, want)
		}
	}
}
