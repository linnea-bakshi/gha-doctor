package lint

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FixableRules lists the rules --fix knows how to repair.
// D004 (fetch-depth: 0) is deliberately not auto-fixed: whether a job needs
// full history is a semantic question the linter can't answer.
var FixableRules = []string{"D001", "D002", "D003", "D008", "D012", "D014", "D015", "D018"}

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
	// findingLine is the line the corresponding lint finding points at,
	// so inline `# gha-doctor: ignore` directives suppress the fix too.
	findingLine int
}

// FixResult describes what happened to one file.
type FixResult struct {
	Path    string   `json:"path"`
	Applied []string `json:"applied,omitempty"` // human-readable, e.g. "D002: timeout-minutes: 30 on job `test`"
	Skipped []string `json:"skipped,omitempty"` // fixable rule matched but was unsafe to auto-edit
	Failed  string   `json:"failed,omitempty"`  // safety valve fired or file unreadable; nothing was written
}

// FixDir applies autofixes to every workflow in wfDir. rootDir is the
// repository root, used to detect package managers for the D003 fix.
// Nothing is written unless the edited file still parses and the fixed
// findings are actually gone.
func FixDir(wfDir, rootDir string, disabled []string) ([]FixResult, error) {
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, err
	}
	pm := detectPackageManagers(rootDir)
	off := map[string]bool{}
	for _, r := range disabled {
		off[strings.ToUpper(strings.TrimSpace(r))] = true
	}
	var results []FixResult
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(wfDir, name)
		res, err := fixFile(path, pm, off)
		if err != nil {
			// One stubborn file must not block fixes for the rest of the
			// repo: record the failure (nothing was written) and move on.
			res.Path = path
			res.Failed = err.Error()
		}
		if len(res.Applied) > 0 || len(res.Skipped) > 0 || res.Failed != "" {
			results = append(results, res)
		}
	}
	return results, nil
}

// FixPreview is a dry-run fix for one file: the FixResult that --fix would
// report, plus the original and fixed content (Fixed is nil when no edit
// applies). Nothing is written to disk.
type FixPreview struct {
	FixResult
	Original []byte
	Fixed    []byte
}

// PreviewDir computes the fixes FixDir would apply, without writing anything.
func PreviewDir(wfDir, rootDir string, disabled []string) ([]FixPreview, error) {
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, err
	}
	pm := detectPackageManagers(rootDir)
	var files []NamedFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(wfDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			files = append(files, NamedFile{Path: path, Data: nil})
			continue
		}
		files = append(files, NamedFile{Path: path, Data: data})
	}
	return PreviewFiles(files, pm, disabled), nil
}

// PreviewFiles computes fixes for in-memory workflow files (e.g. fetched from
// a remote repo). pm is as for FixBytes; disabled lists rule IDs to skip.
func PreviewFiles(files []NamedFile, pm map[string]string, disabled []string) []FixPreview {
	off := map[string]bool{}
	for _, r := range disabled {
		off[strings.ToUpper(strings.TrimSpace(r))] = true
	}
	var previews []FixPreview
	for _, f := range files {
		if f.Data == nil {
			previews = append(previews, FixPreview{FixResult: FixResult{Path: f.Path, Failed: "unreadable"}})
			continue
		}
		out, res, err := FixBytes(f.Path, f.Data, pm, off)
		if err != nil {
			res.Applied = nil
			res.Failed = err.Error()
		}
		p := FixPreview{FixResult: res, Original: f.Data, Fixed: out}
		if len(p.Applied) > 0 || len(p.Skipped) > 0 || p.Failed != "" {
			previews = append(previews, p)
		}
	}
	return previews
}

func fixFile(path string, pm map[string]string, disabled map[string]bool) (FixResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FixResult{Path: path}, err
	}
	out, res, err := FixBytes(path, data, pm, disabled)
	if err != nil || out == nil {
		return res, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		res.Applied = nil
		return res, err
	}
	return res, nil
}

