package lint

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// D017: nothing keeps the repo's action pins up to date. Workflow `uses:`
// pins only move when something moves them; without dependabot's
// github-actions ecosystem or a renovate config they rot in place until
// they hit a version GitHub has shut down (D015) or a retired runner
// image (D016). This is a repo-level check — the evidence lives outside
// .github/workflows — so it runs alongside the per-file rules, not as one
// of them, and is deliberately absent from SARIF output (there is no
// meaningful file to annotate when the finding is that a file is missing).

// dependabotPath is where the finding points when the config is absent.
const dependabotPath = ".github/dependabot.yml"

// renovateConfigNames are the config locations renovate looks for in a
// GitHub repo (root and .github/). A renovate config of any kind counts as
// covered: its github-actions manager is enabled by default.
var renovateConfigNames = []string{
	"renovate.json",
	"renovate.json5",
	".renovaterc",
	".renovaterc.json",
	".renovaterc.json5",
	".github/renovate.json",
	".github/renovate.json5",
}

// FindUpdateConfigLocal locates dependabot/renovate config under a local
// repo root. A missing dependabot file returns nil; a missing renovate
// config returns "".
func FindUpdateConfigLocal(root string) (dependabot *NamedFile, renovatePath string) {
	for _, name := range []string{".github/dependabot.yml", ".github/dependabot.yaml"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if data, err := os.ReadFile(p); err == nil {
			dependabot = &NamedFile{Path: name, Data: data}
			break
		}
	}
	for _, name := range renovateConfigNames {
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil && !fi.IsDir() {
			renovatePath = name
			break
		}
	}
	return dependabot, renovatePath
}

// CheckUpdateAutomation emits D017 when neither dependabot (github-actions
// ecosystem) nor renovate is set up to update action pins. dependabot is
// the repo's dependabot config if present (nil if absent); renovatePath is
// a renovate config path if present ("" if absent).
func CheckUpdateAutomation(dependabot *NamedFile, renovatePath string) []Finding {
	if renovatePath != "" {
		return nil // renovate updates github-actions by default
	}
	if dependabot == nil {
		return []Finding{{
			Rule: "D017", Severity: Info, SevStr: Info.String(),
			File: dependabotPath, Line: 1,
			Message: "no update automation for your actions: no dependabot github-actions ecosystem, no renovate config",
			Advice:  "action pins rot in place until they hit shut-down versions (D015/D016); add a .github/dependabot.yml with package-ecosystem: github-actions — see --explain D017",
		}}
	}
	line, covered, parsed := dependabotActionsEcosystem(dependabot.Data)
	if !parsed || covered {
		// Unparseable dependabot.yml gets the benefit of the doubt: this
		// rule is about missing automation, not about YAML syntax.
		return nil
	}
	f := Finding{
		Rule: "D017", Severity: Info, SevStr: Info.String(),
		File: dependabot.Path, Line: line,
		Message: "dependabot config doesn't include the github-actions ecosystem, so action pins in workflows never get update PRs",
		Advice:  "add `- package-ecosystem: github-actions` to updates: — see --explain D017",
	}
	if parseIgnores(dependabot.Data).matches(f.Line, f.Rule) {
		return nil
	}
	return []Finding{f}
}

// dependabotActionsEcosystem reports whether a dependabot config updates
// the github-actions ecosystem. line is the line of the updates: key (or 1)
// for attaching a finding; parsed is false when the YAML is unusable.
func dependabotActionsEcosystem(data []byte) (line int, covered, parsed bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return 1, false, false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return 1, false, false
	}
	line = 1
	if k := mapKey(doc, "updates"); k != nil {
		line = k.Line
	}
	updates := mapGet(doc, "updates")
	if updates == nil || updates.Kind != yaml.SequenceNode {
		return line, false, true
	}
	for _, u := range updates.Content {
		if u.Kind != yaml.MappingNode {
			continue
		}
		if eco := mapGet(u, "package-ecosystem"); eco != nil && strings.EqualFold(strings.TrimSpace(eco.Value), "github-actions") {
			return line, true, true
		}
	}
	return line, false, true
}
