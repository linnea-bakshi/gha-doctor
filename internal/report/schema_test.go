package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/config"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// ---- minimal JSON Schema validator (exactly the subset the generator emits) ----

type schemaErrs []string

func (e *schemaErrs) addf(format string, a ...any) { *e = append(*e, fmt.Sprintf(format, a...)) }

func validateSchema(t *testing.T, schema map[string]any, data any) []string {
	t.Helper()
	defs, _ := schema["$defs"].(map[string]any)
	var errs schemaErrs
	validateNode(schema, defs, data, "$", &errs)
	return errs
}

func validateNode(s map[string]any, defs map[string]any, data any, path string, errs *schemaErrs) {
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		target, ok := defs[name].(map[string]any)
		if !ok {
			errs.addf("%s: unresolvable $ref %q", path, ref)
			return
		}
		validateNode(target, defs, data, path, errs)
		return
	}
	if alts, ok := s["anyOf"].([]any); ok {
		for _, alt := range alts {
			var sub schemaErrs
			validateNode(alt.(map[string]any), defs, data, path, &sub)
			if len(sub) == 0 {
				return
			}
		}
		errs.addf("%s: value %v matches no anyOf branch", path, data)
		return
	}
	if enum, ok := s["enum"].([]any); ok {
		for _, v := range enum {
			if v == data {
				goto enumOK
			}
		}
		errs.addf("%s: %v not in enum %v", path, data, enum)
		return
	enumOK:
	}
	typ, _ := s["type"].(string)
	switch typ {
	case "null":
		if data != nil {
			errs.addf("%s: expected null, got %T", path, data)
		}
	case "string":
		str, ok := data.(string)
		if !ok {
			errs.addf("%s: expected string, got %T", path, data)
			return
		}
		if s["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339, str); err != nil {
				errs.addf("%s: %q is not RFC3339", path, str)
			}
		}
	case "boolean":
		if _, ok := data.(bool); !ok {
			errs.addf("%s: expected boolean, got %T", path, data)
		}
	case "integer":
		n, ok := data.(float64)
		if !ok || n != math.Trunc(n) {
			errs.addf("%s: expected integer, got %v (%T)", path, data, data)
		}
	case "number":
		if _, ok := data.(float64); !ok {
			errs.addf("%s: expected number, got %T", path, data)
		}
	case "array":
		arr, ok := data.([]any)
		if !ok {
			errs.addf("%s: expected array, got %T", path, data)
			return
		}
		items, _ := s["items"].(map[string]any)
		for i, el := range arr {
			validateNode(items, defs, el, fmt.Sprintf("%s[%d]", path, i), errs)
		}
	case "object":
		obj, ok := data.(map[string]any)
		if !ok {
			errs.addf("%s: expected object, got %T", path, data)
			return
		}
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, present := obj[r.(string)]; !present {
					errs.addf("%s: missing required field %q", path, r)
				}
			}
		}
		addl := s["additionalProperties"]
		for k, v := range obj {
			if ps, ok := props[k].(map[string]any); ok {
				validateNode(ps, defs, v, path+"."+k, errs)
				continue
			}
			switch a := addl.(type) {
			case bool:
				if !a {
					errs.addf("%s: unexpected field %q", path, k)
				}
			case map[string]any:
				validateNode(a, defs, v, path+"."+k, errs)
			default:
				errs.addf("%s: unexpected field %q (no additionalProperties schema)", path, k)
			}
		}
	default:
		errs.addf("%s: schema node with no usable type: %v", path, s)
	}
}

// ---- reflection auto-filler: populates EVERY field so every schema path is exercised ----

func fillValue(v reflect.Value, depth int) {
	if depth > 12 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillValue(v.Elem(), depth+1)
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fillValue(v.Field(i), depth+1)
			}
		}
		if f, ok := v.Interface().(lint.Finding); ok {
			f.SevStr = lint.Warn.String() // must satisfy the severity enum
			v.Set(reflect.ValueOf(f))
		}
	case reflect.Slice:
		el := reflect.New(v.Type().Elem()).Elem()
		fillValue(el, depth+1)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), el))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		el := reflect.New(v.Type().Elem()).Elem()
		fillValue(el, depth+1)
		m.SetMapIndex(reflect.ValueOf("k"), el)
		v.Set(m)
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	}
}

func filled[T any](t *testing.T) T {
	t.Helper()
	var v T
	fillValue(reflect.ValueOf(&v).Elem(), 0)
	return v
}

