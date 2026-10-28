package model_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/saphan-ai/discover/internal/model"
)

const (
	schemaPath  = "../../schema/discover-v1.schema.json"
	schemaID    = "https://github.com/saphan-ai/discover/schema/discover-v1.schema.json"
	schemaTests = "../../testdata/schema"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func compile(t *testing.T, fragment string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	c.AssertFormat = true
	if err := c.AddResource(schemaID, bytes.NewReader(readFile(t, schemaPath))); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(schemaID + fragment)
	if err != nil {
		t.Fatalf("the schema does not compile: %v", err)
	}
	return s
}

func decode(t *testing.T, data []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// canonical re-encodes any JSON document with sorted keys, so two documents compare by content.
func canonical(t *testing.T, data []byte) string {
	t.Helper()
	b, err := json.Marshal(decode(t, data))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type leaf struct{ loc, msg string }

func leaves(e *jsonschema.ValidationError) []leaf {
	if len(e.Causes) == 0 {
		return []leaf{{e.InstanceLocation, e.Message}}
	}
	var out []leaf
	for _, c := range e.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

func parentAndLast(ptr string) (string, string) {
	i := strings.LastIndex(ptr, "/")
	return ptr[:i], ptr[i+1:]
}

// schemaRefusesAt reports whether the schema refused the document at refusedAt: a refusal located there, or a
// refusal of the parent that names the missing or forbidden key.
func schemaRefusesAt(err error, refusedAt string) bool {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	parent, last := parentAndLast(refusedAt)
	for _, l := range leaves(ve) {
		if l.loc == refusedAt || (l.loc == parent && strings.Contains(l.msg, "'"+last+"'")) {
			return true
		}
	}
	return false
}

// goRefusesAt reports whether model.Validate refused the document with a problem at refusedAt whose text names
// the mutated key.
func goRefusesAt(err error, refusedAt, mutated string) bool {
	var me *model.Error
	if !errors.As(err, &me) {
		return false
	}
	_, last := parentAndLast(mutated)
	for _, p := range me.Problems {
		if p.Path == refusedAt && strings.Contains(p.Path+" "+p.Message, last) {
			return true
		}
	}
	return false
}

// diffLeaves lists the places where a and b differ. A key present on one side only, or an array of another
// length, counts once, at the key or the array.
func diffLeaves(a, b any, path string) []string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return []string{path}
		}
		var out []string
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		for k := range keys {
			xv, xok := x[k]
			yv, yok := y[k]
			if xok != yok {
				out = append(out, path+"/"+k)
				continue
			}
			out = append(out, diffLeaves(xv, yv, path+"/"+k)...)
		}
		sort.Strings(out)
		return out
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return []string{path}
		}
		var out []string
		for i := range x {
			out = append(out, diffLeaves(x[i], y[i], fmt.Sprintf("%s/%d", path, i))...)
		}
		return out
	}
	if !reflect.DeepEqual(a, b) {
		return []string{path}
	}
	return nil
}

func files(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(schemaTests, pattern))
	if err != nil || len(m) == 0 {
		t.Fatalf("no files match %s (err %v)", pattern, err)
	}
	return m
}

func TestValidFilesPassBothAndRoundTrip(t *testing.T) {
	sch := compile(t, "")
	for _, f := range files(t, "valid/*.json") {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data := readFile(t, f)
			if err := sch.Validate(decode(t, data)); err != nil {
				t.Fatalf("the schema refuses a valid file: %v", err)
			}
			if err := model.Validate(data); err != nil {
				t.Fatalf("model.Validate refuses a valid file: %v", err)
			}
			var r model.Report
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			back, err := json.Marshal(&r)
			if err != nil {
				t.Fatal(err)
			}
			if canonical(t, data) != canonical(t, back) {
				t.Fatalf("the Go types do not round-trip %s\n in: %s\nout: %s", f, canonical(t, data), canonical(t, back))
			}
		})
	}
}

func TestProfileFixture(t *testing.T) {
	data := readFile(t, filepath.Join(schemaTests, "profile-v1.json"))
	if err := compile(t, "#/$defs/workspace_profile").Validate(decode(t, data)); err != nil {
		t.Fatalf("the schema refuses profile-v1.json: %v", err)
	}
	if err := model.ValidateProfile(data); err != nil {
		t.Fatalf("model.ValidateProfile refuses profile-v1.json: %v", err)
	}
	var p model.WorkspaceProfile
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	back, _ := json.Marshal(&p)
	if canonical(t, data) != canonical(t, back) {
		t.Fatal("the profile does not round-trip")
	}
	// The profile inside every valid report is the same shape.
	var r model.Report
	if err := json.Unmarshal(readFile(t, filepath.Join(schemaTests, "valid/two-repos.json")), &r); err != nil {
		t.Fatal(err)
	}
	one, _ := json.Marshal(r.WorkspaceProfile)
	if err := model.ValidateProfile(one); err != nil {
		t.Fatal(err)
	}
}

