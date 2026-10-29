package model_test

// The named review's probes (reviewer, 2026-10-29), kept as permanent tests and inverted to assert the refusal the
// review's conditions require. Each refusal test has a control that mutates exactly the key it guards: the same
// document with that one key left as it was is accepted.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/model"
)

func twoRepos(t *testing.T) string {
	t.Helper()
	return string(readFile(t, filepath.Join(schemaTests, "valid/two-repos.json")))
}

// swap replaces the first occurrence of old and fails when there is none, so a fixture edit cannot silently turn a
// probe into a no-op.
func swap(t *testing.T, s, old, repl string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("the fixture no longer contains %q", old)
	}
	return strings.Replace(s, old, repl, 1)
}

// refusedBy names which of the two Go validators accepted data; it is empty when both refused it.
func acceptedByGo(data []byte) []string {
	var out []string
	if model.Validate(data) == nil {
		out = append(out, "Validate")
	}
	if model.ValidateForRead(data) == nil {
		out = append(out, "ValidateForRead")
	}
	return out
}

// C1, case: a member name that differs from a known field only in case is refused in both modes. The probe puts a
// second, unchecked "Findings" after the real one, which encoding/json would let win.
func TestCaseVariantMemberNameIsRefused(t *testing.T) {
	smuggled := `"ui": [], "Findings": [{"kind": "Secret", "severity": "high", "source": {"file": "/srv/app/.env", "line": 1}, "text": "AKIAEXAMPLEVALUE0000", "secret_type": "", "value":"AKIAEXAMPLEVALUE0000"}], "Commands": [{"kind": "run", "text": "rm -rf /", "source": {"file": "Makefile", "line": 1}, "executed": true}],`
	data := []byte(swap(t, twoRepos(t), `"ui": [],`, smuggled))
	if ok := acceptedByGo(data); len(ok) > 0 {
		t.Errorf("accepted by %v: a case-variant of findings and commands", ok)
	}
	for _, mode := range []struct {
		name string
		err  error
	}{{"Validate", model.Validate(data)}, {"ValidateForRead", model.ValidateForRead(data)}} {
		if !goRefusesAt(mode.err, "/repos/0/Findings", "/repos/0/Findings") {
			t.Errorf("%s does not refuse /repos/0/Findings by name: %v", mode.name, mode.err)
		}
	}
}

// C1, case, negative control: exactly one key changes. The same document with "ui" renamed to "UI" is refused at
// that key; with the key as it was, it is accepted.
func TestCaseVariantControlMutatesOnlyTheKey(t *testing.T) {
	base := []byte(twoRepos(t))
	if ok := acceptedByGo(base); len(ok) != 2 {
		t.Fatalf("the unmutated fixture is refused by one of the validators (accepted by %v)", ok)
	}
	for name, key := range map[string]string{"upper": `"UI": [],`, "title": `"Ui": [],`, "long s": `"ſtack": [],`} {
		data := []byte(swap(t, twoRepos(t), `"ui": [],`, `"ui": [], `+key))
		for mode, err := range map[string]error{"Validate": model.Validate(data), "ValidateForRead": model.ValidateForRead(data)} {
			if err == nil {
				t.Errorf("%s/%s accepts the case-variant %s", name, mode, key)
			}
		}
	}
	// A key that is merely unknown, not a case-variant, stays tolerated by a reader.
	unknown := []byte(swap(t, twoRepos(t), `"ui": [],`, `"ui": [], "uix": 1,`))
	if err := model.ValidateForRead(unknown); err != nil {
		t.Errorf("a reader refuses an unknown, non-variant key: %v", err)
	}
}