// FixBytes applies autofixes to one workflow's content in memory. It returns
// the fixed content (nil when nothing changed), the FixResult describing what
// was applied or skipped, and an error only when a produced fix failed the
// safety valve (invalid YAML or findings not reduced — a bug, nothing usable
// returned). pm maps ecosystems to lockfile names for the D003 fix (see
// detectPackageManagers); disabled holds upper-cased rule IDs to skip.
func FixBytes(path string, data []byte, pm map[string]string, disabled map[string]bool) ([]byte, FixResult, error) {
	res := FixResult{Path: path}
	w, err := parseWorkflow(path, data)
	if err != nil {
		return nil, res, nil // parse findings are reported by lint; nothing to fix
	}
	// Preserve the file's line endings: a fully-CRLF (Windows-authored) file
	// gets CRLF on inserted lines too, instead of a mixed-EOL result. Edits
	// run in LF space; the original EOL is restored on join. Mixed-EOL input
	// is left exactly as found.
	src := string(data)
	eol := "\n"
	if n := strings.Count(src, "\n"); n > 0 && strings.Count(src, "\r\n") == n {
		eol = "\r\n"
		src = strings.ReplaceAll(src, "\r\n", "\n")
	}
	// An isolated \r (not part of \r\n) is a line break to the YAML parser
	// but not to our \n-split line array, so every node position past it
	// points at the wrong text line — an edit could land anywhere. Refuse
	// loudly instead of guessing (found by fuzzing). Mixed CRLF/LF files
	// are fine: every \r still pairs with a \n, so line counts agree.
	if hasLoneCR(src) {
		res.Skipped = append(res.Skipped,
			"file contains isolated carriage returns (\\r not followed by \\n), so line numbers are ambiguous — fix by hand")
		return nil, res, nil
	}
	lines := strings.Split(src, "\n")

	var edits []edit
	collect := func(es []edit, skips []string) {
		edits = append(edits, es...)
		res.Skipped = append(res.Skipped, skips...)
	}
	collect(fixConcurrency(w, lines))
	collect(fixTimeout(w, lines))
	collect(fixSetupCache(w, pm, lines))
	collect(fixRestoreKeys(w, lines))
	collect(fixNpmInstall(w, lines))
	collect(fixCronMinute(w, lines))
	collect(fixRetiredCache(w, lines))
	collect(fixDeprecatedCommands(w, lines))

	// Respect --disable and inline ignore directives: a suppressed finding
	// must not be "fixed" behind the user's back.
	ign := parseIgnores(data)
	kept := edits[:0]
	for _, e := range edits {
		if disabled[e.rule] || ign.matches(e.findingLine, e.rule) {
			continue
		}
		kept = append(kept, e)
	}
	edits = kept

	// Structural drift guard: every planned edit must correspond to a real
	// finding of its rule at the exact line the fixer claims. A fixer whose
	// trigger condition drifts from its rule (it has happened) now degrades
	// to a loud per-edit skip instead of editing something the rule never
	// flagged — and because findingLine also drives inline-ignore
	// suppression, this doubles as a continuous check that it is accurate.
	before, _ := LintBytes(path, data)
	var driftSkips []string
	edits, driftSkips = dropDriftedEdits(edits, before)
	res.Skipped = append(res.Skipped, driftSkips...)

	if len(edits) == 0 {
		return nil, res, nil
	}

	fixed, notes := applyEdits(lines, edits)
	out := strings.Join(fixed, eol)

	// Safety valve: never emit content that no longer parses, and never
	// claim a fix that didn't remove its finding.
	if _, err := parseWorkflow(path, []byte(out)); err != nil {
		return nil, res, fmt.Errorf("fix produced invalid YAML (bug, nothing written): %w", err)
	}
	after, err := LintBytes(path, []byte(out))
	if err != nil {
		return nil, res, fmt.Errorf("fix produced unlintable YAML (bug, nothing written): %w", err)
	}
	fixedRules := map[string]bool{}
	for _, e := range edits {
		fixedRules[e.rule] = true
	}
	for r := range fixedRules {
		if countRule(after, r) >= countRule(before, r) {
			return nil, res, fmt.Errorf("fix for %s did not reduce findings (bug, nothing written)", r)
		}
	}

	res.Applied = notes
	return []byte(out), res, nil
}

