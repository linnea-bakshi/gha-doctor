// Package report renders lint findings and run analysis for terminals,
// JSON, and Markdown.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Color helpers (respect NO_COLOR and non-TTY via Plain).
type Style struct{ Plain bool }

func (s Style) c(code, str string) string {
	if s.Plain {
		return str
	}
	return "\x1b[" + code + "m" + str + "\x1b[0m"
}
func (s Style) bold(str string) string   { return s.c("1", str) }
func (s Style) red(str string) string    { return s.c("31", str) }
func (s Style) yellow(str string) string { return s.c("33", str) }
func (s Style) green(str string) string  { return s.c("32", str) }
func (s Style) cyan(str string) string   { return s.c("36", str) }
func (s Style) dim(str string) string    { return s.c("2", str) }

// AutoStyle picks colors unless NO_COLOR is set or stdout isn't a TTY.
func AutoStyle() Style {
	if os.Getenv("NO_COLOR") != "" {
		return Style{Plain: true}
	}
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return Style{Plain: true}
	}
	return Style{}
}

// Combined is the full JSON output document.
type Combined struct {
	Findings []lint.Finding `json:"findings"`
	Analysis *api.Analysis  `json:"analysis,omitempty"`
}

// JSON writes the combined report as JSON.
func JSON(w io.Writer, findings []lint.Finding, a *api.Analysis) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if findings == nil {
		findings = []lint.Finding{}
	}
	return enc.Encode(Combined{Findings: findings, Analysis: a})
}

// Findings renders lint findings for the terminal.
func Findings(w io.Writer, s Style, findings []lint.Finding, filesScanned int) {
	fmt.Fprintf(w, "%s\n", s.bold(fmt.Sprintf("── Workflow checkup (%d files) ──", filesScanned)))
	if len(findings) == 0 {
		fmt.Fprintf(w, "%s\n", s.green("✓ no issues found"))
		return
	}
	warns, infos := 0, 0
	for _, f := range findings {
		var tag string
		if f.Severity == lint.Warn {
			tag = s.yellow("warn")
			warns++
		} else {
			tag = s.cyan("info")
			infos++
		}
		fmt.Fprintf(w, "%s %s %s %s\n", tag, s.bold(f.Rule), s.dim(fmt.Sprintf("%s:%d", f.File, f.Line)), f.Message)
		if f.Advice != "" {
			fmt.Fprintf(w, "     %s %s\n", s.dim("fix:"), f.Advice)
		}
	}
	fmt.Fprintf(w, "%s\n", s.dim(fmt.Sprintf("%d warnings, %d suggestions", warns, infos)))
}