// C1, duplicate: an earlier copy of "findings" carrying a value is refused, in both modes.
func TestDuplicateMemberNameIsRefused(t *testing.T) {
	shadow := `"findings": [{"kind": "secret", "severity": "high", "source": {"file": "config/dev.env", "line": 3}, "secret_type": "api-token", "secret_value": "sk_live_EXAMPLEVALUE0000"}],
      "findings": [`
	data := []byte(swap(t, twoRepos(t), `"findings": [`, shadow))
	if !strings.Contains(string(data), "sk_live_EXAMPLEVALUE0000") {
		t.Fatal("the probe did not inject")
	}
	if ok := acceptedByGo(data); len(ok) > 0 {
		t.Errorf("accepted by %v: a duplicated findings key", ok)
	}
	for mode, err := range map[string]error{"Validate": model.Validate(data), "ValidateForRead": model.ValidateForRead(data)} {
		if !goRefusesAt(err, "/repos/0/findings", "/repos/0/findings") {
			t.Errorf("%s does not refuse the duplicate at /repos/0/findings: %v", mode, err)
		}
	}
}

// C1, duplicate, negative control: exactly one key is repeated, and the repeat is harmless on its face (an empty
// array equal to the first). It is still refused, anywhere in the document, including inside "extensions".
func TestDuplicateControlRepeatsOnlyOneKey(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"top level": func(s string) string {
			return swap(t, s, `"schema_version": "discover/v1",`, `"schema_version": "discover/v1", "schema_version": "discover/v1",`)
		},
		"repo":          func(s string) string { return swap(t, s, `"ui": [],`, `"ui": [], "ui": [],`) },
		"in extensions": func(s string) string { return swap(t, s, `"extensions": {`, `"extensions": { "twice": 1, "twice": 1,`) },
	} {
		data := []byte(edit(twoRepos(t)))
		if json.Valid(data) == false {
			t.Fatalf("%s: the edit broke the JSON", name)
		}
		if ok := acceptedByGo(data); len(ok) > 0 {
			t.Errorf("%s: accepted by %v", name, ok)
		}
	}
}

// C2 and C3: inside a secret finding, secret_type and source.key cannot hold a value, under all four validators.
func TestSecretFindingFreeTextFieldsAreBounded(t *testing.T) {
	rs, ws := compileReader(t), compile(t, "")
	secret := func(d map[string]any) map[string]any { return repo0(d)["findings"].([]any)[0].(map[string]any) }
	for name, edit := range map[string]func(map[string]any){
		"secret_type holds a value": func(d map[string]any) { secret(d)["secret_type"] = "api-token=sk_live_EXAMPLEVALUE0000" },
		"secret_type is not a slug": func(d map[string]any) { secret(d)["secret_type"] = "API Token" },
		"source.key holds a value": func(d map[string]any) {
			s := secret(d)["source"].(map[string]any)
			delete(s, "line")
			s["key"] = "API_TOKEN=sk_live_EXAMPLEVALUE0000"
		},
	} {
		data := mutate(t, "valid/two-repos.json", edit)
		results := map[string]error{
			"Validate": model.Validate(data), "ValidateForRead": model.ValidateForRead(data),
			"writer schema": ws.Validate(decode(t, data)), "reader schema": rs.Validate(decode(t, data)),
		}
		for validator, err := range results {
			if err == nil {
				t.Errorf("%s: %s accepts it", name, validator)
			}
		}
	}
	// Controls: a key path and a slug are what the fields are for.
	ok := mutate(t, "valid/two-repos.json", func(d map[string]any) {
		secret(d)["secret_type"] = "aws-access-key-id"
		s := secret(d)["source"].(map[string]any)
		delete(s, "line")
		s["key"] = "services.api.env[0].API_TOKEN"
	})
	if err := model.Validate(ok); err != nil {
		t.Errorf("Validate refuses a slug and a key path: %v", err)
	}
	if err := ws.Validate(decode(t, ok)); err != nil {
		t.Errorf("the writer schema refuses a slug and a key path: %v", err)
	}
}