// insertIndent returns the indentation to use for a new sibling line
// inserted next to a node that starts at (line, col), both 1-based. It
// returns ok=false when the text before the node on its own line is
// anything but spaces or block-sequence dashes — e.g. an explicit-key
// `?` entry, where a column-derived indent breaks the YAML (found by
// fuzzing: a job body of `?` made the D002 insert invalid).
func insertIndent(lines []string, line, col int) (string, bool) {
	ind := strings.Repeat(" ", col-1)
	if line-1 >= len(lines) || len(lines[line-1]) < col-1 {
		return ind, true
	}
	if strings.Trim(lines[line-1][:col-1], " -") != "" {
		return "", false
	}
	// The `?` explicit-key marker may sit alone on the line ABOVE the
	// node (yaml.v3 reports the key node at its value's position, so the
	// same-line prefix check above never sees it). Inserting a sibling
	// next to such a node lands inside the explicit key (fuzz crasher:
	// "jobs:\n 0:\n  ?\n   0"). A `? ` prefix likewise means the node
	// line continues a multi-line explicit key.
	for i := line - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t == "?" || strings.HasPrefix(t, "? ") {
			return "", false
		}
		break
	}
	return ind, true
}

// runSpan returns the 1-based inclusive line range holding a run
// scalar's text. yaml.v3 sets run.Line to the `run: |` HEADER line for
// literal/folded block scalars (content occupies exactly the next N
// value lines) but to the value's OWN line for plain/quoted scalars.
// A style-blind "+1 slack" span overshoots into the next step, whose
// line can hold the same fixable pattern and corrupt edit bookkeeping
// (caught live while building D018; D012 had the same latent bug).
func runSpan(run *yaml.Node) (start, end int) {
	if run.Style == yaml.LiteralStyle || run.Style == yaml.FoldedStyle {
		start = run.Line + 1
		end = run.Line + strings.Count(strings.TrimSuffix(run.Value, "\n"), "\n") + 1
		return start, end
	}
	return run.Line, run.Line + strings.Count(run.Value, "\n")
}

// hasLoneCR reports whether s contains a carriage return that is not
// immediately followed by a line feed. YAML counts such a \r as a line
// break; a \n-based line array does not, so positions diverge.
func hasLoneCR(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' && (i+1 >= len(s) || s[i+1] != '\n') {
			return true
		}
	}
	return false
}

// dropDriftedEdits keeps only edits whose (rule, findingLine) matches an
// actual finding, returning skip notes for the rest. Kept separate from
// FixBytes so the drift path — which no correct fixer can reach — stays
// directly testable.
func dropDriftedEdits(edits []edit, findings []Finding) ([]edit, []string) {
	have := map[string]bool{}
	for _, f := range findings {
		have[f.Rule+"@"+strconv.Itoa(f.Line)] = true
	}
	kept := edits[:0]
	var skips []string
	for _, e := range edits {
		if !have[e.rule+"@"+strconv.Itoa(e.findingLine)] {
			skips = append(skips, fmt.Sprintf(
				"%s: planned a fix at line %d but the rule reports no finding there — skipped (fixer/rule drift; please report this)",
				e.rule, e.findingLine))
			continue
		}
		kept = append(kept, e)
	}
	return kept, skips
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
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].line != sorted[j].line {
			return sorted[i].line > sorted[j].line
		}
		// Same line: apply the replacement while the index still points at
		// the original content, then insert above it. Otherwise the replace
		// would clobber the just-inserted line.
		return sorted[i].replace != "" && sorted[j].replace == ""
	})
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
	trig, on := w.triggers()
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
		fl := 1
		if on != nil {
			fl = on.Line
		}
		return []edit{{
			findingLine: fl,
			line:        jobsKey.Line,
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
		indent, ok := insertIndent(lines, first.Line, first.Column)
		if !ok {
			return nil, []string{"D001: concurrency block uses explicit-key YAML syntax; add cancel-in-progress: true by hand"}
		}
		return []edit{{
			findingLine: conc.Line,
			line:        first.Line,
			insert:      []string{indent + "cancel-in-progress: true"},
			rule:        "D001",
			note:        "added cancel-in-progress: true to existing concurrency group",
		}}, nil
	}
	if cip.Value == "false" && cip.Line-1 < len(lines) {
		orig := lines[cip.Line-1]
		if strings.Contains(orig, "cancel-in-progress") && strings.Contains(orig, "false") {
			return []edit{{
				findingLine: conc.Line,
				line:        cip.Line,
				replace:     strings.Replace(orig, "false", "true", 1),
				rule:        "D001",
				note:        "flipped cancel-in-progress: false -> true",
			}}, nil
		}
	}
	return nil, nil
}

