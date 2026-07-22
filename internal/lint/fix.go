package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FixableRules lists the rules --fix knows how to repair.
var FixableRules = []string{"D001", "D002", "D003"}

// DefaultFixTimeout is the timeout-minutes value the D002 fix inserts.
// Deliberately generous: the point is to cap a hung job at well under the
// 360-minute default, not to guess your build's real duration.
const DefaultFixTimeout = 30

// edit is a surgical change to a file's lines. Insertions go before the
// 1-based line number; a replacement swaps the line's content entirely.
// Working on raw lines (guided by yaml.Node positions) preserves the
// user's comments and formatting, which a YAML re-marshal would destroy.
type edit struct {
	line    int
	insert  []string // inserted before `line` (when replace == "")
	replace string   // if non-empty, replaces line `line`
	rule    string
	note    string
}

// FixResult describes what happened to one file.
type FixResult struct {
	Path    string   `json:"path"`
	Applied []string `json:"applied,omitempty"` // human-readable, e.g. "D002: timeout-minutes: 30 on job `test`"
	Skipped []string `json:"skipped,omitempty"` // fixable rule matched but was unsafe to auto-edit
}

// FixDir applies autofixes to every workflow in wfDir. rootDir is the
// repository root, used to detect package managers for the D003 fix.
// Nothing is written unless the edited file still parses and the fixed
// findings are actually gone.
func FixDir(wfDir, rootDir string) ([]FixResult, error) {
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, err
	}
	pm := detectPackageManagers(rootDir)
	var results []FixResult
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(wfDir, name)
		res, err := fixFile(path, pm)
		if err != nil {
			return results, fmt.Errorf("%s: %w", path, err)
		}
		if len(res.Applied) > 0 || len(res.Skipped) > 0 {
			results = append(results, res)
		}
	}
	return results, nil
}

func fixFile(path string, pm map[string]string) (FixResult, error) {
	res := FixResult{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	w, err := parseWorkflow(path, data)
	if err != nil {
		return res, nil // parse findings are reported by lint; nothing to fix
	}
	lines := strings.Split(string(data), "\n")

	var edits []edit
	collect := func(es []edit, skips []string) {
		edits = append(edits, es...)
		res.Skipped = append(res.Skipped, skips...)
	}
	collect(fixConcurrency(w, lines))
	collect(fixTimeout(w))
	collect(fixSetupCache(w, pm))

	if len(edits) == 0 {
		return res, nil
	}

	fixed, notes := applyEdits(lines, edits)
	out := strings.Join(fixed, "\n")

	// Safety valve: never write a file that no longer parses, and never
	// claim a fix that didn't remove its finding.
	if _, err := parseWorkflow(path, []byte(out)); err != nil {
		return res, fmt.Errorf("fix produced invalid YAML (bug, nothing written): %w", err)
	}
	after, err := LintBytes(path, []byte(out))
	if err != nil {
		return res, fmt.Errorf("fix produced unlintable YAML (bug, nothing written): %w", err)
	}
	fixedRules := map[string]bool{}
	for _, e := range edits {
		fixedRules[e.rule] = true
	}
	before, _ := LintBytes(path, data)
	for r := range fixedRules {
		if countRule(after, r) >= countRule(before, r) {
			return res, fmt.Errorf("fix for %s did not reduce findings (bug, nothing written)", r)
		}
	}

	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return res, err
	}
	res.Applied = notes
	return res, nil
}

func countRule(fs []Finding, rule string) int {
	n := 0
	for _, f := range fs {
		if f.Rule == rule {
			n++
		}
	}
	return n
}

// applyEdits sorts edits bottom-up so line numbers stay valid, applies
// them, and returns the new lines plus human-readable notes in top-down
// order.
func applyEdits(lines []string, edits []edit) ([]string, []string) {
	sorted := make([]edit, len(edits))
	copy(sorted, edits)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].line > sorted[j].line })
	for _, e := range sorted {
		i := e.line - 1
		if i < 0 || i > len(lines) {
			continue
		}
		if e.replace != "" {
			if i < len(lines) {
				lines[i] = e.replace
			}
			continue
		}
		if i == len(lines) {
			lines = append(lines, e.insert...)
			continue
		}
		lines = append(lines[:i], append(append([]string{}, e.insert...), lines[i:]...)...)
	}
	notes := make([]string, 0, len(edits))
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].line < edits[j].line })
	for _, e := range edits {
		notes = append(notes, e.rule+": "+e.note)
	}
	return lines, notes
}

// ---- D001: concurrency ----

func fixConcurrency(w *Workflow, lines []string) ([]edit, []string) {
	trig, _ := w.triggers()
	_, pr := trig["pull_request"]
	_, prt := trig["pull_request_target"]
	if !pr && !prt {
		return nil, nil
	}
	concKey := mapKey(w.Doc, "concurrency")
	conc := mapGet(w.Doc, "concurrency")

	if conc == nil {
		jobsKey := mapKey(w.Doc, "jobs")
		if jobsKey == nil {
			return nil, nil
		}
		return []edit{{
			line: jobsKey.Line,
			insert: []string{
				"concurrency:",
				"  group: ${{ github.workflow }}-${{ github.ref }}",
				"  cancel-in-progress: true",
				"",
			},
			rule: "D001",
			note: "added concurrency group with cancel-in-progress: true",
		}}, nil
	}

	if conc.Kind != yaml.MappingNode {
		return nil, nil
	}
	// Flow style (`concurrency: { group: x }`) — line edits get hairy; skip.
	if concKey != nil && len(conc.Content) > 0 && conc.Content[0].Line == concKey.Line {
		return nil, []string{"D001: concurrency uses flow style; add cancel-in-progress: true by hand"}
	}
	cip := mapGet(conc, "cancel-in-progress")
	if cip == nil {
		if len(conc.Content) == 0 {
			return nil, nil
		}
		first := conc.Content[0]
		indent := strings.Repeat(" ", first.Column-1)
		return []edit{{
			line:   first.Line,
			insert: []string{indent + "cancel-in-progress: true"},
			rule:   "D001",
			note:   "added cancel-in-progress: true to existing concurrency group",
		}}, nil
	}
	if cip.Value == "false" && cip.Line-1 < len(lines) {
		orig := lines[cip.Line-1]
		if strings.Contains(orig, "cancel-in-progress") && strings.Contains(orig, "false") {
			return []edit{{
				line:    cip.Line,
				replace: strings.Replace(orig, "false", "true", 1),
				rule:    "D001",
				note:    "flipped cancel-in-progress: false -> true",
			}}, nil
		}
	}
	return nil, nil
}

