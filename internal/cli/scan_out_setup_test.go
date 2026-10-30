package cli

// Output directories are made before the target is read, through a handle confined to an existing directory. A
// refusal in that setup names no absolute path and says what it left behind.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reviewer's probe: the parent of a not-yet-existing --out becomes a link to the target between the digest passes.
func TestLateParentLinkCreatesNothingInsideTheTarget(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	before := listTree(t, a)
	setHook(t, func(string) {
		late := filepath.Join(x, "late")
		os.Rename(late, filepath.Join(x, "moved"))
		os.Symlink(a, late)
	})
	code, _, stderr := run(t, "--out", filepath.Join(x, "late", "created"), a)
	if code != ExitUsage {
		t.Errorf("exit %d, want %d (stderr %q)", code, ExitUsage, stderr)
	}
	if got := listTree(t, a); got != before {
		t.Errorf("the target changed:\nbefore %s\nafter  %s", before, got)
	}
	mustNotExist(t, filepath.Join(a, "created"))
	if strings.Contains(stderr, "nothing was written") || strings.Contains(stderr, "nothing was created") {
		t.Errorf("the refusal denies what happened: %q", stderr)
	}
	if !strings.Contains(stderr, `"late"`) || !strings.Contains(stderr, `"created"`) {
		t.Errorf("the refusal does not name the directories it created: %q", stderr)
	}
	if strings.Contains(stderr, x) {
		t.Errorf("the refusal names an absolute path: %q", stderr)
	}
}

func TestOutputParentThatIsAFileIsRefusedWithoutAPath(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	block := filepath.Join(x, "block")
	if err := os.WriteFile(block, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	passes := countingHook(t)
	code, _, stderr := run(t, "--out", filepath.Join(block, "child"), a)
	if code != ExitUsage {
		t.Errorf("exit %d, want %d (stderr %q)", code, ExitUsage, stderr)
	}
	for _, p := range []string{x, filepath.Dir(a), a} {
		if strings.Contains(stderr, p) {
			t.Errorf("the refusal names the path %q: %q", p, stderr)
		}
	}
	if *passes != 0 {
		t.Errorf("%d targets were read before the refusal", *passes)
	}
	if !strings.Contains(stderr, `"block"`) {
		t.Errorf("the refusal does not name the output: %q", stderr)
	}
}

// A cleanup that fails is reported, by the temporary file's name inside the output directory.
func TestFailedCleanupOfTheTemporaryFileIsReported(t *testing.T) {
	dir := temp(t)
	// A directory at the output name makes the rename fail, so the temporary file has to be removed.
	if err := os.Mkdir(filepath.Join(dir, attestationFile), 0o755); err != nil {
		t.Fatal(err)
	}
	old := removeTemp
	removeTemp = func(root *os.Root, name string) error { return errors.New("forced") }
	t.Cleanup(func() { removeTemp = old })
	err := writeOutputsIn(t, dir, map[string][]byte{manifestFile: []byte("{}\n"), attestationFile: []byte("{}\n")})
	if err == nil {
		t.Fatal("the write succeeded over a directory")
	}
	if !strings.Contains(err.Error(), ".discover-") || !strings.Contains(err.Error(), ".tmp") {
		t.Errorf("the error does not name the file left behind: %v", err)
	}
	if strings.Contains(err.Error(), dir) {
		t.Errorf("the error names an absolute path: %v", err)
	}
}

func listTree(t *testing.T, dir string) string {
	t.Helper()
	var names []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, p)
		names = append(names, rel)
		return nil
	})
	return strings.Join(names, ",")
}
