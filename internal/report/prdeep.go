package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

// prDeepDoc is the top-level --pr --json document. The schema generator
// reflects over this exact type, so output and schema cannot drift.
type prDeepDoc struct {
	PR *api.PRDeep `json:"pr"`
}

// PRDeepJSON writes the PR deep dive as a standalone JSON document.
func PRDeepJSON(w io.Writer, d *api.PRDeep) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(prDeepDoc{d})
}

// prMetaLine builds the "open · draft · feat → main · 3 commits" line.
func prMetaLine(d *api.PRDeep) []string {
	meta := []string{d.State}
	if d.Draft {
		meta = append(meta, "draft")
	}
	if d.Branch != "" && d.BaseBranch != "" {
		meta = append(meta, d.Branch+" → "+d.BaseBranch)
	}
	if d.Commits > 0 {
		unit := "commits"
		if d.Commits == 1 {
			unit = "commit"
		}
		meta = append(meta, fmt.Sprintf("%d %s", d.Commits, unit))
	}
	if d.Author != "" {
		meta = append(meta, "by "+d.Author)
	}
	if !d.CreatedAt.IsZero() {
		meta = append(meta, "opened "+d.CreatedAt.Format("2006-01-02"))
	}
	return meta
}

// prRunCounts summarizes the head-commit runs: total, failed, in flight.
func prRunCounts(d *api.PRDeep) (failed, running int) {
	for _, r := range d.Runs {
		if r.Status != "completed" {
			running++
		} else if r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "cancelled" && r.Conclusion != "neutral" {
			failed++
		}
	}
	return failed, running
}

// PRDeep renders the pull-request deep dive for the terminal.
func PRDeep(w io.Writer, s Style, d *api.PRDeep) {
	title := fmt.Sprintf("── PR #%d: %s (%s) ──", d.Number, trunc(d.Title, 60), d.Repo)
	fmt.Fprintf(w, "%s\n", s.bold(title))
	fmt.Fprintf(w, "  %s\n", s.dim(strings.Join(prMetaLine(d), " · ")))
	if d.URL != "" {
		fmt.Fprintf(w, "  %s\n", s.dim(d.URL))
	}
	fmt.Fprintln(w)

	if d.RunsNote != "" {
		fmt.Fprintf(w, "  %s %s\n", s.yellow("note:"), d.RunsNote)
	}
	if len(d.Runs) == 0 {
		return
	}

	failed, running := prRunCounts(d)
	head := fmt.Sprintf("Runs on head commit %.7s — %d total", d.HeadSHA, len(d.Runs))
	if failed > 0 {
		head += fmt.Sprintf(", %d failed", failed)
	}
	if running > 0 {
		head += fmt.Sprintf(", %d in progress", running)
	}
	fmt.Fprintf(w, "  %s\n", s.bold(head))
	for _, r := range d.Runs {
		line := fmt.Sprintf("    %s  %s", concLabel(s, r.Conclusion, r.Status), r.Workflow)
		var extra []string
		if r.WallSec > 0 {
			extra = append(extra, humanSec(r.WallSec))
		}
		if r.Attempt > 1 {
			extra = append(extra, fmt.Sprintf("attempt %d", r.Attempt))
		}
		if r.Event != "" {
			extra = append(extra, r.Event)
		}
		if len(extra) > 0 {
			line += "  " + s.dim(strings.Join(extra, " · "))
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)

	if d.FeedbackNote != "" {
		if d.FeedbackSec > 0 {
			fmt.Fprintf(w, "  %s %s (%s)\n", s.bold("Feedback:"), humanSec(d.FeedbackSec), d.FeedbackNote)
		} else {
			fmt.Fprintf(w, "  %s %s\n", s.bold("Feedback:"), d.FeedbackNote)
		}
	}
	if d.RetriedRuns > 0 {
		fmt.Fprintf(w, "  %s %d run(s) on attempt >1 — someone hit re-run; if that turned red green, suspect a flaky check\n",
			s.yellow("⚠"), d.RetriedRuns)
	}
	if d.DeepNote != "" {
		fmt.Fprintf(w, "  %s\n", s.dim(d.DeepNote))
	}
	if d.Deep != nil {
		fmt.Fprintln(w)
		RunDeep(w, s, d.Deep)
	}
}

// PRDeepMarkdown renders the pull-request deep dive as Markdown.
func PRDeepMarkdown(w io.Writer, d *api.PRDeep) {
	fmt.Fprintf(w, "## PR #%d: %s (%s)\n\n", d.Number, d.Title, d.Repo)
	fmt.Fprintf(w, "**%s**\n\n", strings.Join(prMetaLine(d), " · "))
	if d.URL != "" {
		fmt.Fprintf(w, "[View on GitHub](%s)\n\n", d.URL)
	}
	if d.RunsNote != "" {
		fmt.Fprintf(w, "> %s\n\n", d.RunsNote)
	}
	if len(d.Runs) > 0 {
		failed, running := prRunCounts(d)
		summary := fmt.Sprintf("%d run(s) on head commit `%.7s`", len(d.Runs), d.HeadSHA)
		if failed > 0 {
			summary += fmt.Sprintf(", **%d failed**", failed)
		}
		if running > 0 {
			summary += fmt.Sprintf(", %d in progress", running)
		}
		fmt.Fprintf(w, "%s\n\n| workflow | result | took | attempt | event |\n|---|---|---|---|---|\n", summary)
		for _, r := range d.Runs {
			state := r.Conclusion
			if state == "" {
				state = r.Status
			}
			took := "–"
			if r.WallSec > 0 {
				took = humanSec(r.WallSec)
			}
			att := ""
			if r.Attempt > 0 {
				att = fmt.Sprint(r.Attempt)
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", r.Workflow, state, took, att, r.Event)
		}
		fmt.Fprintln(w)
	}
	if d.FeedbackNote != "" {
		if d.FeedbackSec > 0 {
			fmt.Fprintf(w, "**Feedback:** %s (%s)\n\n", humanSec(d.FeedbackSec), d.FeedbackNote)
		} else {
			fmt.Fprintf(w, "**Feedback:** %s\n\n", d.FeedbackNote)
		}
	}
	if d.RetriedRuns > 0 {
		fmt.Fprintf(w, "> ⚠ %d run(s) on attempt >1 — someone hit re-run; if that turned red green, suspect a flaky check.\n\n", d.RetriedRuns)
	}
	if d.DeepNote != "" {
		fmt.Fprintf(w, "_%s_\n\n", d.DeepNote)
	}
	if d.Deep != nil {
		RunDeepMarkdown(w, d.Deep)
	}
}
