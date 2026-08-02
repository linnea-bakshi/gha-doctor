package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Win is one concrete, ranked improvement the repo could make.
type Win struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Rule   string `json:"rule,omitempty"` // Dxxx behind the win, if any
	// USDPerMo is the estimated monthly saving on a private repo
	// (0 = real but not dollar-quantifiable from the sample).
	USDPerMo float64 `json:"est_usd_per_month,omitempty"`
	Fixable  bool    `json:"fixable,omitempty"` // `gha-doctor --fix` handles it
}

// Wins is the ranked action list derived from run history + lint findings.
type Wins struct {
	// Basis says how dollar figures were derived (projection window, or
	// why they are sample totals only).
	Basis string `json:"basis"`
	// Projected is true when USDPerMo values are 30-day projections from
	// a >=3-day sample window (the same honesty gate --org uses).
	Projected bool  `json:"projected"`
	Items     []Win `json:"items"`
}

// TotalUSDPerMo sums the quantified wins.
func (ws *Wins) TotalUSDPerMo() float64 {
	var t float64
	for _, w := range ws.Items {
		t += w.USDPerMo
	}
	return t
}

// winsMax caps the rendered list: five actions is a to-do list, ten is noise.
const winsMax = 5

// minWinUSD keeps pocket-change "wins" out of the ranked list.
const minWinUSD = 0.25

// matrixWinMinSavingMin is the median per-run straggler wait (minutes) a
// matrix group must cost before rebalancing earns a to-do slot.
const matrixWinMinSavingMin = 2.0

// PR-feedback win gates: the wait must be long enough to hurt (median >=
// feedbackWinMinP50 min), one workflow must own the critical path (share >=
// feedbackWinMinShare — otherwise there is no single thing to speed up), and
// speeding it up must actually move the wait (median slack >=
// feedbackWinMinSlackMin min past the next-latest check).
const (
	feedbackWinMinP50      = 15.0
	feedbackWinMinShare    = 0.5
	feedbackWinMinSlackMin = 2.0
)

// slowdownWinMinPct is the p50 slowdown (percent, newer vs older half of
// the sample) a workflow must show before "investigate the slowdown" earns
// a to-do slot. Deliberately above the report threshold (durTrendMinPct):
// the report can note a drift the to-do list shouldn't shout about.
const slowdownWinMinPct = 30.0

