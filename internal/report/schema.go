package report

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

// JSON Schema generation for the --json output documents.
//
// The schemas are produced by reflecting over the exact Go types that
// encoding/json marshals, so they cannot drift from the real output: a new
// field appears in the schema the moment it appears in the struct, and CI
// regenerates docs/schema/ and fails on any diff. The generator supports
// precisely the type subset those structs use (structs, pointers, slices,
// maps with string keys, strings, bools, integers, floats, time.Time) and
// fails loudly on anything else, so an exotic new field can't silently get
// a wrong schema.
//
// Presence semantics mirror encoding/json:
//   - fields without omitempty are required; pointer/slice/map values may be
//     JSON null (a nil Go value marshals to null)
//   - fields with omitempty are optional and never null (the zero value is
//     omitted entirely)

// SchemaDoc describes one top-level JSON output document.
type SchemaDoc struct {
	Name  string // schema file base name, e.g. "report" -> report.schema.json
	Title string
	Desc  string
	typ   reflect.Type
}

// SchemaDocs lists every JSON document gha-doctor can emit.
func SchemaDocs() []SchemaDoc {
	return []SchemaDoc{
		{
			Name:  "report",
			Title: "gha-doctor report",
			Desc:  "Output of `gha-doctor --json` (default mode, with or without run-history analysis): findings, baseline, analysis, score, top wins.",
			typ:   reflect.TypeOf(Combined{}),
		},
		{
			Name:  "org",
			Title: "gha-doctor org scan",
			Desc:  "Output of `gha-doctor --org NAME --json`: per-repo run stats for a whole org or user account.",
			typ:   reflect.TypeOf(api.OrgAnalysis{}),
		},
		{
			Name:  "run",
			Title: "gha-doctor run deep dive",
			Desc:  "Output of `gha-doctor --run ID --json`: one run's jobs, steps, baselines, verdicts, failing tests, and log tails.",
			typ:   reflect.TypeOf(runDeepDoc{}),
		},
		{
			Name:  "fix-preview",
			Title: "gha-doctor fix preview",
			Desc:  "Output of `gha-doctor --diff --json`: the unified diffs --fix would apply, plus per-file applied/skipped/failed fix notes.",
			typ:   reflect.TypeOf(diffPreviewDoc{}),
		},
	}
}

// schemaOverrides tightens specific fields beyond what reflection can know,
// keyed by "pkg.Type.jsonFieldName". Keep this list tiny and defensible.
var schemaOverrides = map[string]func(map[string]any){
	// Finding.SevStr is set from Severity.String(), which returns exactly
	// these two values (see lint.Severity.String).
	"lint.Finding.severity": func(s map[string]any) {
		s["enum"] = []any{"info", "warning"}
	},
}

var timeType = reflect.TypeOf(time.Time{})

type schemaGen struct {
	defs  map[string]any          // $defs
	names map[reflect.Type]string // named struct type -> $defs key
}

// GenerateSchema renders one document's JSON Schema (draft 2020-12).
func GenerateSchema(doc SchemaDoc) ([]byte, error) {
	g := &schemaGen{defs: map[string]any{}, names: map[reflect.Type]string{}}
	root, err := g.object(doc.typ)
	if err != nil {
		return nil, err
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$id"] = "https://linnea-bakshi.github.io/gha-doctor/schema/" + doc.Name + ".schema.json"
	root["title"] = doc.Title
	root["description"] = doc.Desc
	if len(g.defs) > 0 {
		root["$defs"] = g.defs
	}
	out, err := json.MarshalIndent(root, "", "  ") // map keys marshal sorted: deterministic
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// object builds the schema for a struct type's JSON object form.
func (g *schemaGen) object(t reflect.Type) (map[string]any, error) {
	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() && !sf.Anonymous {
			continue
		}
		if sf.Anonymous {
			return nil, fmt.Errorf("%s embeds %s: embedded fields are not supported by the schema generator — flatten it or extend schema.go", t, sf.Type)
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = sf.Name
		}
		if strings.Contains(opts, "string") {
			return nil, fmt.Errorf("%s.%s uses the json \",string\" option: not supported by the schema generator", t, sf.Name)
		}
		s, nullable, err := g.fieldSchema(sf.Type)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t, sf.Name, err)
		}
		if fn, ok := schemaOverrides[t.String()+"."+name]; ok {
			fn(s)
		}
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
			if nullable {
				s = map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
			}
		}
		props[name] = s
	}
	obj := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		sort.Strings(required)
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		obj["required"] = req
	}
	return obj, nil
}

// fieldSchema returns the schema for one Go type plus whether a nil value of
// it marshals to JSON null (pointers, slices, maps).
func (g *schemaGen) fieldSchema(t reflect.Type) (map[string]any, bool, error) {
	switch t.Kind() {
	case reflect.Pointer:
		s, _, err := g.fieldSchema(t.Elem())
		return s, true, err
	case reflect.Struct:
		if t == timeType {
			return map[string]any{"type": "string", "format": "date-time"}, false, nil
		}
		if t.Name() == "" {
			s, err := g.object(t)
			return s, false, err
		}
		name, err := g.define(t)
		if err != nil {
			return nil, false, err
		}
		return map[string]any{"$ref": "#/$defs/" + name}, false, nil
	case reflect.Slice:
		item, itemNullable, err := g.fieldSchema(t.Elem())
		if err != nil {
			return nil, false, err
		}
		if itemNullable {
			item = map[string]any{"anyOf": []any{item, map[string]any{"type": "null"}}}
		}
		return map[string]any{"type": "array", "items": item}, true, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, false, fmt.Errorf("map key type %s is not a string", t.Key())
		}
		val, valNullable, err := g.fieldSchema(t.Elem())
		if err != nil {
			return nil, false, err
		}
		if valNullable {
			val = map[string]any{"anyOf": []any{val, map[string]any{"type": "null"}}}
		}
		return map[string]any{"type": "object", "additionalProperties": val}, true, nil
	case reflect.String:
		return map[string]any{"type": "string"}, false, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, false, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, false, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, false, nil
	default:
		return nil, false, fmt.Errorf("unsupported type %s (%s)", t, t.Kind())
	}
}

// define registers a named struct type under $defs and returns its key.
func (g *schemaGen) define(t reflect.Type) (string, error) {
	if name, ok := g.names[t]; ok {
		return name, nil
	}
	name := strings.TrimPrefix(t.String(), "report.")
	if r := name[0]; r >= 'a' && r <= 'z' { // don't leak unexported casing
		name = string(r-'a'+'A') + name[1:]
	}
	g.names[t] = name
	g.defs[name] = map[string]any{} // placeholder breaks reference cycles
	obj, err := g.object(t)
	if err != nil {
		return "", err
	}
	g.defs[name] = obj
	return name, nil
}
