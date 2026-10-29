package cli

// With several targets the output goes to <out>/<label>. That directory must never be a target or lie inside one.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

func countingHook(t *testing.T) *int {
	t.Helper()
	n := 0
	setHook(t, func(string) { n++ })
	return &n
}

func tree(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.TreeA(t, dir)
	return dir
}

func TestPerTargetOutputDirectoryThatIsATargetIsRefused(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(x, "a"))
	c := tree(t, filepath.Join(temp(t), "c"))
	passes := countingHook(t)
	code, _, stderr := run(t, "--out", x, a, c)
	if code != ExitUsage || !strings.Contains(stderr, "inside") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	for _, f := range []string{manifestFile, attestationFile} {
		if _, err := os.Stat(filepath.Join(a, f)); err == nil {
			t.Errorf("%s was written into the target", f)
		}
	}
	if _, err := os.Stat(filepath.Join(x, "c")); err == nil {
		t.Error("another target's output was written before the refusal")
	}
	if *passes != 0 {
		t.Errorf("%d targets were read before the refusal", *passes)
	}
	if strings.Contains(stderr, x) {
		t.Errorf("the refusal names an absolute path: %q", stderr)
	}
}

// The second target named `a` takes the directory `a-2`, which is the third target.
func TestPerTargetOutputDirectoryTakenByANumberedLabelIsRefused(t *testing.T) {
	x := temp(t)
	first := tree(t, filepath.Join(temp(t), "a"))
	second := tree(t, filepath.Join(temp(t), "a"))
	third := tree(t, filepath.Join(x, "a-2"))
	passes := countingHook(t)
	code, _, stderr := run(t, "--out", x, first, second, third)
	if code != ExitUsage || !strings.Contains(stderr, "inside") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(third, attestationFile)); err == nil {
		t.Error("the attestation was written into a target")
	}
	if *passes != 0 {
		t.Errorf("%d targets were read before the refusal", *passes)
	}
}

func TestPerTargetOutputDirectoriesOutsideEveryTargetAreWritten(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	c := tree(t, filepath.Join(temp(t), "c"))
	code, _, stderr := run(t, "--out", x, a, c)
	if code != ExitOK {
		t.Fatalf("exit %d %q", code, stderr)
	}
	for _, l := range []string{"a", "c"} {
		if _, err := os.Stat(filepath.Join(x, l, attestationFile)); err != nil {
			t.Errorf("no attestation for %s: %v", l, err)
		}
	}
}

func TestAttestationStatesTheScopeTheProofLinePrints(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(dir string)
		scope string
	}{
		{"plain", func(d string) { tree(t, d) }, scopePlain},
		{"git", func(d string) { os.Mkdir(d, 0o755); fixture.GitRepo(t, d) }, scopeGit},
	} {
		base := temp(t)
		root := filepath.Join(base, c.name)
		c.build(root)
		out := filepath.Join(base, "out")
		code, stdout, stderr := run(t, "--out", out, root)
		if code != ExitOK {
			t.Fatalf("%s: exit %d %q", c.name, code, stderr)
		}
		b, err := os.ReadFile(filepath.Join(out, attestationFile))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Proof map[string]any `json:"read_only_proof"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if got := doc.Proof["scope"]; got != c.scope {
			t.Errorf("%s: scope %v, want %q", c.name, got, c.scope)
		}
		if !strings.Contains(stdout, c.scope) {
			t.Errorf("%s: the proof line does not print the same scope: %q", c.name, stdout)
		}
	}
}

// inside compares identities. A second spelling of the same directory, which a comparison of spellings does not
// match, must still be found; a sibling must not.
func TestInsideComparesIdentitiesNotSpellings(t *testing.T) {
	base := temp(t)
	root := filepath.Join(base, "root")
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.Mkdir(filepath.Join(base, "sibling"), 0o755)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		p    string
		want bool
	}{
		{"the root itself", root, true},
		{"through a symlinked parent", filepath.Join(alias, "missing", "out"), true},
		{"through a symlink to a child", filepath.Join(alias, "sub"), true},
		{"with dot-dot segments", root + "/sub/../sub/x", true},
		{"with a dot segment", root + "/./sub", true},
		{"a sibling", filepath.Join(base, "sibling", "out"), false},
		{"the parent", base, false},
		{"a sibling named by dot-dot", root + "/../sibling", false},
	} {
		if got := inside(c.p, root); got != c.want {
			t.Errorf("%s: inside(%q) = %v, want %v", c.name, c.p, got, c.want)
		}
		spelled := c.p == root || strings.HasPrefix(c.p, root+string(filepath.Separator))
		if c.name == "through a symlinked parent" && spelled {
			t.Error("the second spelling is not a second spelling: the test proves nothing")
		}
	}
}
