// Package report renders lint findings and run analysis for terminals,
// JSON, and Markdown.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/config"
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
	FilesScanned int            `json:"files_scanned"`
	Findings     []lint.Finding `json:"findings"`
	Config       *config.Config `json:"config,omitempty"` // repo config that was applied, if any
	Baseline     *lint.Baseline `json:"baseline,omitempty"`
	Analysis     *api.Analysis  `json:"analysis,omitempty"`
	Score        *Score         `json:"score,omitempty"`
	TopWins      *Wins          `json:"top_wins,omitempty"`
}

// JSON writes the combined report as JSON.
func JSON(w io.Writer, findings []lint.Finding, filesScanned int, cfg *config.Config, b *lint.Baseline, a *api.Analysis, sc *Score, ws *Wins) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if findings == nil {
		findings = []lint.Finding{}
	}
	return enc.Encode(Combined{FilesScanned: filesScanned, Findings: findings, Config: cfg, Baseline: b, Analysis: a, Score: sc, TopWins: ws})
}

func baselineNote(b *lint.Baseline) string {
	note := fmt.Sprintf("vs %s: %d pre-existing hidden", b.Ref, b.Hidden)
	if b.Fixed > 0 {
		note += fmt.Sprintf(", %d fixed", b.Fixed)
	}
	return note
}

