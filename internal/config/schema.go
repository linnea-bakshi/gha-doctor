package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// JSON Schema generation for the .gha-doctor.yml config file.
//
// Unlike the --json output schemas (reflected from the structs that
// encoding/json marshals), the config schema describes an *input* file, so
// it is built from the same tables Parse validates against: the key set
// mirrors Parse's switch, the rule-ID enum comes from lint.RuleMeta, and
// the integer minimums are the ones parseIntKey enforces. CI regenerates
// docs/schema/ and fails on any diff, so the schema cannot drift from the
// parser. TestSchemaMatchesParser additionally proves every schema property
// is a key Parse accepts and vice versa.
//
// Draft-07 is used (not 2020-12) because this schema exists for editors:
// yaml-language-server and friends have the most reliable support there,
// and SchemaStore recommends it.

// SchemaURL is the canonical published location of the config schema.
const SchemaURL = "https://linnea-bakshi.github.io/gha-doctor/schema/gha-doctor-config.schema.json"

const docsURL = "https://linnea-bakshi.github.io/gha-doctor/"

// ruleIDPattern matches the rule IDs the disable key accepts.
var ruleIDPattern = regexp.MustCompile(`^D[0-9]{3}$`)

// DisableableRuleIDs returns the sorted rule IDs that can appear under
// disable: every Dxxx rule in the real rule table ("parse" is a pseudo-rule
// reported for unreadable YAML and cannot be disabled).
func DisableableRuleIDs() []string {
	var ids []string
	for id := range lint.RuleMeta {
		if ruleIDPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// intKey describes one integer config key as parseIntKey enforces it.
type intKey struct {
	name  string // canonical (hyphenated) key
	alias string // accepted underscore variant, "" if same
	min   int
	desc  string
}

// intKeys mirrors the integer cases in Parse's switch. Parse normalizes
// underscores to hyphens, so each hyphenated key has an underscore alias.
var intKeys = []intKey{
	{"runs", "", 1, "How many recent workflow runs the history analysis samples (--runs)."},
	{"cache-logs", "cache_logs", 0, "How many job logs to sample for real cache hit/miss rates; 0 disables (--cache-logs)."},
	{"flaky-logs", "flaky_logs", 0, "How many logs of proven-flaky failures to read to name the flaky tests; 0 disables (--flaky-logs)."},
	{"log-tail", "log_tail", 0, "How many trailing lines of a failing step's log a --run deep dive shows; 0 disables (--log-tail)."},
}

// Schema renders the draft-07 JSON schema for .gha-doctor.yml.
func Schema() ([]byte, error) {
	ruleEnum := make([]any, 0, len(lint.RuleMeta))
	for _, id := range DisableableRuleIDs() {
		m := lint.RuleMeta[id]
		ruleEnum = append(ruleEnum, map[string]any{
			"const":       m.ID,
			"description": fmt.Sprintf("%s — %s\n%srules#%s-%s", m.Name, m.Short, docsURL, lowerASCII(m.ID), lowerASCII(m.Name)),
		})
	}
	disable := map[string]any{
		"description": "Rule IDs this repo has decided not to enforce: a list, or one comma-separated string. Unknown IDs are warned about and skipped.\n" + docsURL + "rules",
		"anyOf": []any{
			map[string]any{
				"type":  "array",
				"items": map[string]any{"anyOf": ruleEnum},
			},
			map[string]any{
				"type":    "string",
				"pattern": `^\s*D[0-9]{3}\s*(,\s*D[0-9]{3}\s*)*$`,
			},
		},
	}

	failOn := map[string]any{
		"description": "Minimum finding severity that makes the exit code 2 (the CI gate): \"any\" finding at all, \"warning\" (the default), or \"never\" (report-only — findings are shown but the exit code stays 0). \"info\", \"warn\", and \"none\" are accepted aliases.\n" + docsURL + "faq",
		"enum":        []any{FailAny, FailWarn, FailNever, "info", "warn", "none"},
	}

	minScore := map[string]any{
		"description": "Health score (0\u2013100) below which the exit code is 2 (--min-score). Gates the whole-repo score the report shows; combine with fail-on: never to gate on score alone.\n" + docsURL + "faq",
		"type":        "integer",
		"minimum":     0,
		"maximum":     100,
	}

	props := map[string]any{
		"disable": disable, "fail-on": failOn, "fail_on": map[string]any{"$ref": "#/properties/fail-on"},
		"min-score": minScore, "min_score": map[string]any{"$ref": "#/properties/min-score"},
	}
	for _, k := range intKeys {
		props[k.name] = map[string]any{
			"type":        "integer",
			"minimum":     k.min,
			"description": k.desc + "\n" + docsURL,
		}
		if k.alias != "" {
			props[k.alias] = map[string]any{"$ref": "#/properties/" + k.name}
		}
	}

	schema := map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"$id":                  SchemaURL,
		"title":                "gha-doctor configuration",
		"description":          "Per-repo configuration for gha-doctor (.gha-doctor.yml, .gha-doctor.yaml, .github/gha-doctor.yml, or .github/gha-doctor.yaml). Explicit CLI flags always win over the file; --no-config ignores it entirely.\n" + docsURL,
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// lowerASCII lowercases A-Z without unicode tables (rule IDs are ASCII).
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
