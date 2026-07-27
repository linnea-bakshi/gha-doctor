package report

import (
	"strings"
	"testing"

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
