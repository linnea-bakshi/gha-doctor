package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func schemaDoc(t *testing.T) map[string]any {
	t.Helper()
	b, err := Schema()
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return doc
}

// parserAccepts reports whether Parse recognizes key (no "unknown key"
// warning), probed behaviorally so this test can't drift from the switch.
func parserAccepts(t *testing.T, key string) bool {
	t.Helper()
	cfg, warns, err := Parse("probe.yml", []byte(key+": 5\n"))
	if err != nil {
		t.Fatalf("Parse(%q: 5): %v", key, err)
	}
	_ = cfg
	for _, w := range warns {
		if strings.Contains(w, "unknown key") {
			return false
		}
	}
	return true
}

// TestSchemaMatchesParser proves the schema's property set is exactly the
// key set Parse accepts (including underscore aliases).
func TestSchemaMatchesParser(t *testing.T) {
	doc := schemaDoc(t)
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object")
	}
	for key := range props {
		if !parserAccepts(t, key) {
			t.Errorf("schema property %q is not accepted by Parse", key)
		}
	}
	// Every canonical key and underscore alias must be present.
	for _, key := range []string{"disable", "runs", "cache-logs", "cache_logs", "flaky-logs", "flaky_logs", "log-tail", "log_tail", "fail-on", "fail_on", "min-score", "min_score"} {
		if !parserAccepts(t, key) {
			t.Fatalf("expected Parse to accept %q — update this list to match the parser", key)
		}
		if _, ok := props[key]; !ok {
			t.Errorf("Parse accepts %q but the schema omits it", key)
		}
	}
	if got, want := len(props), 12; got != want {
		t.Errorf("schema has %d properties, want %d (new config key? add it here and in intKeys)", got, want)
	}
	if ap, _ := doc["additionalProperties"].(bool); ap {
		t.Errorf("additionalProperties must be false (unknown keys draw a warning from Parse)")
	}
}

// TestSchemaRuleEnumMatchesRuleTable proves the disable enum is exactly the
// disableable rule set: every Dxxx in lint.RuleMeta, nothing else, and each
// entry survives a round-trip through parseDisable.
func TestSchemaRuleEnumMatchesRuleTable(t *testing.T) {
	doc := schemaDoc(t)
	items := doc["properties"].(map[string]any)["disable"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)["items"].(map[string]any)
	var enum []string
	for _, v := range items["anyOf"].([]any) {
		m := v.(map[string]any)
		id, _ := m["const"].(string)
		if id == "" {
			t.Fatalf("enum entry without const: %v", m)
		}
		desc, _ := m["description"].(string)
		if !strings.Contains(desc, lint.RuleMeta[id].Name) {
			t.Errorf("enum %s: description %q does not name the rule", id, desc)
		}
		enum = append(enum, id)
	}
	want := DisableableRuleIDs()
	if fmt.Sprint(enum) != fmt.Sprint(want) {
		t.Errorf("enum = %v, want %v", enum, want)
	}
	for id := range lint.RuleMeta {
		inEnum := false
		for _, e := range enum {
			if e == id {
				inEnum = true
			}
		}
		cfg, warns, err := Parse("probe.yml", []byte("disable: ["+id+"]\n"))
		if err != nil {
			t.Fatalf("Parse disable %s: %v", id, err)
		}
		accepted := len(warns) == 0 && len(cfg.Disable) == 1
		if inEnum != accepted {
			t.Errorf("rule %s: in enum = %v but parser accepts = %v (%v)", id, inEnum, accepted, warns)
		}
	}
}