// Analysis renders run-history stats for the terminal.
func Analysis(w io.Writer, s Style, a *api.Analysis) {
	fmt.Fprintf(w, "\n%s\n", s.bold(fmt.Sprintf("── Run history: %s (last %d runs, since %s) ──",
		a.Repo, a.RunsSampled, a.Since.Format("2006-01-02"))))

	// Workflows table
	fmt.Fprintf(w, "\n%s\n", s.bold("Workflows"))
	fmt.Fprintf(w, "  %-38s %5s %9s %8s %8s %7s\n", "name", "runs", "success", "p50", "p95", "queue")
	for _, wf := range a.Workflows {
		rate := fmt.Sprintf("%.0f%%", wf.SuccessRate*100)
		switch {
		case wf.SuccessRate >= 0.95:
			rate = s.green(rate)
		case wf.SuccessRate >= 0.80:
			rate = s.yellow(rate)
		default:
			rate = s.red(rate)
		}
		fmt.Fprintf(w, "  %-38s %5d %9s %7.1fm %7.1fm %6.0fs\n",
			trunc(wf.Name, 38), wf.Runs, rate+pad(wf.SuccessRate), wf.P50Minutes, wf.P95Minutes, wf.AvgQueueSec)
	}

	// Flaky jobs
	fmt.Fprintf(w, "\n%s\n", s.bold("Flaky jobs")+s.dim("  (failed AND passed on the same commit)"))
	if len(a.FlakyJobs) == 0 {
		fmt.Fprintf(w, "  %s\n", s.green("✓ none detected in sample"))
	} else {
		fmt.Fprintf(w, "  %-30s %-24s %7s %9s %8s\n", "job", "workflow", "commits", "flakerate", "wasted")
		for _, fj := range a.FlakyJobs {
			fmt.Fprintf(w, "  %-30s %-24s %7d %8.0f%% %6.0fm\n",
				trunc(fj.Job, 30), trunc(fj.Workflow, 24), fj.FlakyCommits, fj.FlakeRate*100, fj.WastedMinutes)
		}
	}

	// Slowest steps
	fmt.Fprintf(w, "\n%s\n", s.bold("Slowest steps")+s.dim("  (by total time across sample)"))
	fmt.Fprintf(w, "  %-30s %-30s %5s %8s %8s\n", "step", "job", "n", "p50", "total")
	for _, st := range a.SlowSteps {
		fmt.Fprintf(w, "  %-30s %-30s %5d %7.1fm %7.0fm\n",
			trunc(st.Step, 30), trunc(st.Job, 30), st.Count, st.P50Minutes, st.TotalMin)
	}

	// Waste
	fmt.Fprintf(w, "\n%s\n", s.bold("Wasted compute")+s.dim("  (billing-weighted: macOS 10x, Windows 2x)"))
	pct := 0.0
	if a.Waste.ComputeMinutes > 0 {
		pct = a.Waste.TotalMinutes / a.Waste.ComputeMinutes * 100
	}
	fmt.Fprintf(w, "  failed runs: %.0f min   retried attempts: %.0f min\n",
		a.Waste.FailedRunMinutes, a.Waste.RetryMinutes)
	total := fmt.Sprintf("  → %.0f of %.0f minutes bought nothing (%.0f%%)", a.Waste.TotalMinutes, a.Waste.ComputeMinutes, pct)
	if pct >= 15 {
		fmt.Fprintf(w, "%s\n", s.red(total))
	} else if pct >= 5 {
		fmt.Fprintf(w, "%s\n", s.yellow(total))
	} else {
		fmt.Fprintf(w, "%s\n", s.green(total))
	}
}

// Markdown renders the whole report as Markdown (for pasting into issues).
func Markdown(w io.Writer, findings []lint.Finding, filesScanned int, a *api.Analysis) {
	fmt.Fprintf(w, "## gha-doctor report\n\n")
	fmt.Fprintf(w, "### Workflow checkup (%d files)\n\n", filesScanned)
	if len(findings) == 0 {
		fmt.Fprintf(w, "No issues found.\n\n")
	} else {
		fmt.Fprintf(w, "| rule | severity | location | message |\n|---|---|---|---|\n")
		for _, f := range findings {
			fmt.Fprintf(w, "| %s | %s | %s:%d | %s |\n", f.Rule, f.Severity, f.File, f.Line, strings.ReplaceAll(f.Message, "|", "\\|"))
		}
		fmt.Fprintln(w)
	}
	if a == nil {
		return
	}
	fmt.Fprintf(w, "### Run history: %s (last %d runs)\n\n", a.Repo, a.RunsSampled)
	fmt.Fprintf(w, "| workflow | runs | success | p50 | p95 |\n|---|---|---|---|---|\n")
	for _, wf := range a.Workflows {
		fmt.Fprintf(w, "| %s | %d | %.0f%% | %.1fm | %.1fm |\n", wf.Name, wf.Runs, wf.SuccessRate*100, wf.P50Minutes, wf.P95Minutes)
	}
	if len(a.FlakyJobs) > 0 {
		fmt.Fprintf(w, "\n**Flaky jobs** (failed and passed on the same commit):\n\n")
		fmt.Fprintf(w, "| job | workflow | flaky commits | wasted minutes |\n|---|---|---|---|\n")
		for _, fj := range a.FlakyJobs {
			fmt.Fprintf(w, "| %s | %s | %d | %.0f |\n", fj.Job, fj.Workflow, fj.FlakyCommits, fj.WastedMinutes)
		}
	}
	fmt.Fprintf(w, "\n**Wasted compute:** %.0f of %.0f minutes (failed runs %.0f + retries %.0f).\n",
		a.Waste.TotalMinutes, a.Waste.ComputeMinutes, a.Waste.FailedRunMinutes, a.Waste.RetryMinutes)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// pad compensates column width lost to invisible ANSI codes — a no-op hack
// placeholder for future alignment logic.
func pad(float64) string { return "" }
