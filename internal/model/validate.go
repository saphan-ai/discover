package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxTodo caps the TODO/FIXME findings of one repository.
const maxTodo = 30

// Problem is one refusal: the JSON pointer of the field or object that broke a rule, and what rule.
type Problem struct {
	Path    string
	Message string
}

// Error is what Validate returns: every problem found, in document order.
type Error struct {
	Problems []Problem
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "discover report refused: %d problem(s)", len(e.Problems))
	for i, p := range e.Problems {
		if i == 10 {
			fmt.Fprintf(&b, "\n  ... and %d more", len(e.Problems)-i)
			break
		}
		path := p.Path
		if path == "" {
			path = "(document)"
		}
		fmt.Fprintf(&b, "\n  %s: %s", path, p.Message)
	}
	return b.String()
}

// Validate is the writer's check: it refuses a document that is not a valid discover/v1 report. It reads JSON,
// enforces the schema's rules (required fields, no unknown field anywhere, enums, constants, patterns, counts)
// and returns *Error. It is what discover runs before it writes a report.
func Validate(data []byte) error {
	var r Report
	return validate(data, &r, true)
}

// ValidateForRead is the reader's check, for a report some other v1 writer may have produced. It enforces every
// rule Validate does except one: an unknown field is ignored everywhere but inside a finding, which stays closed
// so that no later file can carry a secret's value in a field this reader would pass through.
func ValidateForRead(data []byte) error {
	var r Report
	return validate(data, &r, false)
}

// ValidateProfile does the same as Validate for a bare workspace profile (testdata/schema/profile-v1.json).
func ValidateProfile(data []byte) error {
	var p WorkspaceProfile
	return validate(data, &p, true)
}

// Marshal validates the report and returns its JSON, indented. A nil slice is written as an empty array.
func Marshal(r *Report) ([]byte, error) {
	fillNil(reflect.ValueOf(r).Elem())
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := Validate(out); err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func validate(data []byte, dst any, strict bool) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return &Error{Problems: []Problem{{Message: "not valid JSON: " + err.Error()}}}
	}
	if _, err := dec.Token(); err != io.EOF {
		return &Error{Problems: []Problem{{Message: "not valid JSON: data after the document"}}}
	}
	var problems []Problem
	walk(doc, reflect.TypeOf(dst).Elem(), "", "", strict, &problems)
	if len(problems) > 0 {
		return &Error{Problems: problems}
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return &Error{Problems: []Problem{{Message: "cannot read into the report types: " + err.Error()}}}
	}
	check(reflect.ValueOf(dst).Elem(), "", &problems)
	if len(problems) > 0 {
		return &Error{Problems: problems}
	}
	return nil
}

type fieldInfo struct {
	index     int
	name      string
	omitempty bool
}

func fieldsOf(t reflect.Type) []fieldInfo {
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		parts := strings.Split(t.Field(i).Tag.Get("json"), ",")
		if parts[0] == "" || parts[0] == "-" {
			continue
		}
		out = append(out, fieldInfo{index: i, name: parts[0], omitempty: len(parts) > 1 && parts[1] == "omitempty"})
	}
	return out
}

func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	}
	return "unknown"
}

// walk checks the decoded document v against Go type t: shape, required fields, unknown fields and value rules.
// tag is the struct tag of the field that holds v; slices hand it down to their elements. closed says whether an
// unknown field is refused; it is always true for a writer, and for a reader once inside a finding.
func walk(v any, t reflect.Type, tag reflect.StructTag, path string, closed bool, out *[]Problem) {
	add := func(msg string, a ...any) { *out = append(*out, Problem{Path: path, Message: fmt.Sprintf(msg, a...)}) }
	if t.Kind() == reflect.Ptr {
		if v == nil {
			return
		}
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			add("expected an object, got %s", kindOf(v))
			return
		}
		closed = closed || t == reflect.TypeOf(Finding{})
		known := map[string]bool{}
		for _, f := range fieldsOf(t) {
			known[f.name] = true
			sf := t.Field(f.index)
			fv, present := m[f.name]
			if !present {
				if !f.omitempty {
					*out = append(*out, Problem{Path: path + "/" + f.name, Message: "required field " + f.name + " is missing"})
				}
				continue
			}
			if fv == nil && f.omitempty && sf.Type.Kind() == reflect.Ptr {
				*out = append(*out, Problem{Path: path + "/" + f.name, Message: f.name + " is optional but must not be null; leave it out"})
				continue
			}
			walk(fv, sf.Type, sf.Tag, path+"/"+f.name, closed, out)
		}
		if !closed {
			return
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !known[k] {
				*out = append(*out, Problem{Path: path + "/" + k, Message: "unknown field " + k + "; a writer never writes it, and a reader refuses it inside a finding"})
			}
		}
	case reflect.Map:
		if _, ok := v.(map[string]any); !ok {
			add("expected an object, got %s", kindOf(v))
		}
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			add("expected an array, got %s", kindOf(v))
			return
		}
		if n, ok := intTag(tag, "minItems"); ok && len(a) < n {
			add("needs at least %d item(s), has %d", n, len(a))
		}
		if n, ok := intTag(tag, "maxItems"); ok && len(a) > n {
			add("allows at most %d item(s), has %d", n, len(a))
		}
		for i, e := range a {
			walk(e, t.Elem(), tag, path+"/"+strconv.Itoa(i), closed, out)
		}
	case reflect.String:
		s, ok := v.(string)
		if !ok {
			add("expected a string, got %s", kindOf(v))
			return
		}
		checkString(s, tag, add)
	case reflect.Int, reflect.Int64:
		n, ok := v.(json.Number)
		if !ok {
			add("expected an integer, got %s", kindOf(v))
			return
		}
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(n.String(), 64)
			if ferr != nil || f != float64(int64(f)) {
				add("expected an integer, got %s", n.String())
				return
			}
			i = int64(f)
		}
		if min, ok := intTag(tag, "min"); ok && i < int64(min) {
			add("must be at least %d, got %d", min, i)
		}
		if max, ok := intTag(tag, "max"); ok && i > int64(max) {
			add("must be at most %d, got %d", max, i)
		}
	case reflect.Bool:
		b, ok := v.(bool)
		if !ok {
			add("expected a boolean, got %s", kindOf(v))
			return
		}
		if c, ok := tag.Lookup("const"); ok && strconv.FormatBool(b) != c {
			add("must be %s", c)
		}
	default:
		add("unsupported type %s", t)
	}
}

