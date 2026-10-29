package safepath

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func tree(t *testing.T) (root, outside string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(base, "root")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "sub"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "sub", "f"), []byte("inside"), 0o644)
	os.WriteFile(filepath.Join(outside, "f"), []byte("OUTSIDE-SECRET"), 0o644)
	return root, outside
}

func withHook(t *testing.T, f func(rel string)) {
	t.Helper()
	old := afterLstat
	afterLstat = f
	t.Cleanup(func() { afterLstat = old })
}

// The parent is swapped for a symlink to an outside directory AFTER every check passed: only the confined open stands
// between the read and the outside file.
func TestParentSwappedAfterTheCheckCannotLeaveTheRoot(t *testing.T) {
	root, outside := tree(t)
	r, err := OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	withHook(t, func(string) {
		os.RemoveAll(filepath.Join(root, "sub"))
		os.Symlink(outside, filepath.Join(root, "sub"))
	})
	f, _, err := r.OpenRegular("sub/f")
	if err == nil {
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		f.Close()
		t.Fatalf("read %q through a swapped parent", buf[:n])
	}
	// Negative control: the swap did happen, and a plain open by path would have read the secret.
	if b, err := os.ReadFile(filepath.Join(root, "sub", "f")); err != nil || string(b) != "OUTSIDE-SECRET" {
		t.Fatalf("the swap did not take effect (%q, %v): the test proved nothing", b, err)
	}
}

// A FIFO swapped in after the check: opening it for reading blocks until a writer appears, unless the open is
// non-blocking.
func TestFifoSwappedInAfterTheCheckDoesNotBlock(t *testing.T) {
	root, _ := tree(t)
	r, err := OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := filepath.Join(root, "sub", "f")
	withHook(t, func(string) {
		os.Remove(p)
		if err := syscall.Mkfifo(p, 0o644); err != nil {
			t.Error(err)
		}
	})
	res := make(chan error, 1)
	go func() {
		f, _, err := r.OpenRegular("sub/f")
		if f != nil {
			f.Close()
		}
		res <- err
	}()
	select {
	case err := <-res:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("a FIFO was opened as a regular file: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the open of a swapped-in FIFO blocked")
	}
}

func TestOpenRegularRefusesWhatIsNotARegularFile(t *testing.T) {
	root, outside := tree(t)
	os.Symlink(filepath.Join(root, "sub", "f"), filepath.Join(root, "inner-link"))
	os.Symlink(outside, filepath.Join(root, "outer-dir"))
	r, err := OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, rel := range []string{"inner-link", "outer-dir/f", "sub", "../outside/f", "/etc/hosts", "", "missing"} {
		if f, _, err := r.OpenRegular(rel); err == nil {
			f.Close()
			t.Errorf("%q was opened", rel)
		}
	}
	// Negative control: the plain path in the same root opens.
	f, fi, err := r.OpenRegular("sub/f")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fi.Size() != 6 {
		t.Errorf("size %d", fi.Size())
	}
}
