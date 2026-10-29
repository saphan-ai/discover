package attest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

// An index flag that tells git to stop looking at a path (assume-unchanged, skip-worktree) hides a change from
// `git status`. The dirty count hashes the file itself and does not read the flag, so it still counts the path.
// docs/DIGEST.md states this difference from `git status`.
func TestDirtyCountIgnoresTheIndexFlagsThatHideAPathFromGitStatus(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		root := filepath.Join(realTemp(t), "repo")
		os.Mkdir(root, 0o755)
		fixture.GitRepo(t, root)
		fixture.Git(t, root, "update-index", flag, "a.txt")
		os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed\n"), 0o644)
		if out := fixture.Git(t, root, "status", "--porcelain"); out != "" {
			t.Fatalf("%s: git status shows %q: the flag hides nothing and the test proves nothing", flag, out)
		}
		a, err := Attest(root, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if a.Git.DirtyPaths != 1 {
			t.Errorf("%s: %d dirty paths, want 1", flag, a.Git.DirtyPaths)
		}
	}
}