// ComputeWins turns the analysis + findings into a ranked action list.
// Dollar wins are projected to 30 days when the run sample spans >=3 days
// (below that a bursty afternoon would extrapolate into fiction — same
// gate the --org estimates use); otherwise they rank by sample totals and
// the basis says so. Returns nil when there is no analysis to mine.
func ComputeWins(findings []lint.Finding, a *api.Analysis, now time.Time) *Wins {
	if a == nil {
		return nil
	}
	windowDays := now.Sub(a.Since).Hours() / 24
	if windowDays < 0 {
		windowDays = 0
	}
	projected := windowDays >= 3
	factor := 1.0
	ws := &Wins{Projected: projected}
	if projected {
		factor = 30 / windowDays
		ws.Basis = fmt.Sprintf("dollar wins projected to 30 days from the %.1f-day run sample; private-repo rates, public repos run free", windowDays)
	} else {
		ws.Basis = fmt.Sprintf("run sample spans only %.1f days — too short to project monthly; dollar wins are sample totals", windowDays)
	}

	var quant, rest []Win

	// 1. Failures + retries: minutes that bought nothing. The minute floor
	// guards against sub-minute failures rendering "0 min bought nothing".
	if a.Cost.WastedUSD*factor >= minWinUSD && a.Waste.TotalMinutes >= 1 {
		detail := fmt.Sprintf("%.0f failed-run min + %.0f retried-attempt min bought nothing ($%.2f of the sampled spend)",
			a.Waste.FailedRunMinutes, a.Waste.RetryMinutes, a.Cost.WastedUSD)
		if len(a.FlakyJobs) > 0 {
			fj := a.FlakyJobs[0]
			detail += fmt.Sprintf("; worst flake: %q in %q", fj.Job, fj.Workflow)
		}
		if ft := a.FlakyTests; ft != nil && ft.Available && len(ft.Tests) > 0 {
			t := ft.Tests[0]
			detail += fmt.Sprintf("; most-seen flaky test: %q (%s, failed in %d sampled %s)",
				t.Name, t.Framework, t.Failures, plural(t.Failures, "log"))
		}
		quant = append(quant, Win{
			Title:    "Cut failures and retries",
			Detail:   detail,
			USDPerMo: round2(a.Cost.WastedUSD * factor),
		})
	}

	// 2. Per-job minute round-up: only worth surfacing when it is a
	// meaningful share of spend — every repo has *some* rounding.
	if a.Cost.RoundingUSD*factor >= minWinUSD && a.Cost.EstimatedUSD > 0 &&
		a.Cost.RoundingUSD/a.Cost.EstimatedUSD >= 0.15 {
		quant = append(quant, Win{
			Title: "Consolidate tiny jobs",
			Detail: fmt.Sprintf("per-job round-up billed %.0f min the jobs never used (%.0f%% of spend) — merge short jobs or shrink the matrix",
				a.Cost.RoundingMinutes, a.Cost.RoundingUSD/a.Cost.EstimatedUSD*100),
			USDPerMo: round2(a.Cost.RoundingUSD * factor),
		})
	}

	// 3. Artifact retention: steady-state saving from capping retention at
	// 30 days. Only computable once the artifact estimate itself passed
	// its own >=3-day gate (EstStorageGB > 0). Already a monthly figure —
	// no run-window projection involved.
	if a.Artifacts.Available && a.Artifacts.EstStorageGB > 0 && a.Artifacts.WindowDays >= 3 {
		var savedGB float64
		var names []string
		for _, p := range a.Artifacts.Producers {
			if p.RetentionDays > 30 && p.TotalMB > 0 {
				ratePerDayGB := p.TotalMB / 1024 / a.Artifacts.WindowDays
				savedGB += ratePerDayGB * (p.RetentionDays - 30)
				if len(names) < 2 {
					names = append(names, p.Name)
				}
			}
		}
		if usd := savedGB * 0.008 * 30; usd >= minWinUSD {
			quant = append(quant, Win{
				Title: "Cap artifact retention at 30 days",
				Detail: fmt.Sprintf("~%.1f GB of steady-state storage is uploads kept past 30d (e.g. %s) — set retention-days",
					savedGB, strings.Join(names, ", ")),
				Rule:     "D010",
				USDPerMo: round2(usd),
			})
		}
	}

	// Unquantified wins, in fixed priority order.
	byRule := map[string]int{}
	for _, f := range findings {
		byRule[f.Rule]++
	}
	// Zombie crons lead the unquantified list: a workflow that has been
	// failing for days with nobody noticing is the most actionable finding
	// a report can name. No USDPerMo — its minutes are already inside the
	// failures win above, and double-counting would inflate the total.
	if len(a.ZombieCrons) > 0 {
		z := a.ZombieCrons[0]
		detail := fmt.Sprintf("`%s` has failed its last %d scheduled runs over %.0f days", z.Workflow, z.Fails, z.SpanDays)
		if z.EstUSDPerMo >= 0.01 {
			detail += fmt.Sprintf(" (~$%.2f/mo while it keeps failing — already inside the failure waste)", z.EstUSDPerMo)
		}
		if n := len(a.ZombieCrons); n > 1 {
			detail += fmt.Sprintf("; %d more failing %s below", n-1, plural(n-1, "cron"))
		}
		detail += " — fix the job or disable the schedule"
		rest = append(rest, Win{Title: "Revive or retire the dead cron", Detail: detail})
	}
	// A workflow that got sharply slower inside the sampled window is an
	// investigation, not a dollar figure: the extra minutes are already in
	// the cost totals, and the cause (new step, bigger matrix, cold cache)
	// is for a human to find. Higher bar than the report section
	// (slowdownWinMinPct vs 20%) so the to-do list only carries clear cases.
	if dt := a.DurationTrends; dt != nil {
		for _, t := range dt.Significant {
			if t.ChangePct >= slowdownWinMinPct {
				rest = append(rest, Win{
					Title: "Investigate the CI slowdown",
					Detail: fmt.Sprintf("`%s` p50 went %.1fm → %.1fm (%+.0f%%) between the older and newer half of the sample — a new step, bigger matrix or cold cache usually explains it",
						t.Workflow, t.OlderP50, t.NewerP50, t.ChangePct),
				})
				break // one slot: the worst case is already first
			}
		}
	}
	if n := byRule["D013"]; n > 0 {
		rest = append(rest, Win{
			Title:  "Stop double-running PR pushes",
			Detail: nounVerb(n, "workflow", "triggers", "trigger") + " on both unscoped push and pull_request — every PR commit runs twice; scope push to your default branch",
			Rule:   "D013",
		})
	}
	// Matrix imbalance is a latency win, not a dollar win: the group ends
	// when its slowest shard does, so rebalancing changes nothing on the
	// bill but everything about how long PRs wait. Only worth a to-do
	// slot when the straggler costs >= matrixWinMinSavingMin per run.
	if m := a.Matrix; m != nil && len(m.Imbalanced) > 0 && m.Imbalanced[0].P50SavingMin >= matrixWinMinSavingMin {
		g := m.Imbalanced[0]
		rest = append(rest, Win{
			Title: "Rebalance matrix shards",
			Detail: fmt.Sprintf("`%s` waits ~%.0fm per run on its slowest shard %s (%.1fm vs %.1fm fastest) — pure PR-feedback latency, every run pays it",
				g.Job, g.P50SavingMin, g.SlowestShard, g.SlowestP50, g.FastestP50),
		})
	}
	// PR feedback: like matrix imbalance, a latency win, not a dollar win —
	// contributors wait for the slowest check on every push.
	if fb := a.Feedback; fb != nil && fb.P50Minutes >= feedbackWinMinP50 && len(fb.Gaters) > 0 {
		if g := fb.Gaters[0]; g.Share >= feedbackWinMinShare && g.SlackP50Minutes >= feedbackWinMinSlackMin {
			rest = append(rest, Win{
				Title: "Shorten PR feedback",
				Detail: fmt.Sprintf("contributors wait a median %.0fm for a full verdict; `%s` finishes last on %.0f%% of pushes, a median %.1fm after everything else — speed it up (or split it) and feedback arrives that much sooner",
					fb.P50Minutes, g.Workflow, g.Share*100, g.SlackP50Minutes),
			})
		}
	}
	if n := byRule["D003"]; n > 0 {
		rest = append(rest, Win{
			Title:   "Cache dependencies",
			Detail:  nounVerb(n, "setup step", "downloads", "download") + " the same dependencies every run",
			Rule:    "D003",
			Fixable: true,
		})
	}
	if cl := a.CacheLogs; cl != nil && cl.Available && cl.Restores >= 10 && cl.HitRate < 60 {
		rest = append(rest, Win{
			Title:  "Make caches actually hit",
			Detail: fmt.Sprintf("hit rate is %.0f%% across %d sampled restores — check keys against lockfile hashes and add restore-keys", cl.HitRate, cl.Restores),
			Rule:   "D008",
		})
	}
	if c := a.Cache; c.Available && c.LimitPct >= 90 {
		var dead []string
		if c.StaleMB > 0 {
			dead = append(dead, fmt.Sprintf("%.0f MB stale", c.StaleMB))
		}
		if c.PRRefMB > 0 {
			dead = append(dead, fmt.Sprintf("%.0f MB pinned to PR refs", c.PRRefMB))
		}
		deadStr := ""
		if len(dead) > 0 {
			deadStr = " (" + strings.Join(dead, ", ") + ")"
		}
		rest = append(rest, Win{
			Title: "Free cache space before evictions",
			Detail: fmt.Sprintf("cache is at %s%s — GitHub evicts oldest first, then builds run cold",
				cacheUsagePhrase(c), deadStr),
		})
	}
	// Superseded PR runs: quantified from history when the dollars clear
	// the bar, otherwise the lint finding alone earns an unquantified slot.
	// (Failed/retried superseded runs are counted in win #1, not here.)
	if sup := a.Superseded; sup != nil && sup.Completed > 0 && sup.WastedUSD*factor >= minWinUSD {
		quant = append(quant, Win{
			Title: "Cancel superseded PR runs",
			Detail: fmt.Sprintf("%d PR %s to completion after a newer push had already replaced %s — %.0f billable min bought nothing; concurrency + cancel-in-progress stops this",
				sup.Completed, pluralVerb(sup.Completed, "run ran", "runs ran"), pluralVerb(sup.Completed, "it", "them"), sup.WastedMinutes),
			Rule:     "D001",
			Fixable:  byRule["D001"] > 0,
			USDPerMo: round2(sup.WastedUSD * factor),
		})
	} else if n := byRule["D001"]; n > 0 {
		detail := nounVerb(n, "workflow", "has", "have") + " no concurrency group — runs for already-replaced commits keep burning minutes"
		if sup := a.Superseded; sup != nil && sup.Completed > 0 {
			detail += fmt.Sprintf(" (%d in this sample)", sup.Completed)
		}
		rest = append(rest, Win{
			Title:   "Cancel superseded runs",
			Detail:  detail,
			Rule:    "D001",
			Fixable: true,
		})
	}

	sort.SliceStable(quant, func(i, j int) bool { return quant[i].USDPerMo > quant[j].USDPerMo })
	ws.Items = append(quant, rest...)
	if len(ws.Items) > winsMax {
		ws.Items = ws.Items[:winsMax]
	}
	if len(ws.Items) == 0 {
		return nil
	}
	return ws
}

