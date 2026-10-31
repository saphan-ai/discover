package cli

// Properties of `discover scan` that must hold: every change between the two passes is seen, a read never leaves the
// target, no program named by the target runs, and no error names an absolute path.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saphan-ai/discover/internal/fixture"
	"github.com/saphan-ai/discover/internal/safepath"
)

const (
	gitScope   = "(covers tracked and untracked files, modes, HEAD; not ignored paths or .git internals)"
	plainScope = "not empty directories"
)

// scratch is a temporary directory that is made removable again at the end, whatever a test did to its modes.
func scratch(t *testing.T) string {
	t.Helper()
	d := temp(t)
	t.Cleanup(func() {
		filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error { os.Chmod(p, 0o755); return nil })
	})
	return d
}

func plainFixture(t *testing.T) (string, string) {
	b := scratch(t)
	root := filepath.Join(b, "plain")
	os.Mkdir(root, 0o755)
	fixture.TreeA(t, root)
	return b, root
}

func gitFixture(t *testing.T) (string, string) {
	b := temp(t)
	root := filepath.Join(b, "repo")
	os.Mkdir(root, 0o755)
	fixture.GitRepo(t, root)
	return b, root
}

func mustNotEqual(t *testing.T, base, root string, change func(r string)) {
	t.Helper()
	setHook(t, change)
	code, stdout, stderr := run(t, "--out", filepath.Join(base, "out"), root)
	if code != ExitNotEqual || !strings.Contains(stdout, "NOT-EQUAL") {
		t.Errorf("change between passes NOT detected: exit %d\n%s%s", code, stdout, stderr)
	}
}

// mustStayOutOfScope: the change is outside what the proof covers, so the proof says EQUAL and prints that scope.
func mustStayOutOfScope(t *testing.T, base, root, scope string, change func(r string)) {
	t.Helper()
	setHook(t, change)
	code, stdout, stderr := run(t, "--out", filepath.Join(base, "out"), root)
	if code != ExitOK || !strings.Contains(stdout, "read-only proof: EQUAL ") || !strings.Contains(stdout, scope) {
		t.Errorf("want EQUAL with the scope %q printed: exit %d\n%s%s", scope, code, stdout, stderr)
	}
}

// ---- property 1: every change is seen ----

func TestScanSeesPlainByte(t *testing.T) {
	b, r := plainFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.WriteFile(filepath.Join(r, "a.txt"), []byte("alphA\n"), 0o644) })
}
func TestScanSeesPlainNewFile(t *testing.T) {
	b, r := plainFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.WriteFile(filepath.Join(r, "new.txt"), nil, 0o644) })
}
func TestScanSeesPlainDelete(t *testing.T) {
	b, r := plainFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.Remove(filepath.Join(r, "empty.txt")) })
}
func TestScanSeesPlainRename(t *testing.T) {
	b, r := plainFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.Rename(filepath.Join(r, "a.txt"), filepath.Join(r, "z.txt")) })
}
func TestScanSeesPlainSymlinkRetarget(t *testing.T) {
	b, r := plainFixture(t)
	mustNotEqual(t, b, r, func(r string) {
		os.Remove(filepath.Join(r, "link-in"))
		os.Symlink("b.txt", filepath.Join(r, "link-in"))
	})
}
func TestScanSeesPlainChmod(t *testing.T) {
	b, r := plainFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.Chmod(filepath.Join(r, "a.txt"), 0o755) })
}

// Design rule: an empty directory is declared out of scope (content-addressed, as in git), and the proof says so.
func TestScanSeesPlainEmptyDir(t *testing.T) {
	b, r := plainFixture(t)
	mustStayOutOfScope(t, b, r, plainScope, func(r string) { os.Mkdir(filepath.Join(r, "newdir"), 0o755) })
}

// Two different trees, one digest: a newline in a name forges a second line.
func TestNewlineInANameCannotForgeALine(t *testing.T) {
	b := scratch(t)
	r := filepath.Join(b, "t")
	os.Mkdir(r, 0o755)
	os.WriteFile(filepath.Join(r, "p"), []byte("C"), 0o644)
	os.Symlink("t", filepath.Join(r, "x"))
	mustNotEqual(t, b, r, func(r string) {
		os.Rename(filepath.Join(r, "p"), filepath.Join(r, "p\nlink  x -> t"))
		os.Remove(filepath.Join(r, "x"))
	})
}

// Two different trees, one digest: " -> " is not escaped in a link line.
func TestArrowInALinkNameCannotForgeALink(t *testing.T) {
	b := scratch(t)
	r := filepath.Join(b, "t")
	os.Mkdir(r, 0o755)
	os.Symlink("b -> c", filepath.Join(r, "a"))
	mustNotEqual(t, b, r, func(r string) {
		os.Remove(filepath.Join(r, "a"))
		os.Symlink("c", filepath.Join(r, "a -> b"))
	})
}

