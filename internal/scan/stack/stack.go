// Package stack reads the technology stack a repository states: the languages, runtimes, dependencies, container
// images and tools its manifest files name, each with the version the file pins.
//
// A version is what the file says, as written. A range is reported as the range. When the file pins nothing the row
// has no version and is marked "not pinned": a version is never inferred, never resolved and never "latest". Every row
// cites its file and the key (and the line, when known) where the file says it.
//
// The package reads only the paths it is given, through the Reader it is given, and never runs anything it reads.
package stack

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/inventory"
)

// Reader returns the bytes of one file of the repository. It reports a file over the size cap as manifest.ErrTooLarge.
type Reader func(path string) ([]byte, error)

// Row layers.
const (
	layerLanguage       = "language"
	layerRuntime        = "runtime"
	layerDependency     = "dependency"
	layerDevDependency  = "dev-dependency"
	layerOptional       = "optional-dependency"
	layerBuild          = "build"
	layerLocked         = "locked-dependency"
	layerPackageManager = "package-manager"
	layerContainer      = "container-image"
	layerTooling        = "tooling"
)

// Reasons a file is named in the skipped list.
const (
	reasonOverCap     = "over size cap, not read for the stack"
	reasonLockfile    = "lockfile format not read"
	reasonVendored    = "dependency directory not read for the stack"
	reasonUnreadable  = "could not be read for the stack"
	reasonNotParsable = "not read for the stack: %s"
)

// lockfiles are the lock formats this package does not read; a file with one of these names is listed as skipped.
// package-lock.json is read, beside its package.json.
var lockfiles = map[string]bool{
	"yarn.lock": true, "pnpm-lock.yaml": true, "bun.lock": true, "bun.lockb": true, "composer.lock": true,
	"Gemfile.lock": true, "Cargo.lock": true, "poetry.lock": true, "uv.lock": true, "Pipfile.lock": true,
}

type ctx struct {
	read    Reader
	files   map[string]bool
	rows    []model.StackRow
	skipped []inventory.Skipped
}

// Scan reads the manifest files among files and returns the rows they state and the files it did not read, with the
// reason. files are slash paths relative to the repository root.
func Scan(files []string, read Reader) ([]model.StackRow, []inventory.Skipped) {
	c := &ctx{read: read, files: map[string]bool{}, rows: []model.StackRow{}, skipped: []inventory.Skipped{}}
	for _, f := range files {
		c.files[f] = true
	}
	vendored := map[string]bool{}
	for _, f := range files {
		base := path.Base(f)
		h := handlerFor(base)
		if h == nil && !lockfiles[base] {
			continue
		}
		if dir := vendoredDir(f); dir != "" {
			vendored[dir] = true
			continue
		}
		if h == nil {
			c.skip(f, reasonLockfile)
			continue
		}
		h(c, f)
	}
	for dir := range vendored {
		c.skip(dir, reasonVendored)
	}
	sort.SliceStable(c.skipped, func(i, j int) bool {
		a, b := c.skipped[i], c.skipped[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Reason < b.Reason
	})
	return c.rows, c.skipped
}

func handlerFor(base string) func(*ctx, string) {
	lower := strings.ToLower(base)
	switch {
	case base == "go.mod":
		return goMod
	case base == "package.json":
		return packageJSON
	case base == "pyproject.toml":
		return pyproject
	case strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return requirements
	case base == "Cargo.toml":
		return cargo
	case base == "pom.xml":
		return pom
	case base == "build.gradle" || base == "build.gradle.kts":
		return gradle
	case base == "Gemfile":
		return gemfile
	case base == "composer.json":
		return composer
	case lower == "dockerfile" || lower == "containerfile" || strings.HasPrefix(lower, "dockerfile.") ||
		strings.HasSuffix(lower, ".dockerfile"):
		return dockerfile
	case base == ".tool-versions":
		return toolVersions
	case base == ".nvmrc":
		return nvmrc
	case base == ".python-version":
		return pythonVersion
	case base == "global.json":
		return globalJSON
	}
	return nil
}

// vendoredDir returns the node_modules directory f sits in, or "" when it sits in none.
func vendoredDir(f string) string {
	parts := strings.Split(f, "/")
	for i, p := range parts[:len(parts)-1] {
		if p == "node_modules" {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

// add appends one row. The version goes through pinOf, so a row with no pin carries the "not pinned" note.
func (c *ctx) add(layer, tech, version, file, key string, line int) {
	r := model.StackRow{Layer: layer, Technology: tech, Source: model.Source{File: file, Key: key, Line: line}}
	if v := pinOf(version); v != nil {
		r.Version = v
	} else {
		r.VersionNote = "not pinned"
	}
	c.rows = append(c.rows, r)
}

func (c *ctx) skip(p, reason string) {
	c.skipped = append(c.skipped, inventory.Skipped{Path: p, Reason: reason})
}

// load reads a file; on failure it names the file in the skipped list and reports false.
func (c *ctx) load(p string) ([]byte, bool) {
	b, err := c.read(p)
	switch {
	case err == nil:
		return b, true
	case errors.Is(err, manifest.ErrTooLarge):
		c.skip(p, reasonOverCap)
	default:
		c.skip(p, reasonUnreadable)
	}
	return nil, false
}

func (c *ctx) refuse(p string, why string) {
	c.skip(p, fmt.Sprintf(reasonNotParsable, why))
}

// lineAt is the 1-based line of byte offset off in src.
func lineAt(src string, off int) int {
	if off > len(src) {
		off = len(src)
	}
	return 1 + strings.Count(src[:off], "\n")
}

// jsonLine finds the line of the key name, looking after the first occurrence of the key section when one is given.
// It returns 0 when the key is not found.
func jsonLine(src, section, name string) int {
	start := 0
	if section != "" {
		i := strings.Index(src, `"`+section+`"`)
		if i < 0 {
			return 0
		}
		start = i
	}
	j := strings.Index(src[start:], `"`+name+`"`)
	if j < 0 {
		return 0
	}
	return lineAt(src, start+j)
}
