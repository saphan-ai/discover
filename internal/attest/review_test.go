package attest

// The named review's probes for the recipes in docs/DIGEST.md: each recipe, run exactly as written, prints what the Go
// code computes, including for names that hold a quote or a backslash.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

func probeTree(t *testing.T, root string, extra map[string]string) {
	t.Helper()
	files := map[string]string{
		"with space.txt":      "s\n",
		"zażółć gęślą.txt":    "pl\n",
		"日本/ネスト/深い file.md":   "jp\n",
		"nested/a/b/c/d.txt":  "deep\n",
		" leading space.txt":  "ls\n",
		"trailing space.txt ": "ts\n",
		"-dash-first":         "dash\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink("zażółć gęślą.txt", filepath.Join(root, "łącze"))
	os.Symlink("../with space.txt", filepath.Join(root, "nested", "up link"))
}

func TestProbeP4RecipeDir(t *testing.T) {
	root := filepath.Join(realTemp(t), "plain tree")
	os.Mkdir(root, 0o755)
	probeTree(t, root, nil)
	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sh := shell(t, "recipe-dir", root)
	t.Logf("dir go %s sh %s files %d links %d", a.Tree.Digest, sh, a.Tree.Files, a.Tree.Links)
	if a.Tree.Digest != sh {
		t.Errorf("recipe-dir disagrees: go %s sh %s", a.Tree.Digest, sh)
	}
}

func gitProbe(t *testing.T, extra map[string]string) string {
	root := filepath.Join(realTemp(t), "git tree")
	os.Mkdir(root, 0o755)
	probeTree(t, root, extra)
	fixture.Git(t, root, "init", "-q")
	fixture.Git(t, root, "add", "-A")
	fixture.Git(t, root, "commit", "-q", "-m", "fixture")
	os.WriteFile(filepath.Join(root, "nested", "untracked ü.txt"), []byte("u\n"), 0o644)
	return root
}

func compareGit(t *testing.T, root string) {
	a, err := Attest(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	trk, unt := shell(t, "recipe-git", root), shell(t, "recipe-untracked", root)
	t.Logf("tracked go %s sh %s (files %d links %d)", a.Tree.Digest, trk, a.Tree.Files, a.Tree.Links)
	t.Logf("untracked go %s sh %s (files %d)", a.Untracked.Digest, unt, a.Untracked.Files)
	if a.Tree.Digest != trk {
		t.Errorf("recipe-git disagrees")
	}
	if a.Untracked.Digest != unt {
		t.Errorf("recipe-untracked disagrees")
	}
	if a.Untracked.Files != 1 {
		t.Errorf("untracked files %d, want 1", a.Untracked.Files)
	}
}

func TestProbeP4RecipeGit(t *testing.T) { compareGit(t, gitProbe(t, nil)) }

// Git quotes a name holding `"` or `\` even with core.quotepath=off when -z is absent.
func TestProbeP4RecipeGitQuoteInName(t *testing.T) {
	compareGit(t, gitProbe(t, map[string]string{`say "hi".txt`: "q\n", `back\slash.txt`: "b\n"}))
}

func TestProbeP4RecipeDirQuoteInName(t *testing.T) {
	root := filepath.Join(realTemp(t), "plain q")
	os.Mkdir(root, 0o755)
	probeTree(t, root, map[string]string{`say "hi".txt`: "q\n", `back\slash.txt`: "b\n"})
	a, _ := Attest(root, Options{})
	if sh := shell(t, "recipe-dir", root); sh != a.Tree.Digest {
		t.Errorf("recipe-dir disagrees with quote/backslash: go %s sh %s", a.Tree.Digest, sh)
	}
}