func TestScanSeesGitByte(t *testing.T) {
	b, r := gitFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.WriteFile(filepath.Join(r, "a.txt"), []byte("alphA\n"), 0o644) })
}
func TestScanSeesGitNewUntracked(t *testing.T) {
	b, r := gitFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.WriteFile(filepath.Join(r, "new.txt"), nil, 0o644) })
}
func TestScanSeesGitDelete(t *testing.T) {
	b, r := gitFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.Remove(filepath.Join(r, "b.txt")) })
}
func TestScanSeesGitRename(t *testing.T) {
	b, r := gitFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.Rename(filepath.Join(r, "a.txt"), filepath.Join(r, "z.txt")) })
}
func TestScanSeesGitSymlinkRetarget(t *testing.T) {
	b, r := gitFixture(t)
	mustNotEqual(t, b, r, func(r string) {
		os.Remove(filepath.Join(r, "link-in"))
		os.Symlink("b.txt", filepath.Join(r, "link-in"))
	})
}
func TestScanSeesGitChmodTracked(t *testing.T) {
	b, r := gitFixture(t)
	mustNotEqual(t, b, r, func(r string) { os.Chmod(filepath.Join(r, "a.txt"), 0o755) })
}

// Design rule: ignored paths and .git internals are outside the git-mode proof, and the proof line says so.
func TestScanSeesGitIgnoredFileWritten(t *testing.T) {
	b, r := gitFixture(t)
	os.WriteFile(filepath.Join(r, ".gitignore"), []byte("*.log\n"), 0o644)
	fixture.Git(t, r, "add", ".gitignore")
	fixture.Git(t, r, "commit", "-q", "-m", "ignore")
	mustStayOutOfScope(t, b, r, gitScope, func(r string) { os.WriteFile(filepath.Join(r, "written.log"), []byte("x"), 0o644) })
}
func TestScanSeesGitCommitBetweenPasses(t *testing.T) {
	b, r := gitFixture(t)
	os.WriteFile(filepath.Join(r, "a.txt"), []byte("edited\n"), 0o644)
	mustNotEqual(t, b, r, func(r string) {
		fixture.Git(t, r, "add", "a.txt")
		fixture.Git(t, r, "commit", "-q", "-m", "between")
	})
}
func TestScanSeesGitWriteInsideDotGit(t *testing.T) {
	b, r := gitFixture(t)
	mustStayOutOfScope(t, b, r, gitScope, func(r string) {
		os.WriteFile(filepath.Join(r, ".git", "hooks", "post-checkout"), []byte("x"), 0o755)
	})
}

// ---- property 2: reads stay inside the root ----

// Resolve checks the parents, then the parent is swapped for a symlink before the open.
func TestParentSwappedBetweenCheckAndOpenIsNotRead(t *testing.T) {
	b := scratch(t)
	r := filepath.Join(b, "root")
	os.MkdirAll(filepath.Join(r, "sub"), 0o755)
	os.WriteFile(filepath.Join(r, "sub", "f"), []byte("inside"), 0o644)
	outside := filepath.Join(b, "outside")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "f"), []byte("OUTSIDE-SECRET"), 0o644)

	root, err := safepath.OpenRoot(r)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Check("sub/f"); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(r, "sub"))
	os.Symlink(outside, filepath.Join(r, "sub"))
	f, _, err := root.OpenRegular("sub/f")
	if err == nil {
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		f.Close()
		t.Errorf("opened a file outside the root through a swapped parent: read %q", buf[:n])
	}
}

// ---- property 3: the target is never written, and no program the target names is run ----

func TestStaleStatDoesNotRefreshTheIndex(t *testing.T) {
	b, r := gitFixture(t)
	// same content, new mtime: git must re-check the file, and would refresh the index if it could
	future := time.Now().Add(2 * time.Hour)
	os.Chtimes(filepath.Join(r, "a.txt"), future, future)
	idx := filepath.Join(r, ".git", "index")
	before, _ := os.Stat(idx)
	snap := fixture.Snapshot(t, r)
	code, _, stderr := run(t, "--out", filepath.Join(b, "out"), r)
	if code != ExitOK {
		t.Fatalf("exit %d %s", code, stderr)
	}
	after, _ := os.Stat(idx)
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Errorf(".git/index rewritten")
	}
	now := fixture.Snapshot(t, r)
	for p, s := range snap {
		if now[p] != s {
			t.Errorf("%s changed", p)
		}
	}
	for p := range now {
		if _, ok := snap[p]; !ok {
			t.Errorf("%s appeared", p)
		}
	}
}

