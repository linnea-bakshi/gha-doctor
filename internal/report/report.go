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
// FORCE_COLOR / CLICOLOR_FORCE (any non-empty value except "0") override
// the TTY check — handy for CI logs and piping into `less -R`.
func AutoStyle() Style {
	if os.Getenv("NO_COLOR") != "" {
		return Style{Plain: true}
	}
	for _, v := range []string{os.Getenv("FORCE_COLOR"), os.Getenv("CLICOLOR_FORCE")} {
		if v != "" && v != "0" {
			return Style{}
		}
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
	if filesScanned == 0 && len(findings) == 0 {
		return
	}
	fmt.Fprintf(w, "%s\n", s.bold(fmt.Sprintf("── Workflow checkup (%d %s) ──", filesScanned, plural(filesScanned, "file"))))
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
	fmt.Fprintf(w, "%s\n", s.dim(fmt.Sprintf("%d warnings, %d suggestions — gha-doctor --explain <rule> for details", warns, infos)))
}

// Analysis renders run-history stats for the terminal.
func Analysis(w io.Writer, s Style, a *api.Analysis) {
	fmt.Fprintf(w, "\n%s\n", s.bold(fmt.Sprintf("── Run history: %s (last %d runs, since %s) ──",
		a.Repo, a.RunsSampled, a.Since.Format("2006-01-02"))))

	// Workflows table
	fmt.Fprintf(w, "\n%s\n", s.bold("Workflows"))
	fmt.Fprintf(w, "  %-38s %5s %9s %8s %8s %7s %8s\n", "name", "runs", "success", "p50", "p95", "queue", "est$")
	for _, wf := range a.Workflows {
		plain := fmt.Sprintf("%.0f%%", wf.SuccessRate*100)
		rate := plain
		switch {
		case wf.SuccessRate >= 0.95:
			rate = s.green(rate)
		case wf.SuccessRate >= 0.80:
			rate = s.yellow(rate)
		default:
			rate = s.red(rate)
		}
		// Pad against the plain width: ANSI escape bytes would otherwise
		// eat the %9s alignment when color is on.
		if n := 9 - len(plain); n > 0 {
			rate = strings.Repeat(" ", n) + rate
		}
		fmt.Fprintf(w, "  %-38s %5d %s %7.1fm %7.1fm %6.0fs %8s\n",
			trunc(wf.Name, 38), wf.Runs, rate, wf.P50Minutes, wf.P95Minutes, wf.AvgQueueSec,
			fmt.Sprintf("$%.2f", wf.EstUSD))
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

	// Cost estimate
	if a.Cost.BillableMinutes > 0 {
		fmt.Fprintf(w, "\n%s\n", s.bold("Estimated cost")+s.dim("  (GitHub-hosted rates; free for public repos on standard runners)"))
		fmt.Fprintf(w, "  sample: %s  (%.0f billable min, per-job round-up incl.)\n",
			s.bold(fmt.Sprintf("$%.2f", a.Cost.EstimatedUSD)), a.Cost.BillableMinutes)
		if a.Cost.WastedUSD >= 0.01 {
			fmt.Fprintf(w, "  wasted on failures/retries: %s\n", s.red(fmt.Sprintf("$%.2f", a.Cost.WastedUSD)))
		}
		if a.Cost.RoundingUSD >= 0.01 {
			fmt.Fprintf(w, "  round-up overhead: %s %s\n",
				fmt.Sprintf("$%.2f (%.0f min)", a.Cost.RoundingUSD, a.Cost.RoundingMinutes),
				s.dim("— every job is billed to the next whole minute"))
		}
		if a.Cost.SelfHostedJobs > 0 {
			fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("(%d self-hosted jobs excluded — not billed by GitHub)", a.Cost.SelfHostedJobs)))
		}
	}

	// Cache checkup
	fmt.Fprintf(w, "\n%s\n", s.bold("Cache")+s.dim("  (10 GB per-repo limit; GitHub evicts oldest first)"))
	if !a.Cache.Available {
		fmt.Fprintf(w, "  %s\n", s.dim(a.Cache.Note))
	} else if a.Cache.Count == 0 {
		fmt.Fprintf(w, "  %s\n", s.dim("no caches — if builds download the same deps every run, actions/cache would help"))
	} else {
		usage := fmt.Sprintf("  %d caches, %.0f MB (%.0f%% of limit)", a.Cache.Count, a.Cache.TotalMB, a.Cache.LimitPct)
		switch {
		case a.Cache.LimitPct >= 90:
			fmt.Fprintf(w, "%s %s\n", s.red(usage), s.dim("— evictions imminent; expect cold builds"))
		case a.Cache.LimitPct >= 70:
			fmt.Fprintf(w, "%s\n", s.yellow(usage))
		default:
			fmt.Fprintf(w, "%s\n", s.green(usage))
		}
		if a.Cache.StaleCount > 0 {
			fmt.Fprintf(w, "  stale (unused 7+ days): %d caches, %.0f MB %s\n",
				a.Cache.StaleCount, a.Cache.StaleMB, s.dim("— gh cache delete, or let GitHub evict them"))
		}
		if a.Cache.PRRefCount > 0 {
			fmt.Fprintf(w, "  on PR refs: %d caches, %.0f MB %s\n",
				a.Cache.PRRefCount, a.Cache.PRRefMB, s.dim("— unreachable from other branches; dead weight after merge"))
		}
		for i, e := range a.Cache.Largest {
			if i >= 3 {
				break
			}
			fmt.Fprintf(w, "  %s %-46s %7.0f MB %s\n", s.dim("·"), trunc(e.Key, 46), e.SizeMB, s.dim(trunc(e.Ref, 24)))
		}
	}
	CacheHitRate(w, s, a.CacheLogs)
}

