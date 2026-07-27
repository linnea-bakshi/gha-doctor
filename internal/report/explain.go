package report

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/docs"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Explain prints the embedded rules-reference section for ruleID (e.g.
// "D004"), lightly rendered for the terminal. Returns an error naming the
// valid IDs if the rule doesn't exist.
func Explain(w io.Writer, ruleID string, s Style) error {
	id := strings.ToUpper(strings.TrimSpace(ruleID))
	if _, ok := lint.RuleMeta[id]; !ok || id == "PARSE" {
		return fmt.Errorf("unknown rule %q (valid: %s)", ruleID, strings.Join(ruleIDs(), ", "))
	}
	section := extractSection(docs.Rules, id)
	if section == "" {
		// Shouldn't happen (rules.md documents every rule); degrade gracefully.
		m := lint.RuleMeta[id]
		fmt.Fprintf(w, "%s: %s\n\n%s\n", s.bold(m.ID), m.Name, m.Short)
		return nil
	}
	renderMarkdown(w, section, s)
	fmt.Fprintf(w, "\n%s\n", s.dim("Full reference: https://github.com/linnea-bakshi/gha-doctor/blob/main/docs/rules.md#"+anchor(id)))
	return nil
}

// ruleIDs returns the explainable rule IDs, sorted.
func ruleIDs() []string {
	ids := make([]string, 0, len(lint.RuleMeta))
	for id := range lint.RuleMeta {
		if id == "parse" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// extractSection returns the "## Dxxx: ..." section of md, up to the next
// "## " heading or horizontal rule.
func extractSection(md, id string) string {
	lines := strings.Split(md, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "## "+id+":") {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
}

// anchor converts "D004" to the GitHub anchor for its heading, e.g.
// "d004-fullfetchdepth".
func anchor(id string) string {
	m := lint.RuleMeta[id]
	return strings.ToLower(id + "-" + m.Name)
}

var linkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// renderMarkdown does a light terminal rendering: headings and **bold**
// bold (bold may span lines), fenced code blocks indented, links unwrapped.
func renderMarkdown(w io.Writer, md string, s Style) {
	inCode, inBold := false, false
	for _, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(line, "```"):
			inCode = !inCode
		case inCode:
			fmt.Fprintf(w, "    %s\n", s.cyan(line))
		case strings.HasPrefix(line, "## "):
			fmt.Fprintf(w, "%s\n", s.bold(strings.TrimPrefix(line, "## ")))
		default:
			line = linkRe.ReplaceAllString(line, "$1 ($2)")
			fmt.Fprintln(w, renderBold(line, s, &inBold))
		}
	}
}

// renderBold replaces ** toggles with ANSI bold on/off, carrying the bold
// state across lines (closed at each line end, reopened on the next).
func renderBold(line string, s Style, inBold *bool) string {
	parts := strings.Split(line, "**")
	if len(parts) == 1 && !*inBold {
		return line
	}
	var b strings.Builder
	for i, seg := range parts {
		if i > 0 {
			*inBold = !*inBold
		}
		if *inBold && seg != "" {
			b.WriteString(s.bold(seg))
		} else {
			b.WriteString(seg)
		}
	}
	return b.String()
}