// A clean filter in the target's own .git/config is a program git runs during `git status`.
func TestRepoConfigFilterIsNotRun(t *testing.T) {
	b, r := gitFixture(t)
	marker := filepath.Join(b, "filter-ran")
	os.WriteFile(filepath.Join(r, ".gitattributes"), []byte("*.txt filter=probe\n"), 0o644)
	fixture.Git(t, r, "add", ".gitattributes")
	fixture.Git(t, r, "commit", "-q", "-m", "attrs")
	fixture.Git(t, r, "config", "filter.probe.clean", "tee "+marker)
	os.Remove(marker)
	future := time.Now().Add(2 * time.Hour)
	os.Chtimes(filepath.Join(r, "a.txt"), future, future)
	run(t, "--out", filepath.Join(b, "out"), r)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("scan ran a program named by the target's .git/config (filter.probe.clean); marker %s written", filepath.Base(marker))
	}
}

// A submodule carries its own config and its own attributes: a filter named there is a program too.
func TestSubmoduleConfigFilterIsNotRun(t *testing.T) {
	b, r := gitFixture(t)
	src := filepath.Join(b, "subsrc")
	os.Mkdir(src, 0o755)
	os.WriteFile(filepath.Join(src, "x.txt"), []byte("x\n"), 0o644)
	fixture.Git(t, src, "init", "-q")
	fixture.Git(t, src, "add", "-A")
	fixture.Git(t, src, "commit", "-q", "-m", "sub")
	fixture.Git(t, r, "-c", "protocol.file.allow=always", "submodule", "add", "-q", src, "mods/s")
	fixture.Git(t, r, "commit", "-q", "-m", "add submodule")

	sub := filepath.Join(r, "mods", "s")
	marker := filepath.Join(b, "sub-filter-ran")
	os.WriteFile(filepath.Join(sub, ".gitattributes"), []byte("*.txt filter=probe\n"), 0o644)
	fixture.Git(t, sub, "config", "filter.probe.clean", "tee "+marker)
	os.Remove(marker)
	future := time.Now().Add(2 * time.Hour)
	os.Chtimes(filepath.Join(sub, "x.txt"), future, future)
	code, _, stderr := run(t, "--out", filepath.Join(b, "out"), r)
	if code != ExitOK && code != ExitNotEqual {
		t.Fatalf("scan failed: exit %d %s", code, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("scan ran a program named by a submodule's config (filter.probe.clean); marker %s written", filepath.Base(marker))
	}
}

// ---- --out inside the target, spelled with another case (macOS default APFS is case-insensitive) ----

func TestOutWithAnotherCaseIsRefusedOnACaseInsensitiveHost(t *testing.T) {
	b, r := plainFixture(t)
	upper := filepath.Join(b, "PLAIN")
	if _, err := os.Stat(upper); err != nil {
		t.Skip("case-sensitive filesystem")
	}
	code, _, stderr := run(t, "--out", filepath.Join(upper, "out"), r)
	if code != ExitUsage {
		_, err := os.Stat(filepath.Join(r, "out", "ATTESTATION.json"))
		t.Errorf("--out PLAIN/out accepted (exit %d, %s); written into the target: %v", code, stderr, err == nil)
	}
}

// ---- property 5: no absolute root in an error ----

func TestUnreadableRootErrorLeaksNoPath(t *testing.T) {
	b, r := plainFixture(t)
	os.Chmod(r, 0)
	defer os.Chmod(r, 0o755)
	code, stdout, stderr := run(t, "--out", filepath.Join(b, "out"), r)
	t.Logf("exit %d stdout %q stderr %q", code, stdout, stderr)
	if strings.Contains(stderr+stdout, filepath.Dir(r)) {
		t.Errorf("the absolute root leaks without --record-root: %q", stderr)
	}
}

// ---- several targets: output directory names collide ----

func TestSeveralTargetsWithOneNameKeepSeparateOutputs(t *testing.T) {
	b := scratch(t)
	var roots []string
	for _, p := range []string{"x/plain", "y/plain", "z/plain-2"} {
		r := filepath.Join(b, p)
		os.MkdirAll(r, 0o755)
		os.WriteFile(filepath.Join(r, "id"), []byte(p), 0o644)
		roots = append(roots, r)
	}
	out := filepath.Join(b, "out")
	run(t, append([]string{"--out", out}, roots...)...)
	ents, _ := os.ReadDir(out)
	if len(ents) != 3 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("3 targets, %d output directories %v: one attestation overwrote another", len(ents), names)
	}
}
