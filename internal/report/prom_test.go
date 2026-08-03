package report

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

var promNow = time.Date(2026, 8, 3, 7, 0, 0, 0, time.UTC)

func promOut(t *testing.T, repo string, findings []lint.Finding, filesScanned int, a *api.Analysis, sc *Score) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Prom(&buf, "v1.2.3", repo, findings, filesScanned, a, sc, promNow); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// promSampleRe matches one sample line of the text exposition format.
var promSampleRe = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^{}]*\})? (-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?|NaN|[+-]Inf)$`)

// validateProm checks the structural rules our writer promises: every line
// is a well-formed comment or sample, each sample's family was declared
// with HELP and TYPE first, and no series (name + label set) repeats.
func validateProm(t *testing.T, out string) map[string]string {
	t.Helper()
	declared := map[string]bool{}
	series := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, "# HELP ") || strings.HasPrefix(line, "# TYPE ") {
			f := strings.Fields(line)
			if len(f) < 4 {
				t.Fatalf("malformed comment: %q", line)
			}
			if strings.HasPrefix(line, "# TYPE ") {
				if f[3] != "gauge" {
					t.Errorf("non-gauge TYPE: %q", line)
				}
				declared[f[2]] = true
			}
			continue
		}
		m := promSampleRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("malformed sample line: %q", line)
		}
		if !declared[m[1]] {
			t.Errorf("sample before its # TYPE declaration: %q", line)
		}
		key := m[1] + m[2]
		if _, dup := series[key]; dup {
			t.Errorf("duplicate series: %q", key)
		}
		series[key] = m[3]
	}
	return series
}

func TestPromFullReport(t *testing.T) {
	a := sampleAnalysis()
	a.Superseded = &api.SupersededStats{PRRuns: 30, Completed: 4, Cancelled: 10, WastedMinutes: 207, WastedUSD: 1.66}
	a.Feedback = &api.FeedbackStats{Pushes: 12, PRRuns: 30, P50Minutes: 11.9, P95Minutes: 40}
	a.CacheLogs = &api.CacheLogStats{Available: true, JobsSampled: 20, Restores: 18, Hits: 15, PartialHits: 1, Misses: 2, HitRate: 16.0 / 18}
	a.ZombieCrons = []api.ZombieCron{{Workflow: "Lock Threads", Fails: 26, SpanDays: 25}}
	sc := &Score{Points: 61, Grade: "C", Basis: "static + history", Components: []ScoreComponent{{Name: "x", Max: 10}}}
	out := promOut(t, "o/r", sampleFindings(), 2, a, sc)
	series := validateProm(t, out)

	want := map[string]string{
		`gha_doctor_info{repo="o/r",version="v1.2.3"}`:                       "1",
		`gha_doctor_files_scanned{repo="o/r"}`:                               "2",
		`gha_doctor_findings{repo="o/r",severity="warning"}`:                 "1",
		`gha_doctor_findings{repo="o/r",severity="info"}`:                    "1",
		`gha_doctor_health_score_points{repo="o/r"}`:                         "61",
		`gha_doctor_runs_sampled{repo="o/r"}`:                                "42",
		`gha_doctor_runs_missing_job_data{repo="o/r"}`:                       "0",
		`gha_doctor_workflow_runs{repo="o/r",workflow="CI"}`:                 "40",
		`gha_doctor_workflow_success_ratio{repo="o/r",workflow="CI"}`:        "0.75",
		`gha_doctor_workflow_duration_p50_seconds{repo="o/r",workflow="CI"}`: "492",
		`gha_doctor_flaky_jobs{repo="o/r"}`:                                  "1",
		`gha_doctor_zombie_crons{repo="o/r"}`:                                "1",
		`gha_doctor_waste_failed_run_seconds{repo="o/r"}`:                    "6000",
		`gha_doctor_compute_seconds{repo="o/r"}`:                             "54000",
		`gha_doctor_cache_entries{repo="o/r"}`:                               "7",
		`gha_doctor_cache_size_bytes{repo="o/r"}`:                            "9961472000",
		`gha_doctor_cache_limit_ratio{repo="o/r"}`:                           "0.928",
		`gha_doctor_cache_log_restores{repo="o/r"}`:                          "18",
		`gha_doctor_artifact_entries{repo="o/r"}`:                            "841",
		`gha_doctor_superseded_completed_runs{repo="o/r"}`:                   "4",
		`gha_doctor_superseded_wasted_seconds{repo="o/r"}`:                   "12420",
		`gha_doctor_superseded_wasted_cost_usd{repo="o/r"}`:                  "1.66",
		`gha_doctor_pr_feedback_pushes{repo="o/r"}`:                          "12",
		`gha_doctor_pr_feedback_p50_seconds{repo="o/r"}`:                     "714",
		`gha_doctor_last_run_timestamp_seconds{repo="o/r"}`:                  "1785740400",
		`gha_doctor_sample_since_timestamp_seconds{repo="o/r"}`:              "1780272000",
	}
	for k, v := range want {
		got, ok := series[k]
		if !ok {
			t.Errorf("missing series %s", k)
			continue
		}
		if got != v {
			t.Errorf("%s = %s, want %s", k, got, v)
		}
	}
}

func TestPromOmitsUnmeasuredSections(t *testing.T) {
	// Lint-only run: no analysis, no score → no run/cost/cache families at
	// all. Absent is the honest zero for "we didn't look".
	out := promOut(t, "o/r", sampleFindings(), 2, nil, nil)
	validateProm(t, out)
	for _, family := range []string{
		"gha_doctor_runs_sampled", "gha_doctor_health_score_points",
		"gha_doctor_compute_seconds", "gha_doctor_cache_entries",
		"gha_doctor_pr_feedback_pushes", "gha_doctor_flaky_jobs",
	} {
		if strings.Contains(out, family) {
			t.Errorf("unmeasured family %s should be absent:\n%s", family, out)
		}
	}
	if !strings.Contains(out, `gha_doctor_findings{repo="o/r",severity="warning"} 1`) {
		t.Errorf("findings series missing:\n%s", out)
	}
}

func TestPromMeasuredZeroIsEmitted(t *testing.T) {
	// A sampled window with zero flaky jobs and zero zombie crons: "we
	// looked, it's zero" must be a real 0 sample, not a gap.
	a := sampleAnalysis()
	a.FlakyJobs = nil
	a.ZombieCrons = nil
	out := promOut(t, "o/r", nil, 0, a, nil)
	series := validateProm(t, out)
	if series[`gha_doctor_flaky_jobs{repo="o/r"}`] != "0" {
		t.Errorf("flaky_jobs should be an explicit 0:\n%s", out)
	}
	if series[`gha_doctor_zombie_crons{repo="o/r"}`] != "0" {
		t.Errorf("zombie_crons should be an explicit 0:\n%s", out)
	}
	// Cache unavailable (e.g. no token) → cache families absent entirely.
	a.Cache = api.CacheStats{Available: false}
	a.Artifacts = api.ArtifactStats{Available: false}
	out = promOut(t, "o/r", nil, 0, a, nil)
	validateProm(t, out)
	if strings.Contains(out, "gha_doctor_cache_") || strings.Contains(out, "gha_doctor_artifact_") {
		t.Errorf("unavailable cache/artifact families should be absent:\n%s", out)
	}
}

func TestPromSuccessRatioNeedsDecisiveRuns(t *testing.T) {
	a := sampleAnalysis()
	a.Workflows = append(a.Workflows, api.WorkflowStats{Name: "Docs", Runs: 2, Decisive: 0})
	out := promOut(t, "o/r", nil, 0, a, nil)
	series := validateProm(t, out)
	if _, ok := series[`gha_doctor_workflow_runs{repo="o/r",workflow="Docs"}`]; !ok {
		t.Errorf("Docs runs series missing:\n%s", out)
	}
	if _, ok := series[`gha_doctor_workflow_success_ratio{repo="o/r",workflow="Docs"}`]; ok {
		t.Errorf("success_ratio for a workflow with no decisive runs is undefined, not a number:\n%s", out)
	}
	if _, ok := series[`gha_doctor_workflow_duration_p50_seconds{repo="o/r",workflow="Docs"}`]; ok {
		t.Errorf("p50 for a workflow with no decisive runs should be absent:\n%s", out)
	}
}

func TestPromLabelEscaping(t *testing.T) {
	a := sampleAnalysis()
	a.Workflows[0].Name = "we\"ird \\ work\nflow"
	out := promOut(t, "o/r", nil, 0, a, nil)
	validateProm(t, out)
	want := `workflow="we\"ird \\ work\nflow"`
	if !strings.Contains(out, want) {
		t.Errorf("escaped label %s not found in:\n%s", want, out)
	}
	if strings.Count(out, "\n") != strings.Count(strings.TrimRight(out, "\n"), "\n")+1 {
		t.Errorf("raw newline leaked into output")
	}
}

func TestPromNoRepoLabel(t *testing.T) {
	out := promOut(t, "", sampleFindings(), 2, nil, nil)
	series := validateProm(t, out)
	if _, ok := series[`gha_doctor_findings{severity="warning"}`]; !ok {
		t.Errorf("expected label set without repo:\n%s", out)
	}
	if strings.Contains(out, "repo=") {
		t.Errorf("repo label should be absent when the repo is unknown:\n%s", out)
	}
}