// Findings renders lint findings for the terminal. When b is non-nil the
// findings are only those introduced since the baseline ref.
func Findings(w io.Writer, s Style, findings []lint.Finding, filesScanned int, b *lint.Baseline) {
	if filesScanned == 0 && len(findings) == 0 && b == nil {
		return
	}
	fmt.Fprintf(w, "%s\n", s.bold(fmt.Sprintf("── Workflow checkup (%d %s) ──", filesScanned, plural(filesScanned, "file"))))
	if len(findings) == 0 {
		if b != nil {
			fmt.Fprintf(w, "%s %s\n", s.green(fmt.Sprintf("✓ no new issues since %s", b.Ref)), s.dim("("+baselineNote(b)+")"))
		} else {
			fmt.Fprintf(w, "%s\n", s.green("✓ no issues found"))
		}
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
	suffix := ""
	if b != nil {
		suffix = " new since " + b.Ref + " (" + baselineNote(b) + ")"
	}
	fmt.Fprintf(w, "%s\n", s.dim(fmt.Sprintf("%d %s, %d %s%s — gha-doctor --explain <rule> for details",
		warns, plural(warns, "warning"), infos, plural(infos, "suggestion"), suffix)))
}

// Analysis renders run-history stats for the terminal.
func Analysis(w io.Writer, s Style, a *api.Analysis) {
	fmt.Fprintf(w, "\n%s\n", s.bold(fmt.Sprintf("── Run history: %s (last %d runs, since %s) ──",
		a.Repo, a.RunsSampled, a.Since.Format("2006-01-02"))))

	// Workflows table
	fmt.Fprintf(w, "\n%s\n", s.bold("Workflows"))
	fmt.Fprintf(w, "  %-38s %5s %9s %8s %8s %7s %8s\n", "name", "runs", "success", "p50", "p95", "queue", "est$")
	shown, rest := splitWorkflowTail(a.Workflows)
	for _, wf := range shown {
		plain := fmt.Sprintf("%.0f%%", wf.SuccessRate*100)
		rate := plain
		switch {
		case wf.Decisive == 0:
			// Every sampled run was skipped/cancelled: no verdicts to rate.
			plain = "n/a"
			rate = plain
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
	if rest.Count > 0 {
		fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("… %d more %s (%d %s, $%.2f) — full list in --json",
			rest.Count, plural(rest.Count, "workflow"), rest.Runs, plural(rest.Runs, "run"), rest.EstUSD)))
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
		if a.FlakyTests == nil {
			fmt.Fprintf(w, "  %s\n", s.dim("(add --flaky-logs 20 to name the flaky tests from these jobs' logs; needs auth)"))
		}
	}
	FlakyTestNames(w, s, a.FlakyTests)

	// Slowest steps
	fmt.Fprintf(w, "\n%s\n", s.bold("Slowest steps")+s.dim("  (by total time across sample)"))
	fmt.Fprintf(w, "  %-30s %-30s %5s %8s %8s\n", "step", "job", "n", "p50", "total")
	for _, st := range a.SlowSteps {
		fmt.Fprintf(w, "  %-30s %-30s %5d %7.1fm %7.0fm\n",
			trunc(st.Step, 30), trunc(st.Job, 30), st.Count, st.P50Minutes, st.TotalMin)
	}

	// Matrix balance (only when at least one group could be measured)
	if m := a.Matrix; m != nil {
		fmt.Fprintf(w, "\n%s\n", s.bold("Matrix balance")+s.dim("  (a matrix finishes when its slowest shard does)"))
		if len(m.Imbalanced) == 0 {
			fmt.Fprintf(w, "  %s\n", s.green(fmt.Sprintf("✓ shards look balanced across %d measured %s", m.GroupsMeasured, plural(m.GroupsMeasured, "group"))))
		} else {
			fmt.Fprintf(w, "  %-30s %-20s %6s %7s %7s %8s\n", "job", "workflow", "shards", "wall", "ideal", "waiting")
			for _, g := range m.Imbalanced {
				fmt.Fprintf(w, "  %-30s %-20s %6d %6.1fm %6.1fm %7.1fm\n",
					trunc(g.Job, 30), trunc(g.Workflow, 20), g.Shards, g.P50WallMin, g.P50IdealMin, g.P50SavingMin)
				fmt.Fprintf(w, "    %s\n", s.dim(fmt.Sprintf("slowest %s %.1fm vs fastest %s %.1fm — rebalancing could cut ~%.0f%% of the wait (median of %d runs)",
					trunc(g.SlowestShard, 34), g.SlowestP50, trunc(g.FastestShard, 34), g.FastestP50, (1-1/g.Ratio)*100, g.RunsMeasured)))
			}
			fmt.Fprintf(w, "  %s\n", s.dim("wall = slowest shard, ideal = even split of the same work; billable minutes are unchanged — this is PR feedback latency"))
		}
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

	// Zombie crons: scheduled workflows failing on repeat
	if len(a.ZombieCrons) > 0 {
		fmt.Fprintf(w, "\n%s\n", s.bold("Failing scheduled workflows")+s.dim("  (crons failing on repeat — nobody is watching)"))
		for _, z := range a.ZombieCrons {
			atLeast := ""
			if z.StreakOpen {
				atLeast = "≥ " // streak reaches the sample edge; may be longer
			}
			line := fmt.Sprintf("  ✗ %s — %s%d consecutive scheduled failures over %.0f days", trunc(z.Workflow, 32), atLeast, z.Fails, z.SpanDays)
			if z.EstUSDPerMo >= 0.01 {
				line += fmt.Sprintf(" (~%.0f min/mo, $%.2f/mo while it keeps failing)", z.EstMinPerMo, z.EstUSDPerMo)
			}
			fmt.Fprintf(w, "%s\n", s.red(line))
			fmt.Fprintf(w, "    %s\n", s.dim(fmt.Sprintf("last failed %s — %s", z.LastFailedAt.Format("2006-01-02"), z.URL)))
		}
		fmt.Fprintf(w, "  %s\n", s.dim("these minutes are inside the waste bucket above — fix the job or disable the schedule"))
	}

	// Superseded PR runs (only when the sample had something to say)
	if sup := a.Superseded; sup != nil && (sup.Completed > 0 || sup.Cancelled > 0) {
		fmt.Fprintf(w, "\n%s\n", s.bold("Superseded PR runs")+s.dim("  (a newer push replaced them while they were still running)"))
		if sup.Completed == 0 {
			fmt.Fprintf(w, "  %s\n", s.green(fmt.Sprintf("✓ all %d superseded %s cancelled in time — concurrency is doing its job",
				sup.Cancelled, pluralVerb(sup.Cancelled, "run was", "runs were"))))
		} else {
			line := fmt.Sprintf("  %d of %d PR %s to completion anyway", sup.Completed, sup.PRRuns, pluralVerb(sup.Completed, "run ran", "runs ran"))
			if sup.WastedMinutes >= 1 {
				line += fmt.Sprintf(" — %.0f billable min after the replacing push ($%.2f)", sup.WastedMinutes, sup.WastedUSD)
			}
			fmt.Fprintf(w, "%s\n", s.yellow(line))
			if sup.Cancelled > 0 {
				fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("(%d %s cancelled in time)", sup.Cancelled, pluralVerb(sup.Cancelled, "was", "were"))))
			}
			for _, ex := range sup.Examples {
				fmt.Fprintf(w, "    %s\n", s.dim(fmt.Sprintf("%s on %s — %.0f min past supersession", trunc(ex.Workflow, 30), trunc(ex.Branch, 30), ex.WastedMinutes)))
			}
			fmt.Fprintf(w, "  %s\n", s.dim("concurrency + cancel-in-progress stops this (D001, --fix); failed/retried superseded runs are counted in the waste bucket above, not here"))
		}
	}

	// PR feedback time
	if fb := a.Feedback; fb != nil {
		fmt.Fprintf(w, "\n%s\n", s.bold("PR feedback time")+s.dim(fmt.Sprintf("  (push → last check finishes; %d pushes with a full verdict)", fb.Pushes)))
		line := fmt.Sprintf("  median %.1fm, p95 %.1fm", fb.P50Minutes, fb.P95Minutes)
		switch {
		case fb.P50Minutes >= 30:
			fmt.Fprintf(w, "%s\n", s.red(line))
		case fb.P50Minutes >= 15:
			fmt.Fprintf(w, "%s\n", s.yellow(line))
		default:
			fmt.Fprintf(w, "%s\n", s.green(line))
		}
		for _, g := range fb.Gaters {
			if g.Share < 0.15 {
				continue // finishing last once or twice is noise, not a critical path
			}
			fmt.Fprintf(w, "    %s\n", s.dim(fmt.Sprintf("critical path: %s — last to finish on %.0f%% of pushes (median %.1fm after the next-latest check)",
				trunc(g.Workflow, 32), g.Share*100, g.SlackP50Minutes)))
		}
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
		case a.Cache.LimitPct >= overLimitPct:
			usage = fmt.Sprintf("  %d caches, %s — %s over the 10 GB limit", a.Cache.Count, sizeStr(a.Cache.TotalMB), sizeStr(a.Cache.TotalMB-defaultCacheLimitMB))
			fmt.Fprintf(w, "%s %s\n", s.red(usage), s.dim("— eviction churn: GitHub deletes oldest continuously, so restores go cold"))
		case a.Cache.LimitPct >= 90:
			fmt.Fprintf(w, "%s %s\n", s.red(usage), s.dim("— evictions imminent; expect cold builds"))
		case a.Cache.LimitPct >= 70:
			fmt.Fprintf(w, "%s\n", s.yellow(usage))
		default:
			fmt.Fprintf(w, "%s\n", s.green(usage))
		}
		if a.Cache.Sampled {
			fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("breakdown below covers the %d largest entries (full walk would cost %d+ API calls)",
				a.Cache.SampleCount, a.Cache.Count/100)))
		}
		if a.Cache.StaleCount > 0 {
			fmt.Fprintf(w, "  stale (unused 7+ days): %d %s, %.0f MB %s\n",
				a.Cache.StaleCount, plural(a.Cache.StaleCount, "cache"), a.Cache.StaleMB, s.dim("— gh cache delete, or let GitHub evict them"))
		}
		if a.Cache.PRRefCount > 0 {
			fmt.Fprintf(w, "  on PR refs: %d %s, %.0f MB %s\n",
				a.Cache.PRRefCount, plural(a.Cache.PRRefCount, "cache"), a.Cache.PRRefMB, s.dim("— unreachable from other branches; dead weight after merge"))
		}
		for i, e := range a.Cache.Largest {
			if i >= 3 {
				break
			}
			fmt.Fprintf(w, "  %s %-46s %7.0f MB %s\n", s.dim("·"), trunc(e.Key, 46), e.SizeMB, s.dim(trunc(e.Ref, 24)))
		}
	}
	// Artifact checkup
	fmt.Fprintf(w, "\n%s\n", s.bold("Artifacts")+s.dim("  ($0.008/GB-day on private repos; free on public)"))
	ar := a.Artifacts
	if !ar.Available {
		fmt.Fprintf(w, "  %s\n", s.dim(ar.Note))
	} else if ar.Count == 0 {
		fmt.Fprintf(w, "  %s\n", s.dim("no artifacts uploaded"))
	} else {
		scope := ""
		if ar.Sampled {
			scope = fmt.Sprintf("; breakdown from the %d most recent", ar.SampleCount)
		}
		fmt.Fprintf(w, "  %d artifacts%s: %s not yet expired in sample\n", ar.Count, scope, sizeStr(ar.ActiveMB))
		if ar.EstStorageGB >= 0.1 {
			est := fmt.Sprintf("  steady state at this upload rate: ~%.1f GB → ~$%.2f/mo on a private repo", ar.EstStorageGB, ar.EstUSDPerMo)
			switch {
			case ar.EstUSDPerMo >= 20:
				fmt.Fprintf(w, "%s\n", s.red(est))
			case ar.EstUSDPerMo >= 5:
				fmt.Fprintf(w, "%s\n", s.yellow(est))
			default:
				fmt.Fprintf(w, "%s\n", est)
			}
			fmt.Fprintf(w, "  %s\n", s.dim("("+ar.EstimateBasis+")"))
		} else if ar.EstimateBasis != "" && ar.EstStorageGB == 0 && ar.WindowDays < 3 {
			fmt.Fprintf(w, "  %s\n", s.dim(ar.EstimateBasis))
		}
		if len(ar.Producers) > 0 {
			fmt.Fprintf(w, "  %-36s %6s %9s %8s %6s\n", "top producers", "count", "total", "avg", "keeps")
			for i, p := range ar.Producers {
				if i >= 5 {
					break
				}
				hint := ""
				if p.RetentionDays >= 89 && p.TotalMB >= 50 {
					hint = " " + s.yellow("← default 90d retention; set retention-days (D010)")
				}
				fmt.Fprintf(w, "  %-36s %6d %9s %8s %5.0fd%s\n",
					trunc(p.Name, 36), p.Count, mbStr(p.TotalMB), mbStr(p.AvgMB), p.RetentionDays, hint)
			}
		}
	}
	CacheHitRate(w, s, a.CacheLogs)
}

