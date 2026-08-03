package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecipesLintClean extracts every workflow snippet from docs/recipes.md
// and lints it with our own rules. The page promises "every workflow on this
// page lints clean under gha-doctor itself" — this test is that promise, and
// it also means a new rule that would flag one of our published examples
// fails the build instead of shipping an embarrassment (same contract as
// TestInitWorkflowLintsClean for the --init scaffold).
func TestRecipesLintClean(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "recipes.md"))
	if err != nil {
		t.Fatalf("read docs/recipes.md: %v", err)
	}

	workflows := 0
	for i, block := range fencedYAMLBlocks(string(raw)) {
		if !strings.Contains(block, "\njobs:") {
			continue // pre-commit config etc., not a workflow
		}
		workflows++
		name := fmt.Sprintf(".github/workflows/recipe-%d.yml", i)
		findings, err := LintBytes(name, []byte(block))
		if err != nil {
			t.Errorf("recipe snippet %d does not parse: %v\n%s", i, err, block)
			continue
		}
		for _, f := range findings {
			t.Errorf("recipe snippet %d must lint clean, got %s at line %d: %s\n%s",
				i, f.Rule, f.Line, f.Message, block)
		}
	}

	// Guard the extraction itself: if the fence parsing rots, the loop above
	// would vacuously pass.
	if workflows < 5 {
		t.Fatalf("expected at least 5 workflow snippets in docs/recipes.md, found %d — extraction broken or page gutted?", workflows)
	}
}

// fencedYAMLBlocks returns the contents of every ```yaml fenced code block.
func fencedYAMLBlocks(md string) []string {
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(md, "\n") {
		switch {
		case !in && strings.TrimSpace(line) == "```yaml":
			in = true
			cur = cur[:0]
		case in && strings.TrimSpace(line) == "```":
			in = false
			blocks = append(blocks, strings.Join(cur, "\n")+"\n")
		case in:
			cur = append(cur, line)
		}
	}
	return blocks
}
