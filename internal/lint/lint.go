// Package lint implements static performance/cost/reliability checks for
// GitHub Actions workflow files.
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Severity of a finding.
type Severity int

const (
	Info Severity = iota
	Warn
)

func (s Severity) String() string {
	if s == Warn {
		return "warning"
	}
	return "info"
}

// Finding is a single diagnostic emitted by a rule.
type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"-"`
	SevStr   string   `json:"severity"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Message  string   `json:"message"`
	Advice   string   `json:"advice,omitempty"`
}

// Workflow is a parsed workflow file plus its raw YAML document node.
type Workflow struct {
	Path string
	Doc  *yaml.Node // mapping node of the document
}

// Rule analyzes one workflow and returns findings.
type Rule func(w *Workflow) []Finding

// LintDir lints all workflow files under dir (a .github/workflows directory).
func LintDir(dir string) ([]Finding, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var findings []Finding
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(dir, name)
		fs, err := LintFile(path)
		if err != nil {
			findings = append(findings, Finding{
				Rule: "parse", Severity: Warn, SevStr: "warning", File: path, Line: 1,
				Message: fmt.Sprintf("could not parse: %v", err),
			})
			continue
		}
		n++
		findings = append(findings, fs...)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, n, nil
}

// LintFile lints a single workflow file.
func LintFile(path string) ([]Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LintBytes(path, data)
}

// parseWorkflow parses workflow YAML into a Workflow.
func parseWorkflow(path string, data []byte) (*Workflow, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, fmt.Errorf("empty document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("top level is not a mapping")
	}
	return &Workflow{Path: path, Doc: doc}, nil
}

// LintBytes lints workflow YAML content.
func LintBytes(path string, data []byte) ([]Finding, error) {
	w, err := parseWorkflow(path, data)
	if err != nil {
		if err.Error() == "empty document" {
			return nil, nil
		}
		return nil, err
	}
	ign := parseIgnores(data)
	var findings []Finding
	for _, r := range AllRules {
		for _, f := range r(w) {
			if ign.matches(f.Line, f.Rule) {
				continue
			}
			f.File = path
			f.SevStr = f.Severity.String()
			findings = append(findings, f)
		}
	}
	return findings, nil
}

// ---- yaml.Node helpers ----

// mapGet returns the value node for key in a mapping node, or nil.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapKey returns the key node itself (useful for line numbers).
func mapKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i]
		}
	}
	return nil
}

// jobs iterates over the jobs mapping: yields (jobID keyNode, job valueNode).
func (w *Workflow) jobs(fn func(id string, key, job *yaml.Node)) {
	jobs := mapGet(w.Doc, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		fn(jobs.Content[i].Value, jobs.Content[i], jobs.Content[i+1])
	}
}

// steps iterates over the steps of a job.
func jobSteps(job *yaml.Node, fn func(step *yaml.Node)) {
	steps := mapGet(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return
	}
	for _, s := range steps.Content {
		if s.Kind == yaml.MappingNode {
			fn(s)
		}
	}
}

// triggers returns the set of event names the workflow listens to, and the
// value node of the `on:` entry.
func (w *Workflow) triggers() (map[string]*yaml.Node, *yaml.Node) {
	// YAML 1.1 parses bare `on` as boolean true; yaml.v3 keeps the literal
	// string in Value, so look for both.
	var on *yaml.Node
	for _, k := range []string{"on", "true"} {
		if v := mapGet(w.Doc, k); v != nil {
			on = v
			break
		}
	}
	out := map[string]*yaml.Node{}
	if on == nil {
		return out, nil
	}
	switch on.Kind {
	case yaml.ScalarNode:
		out[on.Value] = on
	case yaml.SequenceNode:
		for _, c := range on.Content {
			out[c.Value] = c
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			out[on.Content[i].Value] = on.Content[i+1]
		}
	}
	return out, on
}

// usesAction reports whether a step's `uses:` matches the given action repo
// (ignoring the @version suffix), e.g. "actions/setup-node".
func usesAction(step *yaml.Node, action string) bool {
	u := mapGet(step, "uses")
	if u == nil {
		return false
	}
	name := u.Value
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	return strings.EqualFold(name, action)
}