// ---- D002: timeout-minutes ----

func fixTimeout(w *Workflow, lines []string) ([]edit, []string) {
	var edits []edit
	var skips []string
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
		// Explicit-key syntax (`? job name` / `: runs-on: ...`) puts the
		// value on its own line; inserting between them breaks the YAML.
		if key.Line-1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[key.Line-1]), "?") {
			skips = append(skips, fmt.Sprintf(
				"D002: job `%s` uses explicit-key YAML syntax; add timeout-minutes by hand", id))
			return
		}
		indent, ok := insertIndent(lines, first.Line, first.Column)
		if !ok {
			skips = append(skips, fmt.Sprintf(
				"D002: job `%s` body starts with explicit-key YAML syntax; add timeout-minutes by hand", id))
			return
		}
		edits = append(edits, edit{
			findingLine: key.Line,
			line:        first.Line,
			insert:      []string{fmt.Sprintf("%stimeout-minutes: %d", indent, DefaultFixTimeout)},
			rule:        "D002",
			note:        fmt.Sprintf("set timeout-minutes: %d on job `%s`", DefaultFixTimeout, id),
		})
	})
	return edits, skips
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
	return detectPackageManagersExists(func(n string) bool {
		_, err := os.Stat(filepath.Join(root, n))
		return err == nil
	})
}

// DetectPackageManagersFromList is detectPackageManagers for a repo whose
// root file listing came from an API call instead of the local filesystem
// (remote --diff previews).
func DetectPackageManagersFromList(names []string) map[string]string {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return detectPackageManagersExists(func(n string) bool { return set[n] })
}

func detectPackageManagersExists(exists func(string) bool) map[string]string {
	has := func(names ...string) []string {
		var found []string
		for _, n := range names {
			if exists(n) {
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

func fixSetupCache(w *Workflow, pm map[string]string, lines []string) ([]edit, []string) {
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
					indent, ok := insertIndent(lines, first.Line, first.Column)
					if !ok {
						skips = append(skips,
							fmt.Sprintf("D003: %s in job `%s` uses explicit-key YAML in `with:`; add cache: %s by hand", action, id, cacheVal))
						continue
					}
					edits = append(edits, edit{
						findingLine: usesVal.Line,
						line:        first.Line,
						insert:      []string{indent + "cache: " + cacheVal},
						rule:        "D003",
						note:        fmt.Sprintf("added cache: %s to %s in job `%s`", cacheVal, action, id),
					})
				} else {
					indent, ok := insertIndent(lines, usesKey.Line, usesKey.Column)
					if !ok {
						skips = append(skips,
							fmt.Sprintf("D003: %s in job `%s` uses explicit-key YAML; add with: cache: %s by hand", action, id, cacheVal))
						continue
					}
					edits = append(edits, edit{
						findingLine: usesVal.Line,
						line:        usesKey.Line + 1,
						insert:      []string{indent + "with:", indent + "  cache: " + cacheVal},
						rule:        "D003",
						note:        fmt.Sprintf("added with: cache: %s to %s in job `%s`", cacheVal, action, id),
					})
				}
			}
		})
	})
	return edits, skips
}

// ---- D008: restore-keys ----

