// Package config loads .gha-doctor.yml, the optional per-repo config file.
//
// The config file lets a repo state its policy once — rules it has decided
// not to enforce, how much run history to sample — instead of repeating
// flags in every workflow, alias, and teammate's shell. Explicit CLI flags
// always win over the file; --no-config ignores it entirely.
//
// A broken config must never take the doctor down, but it must be loud:
// unknown keys, unknown rule IDs, and out-of-range values are returned as
// warnings and the rest of the file still applies. Only unreadable YAML
// rejects the whole file (again loudly, never fatally — the caller runs
// unconfigured rather than exiting).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Paths are the recognized config locations relative to the repository
// root, in precedence order: first found wins, the rest are ignored.
var Paths = []string{
	".gha-doctor.yml",
	".gha-doctor.yaml",
	".github/gha-doctor.yml",
	".github/gha-doctor.yaml",
}

// Config is a parsed config file. Scalar fields are pointers so "not set"
// (defer to the CLI default) is distinct from an explicit zero.
type Config struct {
	File      string   `json:"file"`                 // repo-relative path it was loaded from
	Disable   []string `json:"disable,omitempty"`    // rule IDs, upper-cased, sorted
	Runs      *int     `json:"runs,omitempty"`       // history sample size (--runs)
	CacheLogs *int     `json:"cache_logs,omitempty"` // job logs to sample (--cache-logs)
	FlakyLogs *int     `json:"flaky_logs,omitempty"` // flaky-failure logs to read (--flaky-logs)
	LogTail   *int     `json:"log_tail,omitempty"`   // failing-step log lines (--log-tail)
	FailOn    *string  `json:"fail_on,omitempty"`    // minimum severity that exits 2 (--fail-on)
	MinScore  *int     `json:"min_score,omitempty"`  // health score below this exits 2 (--min-score)
}

// Canonical --fail-on / fail-on levels.
const (
	FailAny   = "any"     // any finding exits 2
	FailWarn  = "warning" // warning-severity findings exit 2 (the default)
	FailNever = "never"   // report-only: findings never change the exit code
)

// ParseFailOn normalizes a fail-on value to its canonical form. Aliases
// exist because both halves are guessable ("warn" for warning, "info" for
// any, "none" for never); anything else is an error so a typo can't
// silently weaken or tighten a CI gate.
func ParseFailOn(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "any", "info":
		return FailAny, nil
	case "warning", "warn":
		return FailWarn, nil
	case "never", "none":
		return FailNever, nil
	}
	return "", fmt.Errorf("must be any, warning, or never (got %q)", s)
}

// Summary renders the applied settings for the stderr note, e.g.
// "disable D004, D009; runs 150".
func (c *Config) Summary() string {
	var parts []string
	if len(c.Disable) > 0 {
		parts = append(parts, "disable "+strings.Join(c.Disable, ", "))
	}
	if c.Runs != nil {
		parts = append(parts, fmt.Sprintf("runs %d", *c.Runs))
	}
	if c.CacheLogs != nil {
		parts = append(parts, fmt.Sprintf("cache-logs %d", *c.CacheLogs))
	}
	if c.FlakyLogs != nil {
		parts = append(parts, fmt.Sprintf("flaky-logs %d", *c.FlakyLogs))
	}
	if c.LogTail != nil {
		parts = append(parts, fmt.Sprintf("log-tail %d", *c.LogTail))
	}
	if c.FailOn != nil {
		parts = append(parts, "fail-on "+*c.FailOn)
	}
	if c.MinScore != nil {
		parts = append(parts, fmt.Sprintf("min-score %d", *c.MinScore))
	}
	if len(parts) == 0 {
		return "no settings"
	}
	return strings.Join(parts, "; ")
}

