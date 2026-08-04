package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

// Org renders the fleet-level scan for the terminal.
func Org(w io.Writer, s Style, oa *api.OrgAnalysis) {
	fmt.Fprintf(w, "%s\n", s.bold(fmt.Sprintf("── Org checkup: %s (%d of %d repos scanned) ──",
		oa.Org, oa.ReposScanned, oa.ReposListed)))
	skips := []string{}
	if oa.SkippedForks > 0 {
		skips = append(skips, fmt.Sprintf("%d forks", oa.SkippedForks))
	}
	if oa.SkippedArch > 0 {
		skips = append(skips, fmt.Sprintf("%d archived", oa.SkippedArch))
	}
	if len(skips) > 0 {
		fmt.Fprintf(w, "%s\n", s.dim("   skipped: "+strings.Join(skips, ", ")))
	}

	if len(oa.Repos) == 0 {
		fmt.Fprintf(w, "\n  %s\n", s.dim("no completed workflow runs found in the scanned repos"))
	} else {
		fmt.Fprintf(w, "\n  %-32s %5s %6s %8s %8s %10s  %s\n",
			"repo", "runs", "fail", "p50", "p95", "~min/30d", "last run")
		for _, r := range oa.Repos {
			plain := fmt.Sprintf("%.0f%%", r.FailRate*100)
			fail := plain
			switch {
			case r.FailRate >= 0.30:
				fail = s.red(fail)
			case r.FailRate >= 0.10:
				fail = s.yellow(fail)
			default:
				fail = s.green(fail)
			}
			if n := 6 - len(plain); n > 0 {
				fail = strings.Repeat(" ", n) + fail
			}
			est := fmt.Sprintf("%.0f", r.Est30dMinutes)
			switch {
			case r.Extrapolated:
				est += "*"
			case r.Truncated:
				est += "+"
			}
			fmt.Fprintf(w, "  %-32s %5d %s %7.1fm %7.1fm %10s  %s\n",
				trunc(r.Repo, 32), r.RunsSampled, fail, r.P50Minutes, r.P95Minutes,
				est, s.dim(humanAge(r.LastRun)))
		}
		fmt.Fprintf(w, "\n  total: %s wall-clock run minutes / 30 days across %d active %s; run-weighted fail rate %.0f%%\n",
			s.bold(fmt.Sprintf("~%.0f", oa.TotalEst30d)), len(oa.Repos), plural(len(oa.Repos), "repo"), oa.TotalFailRate*100)
		if hasExtrapolated(oa) {
			fmt.Fprintf(w, "  %s\n", s.dim("* sample truncated inside 30 days; rate extrapolated — raise --runs for precision"))
		}
		if hasTruncated(oa) {
			fmt.Fprintf(w, "  %s\n", s.dim("+ sample exhausted within days (bursty repo); true figure is higher — raise --runs"))
		}
	}
	if oa.QuietRepos > 0 {
		fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("(%d scanned %s had no completed runs)", oa.QuietRepos, plural(oa.QuietRepos, "repo"))))
	}
	if len(oa.ZombieCrons) > 0 {
		fmt.Fprintf(w, "\n  %s\n", s.bold("Failing scheduled workflows")+s.dim("  (crons failing on repeat — nobody is watching)"))
		for _, z := range oa.ZombieCrons {
			atLeast := ""
			if z.StreakOpen {
				atLeast = "≥ " // streak reaches the sample edge; may be longer
			}
			fmt.Fprintf(w, "%s\n", s.red(fmt.Sprintf("  ✗ %s: %s — %s%d consecutive scheduled failures over %.0f days",
				z.Repo, trunc(z.Workflow, 32), atLeast, z.Fails, z.SpanDays)))
			fmt.Fprintf(w, "    %s\n", s.dim(fmt.Sprintf("last failed %s — %s", z.LastFailedAt.Format("2006-01-02"), z.URL)))
		}
		if oa.ZombieCronsMore > 0 {
			fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("…and %d more (full list in --json)", oa.ZombieCronsMore)))
		}
		fmt.Fprintf(w, "  %s\n", s.dim("these streaks inflate the fail column above — drill in with --repo for the billable burn"))
	}
	for _, e := range oa.Errors {
		fmt.Fprintf(w, "  %s\n", s.yellow("! "+e))
	}
	fmt.Fprintf(w, "\n  %s\n", s.dim("wall-clock minutes ≠ billable job minutes (parallel jobs bill in full) —"))
	fmt.Fprintf(w, "  %s\n", s.dim("drill into a repo with `gha-doctor --repo "+oa.Org+"/<name>` for per-job billing, flaky jobs, caches"))
}

