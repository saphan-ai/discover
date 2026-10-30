package attest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ListFiles is git's own list of the files of a git target: the tracked paths and the untracked paths that no
// .gitignore (the root's, a nested one, or the repository's exclude file) hides. It goes through the same isolated
// git runner as Attest. Paths are slash-separated, relative to root, sorted, and listed once. A tracked path whose
// file was removed from the work tree is listed: the reader decides what that file is. A submodule is one path.
//
// root must be the top of a git repository; a plain directory is an error. An error names the target by its base
// name, never by its absolute path.
func ListFiles(root string, opts Options) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, scrub(err, filepath.Base(abs), abs)
	}
	files, err := listFiles(real, opts)
	if err != nil {
		err = scrub(err, filepath.Base(abs), real, abs)
	}
	return files, err
}

func listFiles(real string, opts Options) ([]string, error) {
	if _, err := os.Lstat(filepath.Join(real, ".git")); err != nil {
		return nil, fmt.Errorf("%s: not a git repository: there is no .git entry at its root", filepath.Base(real))
	}
	if err := checkGit(real, opts); err != nil {
		return nil, err
	}
	out, err := runGit(real, opts, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	files := []string{}
	for _, p := range nulList(out) {
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}
