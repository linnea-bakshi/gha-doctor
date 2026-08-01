package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

func deepFixture() *api.RunDeep {
	return &api.RunDeep{
		Repo: "o/r", RunID: 99, RunNumber: 42, Workflow: "CI",
		Title: "fix the frobnicator", Branch: "main", Event: "push",
		Status: "completed", Conclusion: "success",
		StartedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		Attempt:   1, WallSec: 300,
		BaselineRuns: 8, BaselineWallP50: 120,
		Jobs: []api.DeepJob{
			{
				Name: "test", Conclusion: "success", StartSec: 10, EndSec: 300,
				QueueSec: 10, DurSec: 290, BaselineN: 8, P50Sec: 100, Attempts: 1,
				Steps: []api.DeepStep{
					{Name: "Run tests", Number: 2, Conclusion: "success", DurSec: 250, BaselineN: 8, P50Sec: 60},
					{Name: "Set up job", Number: 1, Conclusion: "success", DurSec: 5},
				},
			},
			{Name: "lint", Conclusion: "skipped"},
		},
	}
}

func TestRunDeepTerminal(t *testing.T) {
	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, deepFixture())
	out := b.String()
	for _, want := range []string{
		"Run #42: CI (o/r)",
		"2.5x the p50 (2m00s)",                     // wall verdict: 300 vs 120
		"\"Run tests\" in test: +3m10s vs its p50", // biggest mover: 250-60
		"4.2x slower",                              // step table ratio 250/60
		"wall clock 5m00s",
		"2.9x p50", // job-level annotation: 290 vs 100
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q\n%s", want, out)
		}
	}
	// Skipped job renders a dash, not "still running".
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "lint") && !strings.Contains(line, "–") {
			t.Errorf("skipped job line should show –, got %q", line)
		}
	}
}

func TestRunDeepFailedVerdicts(t *testing.T) {
	d := deepFixture()
	d.Conclusion = "failure"
	d.Jobs[0].Conclusion = "failure"
	d.Jobs[0].Steps[0].Conclusion = "failure"
	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, d)
	out := b.String()
	if !strings.Contains(out, `job "test" failed at step "Run tests"`) {
		t.Errorf("failed run should name the failing job+step:\n%s", out)
	}
	if strings.Contains(out, "faster") || strings.Contains(out, "✓ this run took") {
		t.Errorf("failed run must not be praised for speed:\n%s", out)
	}
	if !strings.Contains(out, "stopped after 5m00s") {
		t.Errorf("failed run should state the neutral comparison:\n%s", out)
	}
}

func TestRunDeepInProgress(t *testing.T) {
	d := deepFixture()
	d.Status = "in_progress"
	d.Conclusion = ""
	d.InProgress = true
	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, d)
	out := b.String()
	if !strings.Contains(out, "still running: 5m00s so far") {
		t.Errorf("in-progress run should say 'so far', got:\n%s", out)
	}
	if strings.Contains(out, "faster") || strings.Contains(out, "slower than") {
		t.Errorf("in-progress run must not get a speed verdict:\n%s", out)
	}
}

func TestRunDeepRerunVerdict(t *testing.T) {
	d := deepFixture()
	d.Attempt = 3
	d.RetriedJobs = 2
	d.CarriedJobs = 1
	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, d)
	out := b.String()
	if !strings.Contains(out, "attempt 3 of this run: 2 jobs ran again, 1 carried over") {
		t.Errorf("re-run verdict missing:\n%s", out)
	}
}

func TestRunDeepMarkdown(t *testing.T) {
	var b strings.Builder
	RunDeepMarkdown(&b, deepFixture())
	out := b.String()
	for _, want := range []string{
		"## Run #42: CI (o/r)",
		"| test | 10s | 4m50s | 2.9x (p50 1m40s, n=8) |",
		"| lint | 0s | – | – |",
		"| Run tests | test | 4m10s | 1m00s |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n%s", want, out)
		}
	}
}

