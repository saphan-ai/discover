// Package inventory is section 1 of the report: what a repository holds. It lists the files of a target, reads each
// one through the read manifest under a size cap, and counts files, bytes and languages. Nothing is skipped
// silently: a file that was not read, or not counted, is named in Result.Skipped with its reason.
//
// A git target is listed by git itself, so the root and nested .gitignore files are honoured. A plain directory is
// walked through safepath and no link is followed.
package inventory

import (
	"bytes"
	"errors"
	"io/fs"
	"sort"
	"strings"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/safepath"
)

// DefaultMaxBytes is the size cap when Options.MaxBytes says nothing: a file over it is never read.
const DefaultMaxBytes int64 = 1 << 20

// binaryProbe is how much of a file is looked at for a NUL byte.
const binaryProbe = 8 << 10

// The reasons a file is listed in Result.Skipped.
const (
	ReasonOverCap    = "over size cap"
	ReasonBinary     = "binary"
	ReasonUnreadable = "unreadable"
	ReasonNotRegular = "not a regular file"
)

// Options tune one scan.
type Options struct {
	// MaxBytes is the size cap of one file; zero or less means DefaultMaxBytes. The cap is never off.
	MaxBytes int64
	// Attest carries the git settings (binary, deadline) used to list a git target.
	Attest attest.Options
}

// Skipped is a file the scan did not read, or read and did not treat as text.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Result is the inventory and what it left out.
//
// Inventory counts every regular file that could be examined: a text file, a binary file and a file over the cap all
// count, by their size on disk. A file that could not be examined (unreadable, or not a regular file) is not counted.
// Skipped names a binary file, a file over the cap, an unreadable path and a link or submodule. Files is every path
// that was listed, sorted.
type Result struct {
	Inventory model.Inventory
	Skipped   []Skipped
	Files     []string
}

// Scan inventories root. root is an existing directory; mode says whether it is a git work tree. Every file read goes
// through m, which must be a manifest for the same root.
func Scan(root string, mode attest.Mode, m *manifest.Manifest, opts Options) (Result, error) {
	r, err := safepath.OpenRoot(root)
	if err != nil {
		return Result{}, err
	}
	defer r.Close()
	files, skipped, top, err := list(root, r, mode, opts)
	if err != nil {
		return Result{}, err
	}
	limit := opts.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	inv := model.Inventory{TopLevel: top, Languages: []model.LanguageShare{}}
	langs := map[string]int{}
	count := func(p string, size int64) {
		inv.FilesTotal++
		inv.BytesTotal += int(size)
		if l, ok := Language(p); ok {
			langs[l]++
		}
	}
	for _, p := range files {
		if err := r.Check(p); err != nil {
			skipped = append(skipped, Skipped{p, ReasonUnreadable})
			continue
		}
		fi, err := r.Lstat(p)
		switch {
		case err != nil:
			skipped = append(skipped, Skipped{p, ReasonUnreadable})
			continue
		case !fi.Mode().IsRegular():
			skipped = append(skipped, Skipped{p, ReasonNotRegular})
			continue
		}
		data, e, err := m.Read(p, limit)
		switch {
		case errors.Is(err, manifest.ErrTooLarge):
			skipped = append(skipped, Skipped{p, ReasonOverCap})
			count(p, fi.Size())
		case err != nil:
			skipped = append(skipped, Skipped{p, ReasonUnreadable})
		default:
			if isBinary(data) {
				skipped = append(skipped, Skipped{p, ReasonBinary})
			}
			count(p, e.Size)
		}
	}
	for name, n := range langs {
		inv.Languages = append(inv.Languages, model.LanguageShare{Name: name, Files: n})
	}
	sort.Slice(inv.Languages, func(i, j int) bool {
		a, b := inv.Languages[i], inv.Languages[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Name < b.Name
	})
	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].Path != skipped[j].Path {
			return skipped[i].Path < skipped[j].Path
		}
		return skipped[i].Reason < skipped[j].Reason
	})
	return Result{Inventory: inv, Skipped: skipped, Files: files}, nil
}

// isBinary is a NUL byte in the first binaryProbe bytes.
func isBinary(data []byte) bool {
	if len(data) > binaryProbe {
		data = data[:binaryProbe]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// list returns the paths to examine, sorted, the paths that could not be listed, and the top level of the root.
func list(root string, r *safepath.Root, mode attest.Mode, opts Options) ([]string, []Skipped, []model.TopLevelEntry, error) {
	skipped := []Skipped{}
	var files []string
	switch mode {
	case attest.ModeGit:
		var err error
		if files, err = attest.ListFiles(root, opts.Attest); err != nil {
			return nil, nil, nil, err
		}
		return files, skipped, topFromList(r, files), nil
	default:
		err := fs.WalkDir(r.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
			switch {
			case rel == ".":
				return err
			case err != nil:
				skipped = append(skipped, Skipped{rel, ReasonUnreadable})
				return nil
			case !d.IsDir():
				files = append(files, rel)
			}
			return nil
		})
		if err != nil {
			return nil, nil, nil, err
		}
		sort.Strings(files)
		top, err := topFromDir(r)
		return files, skipped, top, err
	}
}

func kindOf(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "dir"
	case mode&fs.ModeSymlink != 0:
		return "symlink"
	case mode.IsRegular():
		return "file"
	}
	return "other"
}

// topFromList derives the top level from git's file list: what git does not list (an ignored entry, .git) is not
// part of the repository.
func topFromList(r *safepath.Root, files []string) []model.TopLevelEntry {
	top := []model.TopLevelEntry{}
	seen := map[string]bool{}
	for _, p := range files {
		name, _, nested := strings.Cut(p, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		kind := "dir"
		if !nested {
			kind = "file" // a tracked file that was removed has no mode to read
			if fi, err := r.Lstat(name); err == nil {
				kind = kindOf(fi.Mode())
			}
		}
		top = append(top, model.TopLevelEntry{Name: name, Kind: kind})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Name < top[j].Name })
	return top
}

// topFromDir lists the root's own entries, empty directories included.
func topFromDir(r *safepath.Root) ([]model.TopLevelEntry, error) {
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return nil, err
	}
	top := make([]model.TopLevelEntry, 0, len(entries))
	for _, e := range entries {
		top = append(top, model.TopLevelEntry{Name: e.Name(), Kind: kindOf(e.Type())})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Name < top[j].Name })
	return top, nil
}

var languages = map[string]string{
	".go": "Go", ".js": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript", ".jsx": "JavaScript",
	".ts": "TypeScript", ".tsx": "TypeScript", ".py": "Python", ".rs": "Rust", ".java": "Java", ".kt": "Kotlin",
	".rb": "Ruby", ".php": "PHP", ".cs": "C#", ".c": "C", ".h": "C", ".cc": "C++", ".cpp": "C++", ".hpp": "C++",
	".swift": "Swift", ".sh": "Shell", ".bash": "Shell", ".html": "HTML", ".htm": "HTML", ".css": "CSS",
	".scss": "CSS", ".md": "Markdown", ".json": "JSON", ".yml": "YAML", ".yaml": "YAML", ".toml": "TOML",
	".sql": "SQL", ".xml": "XML",
}

// Language names the language of a file by its extension, in lower or upper case. A name with no known extension has
// none: it is not counted in any language.
func Language(path string) (string, bool) {
	i := strings.LastIndexByte(path, '.')
	if i < 0 || i < strings.LastIndexByte(path, '/') {
		return "", false
	}
	l, ok := languages[strings.ToLower(path[i:])]
	return l, ok
}