// fixRestoreKeys adds a restore-keys prefix to actions/cache steps whose key
// ends in a ${{ hashFiles(...) }} expression — the one case where a safe
// prefix can be derived mechanically (everything before the hash).
func fixRestoreKeys(w *Workflow, lines []string) ([]edit, []string) {
	var edits []edit
	var skips []string
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			if !usesAction(step, "actions/cache") {
				return
			}
			with := mapGet(step, "with")
			if with == nil || mapGet(with, "restore-keys") != nil {
				return
			}
			if with.Kind != yaml.MappingNode || len(with.Content) == 0 {
				return
			}
			withKey := mapKey(step, "with")
			if withKey != nil && with.Content[0].Line == withKey.Line {
				skips = append(skips, fmt.Sprintf(
					"D008: actions/cache in job `%s` uses flow-style `with:`; add restore-keys by hand", id))
				return
			}
			keyKey := mapKey(with, "key")
			keyVal := mapGet(with, "key")
			if keyKey == nil || keyVal == nil {
				return
			}
			if keyVal.Line != keyKey.Line || strings.Contains(keyVal.Value, "\n") {
				skips = append(skips, fmt.Sprintf(
					"D008: actions/cache in job `%s` has a multi-line `key:`; add restore-keys by hand", id))
				return
			}
			prefix, ok := restoreKeyPrefix(keyVal.Value)
			if !ok {
				skips = append(skips, fmt.Sprintf(
					"D008: actions/cache key in job `%s` doesn't end in ${{ hashFiles(...) }}; can't derive a safe restore-keys prefix", id))
				return
			}
			indent, ok := insertIndent(lines, keyKey.Line, keyKey.Column)
			if !ok {
				skips = append(skips, fmt.Sprintf(
					"D008: actions/cache step in job `%s` uses explicit-key YAML; add restore-keys by hand", id))
				return
			}
			fl := keyKey.Line
			if u := mapGet(step, "uses"); u != nil {
				fl = u.Line
			}
			edits = append(edits, edit{
				findingLine: fl,
				line:        keyKey.Line + 1,
				insert: []string{
					indent + "restore-keys: |",
					indent + "  " + prefix,
				},
				rule: "D008",
				note: fmt.Sprintf("added restore-keys prefix `%s` to actions/cache in job `%s`", prefix, id),
			})
		})
	})
	return edits, skips
}

// restoreKeyPrefix strips a trailing ${{ hashFiles(...) }} expression from a
// cache key, returning the prefix to use as a restore key.
func restoreKeyPrefix(key string) (string, bool) {
	v := strings.TrimSpace(key)
	if !strings.HasSuffix(v, "}}") {
		return "", false
	}
	idx := strings.LastIndex(v, "${{")
	if idx <= 0 || !strings.Contains(v[idx:], "hashFiles") {
		return "", false
	}
	prefix := strings.TrimRight(v[:idx], " ")
	if prefix == "" {
		return "", false
	}
	return prefix, true
}

// ---- D012: npm install -> npm ci ----

// fixNpmInstall rewrites bare `npm install` lines to `npm ci`. Lines with
// arguments (`npm install <pkg>` / flags) are left alone: npm ci takes no
// package arguments, so a mechanical rewrite could change behavior.
func fixNpmInstall(w *Workflow, lines []string) ([]edit, []string) {
	var edits []edit
	var skips []string
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			run := mapGet(step, "run")
			if run == nil {
				return
			}
			var bare, withArgs bool
			for _, l := range strings.Split(run.Value, "\n") {
				t := strings.TrimSpace(l)
				switch {
				case t == "npm install":
					bare = true
				case strings.HasPrefix(t, "npm install ") &&
					!strings.Contains(t, "-g") && !strings.Contains(t, "--global"):
					withArgs = true
				}
			}
			if !bare && !withArgs {
				return
			}
			if withArgs {
				skips = append(skips, fmt.Sprintf(
					"D012: `npm install <args>` in job `%s` — npm ci takes no package args; switch by hand", id))
				return
			}
			// Locate the raw line(s). runSpan is style-aware, so the scan
			// can't reach into the following step (whose line could hold
			// another `npm install` and produce a duplicate edit).
			found := false
			start, end := runSpan(run)
			for ln := start; ln <= end && ln <= len(lines); ln++ {
				orig := lines[ln-1]
				t := strings.TrimSpace(orig)
				if t == "npm install" || t == "run: npm install" || t == "- run: npm install" {
					edits = append(edits, edit{
						findingLine: run.Line,
						line:        ln,
						replace:     strings.Replace(orig, "npm install", "npm ci", 1),
						rule:        "D012",
						note:        fmt.Sprintf("replaced `npm install` with `npm ci` in job `%s`", id),
					})
					found = true
				}
			}
			if !found {
				skips = append(skips, fmt.Sprintf(
					"D012: couldn't locate the `npm install` line in job `%s` (quoted or folded scalar); edit by hand", id))
			}
		})
	})
	return edits, skips
}

