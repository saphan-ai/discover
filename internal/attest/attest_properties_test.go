package attest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saphan-ai/discover/internal/fixture"
)

// The encoding: a name that holds a backslash, a `>` or ` -> `, a link whose target holds them, and an executable
// file, in both digests, against the recipes.
func TestEscapingAndModeAgreeWithTheRecipes(t *testing.T) {
	base := realTemp(t)
	build := func(root string) {
		os.Mkdir(root, 0o755)
		fixture.TreeA(t, root)
		os.WriteFile(filepath.Join(root, `a -> b.txt`), []byte("arrow\n"), 0o644)
		os.WriteFile(filepath.Join(root, `back\slash>.txt`), []byte("bs\n"), 0o644)
		os.WriteFile(filepath.Join(root, "run.sh"), []byte("#!/bin/sh\n"), 0o755)
		os.Chmod(filepath.Join(root, "run.sh"), 0o755)
		os.Symlink(`t -> a\b`, filepath.Join(root, `l -> k`))
	}
	plain := filepath.Join(base, "plain")
	build(plain)
	a, err := Attest(plain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sh := shell(t, "recipe-dir", plain); sh != a.Tree.Digest {
		t.Errorf("recipe-dir: go %s sh %s", a.Tree.Digest, sh)
	}

	repo := filepath.Join(base, "repo")
	build(repo)
	fixture.Git(t, repo, "init", "-q")
	fixture.Git(t, repo, "add", "-A")
	fixture.Git(t, repo, "commit", "-q", "-m", "x")
	os.WriteFile(filepath.Join(repo, `new -> one.sh`), []byte("u\n"), 0o755)
	os.Chmod(filepath.Join(repo, `new -> one.sh`), 0o755)
	g, err := Attest(repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sh := shell(t, "recipe-git", repo); sh != g.Tree.Digest {
		t.Errorf("recipe-git: go %s sh %s", g.Tree.Digest, sh)
	}
	if sh := shell(t, "recipe-untracked", repo); sh != g.Untracked.Digest {
		t.Errorf("recipe-untracked: go %s sh %s", g.Untracked.Digest, sh)
	}
	if g.Git.DirtyPaths != 1 {
		t.Errorf("dirty %+v, want the one untracked file", *g.Git)
	}
}

func TestEscapeIsInjective(t *testing.T) {
	seen := map[string]string{}
	for _, s := range []string{"a", `a\`, `a\n`, "a\n", `\n`, "\n", `>`, `\>`, `\\>`, "a>", "a -> b", `a \-> b`, ""} {
		e := esc(s)
		if strings.Contains(e, "\n") {
			t.Errorf("%q escapes to a string with a raw newline", s)
		}
		if prev, ok := seen[e]; ok {
			t.Errorf("%q and %q both escape to %q", prev, s, e)
		}
		seen[e] = s
		if strings.Contains(e, "->") {
			t.Errorf("%q escapes to %q, which still holds the link separator", s, e)
		}
	}
}

// A rename and a copy in the index: the dirty count is by path, and no path is lost or counted twice.
func TestDirtyCountOfARenameAndACopy(t *testing.T) {
	root := filepath.Join(realTemp(t), "repo")
	os.Mkdir(root, 0o755)
	fixture.GitRepo(t, root)
	count := func() int {
		a, err := Attest(root, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if a.Git.Clean != (a.Git.DirtyPaths == 0) {
			t.Errorf("clean %v with %d dirty", a.Git.Clean, a.Git.DirtyPaths)
		}
		return a.Git.DirtyPaths
	}
	if n := count(); n != 0 {
		t.Fatalf("fresh fixture: %d dirty", n)
	}
	fixture.Git(t, root, "mv", "a.txt", "renamed.txt") // git status shows one R record; two paths differ
	if n := count(); n != 2 {
		t.Errorf("rename: %d dirty paths, want 2 (old name removed, new name added)", n)
	}
	body, _ := os.ReadFile(filepath.Join(root, "b.txt"))
	os.WriteFile(filepath.Join(root, "copied.txt"), body, 0o644)
	fixture.Git(t, root, "add", "copied.txt")
	if n := count(); n != 3 {
		t.Errorf("rename and copy: %d dirty paths, want 3", n)
	}
	os.WriteFile(filepath.Join(root, "copied.txt"), []byte("edited after staging\n"), 0o644)
	if n := count(); n != 3 {
		t.Errorf("a path both staged and modified: %d dirty paths, want 3 (counted once)", n)
	}
	fixture.Git(t, root, "add", "-A")
	fixture.Git(t, root, "commit", "-q", "-m", "all")
	if n := count(); n != 0 {
		t.Errorf("after the commit: %d dirty paths", n)
	}
}

func TestCompareNamesWhatDiffers(t *testing.T) {
	g := func(head, branch string, dirty int) *GitState {
		return &GitState{Head: head, Branch: branch, DirtyPaths: dirty, Clean: dirty == 0}
	}
	base := Attestation{Mode: ModeGit, Git: g("h1", "main", 0), Tree: Tree{Digest: "t"}, Untracked: &Tree{Digest: "u"}}
	if d := Compare(base, base); len(d) != 0 {
		t.Fatalf("identical attestations differ: %+v", d)
	}
	cases := []struct {
		field  string
		change func(a *Attestation)
	}{
		{"mode", func(a *Attestation) { a.Mode = ModeDir }},
		{"tree", func(a *Attestation) { a.Tree.Digest = "t2" }},
		{"untracked", func(a *Attestation) { a.Untracked = &Tree{Digest: "u2"} }},
		{"untracked", func(a *Attestation) { a.Untracked = nil }}, // present on one side only
		{"head", func(a *Attestation) { a.Git = g("h2", "main", 0) }},
		{"branch", func(a *Attestation) { a.Git = g("h1", "other", 0) }},
		{"dirty-paths", func(a *Attestation) { a.Git = g("h1", "main", 3) }},
	}
	for _, c := range cases {
		after := base
		c.change(&after)
		found := false
		for _, d := range Compare(base, after) {
			found = found || d.Field == c.field
		}
		if !found {
			t.Errorf("a change of %s is not reported: %+v", c.field, Compare(base, after))
		}
	}
}

// A git that never answers: the call is killed at the deadline and the error names it.
func TestGitCallHasADeadlineAndTheErrorNamesIt(t *testing.T) {
	dir := realTemp(t)
	root := filepath.Join(dir, "repo")
	os.Mkdir(root, 0o755)
	fixture.GitRepo(t, root)
	fake := filepath.Join(dir, "slowgit")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := Attest(root, Options{GitBin: fake, GitTimeout: 300 * time.Millisecond})
	if err == nil {
		t.Fatal("a git that never answers was waited for")
	}
	if !strings.Contains(err.Error(), "git ") || !strings.Contains(err.Error(), "timed out after 300ms") {
		t.Errorf("the error does not name the call that timed out: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestErrorsNameTheRootByItsBaseName(t *testing.T) {
	root := filepath.Join(realTemp(t), "tree")
	os.Mkdir(root, 0o755)
	os.Chmod(root, 0)
	defer os.Chmod(root, 0o755)
	if f, err := os.Open(root); err == nil {
		f.Close()
		t.Skip("rights that ignore directory modes")
	}
	_, err := Attest(root, Options{})
	if err == nil {
		t.Fatal("an unreadable root was attested")
	}
	if strings.Contains(err.Error(), filepath.Dir(root)) {
		t.Errorf("the absolute root is in the error: %v", err)
	}
}
