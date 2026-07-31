package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// diffCtx is the number of context lines around each change in --diff output,
// matching git's default.
const diffCtx = 3

// DiffPreview renders --diff results for the terminal: a colored unified diff
// per file, skip notes, and a summary telling the user how to apply.
// remoteRepo is "owner/name" when the preview came from a fetched repo (the
// summary then can't suggest --fix, which needs a local checkout).
func DiffPreview(w io.Writer, s Style, previews []lint.FixPreview, remoteRepo string) {
	fixes, files := 0, 0
	for _, p := range previews {
		if p.Fixed != nil {
			if files > 0 {
				fmt.Fprintln(w)
			}
			files++
			fixes += len(p.Applied)
			ud := lint.UnifiedDiff("a/"+p.Path, "b/"+p.Path, string(p.Original), string(p.Fixed), diffCtx)
			for _, line := range strings.Split(strings.TrimSuffix(ud, "\n"), "\n") {
				fmt.Fprintln(w, colorDiffLine(s, line))
			}
		}
		for _, sk := range p.Skipped {
			fmt.Fprintf(w, "%s %s  %s\n", s.yellow("skip"), p.Path, sk)
		}
		if p.Failed != "" {
			fmt.Fprintf(w, "%s %s  %s\n", s.red("fail"), p.Path, p.Failed)
		}
	}
	if files > 0 {
		fmt.Fprintln(w)
	}
	if fixes == 0 {
		fmt.Fprintln(w, "nothing to fix (fixable rules: "+strings.Join(lint.FixableRules, ", ")+")")
		return
	}
	apply := "apply with --fix"
	if remoteRepo != "" {
		apply = "to apply, run `gha-doctor --fix` inside a checkout of " + remoteRepo
	}
	fmt.Fprintf(w, "%d %s available in %d %s — nothing was written; %s\n",
		fixes, pluralVerb(fixes, "fix", "fixes"), files, plural(files, "file"), apply)
}

func colorDiffLine(s Style, line string) string {
	switch {
	case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
		return s.bold(line)
	case strings.HasPrefix(line, "@@"):
		return s.cyan(line)
	case strings.HasPrefix(line, "+"):
		return s.green(line)
	case strings.HasPrefix(line, "-"):
		return s.red(line)
	case strings.HasPrefix(line, "\\"):
		return s.dim(line)
	}
	return line
}

// DiffPreviewMD renders --diff results as Markdown with ```diff fences.
func DiffPreviewMD(w io.Writer, previews []lint.FixPreview, remoteRepo string) {
	fmt.Fprintln(w, "## Autofix preview")
	fmt.Fprintln(w)
	fixes, files := 0, 0
	for _, p := range previews {
		if p.Fixed != nil {
			files++
			fixes += len(p.Applied)
			fmt.Fprintf(w, "### `%s`\n\n", p.Path)
			fmt.Fprintln(w, "```diff")
			fmt.Fprint(w, lint.UnifiedDiff("a/"+p.Path, "b/"+p.Path, string(p.Original), string(p.Fixed), diffCtx))
			fmt.Fprintln(w, "```")
			fmt.Fprintln(w)
		}
		for _, sk := range p.Skipped {
			fmt.Fprintf(w, "- skip `%s` — %s\n", p.Path, sk)
		}
		if p.Failed != "" {
			fmt.Fprintf(w, "- fail `%s` — %s\n", p.Path, p.Failed)
		}
	}
	if fixes == 0 {
		fmt.Fprintln(w, "Nothing to fix (fixable rules: "+strings.Join(lint.FixableRules, ", ")+").")
		return
	}
	apply := "apply with `--fix`"
	if remoteRepo != "" {
		apply = "to apply, run `gha-doctor --fix` inside a checkout of " + remoteRepo
	}
	fmt.Fprintf(w, "\n%d %s available in %d %s — nothing was written; %s.\n",
		fixes, pluralVerb(fixes, "fix", "fixes"), files, plural(files, "file"), apply)
}

// diffPreviewFile is one file's entry in --diff --json output.
type diffPreviewFile struct {
	Path    string   `json:"path"`
	Applied []string `json:"applied,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Failed  string   `json:"failed,omitempty"`
	Diff    string   `json:"diff,omitempty"`
}

// DiffPreviewJSON renders --diff results as JSON.
func DiffPreviewJSON(w io.Writer, previews []lint.FixPreview) error {
	out := struct {
		FixPreview     []diffPreviewFile `json:"fix_preview"`
		FixesAvailable int               `json:"fixes_available"`
	}{FixPreview: []diffPreviewFile{}}
	for _, p := range previews {
		f := diffPreviewFile{Path: p.Path, Applied: p.Applied, Skipped: p.Skipped, Failed: p.Failed}
		if p.Fixed != nil {
			f.Diff = lint.UnifiedDiff("a/"+p.Path, "b/"+p.Path, string(p.Original), string(p.Fixed), diffCtx)
		}
		out.FixesAvailable += len(p.Applied)
		out.FixPreview = append(out.FixPreview, f)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