// CacheHitRate renders the sampled-log cache hit/miss section.
// FlakyTestNames renders the flaky tests named from failed-job logs
// (--flaky-logs). Nil means the sampling was not requested; the flaky-jobs
// section prints the hint in that case.
func FlakyTestNames(w io.Writer, s Style, ft *api.FlakyTestStats) {
	if ft == nil {
		return
	}
	sub := fmt.Sprintf("  (tests seen failing in %d of %d flaky-failure logs)", ft.LogsSampled, ft.LogsTotal)
	fmt.Fprintf(w, "\n%s\n", s.bold("Flaky tests")+s.dim(sub))
	if !ft.Available || len(ft.Tests) == 0 {
		fmt.Fprintf(w, "  %s\n", s.dim(ft.Note))
		return
	}
	fmt.Fprintf(w, "  %-52s %-10s %5s %7s  %s\n", "test", "fw", "fails", "commits", "job")
	for _, t := range ft.Tests {
		fmt.Fprintf(w, "  %s %s %5d %7d  %s\n",
			padRight(trunc(t.Name, 52), 52), padRight(t.Framework, 10),
			t.Failures, t.Commits, trunc(strings.Join(t.Jobs, ", "), 34))
	}
	if ft.JobsSkipped > 0 {
		fmt.Fprintf(w, "  %s\n", s.dim(fmt.Sprintf("(%d log %s could not be fetched — old logs expire)", ft.JobsSkipped, plural(ft.JobsSkipped, "download"))))
	}
	fmt.Fprintf(w, "  %s\n", s.dim("a test named here failed in a run whose commit also passed — the failure did not reproduce"))
}

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
	if tr := cl.Trend; tr != nil {
		var verdict string
		switch {
		case tr.DeltaPts >= 5:
			verdict = s.green(fmt.Sprintf("improving (+%.0f pts)", tr.DeltaPts))
		case tr.DeltaPts <= -5:
			verdict = s.red(fmt.Sprintf("degrading (%.0f pts)", tr.DeltaPts))
		default:
			verdict = s.dim(fmt.Sprintf("stable (%+.0f pts)", tr.DeltaPts))
		}
		fmt.Fprintf(w, "  trend: %.0f%% (%s, %d restores) → %.0f%% (%s, %d restores) — %s\n",
			tr.OlderHitRate, dateRange(tr.OlderFrom, tr.OlderTo), tr.OlderRestores,
			tr.NewerHitRate, dateRange(tr.NewerFrom, tr.NewerTo), tr.NewerRestores, verdict)
	}
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
// When b is non-nil the findings are only those introduced since the
// baseline ref, and the checkup section says so.
func Markdown(w io.Writer, findings []lint.Finding, filesScanned int, b *lint.Baseline, a *api.Analysis, sc *Score, ws *Wins) {
	fmt.Fprintf(w, "## gha-doctor report\n\n")
	fmt.Fprintf(w, "### Workflow checkup (%d %s)\n\n", filesScanned, plural(filesScanned, "file"))
	if len(findings) == 0 {
		if b != nil {
			fmt.Fprintf(w, "No new issues since `%s`.\n\n", b.Ref)
		} else {
			fmt.Fprintf(w, "No issues found.\n\n")
		}
	} else {
		fmt.Fprintf(w, "| rule | severity | location | message |\n|---|---|---|---|\n")
		for _, f := range findings {
			fmt.Fprintf(w, "| %s | %s | %s:%d | %s |\n", f.Rule, f.Severity, f.File, f.Line, strings.ReplaceAll(f.Message, "|", "\\|"))
		}
		fmt.Fprintln(w)
	}
	if b != nil {
		fmt.Fprintf(w, "_Compared with `%s`: %d pre-existing finding(s) hidden, %d fixed._\n\n", b.Ref, b.Hidden, b.Fixed)
	}
	if a == nil {
		if sc != nil {
			ScoreMarkdown(w, *sc)
		}
		return
	}
	fmt.Fprintf(w, "### Run history: %s (last %d runs)\n\n", a.Repo, a.RunsSampled)
	fmt.Fprintf(w, "| workflow | runs | success | p50 | p95 |\n|---|---|---|---|---|\n")
	mdShown, mdRest := splitWorkflowTail(a.Workflows)
	for _, wf := range mdShown {
		rate := fmt.Sprintf("%.0f%%", wf.SuccessRate*100)
		if wf.Decisive == 0 {
			rate = "n/a"
		}
		fmt.Fprintf(w, "| %s | %d | %s | %.1fm | %.1fm |\n", wf.Name, wf.Runs, rate, wf.P50Minutes, wf.P95Minutes)
	}
	if mdRest.Count > 0 {
		fmt.Fprintf(w, "| _… %d more %s_ | %d | | | |\n", mdRest.Count, plural(mdRest.Count, "workflow"), mdRest.Runs)
	}
	if len(a.FlakyJobs) > 0 {
		fmt.Fprintf(w, "\n**Flaky jobs** (failed and passed on the same commit):\n\n")
		fmt.Fprintf(w, "| job | workflow | flaky commits | wasted minutes |\n|---|---|---|---|\n")
		for _, fj := range a.FlakyJobs {
			fmt.Fprintf(w, "| %s | %s | %d | %.0f |\n", fj.Job, fj.Workflow, fj.FlakyCommits, fj.WastedMinutes)
		}
	}
	if ft := a.FlakyTests; ft != nil && ft.Available && len(ft.Tests) > 0 {
		fmt.Fprintf(w, "\n**Flaky tests** (seen failing in %d of %d flaky-failure logs; the same commit also passed):\n\n", ft.LogsSampled, ft.LogsTotal)
		fmt.Fprintf(w, "| test | framework | fails | commits | job |\n|---|---|---|---|---|\n")
		for _, t := range ft.Tests {
			fmt.Fprintf(w, "| `%s` | %s | %d | %d | %s |\n",
				mdEscapePipes(t.Name), t.Framework, t.Failures, t.Commits, mdEscapePipes(strings.Join(t.Jobs, ", ")))
		}
	}
	if m := a.Matrix; m != nil && len(m.Imbalanced) > 0 {
		fmt.Fprintf(w, "\n**Matrix balance** (a matrix finishes when its slowest shard does; billable minutes unchanged — this is PR feedback latency):\n\n")
		fmt.Fprintf(w, "| job | workflow | shards | wall p50 | even-split p50 | waiting on straggler |\n|---|---|---|---|---|---|\n")
		for _, g := range m.Imbalanced {
			fmt.Fprintf(w, "| %s | %s | %d | %.1fm | %.1fm | %.1fm |\n",
				g.Job, g.Workflow, g.Shards, g.P50WallMin, g.P50IdealMin, g.P50SavingMin)
		}
		g := m.Imbalanced[0]
		fmt.Fprintf(w, "\n_Worst: `%s` — slowest shard `%s` %.1fm vs fastest `%s` %.1fm (median of %d runs)._\n",
			g.Job, g.SlowestShard, g.SlowestP50, g.FastestShard, g.FastestP50, g.RunsMeasured)
	}
	fmt.Fprintf(w, "\n**Wasted compute:** %.0f of %.0f minutes (failed runs %.0f + retries %.0f).\n",
		a.Waste.TotalMinutes, a.Waste.ComputeMinutes, a.Waste.FailedRunMinutes, a.Waste.RetryMinutes)
	if len(a.ZombieCrons) > 0 {
		fmt.Fprintf(w, "\n**Failing scheduled workflows** (crons failing on repeat — nobody is watching):\n\n")
		for _, z := range a.ZombieCrons {
			atLeast := ""
			if z.StreakOpen {
				atLeast = "≥ "
			}
			est := ""
			if z.EstUSDPerMo >= 0.01 {
				est = fmt.Sprintf(" (~%.0f min/mo, $%.2f/mo while it keeps failing)", z.EstMinPerMo, z.EstUSDPerMo)
			}
			fmt.Fprintf(w, "- [%s](%s) — %s%d consecutive scheduled failures over %.0f days, last %s%s\n",
				z.Workflow, z.URL, atLeast, z.Fails, z.SpanDays, z.LastFailedAt.Format("2006-01-02"), est)
		}
		fmt.Fprintf(w, "\n_These minutes are inside the waste bucket above — fix the job or disable the schedule._\n")
	}
	if sup := a.Superseded; sup != nil && (sup.Completed > 0 || sup.Cancelled > 0) {
		if sup.Completed == 0 {
			fmt.Fprintf(w, "\n**Superseded PR runs:** all %d superseded %s cancelled in time — concurrency is doing its job.\n",
				sup.Cancelled, pluralVerb(sup.Cancelled, "run was", "runs were"))
		} else {
			fmt.Fprintf(w, "\n**Superseded PR runs:** %d of %d PR %s to completion after a newer push had already replaced %s",
				sup.Completed, sup.PRRuns, pluralVerb(sup.Completed, "run ran", "runs ran"), pluralVerb(sup.Completed, "it", "them"))
			if sup.WastedMinutes >= 1 {
				fmt.Fprintf(w, " — %.0f billable min past the point of supersession ($%.2f)", sup.WastedMinutes, sup.WastedUSD)
			}
			fmt.Fprintf(w, ". `concurrency` + `cancel-in-progress` stops this (D001, `--fix`). Failed/retried superseded runs are counted in the waste bucket, not here.\n")
			if len(sup.Examples) > 0 {
				ex := sup.Examples[0]
				fmt.Fprintf(w, "\n_Worst: [%s on %s](%s) — %.0f min past supersession._\n", ex.Workflow, ex.Branch, ex.URL, ex.WastedMinutes)
			}
		}
	}
	if fb := a.Feedback; fb != nil {
		fmt.Fprintf(w, "\n**PR feedback time** (push → last check finishes; %d pushes with a full verdict): median %.1fm, p95 %.1fm.\n",
			fb.Pushes, fb.P50Minutes, fb.P95Minutes)
		for _, g := range fb.Gaters {
			if g.Share < 0.15 {
				continue
			}
			fmt.Fprintf(w, "_Critical path: `%s` — last to finish on %.0f%% of pushes (median %.1fm after the next-latest check)._\n",
				g.Workflow, g.Share*100, g.SlackP50Minutes)
		}
	}
	if a.Cost.BillableMinutes > 0 {
		fmt.Fprintf(w, "\n**Estimated cost** (GitHub-hosted rates; free for public repos on standard runners): $%.2f for the sample (%.0f billable min), of which $%.2f went to failures/retries and $%.2f to per-job minute round-up.\n",
			a.Cost.EstimatedUSD, a.Cost.BillableMinutes, a.Cost.WastedUSD, a.Cost.RoundingUSD)
	}
	if a.Cache.Available {
		if a.Cache.LimitPct >= overLimitPct {
			fmt.Fprintf(w, "\n**Cache:** %d caches, %s — %s over the 10 GB limit (GitHub evicts oldest continuously; expect cold restores); %s stale (unused 7+ days), %s pinned to PR refs.\n",
				a.Cache.Count, sizeStr(a.Cache.TotalMB), sizeStr(a.Cache.TotalMB-defaultCacheLimitMB), sizeStr(a.Cache.StaleMB), sizeStr(a.Cache.PRRefMB))
		} else {
			fmt.Fprintf(w, "\n**Cache:** %d caches, %.0f MB (%.0f%% of the 10 GB limit); %.0f MB stale (unused 7+ days), %.0f MB pinned to PR refs.\n",
				a.Cache.Count, a.Cache.TotalMB, a.Cache.LimitPct, a.Cache.StaleMB, a.Cache.PRRefMB)
		}
		if a.Cache.Sampled {
			fmt.Fprintf(w, "_Stale/PR-ref figures cover the %d largest entries._\n", a.Cache.SampleCount)
		}
	}
	if ar := a.Artifacts; ar.Available && ar.Count > 0 {
		fmt.Fprintf(w, "\n**Artifacts:** %d total; %s not yet expired in the %d most recent.",
			ar.Count, sizeStr(ar.ActiveMB), ar.SampleCount)
		if ar.EstStorageGB >= 0.1 {
			fmt.Fprintf(w, " Steady state at this upload rate: ~%.1f GB → ~$%.2f/mo on a private repo (%s).",
				ar.EstStorageGB, ar.EstUSDPerMo, ar.EstimateBasis)
		}
		if len(ar.Producers) > 0 && ar.Producers[0].TotalMB >= 1 {
			p := ar.Producers[0]
			fmt.Fprintf(w, " Top producer: `%s` (%d uploads, %.0f MB, kept %.0fd).", p.Name, p.Count, p.TotalMB, p.RetentionDays)
		}
		fmt.Fprintf(w, "\n")
	}
	if cl := a.CacheLogs; cl != nil && cl.Available {
		fmt.Fprintf(w, "\n**Cache hit rate** (%d sampled job logs): %.0f%% — %d restores: %d hits, %d partial, %d misses",
			cl.JobsSampled, cl.HitRate, cl.Restores, cl.Hits, cl.PartialHits, cl.Misses)
		if cl.SaveConflicts > 0 {
			fmt.Fprintf(w, "; %d saves lost a reservation race", cl.SaveConflicts)
		}
		fmt.Fprintf(w, ".\n")
		if tr := cl.Trend; tr != nil {
			fmt.Fprintf(w, "Trend: %.0f%% (%s) → %.0f%% (%s), %+.0f pts.\n",
				tr.OlderHitRate, dateRange(tr.OlderFrom, tr.OlderTo),
				tr.NewerHitRate, dateRange(tr.NewerFrom, tr.NewerTo), tr.DeltaPts)
		}
	}
	WinsMarkdown(w, ws)
	if sc != nil {
		ScoreMarkdown(w, *sc)
	}
}

