//go:build js && wasm

// Command playground is the WebAssembly build behind
// https://linnea-bakshi.github.io/gha-doctor/playground/ — the same lint
// engine the CLI uses, compiled for the browser so people can try the
// static rules (and --fix) on a pasted workflow without installing
// anything. Only linting runs here; run-history analysis needs the
// GitHub API and stays in the CLI.
package main

import (
	"encoding/json"
	"sort"
	"strings"
	"syscall/js"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// lintResult is the JSON payload returned to the page.
type lintResult struct {
	Findings []lint.Finding `json:"findings"`
	Error    string         `json:"error,omitempty"`
}

// fixOutput is the JSON payload for ghaDoctorFix.
type fixOutput struct {
	Output  string   `json:"output"`  // fixed YAML ("" when nothing changed)
	Changed bool     `json:"changed"` //
	Applied []string `json:"applied,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// ruleInfo mirrors lint.RuleMeta for the page's rule reference links.
type ruleInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Short   string `json:"short"`
	Fixable bool   `json:"fixable"`
}

func marshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"internal: marshal failed"}`
	}
	return string(b)
}

// ghaDoctorLint(filename, yaml) -> JSON lintResult
func doLint(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return marshal(lintResult{Error: "lint needs (filename, yaml)"})
	}
	name, content := args[0].String(), args[1].String()
	fs, err := lint.LintBytes(name, []byte(content))
	res := lintResult{Findings: fs}
	if fs == nil {
		res.Findings = []lint.Finding{} // JSON [] not null
	}
	if err != nil {
		res.Error = err.Error()
	}
	return marshal(res)
}

// ghaDoctorFix(filename, yaml, pmJSON) -> JSON fixOutput
// pmJSON is an object like {"node":"npm","python":"pip"} mapping ecosystems
// to the cache value the D003 fix should insert; omit an ecosystem when its
// package manager is unknown (the fix is then skipped with a note, same as
// the CLI when no unambiguous lockfile exists).
func doFix(_ js.Value, args []js.Value) any {
	if len(args) < 2 {
		return marshal(fixOutput{Error: "fix needs (filename, yaml[, pmJSON])"})
	}
	name, content := args[0].String(), args[1].String()
	pm := map[string]string{}
	if len(args) >= 3 && args[2].Type() == js.TypeString && args[2].String() != "" {
		if err := json.Unmarshal([]byte(args[2].String()), &pm); err != nil {
			return marshal(fixOutput{Error: "bad pm JSON: " + err.Error()})
		}
	}
	out, res, err := lint.FixBytes(name, []byte(content), pm, map[string]bool{})
	fo := fixOutput{Applied: res.Applied, Skipped: res.Skipped}
	if err != nil {
		fo.Error = err.Error()
		return marshal(fo)
	}
	if out != nil {
		fo.Output = string(out)
		fo.Changed = true
	}
	return marshal(fo)
}

// ghaDoctorRules() -> JSON []ruleInfo sorted by ID
func doRules(_ js.Value, _ []js.Value) any {
	fixable := map[string]bool{}
	for _, r := range lint.FixableRules {
		fixable[r] = true
	}
	var out []ruleInfo
	for id, m := range lint.RuleMeta {
		if id == "parse" {
			continue
		}
		out = append(out, ruleInfo{ID: m.ID, Name: m.Name, Short: m.Short, Fixable: fixable[id]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return marshal(out)
}

func main() {
	js.Global().Set("ghaDoctorLint", js.FuncOf(doLint))
	js.Global().Set("ghaDoctorFix", js.FuncOf(doFix))
	js.Global().Set("ghaDoctorRules", js.FuncOf(doRules))
	js.Global().Set("ghaDoctorVersion", js.ValueOf(strings.TrimSpace(version)))
	select {} // keep the Go runtime alive for callbacks
}