// ---- D002: timeout-minutes ----

func fixTimeout(w *Workflow) ([]edit, []string) {
	var edits []edit
	w.jobs(func(id string, key, job *yaml.Node) {
		if mapGet(job, "uses") != nil || mapGet(job, "timeout-minutes") != nil {
			return
		}
		if job.Kind != yaml.MappingNode || len(job.Content) == 0 {
			return
		}
		first := job.Content[0]
		if first.Line == key.Line { // flow-style job; skip
			return
		}
		indent := strings.Repeat(" ", first.Column-1)
		edits = append(edits, edit{
			line:   first.Line,
			insert: []string{fmt.Sprintf("%stimeout-minutes: %d", indent, DefaultFixTimeout)},
			rule:   "D002",
			note:   fmt.Sprintf("set timeout-minutes: %d on job `%s`", DefaultFixTimeout, id),
		})
	})
	return edits, nil
}

// ---- D003: setup-* cache input ----

var setupCacheActions = map[string]string{
	"actions/setup-node":   "node",
	"actions/setup-python": "python",
	"actions/setup-java":   "java",
}

// detectPackageManagers inspects the repo root for lockfiles/build files and
// returns ecosystem -> cache value. Ambiguous ecosystems (two lockfiles) are
// omitted: guessing wrong would silently cache the wrong directory.
func detectPackageManagers(root string) map[string]string {
	has := func(names ...string) []string {
		var found []string
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(root, n)); err == nil {
				found = append(found, n)
			}
		}
		return found
	}
	pick := func(candidates map[string]string, order []string) string {
		var hits []string
		for _, o := range order {
			if len(has(o)) > 0 {
				hits = append(hits, candidates[o])
			}
		}
		uniq := map[string]bool{}
		for _, h := range hits {
			uniq[h] = true
		}
		if len(uniq) == 1 {
			return hits[0]
		}
		return "" // none, or ambiguous
	}
	out := map[string]string{}
	if v := pick(map[string]string{
		"pnpm-lock.yaml":    "pnpm",
		"yarn.lock":         "yarn",
		"package-lock.json": "npm",
	}, []string{"pnpm-lock.yaml", "yarn.lock", "package-lock.json"}); v != "" {
		out["node"] = v
	}
	if v := pick(map[string]string{
		"poetry.lock":      "poetry",
		"Pipfile.lock":     "pipenv",
		"requirements.txt": "pip",
	}, []string{"poetry.lock", "Pipfile.lock", "requirements.txt"}); v != "" {
		out["python"] = v
	}
	if v := pick(map[string]string{
		"pom.xml":          "maven",
		"build.gradle":     "gradle",
		"build.gradle.kts": "gradle",
		"build.sbt":        "sbt",
	}, []string{"pom.xml", "build.gradle", "build.gradle.kts", "build.sbt"}); v != "" {
		out["java"] = v
	}
	return out
}

func fixSetupCache(w *Workflow, pm map[string]string) ([]edit, []string) {
	var edits []edit
	var skips []string
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			for action, eco := range setupCacheActions {
				if !usesAction(step, action) {
					continue
				}
				with := mapGet(step, "with")
				if mapGet(with, "cache") != nil {
					continue
				}
				cacheVal, ok := pm[eco]
				if !ok {
					skips = append(skips,
						fmt.Sprintf("D003: %s in job `%s` — no (or ambiguous) lockfile found, can't pick a cache value", action, id))
					continue
				}
				usesKey := mapKey(step, "uses")
				usesVal := mapGet(step, "uses")
				if usesKey == nil || usesVal == nil || usesVal.Line != usesKey.Line {
					continue // multi-line uses value; skip
				}
				if with != nil {
					if with.Kind != yaml.MappingNode || len(with.Content) == 0 {
						continue
					}
					withKey := mapKey(step, "with")
					first := with.Content[0]
					if withKey != nil && first.Line == withKey.Line {
						skips = append(skips,
							fmt.Sprintf("D003: %s in job `%s` uses flow-style `with:`; add cache: %s by hand", action, id, cacheVal))
						continue
					}
					indent := strings.Repeat(" ", first.Column-1)
					edits = append(edits, edit{
						line:   first.Line,
						insert: []string{indent + "cache: " + cacheVal},
						rule:   "D003",
						note:   fmt.Sprintf("added cache: %s to %s in job `%s`", cacheVal, action, id),
					})
				} else {
					indent := strings.Repeat(" ", usesKey.Column-1)
					edits = append(edits, edit{
						line:   usesKey.Line + 1,
						insert: []string{indent + "with:", indent + "  cache: " + cacheVal},
						rule:   "D003",
						note:   fmt.Sprintf("added with: cache: %s to %s in job `%s`", cacheVal, action, id),
					})
				}
			}
		})
	})
	return edits, skips
}