func intTag(tag reflect.StructTag, name string) (int, bool) {
	s, ok := tag.Lookup(name)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func checkString(s string, tag reflect.StructTag, add func(string, ...any)) {
	if c, ok := tag.Lookup("const"); ok && s != c {
		add("must be %q, got %q", c, s)
	}
	if e, ok := tag.Lookup("enum"); ok {
		allowed := strings.Split(e, "|")
		found := false
		for _, a := range allowed {
			found = found || a == s
		}
		if !found {
			add("must be one of %s, got %q", strings.Join(allowed, ", "), s)
		}
	}
	if n, ok := intTag(tag, "minLen"); ok && len([]rune(s)) < n {
		add("must not be empty")
	}
	if p, ok := tag.Lookup("pattern"); ok && !regexp.MustCompile(p).MatchString(s) {
		add("does not match the pattern %s", p)
	}
	if f, ok := tag.Lookup("format"); ok && f == "date-time" {
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			add("must be an RFC 3339 date-time, got %q", s)
		}
	}
}

// check runs the rules that span several fields on the typed value. Each reports at the path of its object.
func check(v reflect.Value, path string, out *[]Problem) {
	add := func(msg string) { *out = append(*out, Problem{Path: path, Message: msg}) }
	switch v.Kind() {
	case reflect.Struct:
		switch x := v.Interface().(type) {
		case Source:
			if x.Key == "" && x.Line == 0 {
				add("a source needs key or line besides file")
			}
		case StackRow:
			if x.Version == nil && x.VersionNote == "" {
				add(`version is null, so version_note is required and must be "not pinned"`)
			}
			if x.Version == nil && x.VersionNote != "" && x.VersionNote != "not pinned" {
				*out = append(*out, Problem{Path: path + "/version_note", Message: `must be "not pinned" when version is null`})
			}
			if x.Version != nil && x.VersionNote != "" {
				add("version_note is only allowed when version is null")
			}
		case Finding:
			switch {
			case x.Kind == "secret" && x.SecretType == "":
				add("a secret finding needs secret_type")
			case x.Kind == "secret" && x.Text != "":
				add("a secret finding must not carry text; only its location and secret_type")
			case x.Kind != "secret" && x.SecretType != "":
				add("secret_type is only allowed on a finding of kind secret")
			}
		case Attestation:
			switch {
			case x.Mode == "git" && x.Git == nil:
				*out = append(*out, Problem{Path: path + "/git", Message: "mode is git, so git is required"})
			case x.Mode != "git" && x.Git != nil:
				*out = append(*out, Problem{Path: path + "/git", Message: "git is only allowed when mode is git"})
			case x.Mode != "git" && x.Untracked != nil:
				*out = append(*out, Problem{Path: path + "/untracked", Message: "untracked is only allowed when mode is git"})
			}
		case Repo:
			n := 0
			for _, f := range x.Findings {
				if f.Kind == "todo" {
					n++
				}
			}
			if n > maxTodo {
				*out = append(*out, Problem{Path: path + "/findings", Message: fmt.Sprintf("allows at most %d todo findings, has %d", maxTodo, n)})
			}
		}
		for _, f := range fieldsOf(v.Type()) {
			check(v.Field(f.index), path+"/"+f.name, out)
		}
	case reflect.Ptr:
		if !v.IsNil() {
			check(v.Elem(), path, out)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			check(v.Index(i), path+"/"+strconv.Itoa(i), out)
		}
	}
}

// fillNil replaces every nil slice with an empty one, so that it is written as [] and not null.
func fillNil(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fillNil(v.Field(i))
		}
	case reflect.Slice:
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		}
		for i := 0; i < v.Len(); i++ {
			fillNil(v.Index(i))
		}
	}
}
