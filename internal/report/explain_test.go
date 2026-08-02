package report

import (
	"regexp"
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/docs"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func TestExplainEveryRuleHasSection(t *testing.T) {
	for id := range lint.RuleMeta {
		if id == "parse" {
			continue
		}
		var b strings.Builder
		if err := Explain(&b, id, Style{Plain: true}); err != nil {
			t.Fatalf("Explain(%s): %v", id, err)
		}
		out := b.String()
		if !strings.HasPrefix(out, id+": "+lint.RuleMeta[id].Name) {
			t.Errorf("Explain(%s) missing heading, got: %.80q", id, out)
		}
		if !strings.Contains(out, "docs/rules.md#"+strings.ToLower(id)) {
			t.Errorf("Explain(%s) missing deep link", id)
		}
		// A real section, not the RuleMeta fallback: rules.md sections all
		// have a bold lead sentence and are much longer than Short.
		if len(out) < 2*len(lint.RuleMeta[id].Short) {
			t.Errorf("Explain(%s) looks like the fallback, len=%d", id, len(out))
		}
	}
}

func TestExplainCaseInsensitiveAndTrimmed(t *testing.T) {
	var a, b strings.Builder
	if err := Explain(&a, " d004 ", Style{Plain: true}); err != nil {
		t.Fatal(err)
	}
	if err := Explain(&b, "D004", Style{Plain: true}); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("case/whitespace variants should render identically")
	}
}

func TestExplainUnknownRule(t *testing.T) {
	for _, id := range []string{"D099", "", "parse", "nope"} {
		var b strings.Builder
		err := Explain(&b, id, Style{Plain: true})
		if err == nil {
			t.Errorf("Explain(%q): want error", id)
			continue
		}
		if !strings.Contains(err.Error(), "D001") || !strings.Contains(err.Error(), "D012") {
			t.Errorf("Explain(%q) error should list valid IDs, got: %v", id, err)
		}
	}
}

func TestExplainRendersCodeIndentedAndNoFences(t *testing.T) {
	var b strings.Builder
	if err := Explain(&b, "D001", Style{Plain: true}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "```") {
		t.Error("code fences should not appear in rendered output")
	}
	if !strings.Contains(out, "\n    ") {
		t.Error("code blocks should be indented")
	}
	if strings.Contains(out, "**") {
		t.Error("** markers should be stripped")
	}
}

func TestExtractSectionBoundaries(t *testing.T) {
	md := "intro\n## D001: A\nbody1\n## D002: B\nbody2\n"
	if got := extractSection(md, "D001"); !strings.Contains(got, "body1") || strings.Contains(got, "body2") {
		t.Errorf("D001 section wrong: %q", got)
	}
	if got := extractSection(md, "D003"); got != "" {
		t.Errorf("missing section should be empty, got %q", got)
	}
}

// TestRulesTableRowSync guards the summary table at the top of docs/rules.md:
// wake 89 found D019 had a full section but no table row (the section sync
// test can't see that). Every rule must have exactly one row, with the right
// anchor, the right name, and a --fix cell that agrees with lint.FixableRules.
func TestRulesTableRowSync(t *testing.T) {
	fixable := map[string]bool{}
	for _, id := range lint.FixableRules {
		fixable[id] = true
	}

	// Rows look like: | [D001](#d001-...) | Name | warning | ✅ |
	rows := map[string][]string{} // id -> cells
	for _, line := range strings.Split(docs.Rules, "\n") {
		if !strings.HasPrefix(line, "| [D") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) != 4 {
			t.Errorf("table row has %d cells, want 4: %q", len(cells), line)
			continue
		}
		m := regexp.MustCompile(`^\[(D\d+)\]\(#([^)]+)\)$`).FindStringSubmatch(cells[0])
		if m == nil {
			t.Errorf("unparseable ID cell: %q", cells[0])
			continue
		}
		if _, dup := rows[m[1]]; dup {
			t.Errorf("duplicate table row for %s", m[1])
		}
		rows[m[1]] = []string{m[2], cells[1], cells[2], cells[3]}
	}

	for id, meta := range lint.RuleMeta {
		if id == "parse" {
			continue
		}
		row, ok := rows[id]
		if !ok {
			t.Errorf("docs/rules.md summary table is missing a row for %s (%s)", id, meta.Name)
			continue
		}
		if want := anchor(id); row[0] != want {
			t.Errorf("%s: table row anchor = %q, want %q", id, row[0], want)
		}
		if row[1] != meta.Name {
			t.Errorf("%s: table row name = %q, want %q", id, row[1], meta.Name)
		}
		if row[2] != "warning" && row[2] != "info" {
			t.Errorf("%s: table row severity = %q, want warning or info", id, row[2])
		}
		hasCheck := strings.Contains(row[3], "✅")
		if hasCheck != fixable[id] {
			t.Errorf("%s: table row --fix cell %q disagrees with lint.FixableRules (fixable=%v)", id, row[3], fixable[id])
		}
	}
	for id := range rows {
		if _, ok := lint.RuleMeta[id]; !ok {
			t.Errorf("docs/rules.md summary table has a row for unknown rule %s", id)
		}
	}
}
