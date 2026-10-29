// Package safepath keeps every read of a target inside the target.
//
// discover reads repositories it does not own. A path that names a file outside the root, whether by `..` or by a
// symlink in a directory position, is refused before anything is opened. The open itself goes through os.Root, which
// refuses an escape atomically: a parent swapped for a symlink after the check cannot lead the read out of the root.
package safepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	// ErrEscape means the path leaves the root, or runs through a symlink.
	ErrEscape = errors.New("path escapes the root")
	// ErrNotRegular means the final component is not a regular file.
	ErrNotRegular = errors.New("not a regular file")
)

// afterLstat runs between the check of a file and its open. Tests use it to swap the file in that gap.
var afterLstat = func(rel string) {}

// Resolve joins a slash-separated relative path to an absolute root. It refuses an empty path, an absolute path, a
// `..` element, a NUL byte, and any directory component that is a symlink. The final component is not inspected.
//
// Resolve only checks; a read must go through Root, which is what keeps the open inside the root.
func Resolve(root, rel string) (string, error) {
	if err := lexical(rel); err != nil {
		return "", err
	}
	cur := root
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "" || p == "." {
			continue
		}
		cur = filepath.Join(cur, p)
		if i == len(parts)-1 {
			break
		}
		fi, err := os.Lstat(cur)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %q passes through a symlink", ErrEscape, rel)
		}
	}
	return cur, nil
}

func lexical(rel string) error {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: %q", ErrEscape, rel)
	}
	for _, p := range strings.Split(rel, "/") {
		if p == ".." {
			return fmt.Errorf("%w: %q", ErrEscape, rel)
		}
	}
	return nil
}

// Root is a directory that reads are confined to.
type Root struct {
	r *os.Root
}

// OpenRoot opens dir as a confinement root.
func OpenRoot(dir string) (*Root, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Root{r: r}, nil
}

// Close releases the root.
func (r *Root) Close() error { return r.r.Close() }

// FS is the root as a read-only file system. Names given to it never leave the root.
func (r *Root) FS() fs.FS { return r.r.FS() }

// Check refuses a path that is not a plain relative path inside the root: see Resolve.
func (r *Root) Check(rel string) error {
	if err := lexical(rel); err != nil {
		return err
	}
	parts := strings.Split(rel, "/")
	cur := ""
	for i, p := range parts {
		if p == "" || p == "." {
			continue
		}
		if cur == "" {
			cur = p
		} else {
			cur += "/" + p
		}
		if i == len(parts)-1 {
			break
		}
		fi, err := r.r.Lstat(cur)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q passes through a symlink", ErrEscape, rel)
		}
	}
	return nil
}

// Lstat describes rel without following a final symlink.
func (r *Root) Lstat(rel string) (fs.FileInfo, error) { return r.r.Lstat(rel) }

// Readlink returns the target string of the symlink rel.
func (r *Root) Readlink(rel string) (string, error) { return r.r.Readlink(rel) }

// OpenRegular opens a regular file read-only. It refuses a symlink or any other kind, and a file that was swapped
// between the check and the open. The open never blocks, so a FIFO swapped in after the check cannot hang the read.
func (r *Root) OpenRegular(rel string) (*os.File, fs.FileInfo, error) {
	if err := r.Check(rel); err != nil {
		return nil, nil, err
	}
	before, err := r.r.Lstat(rel)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, ErrNotRegular
	}
	afterLstat(rel)
	f, err := r.r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		f.Close()
		return nil, nil, ErrNotRegular
	}
	return f, after, nil
}