// nounVerb renders "1 workflow triggers" / "3 workflows trigger" —
// noun pluralization and verb agreement move in opposite directions.
func nounVerb(n int, noun, singularVerb, pluralVerb string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s %s", noun, singularVerb)
	}
	return fmt.Sprintf("%d %ss %s", n, noun, pluralVerb)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// WinsSection renders the ranked action list for the terminal.
func WinsSection(w io.Writer, s Style, ws *Wins) {
	if ws == nil {
		return
	}
	head := "── Top wins ──"
	if t := ws.TotalUSDPerMo(); t >= minWinUSD && ws.Projected {
		head += fmt.Sprintf("  est. ~$%.2f/mo on a private repo", t)
	}
	fmt.Fprintf(w, "\n%s\n", s.bold(head))
	for i, win := range ws.Items {
		amt := ""
		if win.USDPerMo > 0 {
			unit := "/mo"
			if !ws.Projected {
				unit = " in sample"
			}
			amt = "  " + s.bold(fmt.Sprintf("~$%.2f%s", win.USDPerMo, unit))
		}
		fmt.Fprintf(w, "  %d. %s%s\n", i+1, s.cyan(win.Title), amt)
		fmt.Fprintf(w, "     %s\n", win.Detail)
		if hint := winHint(win); hint != "" {
			fmt.Fprintf(w, "     %s\n", s.dim(hint))
		}
	}
	fmt.Fprintf(w, "  %s\n", s.dim("("+ws.Basis+")"))
}

// WinsMarkdown renders the ranked action list for --md.
func WinsMarkdown(w io.Writer, ws *Wins) {
	if ws == nil {
		return
	}
	fmt.Fprintf(w, "\n### Top wins\n\n")
	for i, win := range ws.Items {
		amt := ""
		if win.USDPerMo > 0 {
			unit := "/mo"
			if !ws.Projected {
				unit = " in sample"
			}
			amt = fmt.Sprintf(" — **~$%.2f%s**", win.USDPerMo, unit)
		}
		fmt.Fprintf(w, "%d. **%s**%s — %s", i+1, win.Title, amt, win.Detail)
		if hint := winHint(win); hint != "" {
			fmt.Fprintf(w, " _(%s)_", hint)
		}
		fmt.Fprintf(w, "\n")
	}
	fmt.Fprintf(w, "\n_%s._\n", ws.Basis)
}

func winHint(win Win) string {
	switch {
	case win.Fixable:
		return fmt.Sprintf("gha-doctor --fix handles this (%s)", win.Rule)
	case win.Rule != "":
		return fmt.Sprintf("see: gha-doctor --explain %s", win.Rule)
	}
	return ""
}
