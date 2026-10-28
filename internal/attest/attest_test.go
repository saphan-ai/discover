package attest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

// recipe extracts the fenced block tagged "sh <name>" from docs/DIGEST.md.
func recipe(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "DIGEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var body []string
	in := false
	for _, l := range lines {
		switch {
		case l == "```sh "+name:
			in = true
		case in && l == "```":
			return strings.Join(body, "\n")
		case in:
			body = append(body, l)
		}
	}
	t.Fatalf("recipe %q not found in docs/DIGEST.md", name)
	return ""
}

// shell runs a recipe with sh over root and returns the digest it prints.
func shell(t *testing.T, name, root string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", recipe(t, name))
	cmd.Env = append(os.Environ(), "ROOT="+root, "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("recipe %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDigestMatchesShellRecipe(t *testing.T) {
	root := filepath.Join(realTemp(t), "tree-a")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.TreeA(t, root)
	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sh := shell(t, "recipe-dir", root)
	t.Logf("go digest    %s", a.Tree.Digest)
	t.Logf("shell digest %s", sh)
	if a.Mode != ModeDir || a.Root != "tree-a" {
		t.Errorf("mode %q root %q", a.Mode, a.Root)
	}
	if a.Tree.Digest != sh {
		t.Fatalf("go %s != shell %s", a.Tree.Digest, sh)
	}
	if a.Tree.Files != 7 || a.Tree.Links != 2 || a.Tree.Unreadable != 0 {
		t.Errorf("counts %+v, want 7 files 2 links 0 unreadable", a.Tree)
	}
	if want := int64(len("alpha\nbravo\ncharlie\ndelta\necho foxtrot\n# golf\n")); a.Tree.Bytes != want {
		t.Errorf("bytes %d, want %d", a.Tree.Bytes, want)
	}
}

// The shell recipe and the Go code must both react to one changed byte: a digest that never moves proves nothing.
func TestDigestMovesWithOneByte(t *testing.T) {
	root := realTemp(t)
	fixture.TreeA(t, root)
	a1, _ := Attest(root, Options{})
	s1 := shell(t, "recipe-dir", root)
	if err := os.WriteFile(filepath.Join(root, "sub", "c.txt"), []byte("charlie!\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a2, _ := Attest(root, Options{})
	s2 := shell(t, "recipe-dir", root)
	if a1.Tree.Digest == a2.Tree.Digest || s1 == s2 {
		t.Fatalf("digest did not move: go %s/%s shell %s/%s", a1.Tree.Digest, a2.Tree.Digest, s1, s2)
	}
	if a2.Tree.Digest != s2 {
		t.Fatalf("go %s != shell %s", a2.Tree.Digest, s2)
	}
}

func TestEmptyDirDigestIsSHA256OfNothing(t *testing.T) {
	root := realTemp(t)
	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(nil)
	if want := hex.EncodeToString(sum[:]); a.Tree.Digest != want {
		t.Fatalf("empty digest %s, want %s", a.Tree.Digest, want)
	}
	if s := shell(t, "recipe-dir", root); s != a.Tree.Digest {
		t.Fatalf("shell %s", s)
	}
}

func TestSymlinkIsListedNeverFollowed(t *testing.T) {
	base := realTemp(t)
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside secret-ish content v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "tree")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.TreeA(t, root)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(root, "escape-dir")); err != nil {
		t.Fatal(err)
	}
	a1, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a1.Tree.Links != 4 || a1.Tree.Files != 7 {
		t.Errorf("counts %+v, want 4 links 7 files", a1.Tree)
	}
	// Change the outside content: a followed link would move the digest.
	if err := os.WriteFile(outside, []byte("outside content v2, longer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a2, _ := Attest(root, Options{})
	if a1.Tree.Digest != a2.Tree.Digest {
		t.Fatalf("digest followed a symlink out of the tree")
	}
	if s := shell(t, "recipe-dir", root); s != a2.Tree.Digest {
		t.Fatalf("shell %s != go %s", s, a2.Tree.Digest)
	}
	// Negative control: the same content as a regular file inside the tree does move the digest.
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("outside content v2, longer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a3, _ := Attest(root, Options{})
	if a3.Tree.Digest == a2.Tree.Digest {
		t.Fatal("a regular file did not move the digest")
	}
}

func TestUnreadableIsCountedNotSkipped(t *testing.T) {
	root := realTemp(t)
	fixture.TreeA(t, root)
	locked := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(locked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "closed")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755); os.Chmod(locked, 0o644) })
	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(locked); err == nil {
		f.Close()
		t.Log("running with rights that ignore file modes: nothing is unreadable here")
	} else if a.Tree.Unreadable != 2 {
		t.Errorf("unreadable %d, want 2 (a file and a directory)", a.Tree.Unreadable)
	}
	if s := shell(t, "recipe-dir", root); s != a.Tree.Digest {
		t.Fatalf("shell %s != go %s", s, a.Tree.Digest)
	}
}

func TestGitStateAndDigestsMatchShell(t *testing.T) {
	root := filepath.Join(realTemp(t), "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.GitRepo(t, root)
	head := strings.TrimSpace(fixture.Git(t, root, "rev-parse", "HEAD"))

	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Mode != ModeGit || a.Git == nil {
		t.Fatalf("mode %q git %v", a.Mode, a.Git)
	}
	if a.Git.Branch != "main" || a.Git.Head != head || !a.Git.Clean || a.Git.DirtyPaths != 0 || a.Git.Detached {
		t.Errorf("clean state %+v, want main %s clean", *a.Git, head)
	}
	if a.Tree.Files != 7 || a.Untracked.Files != 0 {
		t.Errorf("clean counts tracked %+v untracked %+v", a.Tree, *a.Untracked)
	}
	if s := shell(t, "recipe-git", root); s != a.Tree.Digest {
		t.Fatalf("tracked: shell %s != go %s", s, a.Tree.Digest)
	}

	// Make it dirty in every way the recipe must agree on.
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "alpha changed\n")                               // modified
	write("new one.txt", "untracked with a space\n")                // untracked
	write("fresh/inner.txt", "untracked in a new directory\n")      // untracked, nested
	write(".gitignore", "ignored.log\n")                            // untracked, and it ignores:
	write("ignored.log", "ignored: in neither digest\n")            // ignored
	if err := os.Remove(filepath.Join(root, "b.txt")); err != nil { // tracked, deleted
		t.Fatal(err)
	}
	fixture.Git(t, root, "update-index", "--add", "--cacheinfo",
		"160000,"+head+",vendor/mod") // a gitlink
	if err := os.MkdirAll(filepath.Join(root, "vendor", "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../a.txt", filepath.Join(root, "fresh", "lnk")); err != nil { // untracked symlink
		t.Fatal(err)
	}

	d, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// modified a.txt, deleted b.txt, staged vendor/mod, untracked: .gitignore, fresh/, "new one.txt"
	if d.Git.Clean || d.Git.DirtyPaths != 6 {
		t.Errorf("dirty state %+v, want 6 dirty paths", *d.Git)
	}
	if d.Tree.Unreadable != 1 || d.Tree.Submodules != 1 {
		t.Errorf("tracked %+v, want 1 unreadable (b.txt) and 1 submodule", d.Tree)
	}
	if s := shell(t, "recipe-git", root); s != d.Tree.Digest {
		t.Fatalf("tracked: shell %s != go %s", s, d.Tree.Digest)
	}
	if s := shell(t, "recipe-untracked", root); s != d.Untracked.Digest {
		t.Fatalf("untracked: shell %s != go %s", s, d.Untracked.Digest)
	}
	if d.Untracked.Files != 3 || d.Untracked.Links != 1 {
		t.Errorf("untracked %+v, want 3 files 1 link", *d.Untracked)
	}
	t.Logf("tracked   go %s", d.Tree.Digest)
	t.Logf("untracked go %s", d.Untracked.Digest)

	// Ignored content is in neither digest.
	write("ignored.log", "ignored changed\n")
	e, _ := Attest(root, Options{})
	if e.Tree.Digest != d.Tree.Digest || e.Untracked.Digest != d.Untracked.Digest {
		t.Error("an ignored file moved a digest")
	}
	// Negative control: an untracked file does.
	write("new one.txt", "changed\n")
	f, _ := Attest(root, Options{})
	if f.Untracked.Digest == e.Untracked.Digest || f.Tree.Digest != e.Tree.Digest {
		t.Error("untracked change did not move exactly the untracked digest")
	}
	if len(Compare(e, f)) != 1 || !Compare(e, f)[0].Untracked {
		t.Errorf("Compare = %+v", Compare(e, f))
	}
}

func TestDetachedHeadAndRecordRoot(t *testing.T) {
	root := filepath.Join(realTemp(t), "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.GitRepo(t, root)
	fixture.Git(t, root, "checkout", "-q", "--detach")
	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Git.Detached || a.Git.Branch != "" || a.Root != "repo" {
		t.Errorf("detached %+v root %q", *a.Git, a.Root)
	}
	b, _ := Attest(root, Options{RecordRoot: true})
	if b.Root != root {
		t.Errorf("record-root gave %q, want %q", b.Root, root)
	}
}

func TestAttestRefusesAFile(t *testing.T) {
	p := filepath.Join(realTemp(t), "f")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Attest(p, Options{}); err == nil {
		t.Fatal("a file was attested")
	}
	if _, err := Attest(filepath.Join(filepath.Dir(p), "missing"), Options{}); err == nil {
		t.Fatal("a missing path was attested")
	}
}
