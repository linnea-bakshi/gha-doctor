package report

import (
	"fmt"
	"io"
	"math"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Score is a 0–100 CI health score with a letter grade. It is computed
// only from components that are actually available for the repo (e.g. a
// --lint-only run scores static hygiene alone), and every deduction is
// itemized so the number is auditable rather than a vibe.
//
// The formula is documented in docs/score.md and is deliberately simple:
// each component has a maximum weight, deductions are linear within it,
// and the final score is normalized to the weights that were available:
//
//	score = round(100 × (Σ max − Σ deducted) / Σ max)
type Score struct {
	Points     int              `json:"points"` // 0–100
	Grade      string           `json:"grade"`  // A+, A, B, C, D, F
	Basis      string           `json:"basis"`  // e.g. "static + history", "static checks only"
	Components []ScoreComponent `json:"components"`
}

// ScoreComponent is one itemized part of the score.
type ScoreComponent struct {
	Name     string  `json:"name"`
	Deducted float64 `json:"deducted"`
	Max      float64 `json:"max"`
	Detail   string  `json:"detail"`
}

// Component weights. Kept as constants so docs/score.md can state them.
const (
	wHygiene = 30 // static lint findings
	wSuccess = 25 // run success rate
	wFlaky   = 15 // jobs that failed and passed on the same commit
	wWaste   = 15 // failed-run + retry minutes share
	wCache   = 10 // cache pressure / hit rate
	wQueue   = 5  // time spent waiting for a runner
)

// ComputeScore builds the health score from whatever inputs are present.
// filesScanned distinguishes "no findings because clean" from "no findings
// because there were no workflows to scan".
func ComputeScore(findings []lint.Finding, filesScanned int, a *api.Analysis) Score {
	var comps []ScoreComponent

	if filesScanned > 0 {
		warns, infos := 0, 0
		for _, f := range findings {
			if f.Severity == lint.Warn {
				warns++
			} else {
				infos++
			}
		}
		d := math.Min(wHygiene, float64(warns)*4+float64(infos)*1)
		comps = append(comps, ScoreComponent{
			Name: "workflow hygiene", Deducted: d, Max: wHygiene,
			Detail: fmt.Sprintf("%d warning(s), %d info finding(s) across %d file(s)", warns, infos, filesScanned),
		})
	}

	if a != nil {
		comps = append(comps, historyComponents(a)...)
	}

	var max, ded float64
	for _, c := range comps {
		max += c.Max
		ded += c.Deducted
	}
	s := Score{Components: comps, Basis: basis(filesScanned > 0, a != nil)}
	if max == 0 {
		// Nothing to score at all; call it unknown-but-perfect is wrong,
		// so mark 0 with an explicit basis.
		s.Basis = "nothing to score"
		s.Grade = "–"
		return s
	}
	s.Points = int(math.Round(100 * (max - ded) / max))
	if s.Points < 0 {
		s.Points = 0
	}
	s.Grade = gradeFor(s.Points)
	return s
}

func historyComponents(a *api.Analysis) []ScoreComponent {
	var comps []ScoreComponent

	// Success rate, weighted by runs per workflow.
	var runs int
	var succWeighted float64
	var queueWeighted float64
	for _, w := range a.Workflows {
		runs += w.Runs
		succWeighted += w.SuccessRate * float64(w.Runs)
		queueWeighted += w.AvgQueueSec * float64(w.Runs)
	}
	if runs > 0 {
		succPct := succWeighted / float64(runs) * 100 // SuccessRate is a 0–1 fraction
		failPct := 100 - succPct
		// Full deduction at a 40% failure rate.
		d := math.Min(wSuccess, failPct*wSuccess/40)
		comps = append(comps, ScoreComponent{
			Name: "success rate", Deducted: round1(d), Max: wSuccess,
			Detail: fmt.Sprintf("%.0f%% of %d sampled runs succeeded", succPct, runs),
		})

		avgQueue := queueWeighted / float64(runs)
		// Full deduction at 120 s average queue time.
		dq := math.Min(wQueue, avgQueue*wQueue/120)
		comps = append(comps, ScoreComponent{
			Name: "queue time", Deducted: round1(dq), Max: wQueue,
			Detail: fmt.Sprintf("average %.0f s waiting for a runner", avgQueue),
		})
	}

	// Flakiness: 5 points per flaky job.
	d := math.Min(wFlaky, float64(len(a.FlakyJobs))*5)
	detail := "no jobs both failed and passed on the same commit"
	if n := len(a.FlakyJobs); n > 0 {
		detail = fmt.Sprintf("%d %s failed AND passed on the same commit", n, plural(n, "job"))
	}
	comps = append(comps, ScoreComponent{
		Name: "flakiness", Deducted: d, Max: wFlaky, Detail: detail,
	})

	// Waste: failed-run + retry minutes as a share of all compute minutes.
	if a.Waste.ComputeMinutes > 0 {
		share := a.Waste.TotalMinutes / a.Waste.ComputeMinutes
		// Full deduction when 30% of minutes bought nothing.
		dw := math.Min(wWaste, share*100*wWaste/30)
		comps = append(comps, ScoreComponent{
			Name: "wasted minutes", Deducted: round1(dw), Max: wWaste,
			Detail: fmt.Sprintf("%.0f%% of sampled compute minutes went to failed runs or retries", share*100),
		})
	}

	// Cache: prefer the measured hit rate (--cache-logs); fall back to
	// cache-storage pressure signals.
	switch {
	case a.CacheLogs != nil && a.CacheLogs.Available && a.CacheLogs.Restores > 0:
		miss := (100 - a.CacheLogs.HitRate) / 100 // HitRate is 0–100
		dc := math.Min(wCache, miss*wCache*2)     // full deduction at 50% miss rate
		comps = append(comps, ScoreComponent{
			Name: "cache hit rate", Deducted: round1(dc), Max: wCache,
			Detail: fmt.Sprintf("%.0f%% of %d sampled restores hit", a.CacheLogs.HitRate, a.CacheLogs.Restores),
		})
	case a.Cache.Available && a.Cache.Count > 0:
		var dc float64
		var why string
		if a.Cache.LimitPct >= 90 {
			dc += 5
			why = fmt.Sprintf("cache storage at %.0f%% of the 10 GB limit (evictions likely)", a.Cache.LimitPct)
		}
		if a.Cache.TotalMB > 0 {
			deadShare := (a.Cache.StaleMB + a.Cache.PRRefMB) / a.Cache.TotalMB
			if deadShare > 0.5 {
				dc += 5
				if why != "" {
					why += "; "
				}
				why += fmt.Sprintf("%.0f%% of cache bytes are stale or pinned to PR refs", deadShare*100)
			}
		}
		if why == "" {
			why = fmt.Sprintf("cache storage at %.0f%% of the 10 GB limit", a.Cache.LimitPct)
		}
		comps = append(comps, ScoreComponent{
			Name: "cache pressure", Deducted: math.Min(wCache, dc), Max: wCache, Detail: why,
		})
	}

	return comps
}

func basis(static, history bool) string {
	switch {
	case static && history:
		return "static checks + run history"
	case static:
		return "static checks only"
	case history:
		return "run history only"
	}
	return "nothing to score"
}

func gradeFor(points int) string {
	switch {
	case points >= 97:
		return "A+"
	case points >= 90:
		return "A"
	case points >= 80:
		return "B"
	case points >= 70:
		return "C"
	case points >= 60:
		return "D"
	}
	return "F"
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// ScoreSection renders the score for terminals.
func ScoreSection(w io.Writer, s Style, sc Score) {
	fmt.Fprintf(w, "\n%s\n", s.bold("Health score"))
	col := s.green
	switch sc.Grade {
	case "C", "D":
		col = s.yellow
	case "F":
		col = s.red
	case "–":
		col = s.dim
	}
	fmt.Fprintf(w, "  %s  (%d/100, %s)\n", col(s.bold(sc.Grade)), sc.Points, sc.Basis)
	for _, c := range sc.Components {
		mark := s.green("✓")
		if c.Deducted >= c.Max/2 {
			mark = s.red("✗")
		} else if c.Deducted > 0 {
			mark = s.yellow("!")
		}
		fmt.Fprintf(w, "  %s %-18s −%-5s %s\n", mark, c.Name,
			trimZero(c.Deducted), s.dim(c.Detail))
	}
}

// ScoreMarkdown renders the score for Markdown output.
func ScoreMarkdown(w io.Writer, sc Score) {
	fmt.Fprintf(w, "\n## Health score: %s (%d/100)\n\n_Basis: %s._\n\n", sc.Grade, sc.Points, sc.Basis)
	fmt.Fprintln(w, "| Component | Deducted | Max | Detail |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, c := range sc.Components {
		fmt.Fprintf(w, "| %s | −%s | %.0f | %s |\n", c.Name, trimZero(c.Deducted), c.Max, c.Detail)
	}
}

func trimZero(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s
}