// CacheHitRate renders the sampled-log cache hit/miss section.
func CacheHitRate(w io.Writer, s Style, cl *api.CacheLogStats) {
	if cl == nil {
		return
	}
	fmt.Fprintf(w, "\n%s\n", s.bold("Cache hit rate")+s.dim(fmt.Sprintf("  (from %d sampled job logs)", cl.JobsSampled)))
	if !cl.Available {
		fmt.Fprintf(w, "  %s\n", s.dim(cl.Note))
		return
	}
	plain := fmt.Sprintf("%.0f%%", cl.HitRate)
	rate := plain
	switch {
	case cl.HitRate >= 90:
		rate = s.green(plain)
	case cl.HitRate >= 60:
		rate = s.yellow(plain)
	default:
		rate = s.red(plain)
	}
	fmt.Fprintf(w, "  %s hit rate — %d restores: %d hits, %d partial (restore-keys), %d misses; %.0f MB downloaded\n",
		rate, cl.Restores, cl.Hits, cl.PartialHits, cl.Misses, cl.RestoredMB)
	if len(cl.Groups) > 0 {
		fmt.Fprintf(w, "  %-44s %8s %6s %8s %6s %8s\n", "key pattern", "restores", "hit%", "partial", "miss", "avg size")
		for i, g := range cl.Groups {
			if i >= 8 {
				fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("… and %d more key patterns", len(cl.Groups)-i)))
				break
			}
			size := "—"
			if g.AvgMB > 0 {
				size = fmt.Sprintf("%.0f MB", g.AvgMB)
			}
			fmt.Fprintf(w, "  %-44s %8d %5.0f%% %8d %6d %8s\n",
				trunc(g.Pattern, 44), g.Restores, g.HitPct, g.Partial, g.Misses, size)
		}
	}
	if cl.SaveConflicts > 0 {
		fmt.Fprintf(w, "  %s\n", s.yellow(fmt.Sprintf("%d cache %s lost a reservation race", cl.SaveConflicts, plural(cl.SaveConflicts, "save")))+
			s.dim(" — concurrent jobs building the same key; scope keys per-job or save from one job only"))
	}
	if cl.PartialHits > cl.Hits {
		fmt.Fprintf(w, "  %s\n", s.dim("mostly partial hits: primary keys rarely match — key may include something that changes every run (e.g. github.sha)"))
	}
	if cl.JobsSkipped > 0 {
		fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("%d job logs unavailable (expired or inaccessible)", cl.JobsSkipped)))
	}
}

// Markdown renders the whole report as Markdown (for pasting into issues).
func Markdown(w io.Writer, findings []lint.Finding, filesScanned int, a *api.Analysis) {
	fmt.Fprintf(w, "## gha-doctor report\n\n")
	fmt.Fprintf(w, "### Workflow checkup (%d %s)\n\n", filesScanned, plural(filesScanned, "file"))
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
	if a.Cost.BillableMinutes > 0 {
		fmt.Fprintf(w, "\n**Estimated cost** (GitHub-hosted rates; free for public repos on standard runners): $%.2f for the sample (%.0f billable min), of which $%.2f went to failures/retries and $%.2f to per-job minute round-up.\n",
			a.Cost.EstimatedUSD, a.Cost.BillableMinutes, a.Cost.WastedUSD, a.Cost.RoundingUSD)
	}
	if a.Cache.Available {
		fmt.Fprintf(w, "\n**Cache:** %d caches, %.0f MB (%.0f%% of the 10 GB limit); %.0f MB stale (unused 7+ days), %.0f MB pinned to PR refs.\n",
			a.Cache.Count, a.Cache.TotalMB, a.Cache.LimitPct, a.Cache.StaleMB, a.Cache.PRRefMB)
	}
	if cl := a.CacheLogs; cl != nil && cl.Available {
		fmt.Fprintf(w, "\n**Cache hit rate** (%d sampled job logs): %.0f%% — %d restores: %d hits, %d partial, %d misses",
			cl.JobsSampled, cl.HitRate, cl.Restores, cl.Hits, cl.PartialHits, cl.Misses)
		if cl.SaveConflicts > 0 {
			fmt.Fprintf(w, "; %d saves lost a reservation race", cl.SaveConflicts)
		}
		fmt.Fprintf(w, ".\n")
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// pad compensates column width lost to invisible ANSI codes — a no-op hack
// placeholder for future alignment logic.

// plural returns the singular noun or its "s" plural based on n.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
