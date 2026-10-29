package model_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/model"
)

// verdicts runs the four validators over one document: the writer schema, model.Validate, the reader schema and
// model.ValidateForRead. A true is an acceptance.
func verdicts(t *testing.T, data []byte) (writerSchema, validate, readerSchema, validateForRead bool) {
	t.Helper()
	ws, rs := compile(t, ""), compileReader(t)
	return ws.Validate(decode(t, data)) == nil, model.Validate(data) == nil,
		rs.Validate(decode(t, data)) == nil, model.ValidateForRead(data) == nil
}

// The four validators agree on every fixture, and on the answer the fixture's directory promises.
func TestFourValidatorsAgreeOnEveryFixture(t *testing.T) {
	// The only fixtures a reader may accept although a writer refuses them: an unknown field outside a finding.
	readerOnly := map[string]bool{"forward/newer-v1-optional-field.json": true, "invalid/unknown-top-level-field.json": true}
	n := 0
	for _, pat := range []string{"valid/*.json", "invalid/*.json", "forward/*.json"} {
		for _, f := range files(t, pat) {
			if filepath.Base(f) == "EXPECT.json" {
				continue
			}
			name := filepath.Base(filepath.Dir(f)) + "/" + filepath.Base(f)
			ws, v, rs, vr := verdicts(t, readFile(t, f))
			n++
			wantWriter := strings.HasPrefix(name, "valid/")
			wantReader := wantWriter || readerOnly[name]
			if ws != wantWriter || v != wantWriter || rs != wantReader || vr != wantReader {
				t.Errorf("%s: writer schema=%v Validate=%v reader schema=%v ValidateForRead=%v; want writer=%v reader=%v",
					name, ws, v, rs, vr, wantWriter, wantReader)
			}
		}
	}
	t.Logf("the four validators agree on %d fixtures", n)
}

// Every path field refuses a leading separator, a drive letter and a parent segment under all four validators, and
// accepts the plain relative paths that merely look similar.
func TestPathFieldsRefuseEscapesUnderAllFour(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"a/b.go", true}, {".github/workflows/ci.yml", true}, {"..a", true}, {"a..b", true}, {"...", true}, {"a.", true}, {"x/.hidden", true},
		{"/etc/passwd", false}, {`\share\x`, false}, {"C:/Users/x", false}, {"c:x", false}, {"..", false}, {"../x", false},
		{"a/../b", false}, {"a/..", false}, {`a\..\b`, false}, {"a//b", false}, {"a/", false}, {"", false},
	} {
		data := mutate(t, "valid/two-repos.json", func(d map[string]any) {
			repo0(d)["entry_points"].([]any)[0].(map[string]any)["source"].(map[string]any)["file"] = tc.path
		})
		ws, v, rs, vr := verdicts(t, data)
		if ws != tc.ok || v != tc.ok || rs != tc.ok || vr != tc.ok {
			t.Errorf("source.file %q: writer schema=%v Validate=%v reader schema=%v ValidateForRead=%v; want all %v", tc.path, ws, v, rs, vr, tc.ok)
		}
	}
}