// Parse reads one config document. Warnings never reject the file.
func Parse(file string, data []byte) (*Config, []string, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("not a YAML mapping: %v", err)
	}
	cfg := &Config{File: file}
	var warns []string
	for key := range raw {
		node := raw[key]
		switch strings.ToLower(strings.ReplaceAll(key, "_", "-")) {
		case "disable":
			ids, w := parseDisable(&node)
			cfg.Disable = ids
			warns = append(warns, w...)
		case "runs":
			cfg.Runs = parseIntKey(&node, key, 1, &warns)
		case "cache-logs":
			cfg.CacheLogs = parseIntKey(&node, key, 0, &warns)
		case "flaky-logs":
			cfg.FlakyLogs = parseIntKey(&node, key, 0, &warns)
		case "log-tail":
			cfg.LogTail = parseIntKey(&node, key, 0, &warns)
		case "fail-on":
			var s string
			if err := node.Decode(&s); err != nil {
				warns = append(warns, fmt.Sprintf("%s: expected a string: %v", key, err))
				break
			}
			v, err := ParseFailOn(s)
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s: %v", key, err))
				break
			}
			cfg.FailOn = &v
		case "min-score":
			// A score gate typo must not silently weaken CI, so out-of-range
			// values warn loudly and are ignored (the parseIntKey contract).
			if v := parseIntKey(&node, key, 0, &warns); v != nil {
				if *v > 100 {
					warns = append(warns, fmt.Sprintf("%s: must be <= 100 (got %d)", key, *v))
				} else {
					cfg.MinScore = v
				}
			}
		default:
			warns = append(warns, fmt.Sprintf("unknown key %q (known: disable, runs, cache-logs, flaky-logs, log-tail, fail-on, min-score)", key))
		}
	}
	sort.Strings(warns)
	return cfg, warns, nil
}

// parseDisable accepts a sequence of rule IDs or a single comma-separated
// string. IDs are validated against the real rule table so a typo can't
// silently disable nothing.
func parseDisable(n *yaml.Node) ([]string, []string) {
	var items []string
	switch n.Kind {
	case yaml.SequenceNode:
		if err := n.Decode(&items); err != nil {
			return nil, []string{fmt.Sprintf("disable: expected a list of rule IDs: %v", err)}
		}
	case yaml.ScalarNode:
		var s string
		if err := n.Decode(&s); err != nil {
			return nil, []string{fmt.Sprintf("disable: %v", err)}
		}
		items = strings.Split(s, ",")
	default:
		return nil, []string{"disable: expected a list of rule IDs (e.g. [D004, D009])"}
	}
	seen := map[string]bool{}
	var ids, warns []string
	for _, it := range items {
		id := strings.ToUpper(strings.TrimSpace(it))
		if id == "" {
			continue
		}
		if _, ok := lint.RuleMeta[id]; !ok {
			warns = append(warns, fmt.Sprintf("disable: unknown rule ID %q", id))
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, warns
}

// parseIntKey decodes an integer >= min, warning (and skipping) otherwise.
func parseIntKey(n *yaml.Node, key string, min int, warns *[]string) *int {
	var v int
	if err := n.Decode(&v); err != nil {
		*warns = append(*warns, fmt.Sprintf("%s: expected an integer: %v", key, err))
		return nil
	}
	if v < min {
		*warns = append(*warns, fmt.Sprintf("%s: must be >= %d (got %d)", key, min, v))
		return nil
	}
	return &v
}

// FindLocal loads the config from a local checkout, trying each recognized
// path in precedence order. No config file returns (nil, nil, nil).
func FindLocal(dir string) (*Config, []string, error) {
	for _, p := range Paths {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		cfg, warns, perr := Parse(p, data)
		if perr != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, perr)
		}
		return cfg, warns, nil
	}
	return nil, nil, nil
}

// PickRemote chooses the config path from directory listings of the repo
// root and .github/, honoring the same precedence as FindLocal. Either
// slice may be nil. Returns "" when the repo has no config file.
func PickRemote(rootNames, githubNames []string) string {
	has := map[string]bool{}
	for _, n := range rootNames {
		has[n] = true
	}
	for _, n := range githubNames {
		has[".github/"+n] = true
	}
	for _, p := range Paths {
		if has[p] {
			return p
		}
	}
	return ""
}