type expectation struct {
	Base      string `json:"base"`
	Mutated   string `json:"mutated"`
	RefusedAt string `json:"refused_at"`
}

func TestInvalidFilesRefusedByBothNamingTheField(t *testing.T) {
	sch := compile(t, "")
	var exp map[string]expectation
	if err := json.Unmarshal(readFile(t, filepath.Join(schemaTests, "invalid/EXPECT.json")), &exp); err != nil {
		t.Fatal(err)
	}
	onDisk := files(t, "invalid/*.json")
	seen := 0
	for _, f := range onDisk {
		name := filepath.Base(f)
		if name == "EXPECT.json" {
			continue
		}
		seen++
		e, ok := exp[name]
		if !ok {
			t.Errorf("%s has no entry in EXPECT.json", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			data := readFile(t, f)
			base := readFile(t, filepath.Join(schemaTests, e.Base))
			if d := diffLeaves(decode(t, base), decode(t, data), ""); len(d) != 1 || d[0] != e.Mutated {
				t.Fatalf("an invalid file mutates exactly one field, %s; it differs from %s at %v", e.Mutated, e.Base, d)
			}
			if err := sch.Validate(decode(t, data)); err == nil {
				t.Fatal("the schema accepts it")
			} else if !schemaRefusesAt(err, e.RefusedAt) {
				t.Fatalf("the schema refuses it, but not at %s: %v", e.RefusedAt, err)
			}
			if err := model.Validate(data); err == nil {
				t.Fatal("model.Validate accepts it")
			} else if !goRefusesAt(err, e.RefusedAt, e.Mutated) {
				t.Fatalf("model.Validate refuses it, but not at %s naming %s: %v", e.RefusedAt, e.Mutated, err)
			}
		})
	}
	if seen != len(exp) {
		t.Errorf("EXPECT.json lists %d files, %d are on disk", len(exp), seen)
	}
	for _, name := range []string{
		"secret-finding-has-value.json", "command-executed-true.json", "stack-row-no-source.json",
		"narrative-claim-empty-citations.json", "schema-version-v2.json",
	} {
		if _, ok := exp[name]; !ok {
			t.Errorf("the required invalid file %s is missing", name)
		}
	}
	t.Logf("%d invalid files, each refused by the schema and by model.Validate", seen)
}

// The harness above must be able to fail: it must reject a wrong location and a two-field mutation.
func TestHarnessCanFail(t *testing.T) {
	sch := compile(t, "")
	data := readFile(t, filepath.Join(schemaTests, "invalid/command-executed-true.json"))
	if schemaRefusesAt(sch.Validate(decode(t, data)), "/repos/0/commands/1/executed") {
		t.Error("the schema matcher accepts a wrong location")
	}
	if goRefusesAt(model.Validate(data), "/repos/0/commands/1/executed", "/repos/0/commands/1/executed") {
		t.Error("the Go matcher accepts a wrong location")
	}
	if schemaRefusesAt(nil, "/x") || goRefusesAt(nil, "/x", "/x") {
		t.Error("a nil error counts as a refusal")
	}
	base := decode(t, readFile(t, filepath.Join(schemaTests, "valid/two-repos.json")))
	two := decode(t, readFile(t, filepath.Join(schemaTests, "invalid/tool-no-commit.json")))
	two.(map[string]any)["schema_version"] = "discover/v2"
	if d := diffLeaves(base, two, ""); len(d) != 2 {
		t.Errorf("the one-field diff counts %v for a two-field mutation", d)
	}
}

func TestValidatorsAgreeOnTodoCap(t *testing.T) {
	sch := compile(t, "")
	var r model.Report
	if err := json.Unmarshal(readFile(t, filepath.Join(schemaTests, "valid/two-repos.json")), &r); err != nil {
		t.Fatal(err)
	}
	todo := model.Finding{Kind: "todo", Severity: "info", Source: model.Source{File: "a.go", Line: 1}, Text: "TODO: x"}
	for _, tc := range []struct {
		todos int
		ok    bool
	}{{30, true}, {31, false}} {
		r.Repos[0].Findings = nil
		for i := 0; i < tc.todos; i++ {
			r.Repos[0].Findings = append(r.Repos[0].Findings, todo)
		}
		data, _ := json.Marshal(&r)
		sErr, mErr := sch.Validate(decode(t, data)), model.Validate(data)
		if (sErr == nil) != tc.ok || (mErr == nil) != tc.ok {
			t.Errorf("%d todo findings: want ok=%v, schema err=%v, model err=%v", tc.todos, tc.ok, sErr, mErr)
		}
		if !tc.ok && !goRefusesAt(mErr, "/repos/0/findings", "/repos/0/findings") {
			t.Errorf("the cap refusal is not at /repos/0/findings: %v", mErr)
		}
	}
}