// ---- D014: top-of-hour cron ----

// fixCronMinute moves minute-0 crons to a deterministic non-zero minute
// derived from the workflow filename and the expression, so the cadence
// is unchanged, the fix is idempotent, and different workflows scatter
// across the hour instead of piling onto :00.
func fixCronMinute(w *Workflow, lines []string) ([]edit, []string) {
	trig, _ := w.triggers()
	sched, ok := trig["schedule"]
	if !ok || sched == nil || sched.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var edits []edit
	var skips []string
	for i, item := range sched.Content {
		cron := mapGet(item, "cron")
		if cron == nil {
			continue
		}
		fields := strings.Fields(cron.Value)
		if len(fields) != 5 || fields[0] != "0" {
			continue
		}
		li := cron.Line - 1
		if li < 0 || li >= len(lines) || !strings.Contains(lines[li], cron.Value) {
			skips = append(skips, fmt.Sprintf(
				"D014: couldn't locate cron `%s` on its line in %s (folded or multi-line scalar); edit by hand",
				cron.Value, filepath.Base(w.Path)))
			continue
		}
		minute := scatterMinute(filepath.Base(w.Path), cron.Value, i)
		fields[0] = strconv.Itoa(minute)
		newExpr := strings.Join(fields, " ")
		edits = append(edits, edit{
			line:        cron.Line,
			replace:     strings.Replace(lines[li], cron.Value, newExpr, 1),
			rule:        "D014",
			note:        fmt.Sprintf("moved cron `%s` to `%s` (same cadence, off the :00 peak)", cron.Value, newExpr),
			findingLine: cron.Line,
		})
	}
	return edits, skips
}

// scatterMinute maps a workflow schedule to a stable minute in 1..59.
// Hashing the filename and expression means the choice never changes from
// run to run, but different workflows (and different crons in the same
// file) land on different minutes.
func scatterMinute(name, expr string, i int) int {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|%s|%d", name, expr, i)
	return int(h.Sum32()%59) + 1
}

// ---- D015: retired cache action ----

// fixRetiredCache bumps actions/cache@v1|v2 (and its restore/save
// subpaths) to @v4: the inputs (path, key, restore-keys) are unchanged
// across those majors, so the rewrite is mechanical. The artifact actions
// are deliberately NOT auto-fixed: v4 changed semantics (uploads to the
// same artifact name across matrix jobs fail, v3/v4 artifacts are not
// cross-compatible), so a mechanical bump could trade a loud failure for
// a quiet wrong result — those get a skip note instead.
func fixRetiredCache(w *Workflow, lines []string) ([]edit, []string) {
	var edits []edit
	var skips []string
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			u := mapGet(step, "uses")
			if u == nil {
				return
			}
			at := strings.IndexByte(u.Value, '@')
			if at < 0 {
				return
			}
			name, ref := u.Value[:at], u.Value[at+1:]
			m := refMajor(ref)
			// Only touch refs the rule actually flags: resolving through the
			// same table keeps fix and finding in lockstep (an edit without a
			// matching finding trips the safety valve — see retiredActionFor).
			ra := retiredActionFor(name)
			if ra == nil || m < 0 || !ra.majors[m] {
				return
			}
			switch strings.ToLower(name) {
			case "actions/cache", "actions/cache/restore", "actions/cache/save":
				if u.Line > len(lines) {
					return
				}
				orig := lines[u.Line-1]
				old := name + "@" + ref
				if !strings.Contains(orig, old) {
					skips = append(skips, fmt.Sprintf(
						"D015: couldn't locate `%s` on its line in job `%s` (quoted or folded?); edit by hand", old, id))
					return
				}
				edits = append(edits, edit{
					findingLine: u.Line,
					line:        u.Line,
					replace:     strings.Replace(orig, old, name+"@v4", 1),
					rule:        "D015",
					note:        fmt.Sprintf("bumped `%s` to %s@v4 in job `%s` (same inputs; old majors were shut down March 2025)", old, name, id),
				})
			case "actions/upload-artifact", "actions/download-artifact":
				skips = append(skips, fmt.Sprintf(
					"D015: `%s` in job `%s` must be updated by hand — v4 changes artifact semantics (same-name uploads across matrix jobs fail; v3/v4 artifacts aren't cross-compatible)", u.Value, id))
			}
		})
	})
	return edits, skips
}