// TestSchemaValidatesRealConfigs runs a positive and negative corpus
// through a minimal validator for the subset of draft-07 the schema uses.
func TestSchemaValidatesRealConfigs(t *testing.T) {
	doc := schemaDoc(t)
	valid := []map[string]any{
		{"disable": []any{"D004", "D009"}, "runs": float64(150)},
		{"disable": "D004, D009"},
		{"cache_logs": float64(0), "log-tail": float64(40), "flaky_logs": float64(4)},
		{"min-score": float64(70), "fail-on": "never"},
		{"min_score": float64(0)},
		{},
	}
	invalid := []map[string]any{
		{"min-score": float64(101)},
		{"min-score": float64(-1)},
		{"fail-on": "always"},
		{"disable": []any{"D999"}},
		{"disable": "D004; D009"},
		{"runs": float64(0)},
		{"log-tail": float64(-1)},
		{"cache-logs": "many"},
		{"unknown-key": float64(1)},
	}
	for i, v := range valid {
		if err := validate(doc, doc, v); err != nil {
			t.Errorf("valid[%d] rejected: %v", i, err)
		}
	}
	for i, v := range invalid {
		if err := validate(doc, doc, v); err == nil {
			t.Errorf("invalid[%d] accepted: %v", i, v)
		}
	}
}

// validate is a purpose-built checker for exactly the schema constructs
// Schema emits (type/object props/additionalProperties/anyOf/const/items/
// pattern/minimum/$ref into #/properties/...). Unknown constructs fail
// loudly so a schema change can't silently skip validation.
func validate(root, schema map[string]any, v any) error {
	if ref, ok := schema["$ref"].(string); ok {
		const p = "#/properties/"
		if !strings.HasPrefix(ref, p) {
			return fmt.Errorf("unsupported $ref %q", ref)
		}
		target, ok := root["properties"].(map[string]any)[strings.TrimPrefix(ref, p)].(map[string]any)
		if !ok {
			return fmt.Errorf("dangling $ref %q", ref)
		}
		return validate(root, target, v)
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		var errs []string
		for _, sub := range anyOf {
			if err := validate(root, sub.(map[string]any), v); err == nil {
				return nil
			} else {
				errs = append(errs, err.Error())
			}
		}
		return fmt.Errorf("no anyOf branch matched: %s", strings.Join(errs, "; "))
	}
	if enum, ok := schema["enum"].([]any); ok {
		for _, e := range enum {
			if v == e {
				return nil
			}
		}
		return fmt.Errorf("%v not in enum %v", v, enum)
	}
	if c, ok := schema["const"]; ok {
		if v != c {
			return fmt.Errorf("%v != const %v", v, c)
		}
		return nil
	}
	typ, _ := schema["type"].(string)
	switch typ {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%v is not an object", v)
		}
		props, _ := schema["properties"].(map[string]any)
		for key, val := range obj {
			sub, ok := props[key].(map[string]any)
			if !ok {
				if ap, apOK := schema["additionalProperties"].(bool); apOK && !ap {
					return fmt.Errorf("unknown property %q", key)
				}
				continue
			}
			if err := validate(root, sub, val); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
		return nil
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%v is not an array", v)
		}
		items, _ := schema["items"].(map[string]any)
		for i, it := range arr {
			if err := validate(root, items, it); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		return nil
	case "string":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%v is not a string", v)
		}
		if pat, ok := schema["pattern"].(string); ok {
			if err := matchPattern(pat, s); err != nil {
				return err
			}
		}
		return nil
	case "integer":
		f, ok := v.(float64)
		if !ok || f != float64(int(f)) {
			return fmt.Errorf("%v is not an integer", v)
		}
		if min, ok := schema["minimum"].(float64); ok && f < min {
			return fmt.Errorf("%v < minimum %v", f, min)
		}
		if max, ok := schema["maximum"].(float64); ok && f > max {
			return fmt.Errorf("%v > maximum %v", f, max)
		}
		return nil
	case "":
		return fmt.Errorf("schema node with no type/const/anyOf/$ref: %v", schema)
	default:
		return fmt.Errorf("unsupported type %q", typ)
	}
}

func matchPattern(pat, s string) error {
	re, err := compilePattern(pat)
	if err != nil {
		return err
	}
	if !re.MatchString(s) {
		return fmt.Errorf("%q does not match %q", s, pat)
	}
	return nil
}

func compilePattern(pat string) (*regexp.Regexp, error) {
	return regexp.Compile(pat)
}