// defaultCacheLimitMB is GitHub's documented per-repository Actions cache
// limit (10 GB). Busy repos routinely sit far above it because oldest-first
// eviction lags behind write volume, so usage is a soft ceiling, not a cap.
const defaultCacheLimitMB = 10240

// overLimitPct is the point past which "% of the 10 GB limit" stops being
// informative — a "1997% of limit" reading (seen live on vercel/next.js)
// looks like a bug to readers. Past this we switch to absolute terms and
// describe the eviction churn that is actually happening.
const overLimitPct = 120

// cacheUsagePhrase describes cache fill relative to the 10 GB limit,
// switching to absolute terms past overLimitPct (see that constant).
func cacheUsagePhrase(c api.CacheStats) string {
	if c.LimitPct >= overLimitPct {
		return fmt.Sprintf("%s — %s over the 10 GB limit", sizeStr(c.TotalMB), sizeStr(c.TotalMB-defaultCacheLimitMB))
	}
	return fmt.Sprintf("%.0f%% of the 10 GB limit", c.LimitPct)
}

// mbStr renders a size in MB compactly: sub-10-MB values keep one decimal
// (a 0.3 MB artifact should not print as "0M"), larger ones round, and
// GB-scale values switch units.
func mbStr(mb float64) string {
	switch {
	case mb >= 1024:
		return fmt.Sprintf("%.1fG", mb/1024)
	case mb < 10:
		return fmt.Sprintf("%.1fM", mb)
	default:
		return fmt.Sprintf("%.0fM", mb)
	}
}

