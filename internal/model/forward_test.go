package model_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/saphan-ai/discover/internal/model"
)

const (
	readerSchemaPath = "../../schema/discover-v1.reader.schema.json"
	readerSchemaID   = "https://github.com/saphan-ai/discover/schema/discover-v1.reader.schema.json"
)

// stripClosed removes `additionalProperties: false` from every object schema under v.
func stripClosed(v any) {
	switch x := v.(type) {
	case map[string]any:
		if ap, ok := x["additionalProperties"]; ok && ap == false {
			delete(x, "additionalProperties")
		}
		for _, c := range x {
			stripClosed(c)
		}
	case []any:
		for _, c := range x {
			stripClosed(c)
		}
	}
}

const defRef = "#/$defs/"

// refsIn adds to out the name of every def that v refers to by `$ref`.
func refsIn(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok && strings.HasPrefix(ref, defRef) {
			out[strings.TrimPrefix(ref, defRef)] = true
		}
		for _, c := range x {
			refsIn(c, out)
		}
	case []any:
		for _, c := range x {
			refsIn(c, out)
		}
	}
}

// reachableDefs returns the defs reachable from the def `from` by following `$ref`s, `from` itself included.
func reachableDefs(defs map[string]any, from string) map[string]bool {
	seen := map[string]bool{from: true}
	for queue := []string{from}; len(queue) > 0; {
		refs := map[string]bool{}
		refsIn(defs[queue[0]], refs)
		queue = queue[1:]
		for r := range refs {
			if !seen[r] {
				seen[r] = true
				queue = append(queue, r)
			}
		}
	}
	return seen
}

// retarget rewrites every `$ref` under v whose def name is in names to the closed_ copy of that def.
func retarget(v any, names map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok && names[strings.TrimPrefix(ref, defRef)] {
			x["$ref"] = defRef + "closed_" + strings.TrimPrefix(ref, defRef)
		}
		for _, c := range x {
			retarget(c, names)
		}
	case []any:
		for _, c := range x {
			retarget(c, names)
		}
	}
}

