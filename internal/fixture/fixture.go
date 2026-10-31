// Package fixture builds the trees the tests read. It is for tests only.
package fixture

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TreeA builds the standard fixture under dir: five files, a subdirectory, a file with a space in its name, an empty
// file, a symlink inside the tree and a dangling symlink.
func TreeA(t testing.TB, dir string) {
	t.Helper()
	files := map[string]string{
		"a.txt":              "alpha\n",
		"b.txt":              "bravo\n",
		"sub/c.txt":          "charlie\n",
		"sub/deep/d.txt":     "delta\n",
		"with space.txt":     "echo foxtrot\n",
		"empty.txt":          "",
		"sub/deep/e file.md": "# golf\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"link-in": "a.txt", "link-dangling": "no such target"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// Git runs git inside dir with a fixed identity and no global configuration.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// GitRepo builds TreeA under dir and commits it on branch main.
func GitRepo(t testing.TB, dir string) {
	t.Helper()
	TreeA(t, dir)
	Git(t, dir, "init", "-q")
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "-m", "fixture")
}

// Stamp is the mode and modification time of one path.
type Stamp struct {
	Mode  fs.FileMode
	Mtime int64
}

// Snapshot records the mode and mtime of every path under root, directories and .git included.
func Snapshot(t testing.TB, root string) map[string]Stamp {
	t.Helper()
	out := map[string]Stamp{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = Stamp{fi.Mode(), fi.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