// ---- D018: deprecated workflow commands -> environment files ----

// stepBashLike reports whether a step's run script executes under a
// bash-compatible shell, walking step shell -> job defaults -> workflow
// defaults -> the runner's default (bash everywhere except Windows,
// where it is pwsh). Returns ok=false with a reason when the shell is
// something else or cannot be determined.
func stepBashLike(w *Workflow, job, step *yaml.Node) (bool, string) {
	shellOf := func(scope *yaml.Node) string {
		d := mapGet(scope, "defaults")
		r := mapGet(d, "run")
		s := mapGet(r, "shell")
		if s != nil {
			return s.Value
		}
		return ""
	}
	shell := ""
	if s := mapGet(step, "shell"); s != nil {
		shell = s.Value
	}
	if shell == "" {
		shell = shellOf(job)
	}
	if shell == "" {
		shell = shellOf(w.Doc)
	}
	if shell != "" {
		f := strings.Fields(strings.ToLower(shell))
		if len(f) == 0 {
			return false, "empty shell value"
		}
		switch f[0] {
		case "bash", "sh":
			return true, ""
		default:
			return false, "shell is `" + f[0] + "`"
		}
	}
	// No explicit shell: the runner decides. Windows defaults to pwsh.
	var labels []string
	ro := mapGet(job, "runs-on")
	collect := func(n *yaml.Node) {
		if k := matrixKeyRef(n.Value); k != "" {
			matrixValues(job, k, func(v *yaml.Node) { labels = append(labels, v.Value) })
		} else if !strings.Contains(n.Value, "${{") {
			labels = append(labels, n.Value)
		}
	}
	if ro != nil {
		switch ro.Kind {
		case yaml.ScalarNode:
			collect(ro)
		case yaml.SequenceNode:
			for _, item := range ro.Content {
				if item.Kind == yaml.ScalarNode {
					collect(item)
				}
			}
		}
	}
	if len(labels) == 0 {
		return false, "can't determine the runner (expression-valued runs-on), so the default shell is unknown"
	}
	for _, l := range labels {
		if strings.Contains(strings.ToLower(l), "windows") {
			return false, "job runs on Windows (default shell pwsh)"
		}
	}
	return true, ""
}

// parseDeprecatedEcho tries to interpret one raw file line as a plain
// `echo` of a single deprecated workflow command and, if it succeeds,
// returns the rewritten line targeting the environment file instead.
// Quoting style and everything before the echo (list dash, `run: `
// prefix, indentation) are preserved verbatim. Lines that are anything
// more complicated — pipes, printf, %-escaped values, embedded quotes —
// are left for a human.
func parseDeprecatedEcho(line string) (rewritten, cmdName string, ok bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "#") {
		return "", "", false
	}
	stripped := t
	stripped = strings.TrimPrefix(stripped, "- ")
	stripped = strings.TrimSpace(strings.TrimPrefix(stripped, "run:"))
	if !strings.HasPrefix(stripped, "echo ") {
		return "", "", false
	}
	arg := strings.TrimSpace(stripped[len("echo "):])
	quote := ""
	inner := arg
	if len(arg) >= 2 && (arg[0] == '"' || arg[0] == '\'') {
		q := arg[0]
		if arg[len(arg)-1] != q {
			return "", "", false // trailing content after the string
		}
		inner = arg[1 : len(arg)-1]
		if strings.ContainsRune(inner, rune(q)) {
			return "", "", false // embedded quote of the same kind
		}
		quote = string(q)
	} else if strings.ContainsAny(inner, `"'`) {
		return "", "", false // mixed quoting, too clever for a mechanical edit
	}
	for _, dc := range deprecatedCommands {
		var newInner string
		if dc.takesKey {
			prefix := "::" + dc.name + " name="
			if !strings.HasPrefix(inner, prefix) {
				continue
			}
			rest := inner[len(prefix):]
			idx := strings.Index(rest, "::")
			if idx <= 0 {
				return "", "", false
			}
			key, val := rest[:idx], rest[idx+2:]
			if !validCommandKey(key) || hasCommandEscapes(val) {
				return "", "", false
			}
			newInner = key + "=" + val
		} else {
			prefix := "::" + dc.name + "::"
			if !strings.HasPrefix(inner, prefix) {
				continue
			}
			val := inner[len(prefix):]
			if val == "" || hasCommandEscapes(val) {
				return "", "", false
			}
			newInner = val
		}
		echoIdx := strings.Index(line, "echo ")
		if echoIdx < 0 {
			return "", "", false
		}
		head := line[:echoIdx+len("echo ")]
		return head + quote + newInner + quote + ` >> "$` + dc.target + `"`, dc.name, true
	}
	return "", "", false
}