// readerSchemaFrom derives the reader's schema from the writer's: every object stops refusing unknown
// properties, except a finding and every def reachable from it, which stay closed. A reachable def is kept closed
// as a closed_ copy that only the finding side refers to, so the same def elsewhere is open. The reader file is
// never edited by hand.
func readerSchemaFrom(t *testing.T, writer []byte) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(writer, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	finding := defs["finding"].(map[string]any)
	if finding["additionalProperties"] != false {
		t.Fatal("the writer schema does not close a finding")
	}
	reach := reachableDefs(defs, "finding")
	delete(reach, "finding")
	copies := map[string]any{}
	for name := range reach {
		var c any
		b, _ := json.Marshal(defs[name])
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		copies["closed_"+name] = c
	}
	retarget(finding, reach)
	for _, c := range copies {
		retarget(c, reach)
	}
	delete(finding, "additionalProperties")
	stripClosed(doc)
	finding["additionalProperties"] = false
	for name, c := range copies {
		defs[name] = c
	}
	doc["$id"] = readerSchemaID
	doc["title"] = "discover report, schema v1, reader variant"
	doc["$comment"] = "Generated from discover-v1.schema.json by a test in internal/model; never edited by hand. " +
		"A v1 reader ignores unknown properties everywhere except inside a finding."
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func compileReader(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	c.AssertFormat = true
	if err := c.AddResource(readerSchemaID, bytes.NewReader(readFile(t, readerSchemaPath))); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(readerSchemaID)
	if err != nil {
		t.Fatalf("the reader schema does not compile: %v", err)
	}
	return s
}

var updateReaderSchema = flag.Bool("update-reader-schema", false, "rewrite schema/discover-v1.reader.schema.json from the writer schema")

// The reader schema file must equal what the writer schema generates. -update-reader-schema rewrites it.
func TestReaderSchemaIsGeneratedFromWriterSchema(t *testing.T) {
	want := readerSchemaFrom(t, readFile(t, schemaPath))
	if *updateReaderSchema {
		if err := os.WriteFile(readerSchemaPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, readerSchemaPath); !bytes.Equal(got, want) {
		t.Fatalf("schema/discover-v1.reader.schema.json drifted from schema/discover-v1.schema.json; " +
			"regenerate it: go test -count=1 -run TestReaderSchemaIsGenerated ./internal/model/ -args -update-reader-schema")
	}
}

// closureProblems checks a reader schema document: the finding and every def reachable from it are closed, nothing
// else is, and no def reachable from the finding is also reachable from outside it by the same name.
func closureProblems(doc map[string]any) []string {
	var out []string
	defs := doc["$defs"].(map[string]any)
	closedSet := reachableDefs(defs, "finding")
	for name, d := range defs {
		_, closed := d.(map[string]any)["additionalProperties"]
		if closed != closedSet[name] {
			out = append(out, fmt.Sprintf("def %s: closed=%v, reachable from a finding=%v", name, closed, closedSet[name]))
		}
	}
	if _, ok := doc["additionalProperties"]; ok {
		out = append(out, "the top level is closed")
	}
	return out
}

// The generator must be able to fail: a closed object that is not reachable from a finding must come out open.
func TestReaderSchemaGeneratorOpensEverythingButFindings(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(readFile(t, readerSchemaPath), &doc); err != nil {
		t.Fatal(err)
	}
	if p := closureProblems(doc); len(p) > 0 {
		t.Errorf("the reader schema: %v", p)
	}
	if !reachableDefs(doc["$defs"].(map[string]any), "finding")["closed_source"] {
		t.Error("closed_source is not reachable from the finding in the reader schema")
	}
}

// A finding that one day refers to a def of its own, which refers to another, keeps both closed for a reader, and
// the check above sees a copy that was left open.
func TestReaderSchemaClosesEveryDefReachableFromFinding(t *testing.T) {
	var writer map[string]any
	if err := json.Unmarshal(readFile(t, schemaPath), &writer); err != nil {
		t.Fatal(err)
	}
	defs := writer["$defs"].(map[string]any)
	defs["finding_detail"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"origin"},
		"properties": map[string]any{"origin": map[string]any{"$ref": "#/$defs/source"}},
	}
	defs["finding"].(map[string]any)["properties"].(map[string]any)["detail"] = map[string]any{"$ref": "#/$defs/finding_detail"}
	in, _ := json.Marshal(writer)

	var reader map[string]any
	if err := json.Unmarshal(readerSchemaFrom(t, in), &reader); err != nil {
		t.Fatal(err)
	}
	if p := closureProblems(reader); len(p) > 0 {
		t.Fatalf("the generated reader schema: %v", p)
	}
	rdefs := reader["$defs"].(map[string]any)
	reach := reachableDefs(rdefs, "finding")
	for _, want := range []string{"closed_finding_detail", "closed_source"} {
		if !reach[want] {
			t.Errorf("%s is not reachable from the finding", want)
		}
	}
	if reach["finding_detail"] {
		t.Error("the open finding_detail is reachable from the finding")
	}

	// Negative control: leave exactly one reachable def open, and the check names it.
	delete(rdefs["closed_finding_detail"].(map[string]any), "additionalProperties")
	if p := closureProblems(reader); len(p) != 1 || !strings.Contains(p[0], "closed_finding_detail") {
		t.Errorf("an open def reachable from a finding was not named: %v", p)
	}
}

func mutate(t *testing.T, file string, edit func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readFile(t, filepath.Join(schemaTests, file)), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func repo0(doc map[string]any) map[string]any { return doc["repos"].([]any)[0].(map[string]any) }

// A later v1 writer may add an optional field anywhere except on a finding; a v1 reader must read such a file.
func TestReaderAcceptsNewerOptionalFieldAndRefusesNewFindingField(t *testing.T) {
	optional := readFile(t, filepath.Join(schemaTests, "forward/newer-v1-optional-field.json"))
	onFinding := readFile(t, filepath.Join(schemaTests, "forward/newer-v1-field-on-finding.json"))
	rs := compileReader(t)

	if err := model.ValidateForRead(optional); err != nil {
		t.Fatalf("a v1 reader refuses a newer v1 file with an optional field: %v", err)
	}
	if err := rs.Validate(decode(t, optional)); err != nil {
		t.Fatalf("the reader schema refuses it too: %v", err)
	}

	err := model.ValidateForRead(onFinding)
	var me *model.Error
	if !errors.As(err, &me) || !goRefusesAt(err, "/repos/0/findings/0/secret_value_hint", "/repos/0/findings/0/secret_value_hint") {
		t.Fatalf("a v1 reader must refuse the new finding field, naming it: %v", err)
	}
	if !schemaRefusesAt(rs.Validate(decode(t, onFinding)), "/repos/0/findings/0/secret_value_hint") {
		t.Fatalf("the reader schema must refuse the new finding field")
	}

	// A new field inside a finding's source is a new field on the finding.
	deep := mutate(t, "valid/two-repos.json", func(d map[string]any) {
		f := repo0(d)["findings"].([]any)[0].(map[string]any)
		f["source"].(map[string]any)["excerpt"] = "x"
	})
	if err := model.ValidateForRead(deep); !goRefusesAt(err, "/repos/0/findings/0/source/excerpt", "/repos/0/findings/0/source/excerpt") {
		t.Errorf("a new field inside a finding's source is accepted by the reader: %v", err)
	}
	if err := rs.Validate(decode(t, deep)); err == nil {
		t.Error("the reader schema accepts a new field inside a finding's source")
	}
	// The same field on a source elsewhere is open.
	open := mutate(t, "valid/two-repos.json", func(d map[string]any) {
		repo0(d)["entry_points"].([]any)[0].(map[string]any)["source"].(map[string]any)["excerpt"] = "x"
	})
	if err := model.ValidateForRead(open); err != nil {
		t.Errorf("the reader refuses a new field on a source outside a finding: %v", err)
	}
	if err := rs.Validate(decode(t, open)); err != nil {
		t.Errorf("the reader schema refuses a new field on a source outside a finding: %v", err)
	}
}

// A writer never writes an unknown field, so Validate refuses both forward files; it names the field.
func TestWriterRefusesForwardFiles(t *testing.T) {
	sch := compile(t, "")
	for file, field := range map[string]string{
		"forward/newer-v1-optional-field.json":   "/repos/0/complexity_hint",
		"forward/newer-v1-field-on-finding.json": "/repos/0/findings/0/secret_value_hint",
	} {
		data := readFile(t, filepath.Join(schemaTests, file))
		if err := model.Validate(data); !goRefusesAt(err, field, field) {
			t.Errorf("%s: model.Validate does not refuse %s: %v", file, field, err)
		}
		if err := sch.Validate(decode(t, data)); !schemaRefusesAt(err, field) {
			t.Errorf("%s: the writer schema does not refuse %s: %v", file, field, err)
		}
	}
}

// The reader schema and ValidateForRead agree on every fixture there is.
func TestReaderSchemaAndValidateForReadAgree(t *testing.T) {
	rs := compileReader(t)
	n, accepted := 0, 0
	for _, pat := range []string{"valid/*.json", "invalid/*.json", "forward/*.json"} {
		for _, f := range files(t, pat) {
			if filepath.Base(f) == "EXPECT.json" {
				continue
			}
			data := readFile(t, f)
			sErr, mErr := rs.Validate(decode(t, data)), model.ValidateForRead(data)
			if (sErr == nil) != (mErr == nil) {
				t.Errorf("%s: reader schema err=%v, ValidateForRead err=%v", f, sErr, mErr)
			}
			n++
			if mErr == nil {
				accepted++
			}
		}
	}
	// Exactly these are readable: the valid files, the optional-field file, and the one invalid file whose only
	// fault is an unknown top-level field.
	if want := len(files(t, "valid/*.json")) + 2; accepted != want {
		t.Errorf("a reader accepted %d of %d files, want %d", accepted, n, want)
	}
	t.Logf("reader schema and ValidateForRead agree on %d files (%d accepted)", n, accepted)
}

// W2's ATTESTATION.json for one plain-directory target, measured by the master at W2's tip cec64df (2026-10-28).
const w2Attestation = `{
  "schema": "discover-attestation/v1",
  "attestation": {
    "root": "w2-target",
    "mode": "dir",
    "tree": { "files": 2, "bytes": 4, "links": 1, "submodules": 0, "unreadable": 0,
              "digest": "bc943991be0bc4f5cc9090ba3fcb71e5a8bdd952464193a4f2ac397d6a67aff7" }
  },
  "read_only_proof": { "status": "EQUAL",
    "before": "bc943991be0bc4f5cc9090ba3fcb71e5a8bdd952464193a4f2ac397d6a67aff7",
    "after":  "bc943991be0bc4f5cc9090ba3fcb71e5a8bdd952464193a4f2ac397d6a67aff7" }
}`

func TestW2AttestationValidatesUnderBothValidators(t *testing.T) {
	var w2 struct {
		Attestation   map[string]any `json:"attestation"`
		ReadOnlyProof map[string]any `json:"read_only_proof"`
	}
	if err := json.Unmarshal([]byte(w2Attestation), &w2); err != nil {
		t.Fatal(err)
	}
	data := mutate(t, "valid/empty-directory.json", func(d map[string]any) {
		r := repo0(d)
		w2.Attestation["read_only_proof"] = w2.ReadOnlyProof
		w2.Attestation["read_manifest"] = r["attestation"].(map[string]any)["read_manifest"]
		r["attestation"] = w2.Attestation
	})
	sch, rs := compile(t, ""), compileReader(t)
	if err := sch.Validate(decode(t, data)); err != nil {
		t.Errorf("the writer schema refuses W2's attestation: %v", err)
	}
	if err := rs.Validate(decode(t, data)); err != nil {
		t.Errorf("the reader schema refuses W2's attestation: %v", err)
	}
	if err := model.Validate(data); err != nil {
		t.Errorf("model.Validate refuses W2's attestation: %v", err)
	}
	if err := model.ValidateForRead(data); err != nil {
		t.Errorf("model.ValidateForRead refuses W2's attestation: %v", err)
	}
}

// The mode/git/untracked rule has three more shapes than the one-field invalid files show; both validators refuse them.
func TestAttestationModeRuleUnderBothValidators(t *testing.T) {
	sch := compile(t, "")
	tree := map[string]any{"files": 0, "bytes": 0, "links": 0, "submodules": 0, "unreadable": 0, "digest": strings.Repeat("0", 64)}
	gitState := map[string]any{"branch": "main", "detached": false, "head": "abc", "clean": true, "dirty_paths": []any{}}
	att := func(d map[string]any) map[string]any { return repo0(d)["attestation"].(map[string]any) }
	for name, tc := range map[string]struct {
		file string
		edit func(map[string]any)
		ok   bool
	}{
		"dir with git":          {"valid/empty-directory.json", func(d map[string]any) { att(d)["git"] = gitState }, false},
		"dir with untracked":    {"valid/empty-directory.json", func(d map[string]any) { att(d)["untracked"] = tree }, false},
		"git with null git":     {"valid/two-repos.json", func(d map[string]any) { att(d)["git"] = nil }, false},
		"git without untracked": {"valid/two-repos.json", func(d map[string]any) { delete(att(d), "untracked") }, true},
		"git with dirty path":   {"valid/two-repos.json", func(d map[string]any) { att(d)["git"].(map[string]any)["dirty_paths"] = []any{"/etc/x"} }, false},
	} {
		data := mutate(t, tc.file, tc.edit)
		sErr, mErr := sch.Validate(decode(t, data)), model.Validate(data)
		if (sErr == nil) != tc.ok || (mErr == nil) != tc.ok {
			t.Errorf("%s: want ok=%v, schema err=%v, model err=%v", name, tc.ok, sErr, mErr)
		}
	}
}
