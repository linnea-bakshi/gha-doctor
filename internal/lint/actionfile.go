package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Action metadata files (action.yml / action.yaml) are a second lint
// surface: the manifests of actions the repository *publishes*, as opposed
// to the workflows it runs. Three rules apply there:
//
//   - D019: runs.using declares a deprecated Node runtime (node12/node16
//     were removed from runners; node20 is deprecated with removal
//     announced for fall 2026).
//   - D015: composite-action steps pinned to shut-down action versions.
//   - D018: composite-action run steps emitting deprecated workflow
//     commands.
//
// Discovery is conventional and bounded (see IsActionPath); files that do
// not parse or lack a `runs:` mapping are skipped silently — unlike
// .github/workflows, where every YAML file is a workflow by definition, a
// discovered action.yml is only *probably* an action manifest, and
// gha-doctor doesn't warn about files it can't prove are in scope.

// deprecatedRuntime describes one runs.using Node runtime GitHub has
// deprecated or removed. The current declared-runtime recommendation is
// node24 (default since June 2, 2026).
type deprecatedRuntime struct {
	status string // what happened to it, for the message
	effect string // what that means at runtime today
}

var deprecatedRuntimes = map[string]deprecatedRuntime{
	"node12": {
		status: "removed from GitHub runners in 2023",
		effect: "the declared runtime no longer exists — GitHub force-runs this action on a newer Node, and Node 20 itself is scheduled for removal in fall 2026",
	},
	"node16": {
		status: "removed from GitHub runners in 2024",
		effect: "the declared runtime no longer exists — GitHub force-runs this action on a newer Node, and Node 20 itself is scheduled for removal in fall 2026",
	},
	"node20": {
		status: "deprecated by GitHub in September 2025; Node 24 became the default runtime on June 2, 2026",
		effect: "GitHub has announced Node 20's removal from runners in fall 2026 — this action will stop working then",
	},
}

// LintActionBytes lints one action metadata file. Content that fails to
// parse or has no `runs:` mapping returns (nil, false): not recognizably an
// action manifest.
func LintActionBytes(p string, data []byte) (findings []Finding, isAction bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return nil, false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, false
	}
	runs := mapGet(doc, "runs")
	if runs == nil || runs.Kind != yaml.MappingNode {
		return nil, false
	}
	ign := parseIgnores(data)
	emit := func(f Finding) {
		if ign.matches(f.Line, f.Rule) {
			return
		}
		f.File = p
		f.SevStr = f.Severity.String()
		findings = append(findings, f)
	}

	using := mapGet(runs, "using")
	if using != nil {
		if dr, ok := deprecatedRuntimes[strings.ToLower(strings.TrimSpace(using.Value))]; ok {
			emit(Finding{
				Rule: "D019", Severity: Warn, Line: using.Line,
				Message: fmt.Sprintf("action declares `runs.using: %s`, %s — %s", using.Value, dr.status, dr.effect),
				Advice:  "update to `runs.using: node24` and verify the bundled code runs on Node 24",
			})
		}
	}

	// Composite actions have steps like a job does; the shared predicate
	// tables (retiredActionFor, deprecatedCmdsIn) keep these findings in
	// lockstep with their workflow-file counterparts.
	if using != nil && strings.EqualFold(strings.TrimSpace(using.Value), "composite") {
		steps := mapGet(runs, "steps")
		if steps != nil && steps.Kind == yaml.SequenceNode {
			for _, step := range steps.Content {
				if step.Kind != yaml.MappingNode {
					continue
				}
				if u := mapGet(step, "uses"); u != nil {
					if at := strings.IndexByte(u.Value, '@'); at >= 0 {
						name, ref := u.Value[:at], u.Value[at+1:]
						if ra := retiredActionFor(name); ra != nil {
							if m := refMajor(ref); m >= 0 && ra.majors[m] {
								emit(Finding{
									Rule: "D015", Severity: Warn, Line: u.Line,
									Message: fmt.Sprintf("`%s` was shut down by GitHub on %s — this composite-action step fails at runtime", u.Value, ra.when),
									Advice:  fmt.Sprintf("update to %s@%s", name, ra.fix),
								})
							}
						}
					}
				}
				if run := mapGet(step, "run"); run != nil {
					for _, dc := range deprecatedCmdsIn(run.Value) {
						effect := "GitHub prints a deprecation warning on every run and has announced its removal"
						if dc.broken {
							effect = "the step errors at runtime"
						}
						emit(Finding{
							Rule: "D018", Severity: Warn, Line: run.Line,
							Message: fmt.Sprintf("run step in composite action uses `::%s`, %s — %s", dc.name, dc.status, effect),
							Advice:  fmt.Sprintf("write to the environment file $%s instead", dc.target),
						})
					}
				}
			}
		}
	}
	return findings, true
}