func TestMarshalWritesEmptyArraysAndRefusesInvalid(t *testing.T) {
	var r model.Report
	if err := json.Unmarshal(readFile(t, filepath.Join(schemaTests, "valid/empty-directory.json")), &r); err != nil {
		t.Fatal(err)
	}
	r.Repos[0].UI = nil
	r.Repos[0].Stack = nil
	out, err := model.Marshal(&r)
	if err != nil {
		t.Fatalf("a nil slice should be written as []: %v", err)
	}
	if !bytes.Contains(out, []byte(`"ui": []`)) {
		t.Errorf("no empty ui array in the output")
	}
	r.Tool.Commit = ""
	if _, err := model.Marshal(&r); err == nil {
		t.Error("Marshal wrote a report with an empty tool.commit")
	}
}

func TestValidateRefusesNotJSON(t *testing.T) {
	for name, in := range map[string]string{
		"empty": "", "text": "hello", "trailing": string(readFile(t, filepath.Join(schemaTests, "valid/empty-directory.json"))) + " {}",
		"null": "null", "array": "[]",
	} {
		if err := model.Validate([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// goPaths lists `path type required` for every JSON field the Go type declares.
func goPaths(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		parts := strings.Split(f.Tag.Get("json"), ",")
		req := "required"
		if len(parts) > 1 && parts[1] == "omitempty" {
			req = "optional"
		}
		path := prefix + parts[0]
		ft := f.Type
		typ := goType(ft)
		*out = append(*out, fmt.Sprintf("%s %s %s", path, typ, req))
		goDescend(ft, path, out)
	}
}

func goDescend(ft reflect.Type, path string, out *[]string) {
	switch ft.Kind() {
	case reflect.Struct:
		goPaths(ft, path+".", out)
	case reflect.Slice:
		*out = append(*out, fmt.Sprintf("%s[] %s", path, goType(ft.Elem())))
		goDescend(ft.Elem(), path+"[]", out)
	}
}

func goType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Ptr:
		return "null|" + goType(t.Elem())
	case reflect.Struct, reflect.Map:
		return "object"
	case reflect.Slice:
		return "array"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int64:
		return "integer"
	case reflect.Bool:
		return "boolean"
	}
	return "?" + t.String()
}

type schemaWalker struct {
	defs map[string]any
	out  []string
}

func (w *schemaWalker) resolve(n map[string]any) map[string]any {
	if ref, ok := n["$ref"].(string); ok {
		return w.defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
	}
	return n
}

func schemaType(n map[string]any) string {
	switch ty := n["type"].(type) {
	case string:
		return ty
	case []any:
		var s []string
		for _, e := range ty {
			s = append(s, e.(string))
		}
		sort.Strings(s)
		return strings.Join(s, "|")
	}
	var sample any
	if c, ok := n["const"]; ok {
		sample = c
	} else if e, ok := n["enum"].([]any); ok {
		sample = e[0]
	}
	switch sample.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	}
	return "?"
}

func (w *schemaWalker) object(n map[string]any, prefix string) {
	props, _ := n["properties"].(map[string]any)
	req := map[string]bool{}
	for _, r := range n["required"].([]any) {
		req[r.(string)] = true
	}
	for name, raw := range props {
		p := w.resolve(raw.(map[string]any))
		path := prefix + name
		r := "optional"
		if req[name] {
			r = "required"
		}
		w.out = append(w.out, fmt.Sprintf("%s %s %s", path, schemaType(p), r))
		w.descend(p, path)
	}
}

func (w *schemaWalker) descend(p map[string]any, path string) {
	switch schemaType(p) {
	case "object":
		if _, ok := p["properties"]; ok {
			w.object(p, path+".")
		}
	case "array":
		it := w.resolve(p["items"].(map[string]any))
		w.out = append(w.out, fmt.Sprintf("%s[] %s", path, schemaType(it)))
		w.descend(it, path+"[]")
	}
}

func TestGoTypesAndSchemaDeclareTheSameFields(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(readFile(t, schemaPath), &doc); err != nil {
		t.Fatal(err)
	}
	w := &schemaWalker{defs: doc["$defs"].(map[string]any)}
	w.object(doc, "")
	var goList []string
	goPaths(reflect.TypeOf(model.Report{}), "", &goList)
	sort.Strings(goList)
	sort.Strings(w.out)
	if !reflect.DeepEqual(goList, w.out) {
		only := func(a, b []string) []string {
			in := map[string]bool{}
			for _, s := range b {
				in[s] = true
			}
			var d []string
			for _, s := range a {
				if !in[s] {
					d = append(d, s)
				}
			}
			return d
		}
		t.Fatalf("the Go types and the schema drifted\n only in Go: %v\n only in schema: %v", only(goList, w.out), only(w.out, goList))
	}
	if len(goList) < 100 {
		t.Fatalf("only %d field paths compared; the walker is not reaching the nested objects", len(goList))
	}
	t.Logf("%d field paths (name, type, required) are equal in the Go types and the schema", len(goList))
}