func loadSchema(t *testing.T, doc SchemaDoc) map[string]any {
	t.Helper()
	b, err := GenerateSchema(doc)
	if err != nil {
		t.Fatalf("GenerateSchema(%s): %v", doc.Name, err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("schema %s is not valid JSON: %v", doc.Name, err)
	}
	return s
}

func docByName(t *testing.T, name string) SchemaDoc {
	t.Helper()
	for _, d := range SchemaDocs() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no schema doc named %q", name)
	return SchemaDoc{}
}

func decode(t *testing.T, buf *bytes.Buffer) any {
	t.Helper()
	var data any
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	return data
}

// ---- tests ----

// Every real output document — with every field populated via reflection, so
// newly added fields are automatically covered — must validate against its
// generated schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	t.Run("report_full", func(t *testing.T) {
		var buf bytes.Buffer
		err := JSON(&buf, []lint.Finding{filled[lint.Finding](t)}, 3,
			filled[*config.Config](t), filled[*lint.Baseline](t),
			filled[*api.Analysis](t), filled[*Score](t), filled[*Wins](t))
		if err != nil {
			t.Fatal(err)
		}
		if errs := validateSchema(t, loadSchema(t, docByName(t, "report")), decode(t, &buf)); len(errs) > 0 {
			t.Fatalf("full report does not match schema:\n%s", strings.Join(errs, "\n"))
		}
	})
	t.Run("report_minimal", func(t *testing.T) {
		var buf bytes.Buffer
		if err := JSON(&buf, nil, 0, nil, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if errs := validateSchema(t, loadSchema(t, docByName(t, "report")), decode(t, &buf)); len(errs) > 0 {
			t.Fatalf("minimal report does not match schema:\n%s", strings.Join(errs, "\n"))
		}
	})
	t.Run("org", func(t *testing.T) {
		var buf bytes.Buffer
		oa := filled[api.OrgAnalysis](t)
		if err := OrgJSON(&buf, &oa); err != nil {
			t.Fatal(err)
		}
		if errs := validateSchema(t, loadSchema(t, docByName(t, "org")), decode(t, &buf)); len(errs) > 0 {
			t.Fatalf("org scan does not match schema:\n%s", strings.Join(errs, "\n"))
		}
	})
	t.Run("run", func(t *testing.T) {
		var buf bytes.Buffer
		d := filled[api.RunDeep](t)
		if err := RunDeepJSON(&buf, &d); err != nil {
			t.Fatal(err)
		}
		if errs := validateSchema(t, loadSchema(t, docByName(t, "run")), decode(t, &buf)); len(errs) > 0 {
			t.Fatalf("run deep dive does not match schema:\n%s", strings.Join(errs, "\n"))
		}
	})
	t.Run("fix_preview", func(t *testing.T) {
		var buf bytes.Buffer
		p := filled[lint.FixPreview](t)
		p.Original, p.Fixed = []byte("a: 1\n"), []byte("a: 2\n")
		if err := DiffPreviewJSON(&buf, []lint.FixPreview{p}); err != nil {
			t.Fatal(err)
		}
		if errs := validateSchema(t, loadSchema(t, docByName(t, "fix-preview")), decode(t, &buf)); len(errs) > 0 {
			t.Fatalf("fix preview does not match schema:\n%s", strings.Join(errs, "\n"))
		}
	})
}

// The validator must actually reject bad documents — otherwise the tests
// above prove nothing.
func TestSchemaValidatorRejects(t *testing.T) {
	schema := loadSchema(t, docByName(t, "report"))
	var buf bytes.Buffer
	if err := JSON(&buf, nil, 0, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	base := decode(t, &buf).(map[string]any)

	t.Run("unknown_field", func(t *testing.T) {
		doc := map[string]any{}
		for k, v := range base {
			doc[k] = v
		}
		doc["bogus"] = 1.0
		if errs := validateSchema(t, schema, doc); len(errs) == 0 {
			t.Fatal("unknown top-level field was not rejected")
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		doc := map[string]any{}
		for k, v := range base {
			doc[k] = v
		}
		doc["files_scanned"] = "three"
		if errs := validateSchema(t, schema, doc); len(errs) == 0 {
			t.Fatal("wrong-typed field was not rejected")
		}
	})
	t.Run("missing_required", func(t *testing.T) {
		doc := map[string]any{}
		for k, v := range base {
			doc[k] = v
		}
		delete(doc, "findings")
		if errs := validateSchema(t, schema, doc); len(errs) == 0 {
			t.Fatal("missing required field was not rejected")
		}
	})
	t.Run("bad_enum", func(t *testing.T) {
		doc := map[string]any{}
		for k, v := range base {
			doc[k] = v
		}
		doc["findings"] = []any{map[string]any{
			"rule": "D001", "severity": "fatal", "file": "a.yml", "line": 1.0, "message": "m",
		}}
		if errs := validateSchema(t, schema, doc); len(errs) == 0 {
			t.Fatal("out-of-enum severity was not rejected")
		}
	})
}

// Every $ref in every schema must resolve to a $defs entry.
func TestSchemaRefsResolve(t *testing.T) {
	for _, doc := range SchemaDocs() {
		s := loadSchema(t, doc)
		defs, _ := s["$defs"].(map[string]any)
		var walk func(node any)
		var missing []string
		walk = func(node any) {
			switch n := node.(type) {
			case map[string]any:
				if ref, ok := n["$ref"].(string); ok {
					name := strings.TrimPrefix(ref, "#/$defs/")
					if _, ok := defs[name]; !ok {
						missing = append(missing, doc.Name+": "+ref)
					}
				}
				for _, v := range n {
					walk(v)
				}
			case []any:
				for _, v := range n {
					walk(v)
				}
			}
		}
		walk(s)
		if len(missing) > 0 {
			t.Fatalf("unresolved $refs: %v", missing)
		}
	}
}