// sizeStr is mbStr with a space and full unit, for prose lines.
func sizeStr(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	if mb > 0 && mb < 10 {
		return fmt.Sprintf("%.1f MB", mb) // avoid a misleading "0 MB" for sub-MB totals
	}
	return fmt.Sprintf("%.0f MB", mb)
}

// maxWorkflowRows caps the Workflows table in human-readable output.
// Repos that generate workflow names dynamically (e.g. dependency-graph
// updaters that embed a PR number in the name) can have dozens of
// single-run entries; the tail is aggregated into one summary row.
// JSON output always carries the full list.
const maxWorkflowRows = 15

type workflowTail struct {
	Count  int
	Runs   int
	EstUSD float64
}

// splitWorkflowTail returns the workflows to display and an aggregate of
// the rest. The input is already sorted by run count descending, so the
// tail is the low-signal end. A tail of exactly one row is not worth
// hiding — the summary line would take the same space.
func splitWorkflowTail(wfs []api.WorkflowStats) ([]api.WorkflowStats, workflowTail) {
	if len(wfs) <= maxWorkflowRows+1 {
		return wfs, workflowTail{}
	}
	var t workflowTail
	for _, wf := range wfs[maxWorkflowRows:] {
		t.Count++
		t.Runs += wf.Runs
		t.EstUSD += wf.EstUSD
	}
	return wfs[:maxWorkflowRows], t
}

// dateRange formats a compact day range like "Jul 20–25" or, across
// months, "Jul 28 – Aug 2". A single-day range collapses to one date.
func dateRange(from, to time.Time) string {
	f, t := from.Format("Jan 2"), to.Format("Jan 2")
	if f == t {
		return f
	}
	if from.Month() == to.Month() && from.Year() == to.Year() {
		return fmt.Sprintf("%s–%d", f, to.Day())
	}
	return f + " – " + t
}

// trunc shortens s to n visible characters. Rune-aware: test and workflow
// names can contain multibyte characters (playwright separates segments
// with "›") and a byte slice could cut one in half.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// padRight pads s with spaces to n visible characters. fmt's %-Ns pads by
// bytes, which under-pads multibyte names and skews columns.
func padRight(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// plural returns the singular noun or its "s" plural based on n.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// pluralVerb picks between full singular/plural phrases ("run was"/"runs were").
func pluralVerb(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// mdEscapePipes escapes literal pipes so free-text (test names can contain
// anything) cannot break a Markdown table row.
func mdEscapePipes(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