func TestHumanSec(t *testing.T) {
	cases := map[float64]string{
		0:    "0s",
		47:   "47s",
		59.6: "1m00s",
		192:  "3m12s",
		3599: "59m59s",
		3840: "1h04m",
		7325: "2h02m",
	}
	for in, want := range cases {
		if got := humanSec(in); got != want {
			t.Errorf("humanSec(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestRunDeepBaselineNote(t *testing.T) {
	d := deepFixture()
	d.BaselineRuns = 1
	d.BaselineWallP50 = 0
	d.BaselineNote = "only 1 comparable successful runs of this workflow — too few to compare against"
	d.Jobs[0].BaselineN = 0
	d.Jobs[0].P50Sec = 0
	d.Jobs[0].Steps[0].BaselineN = 0
	d.Jobs[0].Steps[0].P50Sec = 0
	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, d)
	out := b.String()
	if !strings.Contains(out, "too few to compare") {
		t.Errorf("baseline note missing:\n%s", out)
	}
	if strings.Contains(out, "p50 (") {
		t.Errorf("no wall comparison should render without a baseline:\n%s", out)
	}
}

func TestRunDeepNonDecisiveVerdict(t *testing.T) {
	for _, concl := range []string{"skipped", "cancelled"} {
		d := deepFixture()
		d.Conclusion = concl
		var b strings.Builder
		RunDeep(&b, Style{Plain: true}, d)
		out := b.String()
		if strings.Contains(out, "faster") || strings.Contains(out, "✓ this run took") {
			t.Errorf("%s run must not get a speed verdict:\n%s", concl, out)
		}
		if !strings.Contains(out, "this run was "+concl+" after 5m00s") {
			t.Errorf("%s run should state its state neutrally:\n%s", concl, out)
		}
	}
}

func TestRunDeepLogTailSections(t *testing.T) {
	d := deepFixture()
	d.Conclusion = "failure"
	d.Jobs[0].Conclusion = "failure"
	d.Jobs[0].LogStep = "Run tests"
	d.Jobs[0].LogTail = []string{"--- FAIL: TestThing", "##[error]Process completed with exit code 1."}

	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, d)
	out := b.String()
	for _, want := range []string{
		"Failing step log — test › Run tests",
		"(last 2 lines)",
		"--- FAIL: TestThing",
		"##[error]Process completed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q\n%s", want, out)
		}
	}

	b.Reset()
	RunDeepMarkdown(&b, d)
	md := b.String()
	if !strings.Contains(md, "### Failing step log — test › Run tests") ||
		!strings.Contains(md, "```text\n--- FAIL: TestThing") {
		t.Errorf("markdown output missing log tail section\n%s", md)
	}

	// No tail (e.g. unauthenticated): note renders, section doesn't.
	d.Jobs[0].LogTail = nil
	d.LogNote = "failing-step log tails need authentication"
	b.Reset()
	RunDeep(&b, Style{Plain: true}, d)
	out = b.String()
	if strings.Contains(out, "Failing step log") {
		t.Errorf("log section rendered with no tail\n%s", out)
	}
	if !strings.Contains(out, "note: failing-step log tails need authentication") {
		t.Errorf("LogNote missing\n%s", out)
	}
}

func TestRunDeepFailedTests(t *testing.T) {
	d := deepFixture()
	d.Conclusion = "failure"
	d.Jobs[0].Conclusion = "failure"
	d.Jobs[0].Steps = []api.DeepStep{{Name: "Run tests", Number: 2, Conclusion: "failure", DurSec: 30}}
	for i := 0; i < 12; i++ {
		d.Jobs[0].FailedTests = append(d.Jobs[0].FailedTests,
			api.RunFailedTest{Name: fmt.Sprintf("test_case_%02d", i), Framework: "pytest"})
	}
	d.Jobs[0].FailedTestsMore = 3 // 15 recognized in total

	var b strings.Builder
	RunDeep(&b, Style{Plain: true}, d)
	out := b.String()
	for _, want := range []string{
		"Failing tests — test",
		"✗ test_case_00  (pytest)",
		"✗ test_case_09  (pytest)",
		"… and 5 more", // 2 past display cap + 3 past storage cap
		`— 15 failing tests incl. test_case_00`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "test_case_10") {
		t.Errorf("display cap not applied\n%s", out)
	}

	b.Reset()
	RunDeepMarkdown(&b, d)
	md := b.String()
	for _, want := range []string{
		"### Failing tests — test",
		"- `test_case_00` (pytest)",
		"- … and 5 more",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown output missing %q\n%s", want, md)
		}
	}

	// Single failing test: verdict names it without a count.
	d.Jobs[0].FailedTests = d.Jobs[0].FailedTests[:1]
	d.Jobs[0].FailedTestsMore = 0
	b.Reset()
	RunDeep(&b, Style{Plain: true}, d)
	if !strings.Contains(b.String(), "— failing test: test_case_00") {
		t.Errorf("single-test verdict missing\n%s", b.String())
	}
}