// OrgJSON writes the org scan as JSON.
func OrgJSON(w io.Writer, oa *api.OrgAnalysis) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(oa)
}

// OrgMarkdown renders the org scan as a Markdown table.
func OrgMarkdown(w io.Writer, oa *api.OrgAnalysis) {
	fmt.Fprintf(w, "## Org checkup: %s\n\n", oa.Org)
	fmt.Fprintf(w, "%d of %d repos scanned (skipped %d forks, %d archived; %d had no completed runs).\n\n",
		oa.ReposScanned, oa.ReposListed, oa.SkippedForks, oa.SkippedArch, oa.QuietRepos)
	if len(oa.Repos) == 0 {
		fmt.Fprintln(w, "_No completed workflow runs found._")
		return
	}
	fmt.Fprintln(w, "| repo | runs | fail | p50 | p95 | ~min/30d | last run |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---|")
	for _, r := range oa.Repos {
		est := fmt.Sprintf("%.0f", r.Est30dMinutes)
		switch {
		case r.Extrapolated:
			est += "\\*"
		case r.Truncated:
			est += "+"
		}
		fmt.Fprintf(w, "| %s | %d | %.0f%% | %.1fm | %.1fm | %s | %s |\n",
			r.Repo, r.RunsSampled, r.FailRate*100, r.P50Minutes, r.P95Minutes, est, humanAge(r.LastRun))
	}
	fmt.Fprintf(w, "\n**Total: ~%.0f wall-clock run minutes / 30 days** across %d active %s; run-weighted fail rate %.0f%%.\n",
		oa.TotalEst30d, len(oa.Repos), plural(len(oa.Repos), "repo"), oa.TotalFailRate*100)
	if hasExtrapolated(oa) {
		fmt.Fprintln(w, "\n\\* sample truncated inside 30 days; rate extrapolated — raise `--runs` for precision.")
	}
	if hasTruncated(oa) {
		fmt.Fprintln(w, "\n\\+ sample exhausted within days (bursty repo); true figure is higher — raise `--runs`.")
	}
	if len(oa.ZombieCrons) > 0 {
		fmt.Fprintf(w, "\n**Failing scheduled workflows** (crons failing on repeat — nobody is watching):\n\n")
		for _, z := range oa.ZombieCrons {
			atLeast := ""
			if z.StreakOpen {
				atLeast = "≥ "
			}
			fmt.Fprintf(w, "- %s: [%s](%s) — %s%d consecutive scheduled failures over %.0f days, last %s\n",
				z.Repo, z.Workflow, z.URL, atLeast, z.Fails, z.SpanDays, z.LastFailedAt.Format("2006-01-02"))
		}
		if oa.ZombieCronsMore > 0 {
			fmt.Fprintf(w, "- …and %d more (full list in `--json`)\n", oa.ZombieCronsMore)
		}
		fmt.Fprintln(w, "\n_These streaks inflate the fail column above. Drill in with `gha-doctor --repo` for the billable burn._")
	}
	fmt.Fprintln(w, "\n_Wall-clock minutes ≠ billable job minutes (parallel jobs bill in full). Drill into a repo with `gha-doctor --repo` for per-job billing._")
}

func hasExtrapolated(oa *api.OrgAnalysis) bool {
	for _, r := range oa.Repos {
		if r.Extrapolated {
			return true
		}
	}
	return false
}

func hasTruncated(oa *api.OrgAnalysis) bool {
	for _, r := range oa.Repos {
		if r.Truncated {
			return true
		}
	}
	return false
}

// humanAge renders a timestamp as a rough "3d ago" age.
func humanAge(t time.Time) string { return humanAgeAt(t, time.Now()) }

// humanAgeAt is humanAge with an injectable clock (the SVG card is
// snapshot-tested, so its output must not depend on wall time).
func humanAgeAt(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