// validCommandKey accepts the output/env/state names a mechanical rewrite
// can carry over safely: no spaces, quotes, or expression syntax.
func validCommandKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// hasCommandEscapes reports whether a workflow-command value uses the
// %25/%0A/%0D escape sequences, which environment files express with
// heredoc delimiters instead — not a one-line rewrite.
func hasCommandEscapes(v string) bool {
	lv := strings.ToLower(v)
	return strings.Contains(lv, "%25") || strings.Contains(lv, "%0a") || strings.Contains(lv, "%0d")
}

// fixDeprecatedCommands rewrites simple `echo "::set-output ..."`-style
// lines to their environment-file equivalents, only when the step runs
// under a bash-compatible shell. Anything it can't rewrite mechanically
// becomes a skip note.
func fixDeprecatedCommands(w *Workflow, lines []string) ([]edit, []string) {
	var edits []edit
	var skips []string
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			run := mapGet(step, "run")
			if run == nil {
				return
			}
			present := deprecatedCmdsIn(run.Value)
			if len(present) == 0 {
				return
			}
			names := make([]string, len(present))
			for i, dc := range present {
				names[i] = "::" + dc.name
			}
			if ok, reason := stepBashLike(w, job, step); !ok {
				skips = append(skips, fmt.Sprintf(
					"D018: %s in job `%s` — %s; rewrite to environment files by hand", strings.Join(names, ", "), id, reason))
				return
			}
			// Count how many script lines mention each command: the fix
			// for a command is all-or-nothing per step, because the rule
			// emits one finding per (step, command) and the safety valve
			// requires each edited rule's findings to actually go away.
			want := map[string]int{}
			for _, l := range strings.Split(run.Value, "\n") {
				t := strings.TrimSpace(l)
				if strings.HasPrefix(t, "#") {
					continue
				}
				for _, dc := range present {
					if strings.Contains(t, "::"+dc.name) {
						want[dc.name]++
					}
				}
			}
			got := map[string]int{}
			type namedEdit struct {
				name string
				e    edit
			}
			var stepEdits []namedEdit
			// runSpan keeps the scan inside this step — the next step's line
			// could hold another deprecated-command echo and corrupt the
			// all-or-nothing bookkeeping (TestFixDeprecatedCommandsAdjacentSteps).
			start, end := runSpan(run)
			for ln := start; ln <= end && ln <= len(lines); ln++ {
				if rewritten, name, ok := parseDeprecatedEcho(lines[ln-1]); ok {
					stepEdits = append(stepEdits, namedEdit{name, edit{
						findingLine: run.Line,
						line:        ln,
						replace:     rewritten,
						rule:        "D018",
						note:        fmt.Sprintf("rewrote `::%s` to an environment-file write in job `%s`", name, id),
					}})
					got[name]++
				}
			}
			for _, ne := range stepEdits {
				if got[ne.name] == want[ne.name] {
					edits = append(edits, ne.e)
				}
			}
			for _, dc := range present {
				if got[dc.name] != want[dc.name] {
					skips = append(skips, fmt.Sprintf(
						"D018: `::%s` in job `%s` isn't (all) plain single-echo lines (pipes, printf, %%-escapes, or folded scalar) — rewrite by hand", dc.name, id))
				}
			}
		})
	})
	return edits, skips
}
