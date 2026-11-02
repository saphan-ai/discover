package attest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListFilesIsGitsOwnList(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "*.log\n/build/\n")
	write(t, dir, "keep.txt", "k\n")
	write(t, dir, "sub/.gitignore", "*.tmp\n")
	write(t, dir, "sub/a.go", "package a\n")
	fixture.Git(t, dir, "init", "-q")
	fixture.Git(t, dir, "add", "-A")
	fixture.Git(t, dir, "commit", "-q", "-m", "base")
	write(t, dir, "new file.txt", "untracked, not ignored\n")
	write(t, dir, "root.log", "ignored by the root file\n")
	write(t, dir, "build/out.bin", "ignored directory\n")
	write(t, dir, "sub/x.tmp", "ignored by the nested file\n")
	write(t, dir, "sub/x.log", "ignored by the root file, one level down\n")

	got, err := ListFiles(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "keep.txt", "new file.txt", "sub/.gitignore", "sub/a.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListFiles = %q, want %q", got, want)
	}
}

func TestListFilesKeepsATrackedFileThatWasRemoved(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "gone.txt", "x\n")
	write(t, dir, "stay.txt", "y\n")
	fixture.Git(t, dir, "init", "-q")
	fixture.Git(t, dir, "add", "-A")
	fixture.Git(t, dir, "commit", "-q", "-m", "base")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	got, err := ListFiles(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gone.txt", "stay.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListFiles = %q, want %q: a path git tracks is listed, the reader decides it is unreadable", got, want)
	}
}

func TestListFilesRefusesADirectoryThatIsNotARepository(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "a\n")
	_, err := ListFiles(dir, Options{})
	if err == nil {
		t.Fatal("ListFiles on a plain directory returned no error")
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("the error names the absolute root: %v", err)
	}
}