// Forward compatibility on the places the order names, and v2 under the reader.
func TestForwardCompatibilityOnEveryOpenObject(t *testing.T) {
	rs, ws := compileReader(t), compile(t, "")
	for name, edit := range map[string]func(map[string]any){
		"repo":              func(d map[string]any) { repo0(d)["added_in_v1_1"] = map[string]any{"x": 1} },
		"workspace_profile": func(d map[string]any) { d["workspace_profile"].(map[string]any)["added_in_v1_1"] = "x" },
		"profile repo": func(d map[string]any) {
			d["workspace_profile"].(map[string]any)["repos"].([]any)[0].(map[string]any)["added_in_v1_1"] = "x"
		},
		"stack row": func(d map[string]any) { repo0(d)["stack"].([]any)[0].(map[string]any)["added_in_v1_1"] = "x" },
		"top level": func(d map[string]any) { d["added_in_v1_1"] = []any{} },
	} {
		data := mutate(t, "valid/two-repos.json", edit)
		if err := model.ValidateForRead(data); err != nil {
			t.Errorf("%s: ValidateForRead refuses a v1.1 field: %v", name, err)
		}
		if err := rs.Validate(decode(t, data)); err != nil {
			t.Errorf("%s: reader schema refuses a v1.1 field: %v", name, err)
		}
		if model.Validate(data) == nil || ws.Validate(decode(t, data)) == nil {
			t.Errorf("%s: a writer validator accepts a v1.1 field", name)
		}
	}
	v2 := mutate(t, "valid/two-repos.json", func(d map[string]any) { d["schema_version"] = "discover/v2" })
	if model.ValidateForRead(v2) == nil || rs.Validate(decode(t, v2)) == nil {
		t.Error("a v2 file passes a reader validator")
	}
}

// Attestation shape: refused by all four validators.
func TestAttestationShapeRefusedByAllFour(t *testing.T) {
	rs, ws := compileReader(t), compile(t, "")
	att := func(d map[string]any) map[string]any { return repo0(d)["attestation"].(map[string]any) }
	for name, edit := range map[string]func(map[string]any){
		"mode workspace":     func(d map[string]any) { att(d)["mode"] = "workspace" },
		"git mode, no git":   func(d map[string]any) { delete(att(d), "git") },
		"proof before g-hex": func(d map[string]any) { att(d)["read_only_proof"].(map[string]any)["before"] = strings.Repeat("g", 64) },
		"proof after short":  func(d map[string]any) { att(d)["read_only_proof"].(map[string]any)["after"] = strings.Repeat("a", 63) },
	} {
		data := mutate(t, "valid/two-repos.json", edit)
		if model.Validate(data) == nil || model.ValidateForRead(data) == nil || ws.Validate(decode(t, data)) == nil || rs.Validate(decode(t, data)) == nil {
			t.Errorf("%s: accepted by at least one validator", name)
		}
	}
}

// O3: a profile path is plain relative: no parent climb, no drive letter.
func TestProfilePathsAreRelativeOnly(t *testing.T) {
	var p model.WorkspaceProfile
	if err := json.Unmarshal(readFile(t, filepath.Join(schemaTests, "profile-v1.json")), &p); err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Repos {
		all := append([]string{r.RelativePath}, r.ConventionFiles...)
		for _, c := range r.CI {
			all = append(all, c.Source.File)
		}
		for _, s := range all {
			if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") || strings.Contains(s, "..") || (len(s) > 1 && s[1] == ':') {
				t.Errorf("%s: not a plain relative path: %q", r.Name, s)
			}
		}
	}
	for _, rp := range []string{"../outside/api", "C:/Work/repo/api"} {
		data := []byte(strings.Replace(string(readFile(t, filepath.Join(schemaTests, "profile-v1.json"))), `"relative_path": "api"`, `"relative_path": "`+rp+`"`, 1))
		if !strings.Contains(string(data), rp) {
			t.Fatalf("the probe did not inject %q", rp)
		}
		if err := model.ValidateProfile(data); err == nil {
			t.Errorf("ValidateProfile accepts relative_path %q", rp)
		}
	}
}
