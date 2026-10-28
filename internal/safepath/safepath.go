// Package safepath keeps every read of a target inside the target.
//
// discover reads repositories it does not own. A path that names a file outside the root, whether by `..` or by a
// symlink in a directory position, is refused before anything is opened.
package safepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrEscape means the path leaves the root, or runs through a symlink.
	ErrEscape = errors.New("path escapes the root")
	// ErrNotRegular means the final component is not a regular file.
	ErrNotRegular = errors.New("not a regular file")
)

// Resolve joins a slash-separated relative path to an absolute root. It refuses an empty path, an absolute path, a
// `..` element, a NUL byte, and any directory component that is a symlink. The final component is not inspected.
func Resolve(root, rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", ErrEscape, rel)
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == ".." {
			return "", fmt.Errorf("%w: %q", ErrEscape, rel)
		}
	}
	cur := root
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

// OpenRegular opens a regular file read-only. It refuses a symlink or any other kind, and refuses a file that was
// swapped between the check and the open.
func OpenRegular(abs string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(abs)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, ErrNotRegular
	}
	f, err := os.OpenFile(abs, os.O_RDONLY, 0)
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
