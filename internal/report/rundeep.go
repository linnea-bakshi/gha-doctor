package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

// Waterfall bar width in characters (the timeline area only).
const runBarWidth = 44

// How much slower than the historical p50 a step must be before it is
// called out: at least this ratio AND at least this many seconds, so a 4s
// step at 3x doesn't shout while a 5-minute regression does.
const (
	runSlowRatio  = 1.5
	runSlowMinSec = 20.0
	runFastRatio  = 0.67
	maxDeepSteps  = 10
	maxDeepMovers = 3

	// Failing tests listed per job before the count takes over.
	maxDeepFailedTestsShown = 10
)

// RunDeepJSON writes the deep dive as a standalone JSON document.
func RunDeepJSON(w io.Writer, d *api.RunDeep) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Run *api.RunDeep `json:"run"`
	}{d})
}

// RunDeep renders the single-run deep dive for the terminal.
func RunDeep(w io.Writer, s Style, d *api.RunDeep) {
	title := fmt.Sprintf("── Run #%d: %s (%s) ──", d.RunNumber, d.Workflow, d.Repo)
	fmt.Fprintf(w, "%s\n", s.bold(title))
	line := fmt.Sprintf("  %s", concLabel(s, d.Conclusion, d.Status))
	if d.Title != "" {
		line += "  " + s.dim(trunc(d.Title, 60))
	}
	meta := []string{}
	if d.Event != "" {
		meta = append(meta, d.Event)
	}
	if d.Branch != "" {
		meta = append(meta, d.Branch)
	}
	meta = append(meta, d.StartedAt.Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(w, "%s\n  %s\n", line, s.dim(strings.Join(meta, " · ")))

	// Verdict.
	fmt.Fprintln(w)
	for _, v := range runVerdicts(d) {
		fmt.Fprintf(w, "  %s\n", verdictColor(s, v))
	}

	// Waterfall.
	fmt.Fprintf(w, "\n%s\n", s.bold("Timeline")+s.dim("  (· queued, █ running; offsets from run start)"))
	scale := d.WallSec
	for _, j := range d.Jobs {
		if j.EndSec > scale {
			scale = j.EndSec
		}
	}
	if scale <= 0 {
		scale = 1
	}
	for _, j := range d.Jobs {
		bar := renderBar(s, j, scale)
		dur := "…" // still running or queued
		switch {
		case j.Conclusion == "skipped":
			dur = "–"
		case j.DurSec > 0 || j.EndSec > 0:
			dur = humanSec(j.DurSec)
		}
		// No per-job attempt tag: any re-execution bumps the run attempt,
		// so on an attempt-N timeline every drawn job ran in attempt N —
		// the verdict line reports the re-run once.
		note := ""
		if j.BaselineN > 0 && j.P50Sec > 0 && j.DurSec >= j.P50Sec*runSlowRatio && j.DurSec-j.P50Sec >= runSlowMinSec {
			note += s.red(fmt.Sprintf(" %.1fx p50", j.DurSec/j.P50Sec))
		}
		fmt.Fprintf(w, "  %-28s %s %8s%s\n", trunc(j.Name, 28), bar, dur, note)
	}
	if d.WallSec > 0 {
		wall := fmt.Sprintf("wall clock %s", humanSec(d.WallSec))
		if d.InProgress {
			wall += " so far (run still in progress)"
		}
		fmt.Fprintf(w, "  %s\n", s.dim(wall))
	}

	// Slowest steps vs their own history.
	steps := flattenSteps(d)
	if len(steps) > 0 {
		fmt.Fprintf(w, "\n%s\n", s.bold("Slowest steps")+s.dim("  (this run; p50 = same step in recent successful runs)"))
		fmt.Fprintf(w, "  %-32s %-24s %8s %8s  %s\n", "step", "job", "took", "p50", "vs p50")
		for _, st := range steps {
			delta := s.dim("–")
			if st.BaselineN > 0 && st.P50Sec > 0 {
				ratio := st.DurSec / st.P50Sec
				switch {
				case st.DurSec >= st.P50Sec*runSlowRatio && st.DurSec-st.P50Sec >= runSlowMinSec:
					delta = s.red(fmt.Sprintf("%.1fx slower", ratio))
				case ratio <= runFastRatio:
					delta = s.green(fmt.Sprintf("%.1fx faster", 1/ratio))
				default:
					delta = s.dim("~typical")
				}
			}
			p50 := "–"
			if st.P50Sec > 0 {
				p50 = humanSec(st.P50Sec)
			}
			fmt.Fprintf(w, "  %-32s %-24s %8s %8s  %s\n",
				trunc(st.Name, 32), trunc(st.Job, 24), humanSec(st.DurSec), p50, delta)
		}
	}
	// Failing tests + failing step log tails.
	for _, j := range d.Jobs {
		if len(j.FailedTests) > 0 {
			fmt.Fprintf(w, "\n%s%s\n", s.bold(fmt.Sprintf("Failing tests — %s", j.Name)),
				s.dim("  (recognized in the job log)"))
			shown := j.FailedTests
			if len(shown) > maxDeepFailedTestsShown {
				shown = shown[:maxDeepFailedTestsShown]
			}
			for _, tf := range shown {
				fmt.Fprintf(w, "    %s %s  %s\n", s.red("✗"), tf.Name, s.dim("("+tf.Framework+")"))
			}
			if more := len(j.FailedTests) - len(shown) + j.FailedTestsMore; more > 0 {
				fmt.Fprintf(w, "    %s\n", s.dim(fmt.Sprintf("… and %d more", more)))
			}
		}
		if len(j.LogTail) == 0 {
			continue
		}
		head := fmt.Sprintf("Failing step log — %s", j.Name)
		if j.LogStep != "" {
			head += " › " + j.LogStep
		}
		fmt.Fprintf(w, "\n%s%s\n", s.bold(head), s.dim(fmt.Sprintf("  (last %d lines)", len(j.LogTail))))
		for _, l := range j.LogTail {
			if strings.Contains(l, "##[error]") {
				fmt.Fprintf(w, "    %s\n", s.red(l))
			} else {
				fmt.Fprintf(w, "    %s\n", s.dim(l))
			}
		}
	}
	if d.LogNote != "" {
		fmt.Fprintf(w, "\n  %s\n", s.dim("note: "+d.LogNote))
	}
	if d.BaselineNote != "" {
		fmt.Fprintf(w, "\n  %s\n", s.dim("note: "+d.BaselineNote))
	}
}

// RunDeepMarkdown renders the deep dive as Markdown.
func RunDeepMarkdown(w io.Writer, d *api.RunDeep) {
	fmt.Fprintf(w, "## Run #%d: %s (%s)\n\n", d.RunNumber, d.Workflow, d.Repo)
	state := d.Conclusion
	if state == "" {
		state = d.Status
	}
	fmt.Fprintf(w, "**%s** · %s · %s · %s\n\n", state, d.Event, d.Branch, d.StartedAt.Format("2006-01-02 15:04 MST"))
	if d.URL != "" {
		fmt.Fprintf(w, "[View on GitHub](%s)\n\n", d.URL)
	}
	for _, v := range runVerdicts(d) {
		fmt.Fprintf(w, "> %s\n", v)
	}
	fmt.Fprintf(w, "\n### Jobs\n\n| job | queued | took | vs own p50 |\n|---|---|---|---|\n")
	for _, j := range d.Jobs {
		vs := "–"
		if j.BaselineN > 0 && j.P50Sec > 0 && j.DurSec > 0 {
			vs = fmt.Sprintf("%.1fx (p50 %s, n=%d)", j.DurSec/j.P50Sec, humanSec(j.P50Sec), j.BaselineN)
		}
		took := "…"
		switch {
		case j.Conclusion == "skipped":
			took = "–"
		case j.DurSec > 0 || j.EndSec > 0:
			took = humanSec(j.DurSec)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", j.Name, humanSec(j.QueueSec), took, vs)
	}
	steps := flattenSteps(d)
	if len(steps) > 0 {
		fmt.Fprintf(w, "\n### Slowest steps\n\n| step | job | took | p50 |\n|---|---|---|---|\n")
		for _, st := range steps {
			p50 := "–"
			if st.P50Sec > 0 {
				p50 = humanSec(st.P50Sec)
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", st.Name, st.Job, humanSec(st.DurSec), p50)
		}
	}
	for _, j := range d.Jobs {
		if len(j.FailedTests) > 0 {
			fmt.Fprintf(w, "\n### Failing tests — %s\n\n", j.Name)
			shown := j.FailedTests
			if len(shown) > maxDeepFailedTestsShown {
				shown = shown[:maxDeepFailedTestsShown]
			}
			for _, tf := range shown {
				fmt.Fprintf(w, "- `%s` (%s)\n", strings.ReplaceAll(tf.Name, "`", "'"), tf.Framework)
			}
			if more := len(j.FailedTests) - len(shown) + j.FailedTestsMore; more > 0 {
				fmt.Fprintf(w, "- … and %d more\n", more)
			}
		}
		if len(j.LogTail) == 0 {
			continue
		}
		head := j.Name
		if j.LogStep != "" {
			head += " › " + j.LogStep
		}
		fmt.Fprintf(w, "\n### Failing step log — %s\n\n```text\n", head)
		for _, l := range j.LogTail {
			fmt.Fprintln(w, strings.ReplaceAll(l, "```", "`\u200b``"))
		}
		fmt.Fprint(w, "```\n")
	}
	if d.LogNote != "" {
		fmt.Fprintf(w, "\n_%s_\n", d.LogNote)
	}
	if d.BaselineNote != "" {
		fmt.Fprintf(w, "\n_%s_\n", d.BaselineNote)
	}
}

// flatStep is a step tagged with its job for cross-job sorting.
type flatStep struct {
	api.DeepStep
	Job string
}

func flattenSteps(d *api.RunDeep) []flatStep {
	var out []flatStep
	for _, j := range d.Jobs {
		for _, st := range j.Steps {
			if st.DurSec < 1 { // sub-second bookkeeping steps are noise
				continue
			}
			out = append(out, flatStep{st, j.Name})
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].DurSec > out[k].DurSec })
	if len(out) > maxDeepSteps {
		out = out[:maxDeepSteps]
	}
	return out
}

// runVerdicts builds the plain-text verdict lines: overall wall clock vs
// the workflow's p50, the biggest step regressions, and queue callouts.
func runVerdicts(d *api.RunDeep) []string {
	var out []string
	failed := d.Conclusion == "failure" || d.Conclusion == "timed_out"
	// Where did it fail? Lead with that — it's the first question anyone
	// asks of a red run.
	if failed {
		called := 0
		for _, j := range d.Jobs {
			if j.Conclusion != "failure" && j.Conclusion != "timed_out" {
				continue
			}
			v := fmt.Sprintf("✗ job %q failed", j.Name)
			for _, st := range j.Steps {
				if st.Conclusion == "failure" || st.Conclusion == "timed_out" {
					v = fmt.Sprintf("✗ job %q failed at step %q (step ran %s)", j.Name, st.Name, humanSec(st.DurSec))
					break
				}
			}
			if n := len(j.FailedTests) + j.FailedTestsMore; n > 0 {
				if n == 1 {
					v += fmt.Sprintf(" — failing test: %s", j.FailedTests[0].Name)
				} else {
					v += fmt.Sprintf(" — %d failing tests incl. %s", n, j.FailedTests[0].Name)
				}
			}
			out = append(out, v)
			if called++; called == maxDeepMovers {
				break
			}
		}
	}
	// Skipped/cancelled runs never ran to completion — comparing their
	// wall clock to successful runs proves nothing, so no speed verdict.
	nonDecisive := d.Conclusion == "skipped" || d.Conclusion == "cancelled" ||
		d.Conclusion == "action_required" || d.Conclusion == "neutral" || d.Conclusion == "stale"
	if d.WallSec > 0 && d.BaselineWallP50 > 0 {
		ratio := d.WallSec / d.BaselineWallP50
		switch {
		case nonDecisive:
			out = append(out, fmt.Sprintf("· this run was %s after %s; the last %d successful runs have a p50 of %s",
				d.Conclusion, humanSec(d.WallSec), d.BaselineRuns, humanSec(d.BaselineWallP50)))
		case d.InProgress:
			// No verdict on an unfinished run — just orient the reader.
			out = append(out, fmt.Sprintf("· still running: %s so far; the last %d successful runs have a p50 of %s",
				humanSec(d.WallSec), d.BaselineRuns, humanSec(d.BaselineWallP50)))
		case failed:
			// A red run that "beat the p50" just stopped early — never
			// praise it; state the comparison neutrally.
			out = append(out, fmt.Sprintf("· this run stopped after %s; the last %d successful runs have a p50 of %s",
				humanSec(d.WallSec), d.BaselineRuns, humanSec(d.BaselineWallP50)))
		case ratio >= 1.25 && d.WallSec-d.BaselineWallP50 >= runSlowMinSec:
			out = append(out, fmt.Sprintf("⚠ this run took %s — %.1fx the p50 (%s) of the last %d successful runs",
				humanSec(d.WallSec), ratio, humanSec(d.BaselineWallP50), d.BaselineRuns))
		case ratio <= 0.75:
			out = append(out, fmt.Sprintf("✓ this run took %s — %.1fx faster than the p50 (%s) of the last %d successful runs",
				humanSec(d.WallSec), 1/ratio, humanSec(d.BaselineWallP50), d.BaselineRuns))
		default:
			out = append(out, fmt.Sprintf("✓ this run took %s — in line with the p50 (%s) of the last %d successful runs",
				humanSec(d.WallSec), humanSec(d.BaselineWallP50), d.BaselineRuns))
		}
	}
	// Biggest step regressions, by absolute seconds lost vs p50.
	type mover struct {
		name, job string
		lostSec   float64
		ratio     float64
	}
	var movers []mover
	for _, j := range d.Jobs {
		for _, st := range j.Steps {
			if st.BaselineN == 0 || st.P50Sec <= 0 {
				continue
			}
			if st.DurSec >= st.P50Sec*runSlowRatio && st.DurSec-st.P50Sec >= runSlowMinSec {
				movers = append(movers, mover{st.Name, j.Name, st.DurSec - st.P50Sec, st.DurSec / st.P50Sec})
			}
		}
	}
	sort.Slice(movers, func(i, k int) bool { return movers[i].lostSec > movers[k].lostSec })
	if len(movers) > maxDeepMovers {
		movers = movers[:maxDeepMovers]
	}
	for _, m := range movers {
		out = append(out, fmt.Sprintf("⚠ %q in %s: +%s vs its p50 (%.1fx slower)", m.name, m.job, humanSec(m.lostSec), m.ratio))
	}
	// Queue time: call out jobs that waited longer than they ran, or >2m.
	var worstQ api.DeepJob
	for _, j := range d.Jobs {
		if j.QueueSec > worstQ.QueueSec {
			worstQ = j
		}
	}
	if worstQ.QueueSec >= 120 || (worstQ.QueueSec >= 30 && worstQ.DurSec > 0 && worstQ.QueueSec > worstQ.DurSec) {
		out = append(out, fmt.Sprintf("⚠ %s waited %s for a runner before starting", worstQ.Name, humanSec(worstQ.QueueSec)))
	}
	if d.Attempt > 1 {
		v := fmt.Sprintf("⚠ attempt %d of this run: %s again", d.Attempt, nounVerbRan(d.RetriedJobs, "job"))
		if d.CarriedJobs > 0 {
			v += fmt.Sprintf(", %d carried over from earlier attempts", d.CarriedJobs)
		}
		out = append(out, v+" — earlier attempts also billed")
	}
	if len(out) == 0 && d.WallSec > 0 {
		switch {
		case d.InProgress:
			out = append(out, fmt.Sprintf("· still running: %s so far", humanSec(d.WallSec)))
		case nonDecisive:
			out = append(out, fmt.Sprintf("· this run was %s after %s", d.Conclusion, humanSec(d.WallSec)))
		default:
			out = append(out, fmt.Sprintf("✓ this run took %s — nothing unusual found", humanSec(d.WallSec)))
		}
	}
	return out
}

func verdictColor(s Style, v string) string {
	switch {
	case strings.HasPrefix(v, "⚠"):
		return s.yellow(v)
	case strings.HasPrefix(v, "✗"):
		return s.red(v)
	case strings.HasPrefix(v, "·"):
		return s.dim(v)
	default:
		return s.green(v)
	}
}

// nounVerbRan: "1 job ran" / "3 jobs ran".
func nounVerbRan(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ran", noun)
	}
	return fmt.Sprintf("%d %ss ran", n, noun)
}

// renderBar draws one job's timeline bar: dim dots for queue wait, blocks
// for execution, colored by conclusion.
func renderBar(s Style, j api.DeepJob, scale float64) string {
	pos := func(sec float64) int {
		p := int(sec / scale * float64(runBarWidth))
		if p > runBarWidth {
			p = runBarWidth
		}
		return p
	}
	qStart := pos(clampNonNegR(j.StartSec - j.QueueSec))
	start := pos(j.StartSec)
	end := pos(j.EndSec)
	if j.EndSec <= 0 { // still running: draw to the scale edge
		end = runBarWidth
	}
	if end <= start {
		end = start + 1 // sub-cell jobs still get one visible block
	}
	if end > runBarWidth {
		end = runBarWidth
	}
	if start > runBarWidth-1 {
		start = runBarWidth - 1
	}
	if qStart > start {
		qStart = start
	}
	blocks := strings.Repeat("█", end-start)
	switch j.Conclusion {
	case "success":
		blocks = s.green(blocks)
	case "failure", "timed_out":
		blocks = s.red(blocks)
	case "cancelled", "skipped":
		blocks = s.dim(blocks)
	default: // in_progress / queued
		blocks = s.yellow(blocks)
	}
	return strings.Repeat(" ", qStart) + s.dim(strings.Repeat("·", start-qStart)) + blocks + strings.Repeat(" ", runBarWidth-end)
}

func clampNonNegR(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// concLabel renders the run's state with color.
func concLabel(s Style, conclusion, status string) string {
	v := conclusion
	if v == "" {
		v = status
	}
	switch v {
	case "success":
		return s.green("✓ " + v)
	case "failure", "timed_out":
		return s.red("✗ " + v)
	default:
		return s.yellow("● " + v)
	}
}

// humanSec renders seconds as 47s / 3m12s / 1h04m.
func humanSec(sec float64) string {
	s := int(sec + 0.5)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	}
}