// LintActionFiles lints in-memory action metadata files (local or fetched
// remotely). Returns findings plus how many files were recognized as action
// manifests.
func LintActionFiles(files []NamedFile) ([]Finding, int) {
	var findings []Finding
	n := 0
	for _, f := range files {
		fs, ok := LintActionBytes(f.Path, f.Data)
		if !ok {
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
	return findings, n
}

// ---- discovery ----

// actionWalkPrune lists directory names never descended into: dependency
// trees and build output routinely vendor other projects' action.yml files,
// which are not this repository's to fix.
var actionWalkPrune = map[string]bool{
	"node_modules": true, "vendor": true, "third_party": true,
	"testdata": true, "dist": true, "build": true, "target": true,
}

// maxActionFiles caps discovery; remotely each file costs one API request.
const MaxActionFiles = 25

// maxActionDepth limits how deep (in path segments) a discovered
// action.yml may sit, outside the .github/actions convention.
const maxActionDepth = 3

// IsActionPath reports whether a slash-separated repo-relative path is a
// conventionally-placed action metadata file: action.yml or action.yaml at
// the root, in a shallow subdirectory (monorepos like actions/cache keep
// restore/action.yml, save/action.yml), or anywhere under .github/actions.
// Shared by the local walker, the remote tree filter, and --baseline so
// their notion of "in scope" cannot drift.
func IsActionPath(p string) bool {
	base := path.Base(p)
	if base != "action.yml" && base != "action.yaml" {
		return false
	}
	segs := strings.Split(p, "/")
	for i, s := range segs[:len(segs)-1] {
		if actionWalkPrune[s] {
			return false
		}
		if strings.HasPrefix(s, ".") {
			// Hidden directories are out, except the .github/actions tree.
			if !(i == 0 && s == ".github" && len(segs) > 2 && segs[1] == "actions") {
				return false
			}
		}
	}
	if segs[0] == ".github" {
		return len(segs) >= 3 && segs[1] == "actions" && len(segs) <= 6
	}
	return len(segs) <= maxActionDepth
}

// DiscoverActionFiles walks root for action metadata files, bounded by
// IsActionPath and capped at MaxActionFiles (truncated reports the cap
// being hit). Paths are returned relative to root, slash-separated.
func DiscoverActionFiles(root string) (paths []string, truncated bool) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, don't fail discovery
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if actionWalkPrune[name] {
				return fs.SkipDir
			}
			if strings.HasPrefix(name, ".") {
				if rel == ".github" {
					return nil // descend, for .github/actions
				}
				return fs.SkipDir
			}
			if strings.HasPrefix(rel, ".github/") {
				// Under .github only the actions subtree is in scope.
				if rel != ".github/actions" && !strings.HasPrefix(rel, ".github/actions/") {
					return fs.SkipDir
				}
				return nil
			}
			// Elsewhere, directories deep enough that any action.yml inside
			// would exceed maxActionDepth segments are not worth walking.
			if strings.Count(rel, "/") >= maxActionDepth-1 {
				return fs.SkipDir
			}
			return nil
		}
		if !IsActionPath(rel) {
			return nil
		}
		if len(paths) >= MaxActionFiles {
			truncated = true
			return fs.SkipAll
		}
		paths = append(paths, rel)
		return nil
	})
	sort.Strings(paths)
	return paths, truncated
}

// ReadActionFiles loads discovered action files as NamedFiles with
// root-relative paths.
func ReadActionFiles(root string, paths []string) []NamedFile {
	var out []NamedFile
	for _, p := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		out = append(out, NamedFile{Path: p, Data: data})
	}
	return out
}
