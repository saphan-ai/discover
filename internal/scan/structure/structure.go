// Package structure is section 3 of the report: how a repository is laid out. It reads only the list of file paths
// the inventory produced, never a file's content, so every observation is about names and places. Each observation
// cites example files, with a key that says what the file is an example of.
package structure

import (
	"fmt"
	"sort"
	"strings"

	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/inventory"
)

// programming holds the languages whose files count as source code. Markup and data files do not.
var programming = map[string]bool{
	"Go": true, "JavaScript": true, "TypeScript": true, "Python": true, "Rust": true, "Java": true, "Kotlin": true,
	"Ruby": true, "PHP": true, "C#": true, "C": true, "C++": true, "Swift": true, "Shell": true,
}

// testDirs are the directory names that hold tests.
var testDirs = map[string]bool{"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true, "e2e": true}

// maxExamples bounds the files one observation cites.
const maxExamples = 3

type file struct {
	path string
	top  string // the first path element; empty for a file at the root
	base string
}

// Scan describes the layout of the repository whose files are listed, slash-separated and relative to its root.
// The order of files does not matter. Every slice of the result is non-nil.
func Scan(paths []string) model.Structure {
	s := model.Structure{Patterns: []model.Observation{}, Naming: []model.Observation{}, Architecture: []model.Observation{}}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)

	var src, tests, colocated []file
	testRoots := map[string][]file{}
	for _, p := range sorted {
		f := file{path: p, base: p[strings.LastIndexByte(p, '/')+1:]}
		if i := strings.IndexByte(p, '/'); i >= 0 {
			f.top = p[:i]
		}
		lang, ok := inventory.Language(p)
		if !ok || !programming[lang] {
			if f.top != "" && testDirs[f.top] {
				testRoots[f.top] = append(testRoots[f.top], f)
			}
			continue
		}
		switch {
		case f.top != "" && testDirs[f.top]:
			testRoots[f.top] = append(testRoots[f.top], f)
			tests = append(tests, f)
		case testKind(f.base) != "":
			colocated = append(colocated, f)
			tests = append(tests, f)
		default:
			src = append(src, f)
		}
	}

	roots := map[string][]file{}
	for _, f := range src {
		if f.top != "" {
			roots[f.top] = append(roots[f.top], f)
		}
	}
	rootNames := sortedKeys(roots)

	if text, ex := layout(src, roots); text != "" {
		s.Patterns = append(s.Patterns, observation(text, "layout", ex))
	}
	for _, name := range rootNames {
		s.Patterns = append(s.Patterns, observation("source root: "+name, "source-root", roots[name]))
	}
	for _, name := range sortedKeys(testRoots) {
		s.Patterns = append(s.Patterns, observation("test root: "+name, "test-root", testRoots[name]))
	}
	if len(colocated) > 0 {
		s.Patterns = append(s.Patterns, observation("tests sit beside the code they test", "colocated-tests", colocated))
	}

	if o, ok := sourceNaming(src); ok {
		s.Naming = append(s.Naming, o)
	}
	if o, ok := testNaming(tests); ok {
		s.Naming = append(s.Naming, o)
	}
	return s
}

func sortedKeys(m map[string][]file) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// observation cites the first maxExamples files, which are the first in path order.
func observation(text, key string, files []file) model.Observation {
	o := model.Observation{Text: text, Examples: []model.Source{}}
	for i, f := range files {
		if i == maxExamples {
			break
		}
		o.Examples = append(o.Examples, model.Source{File: f.path, Key: key})
	}
	return o
}

// layout names the shape of the source tree. It returns no text when there is no source code to describe.
func layout(src []file, roots map[string][]file) (string, []file) {
	if len(src) == 0 {
		return "", nil
	}
	switch {
	case len(roots["cmd"]) > 0 && len(roots["internal"]) > 0:
		return "layout: Go command layout (cmd/ and internal/)", append(append([]file{}, roots["cmd"]...), roots["internal"]...)
	case len(roots["src"]) > 0:
		return "layout: src layout", roots["src"]
	case len(roots["apps"])+len(roots["packages"]) > 0:
		return "layout: monorepo (apps/ or packages/)", append(append([]file{}, roots["apps"]...), roots["packages"]...)
	}
	var atRoot []file
	for _, f := range src {
		if f.top == "" {
			atRoot = append(atRoot, f)
		}
	}
	if len(atRoot) > 0 {
		return "layout: flat (source files at the root)", atRoot
	}
	return "layout: source in nested directories", src
}

// style names how a file name stem is written.
func style(stem string) string {
	switch {
	case stem == "":
		return ""
	case strings.Contains(stem, "-"):
		return "kebab-case"
	case strings.Contains(stem, "_"):
		return "snake_case"
	case stem[0] >= 'A' && stem[0] <= 'Z':
		return "PascalCase"
	case strings.ToLower(stem) != stem:
		return "camelCase"
	}
	return "single lower-case words"
}

func stemOf(base string) string {
	if i := strings.IndexByte(base, '.'); i >= 0 {
		return base[:i]
	}
	return base
}

// sourceNaming reports the style most source file names share. A multi-word style wins over single words, because
// single words say nothing about a convention.
func sourceNaming(src []file) (model.Observation, bool) {
	byStyle := map[string][]file{}
	for _, f := range src {
		if st := style(stemOf(f.base)); st != "" {
			byStyle[st] = append(byStyle[st], f)
		}
	}
	best := ""
	for _, st := range sortedKeys(byStyle) {
		if st == "single lower-case words" {
			continue
		}
		if best == "" || len(byStyle[st]) > len(byStyle[best]) {
			best = st
		}
	}
	if best == "" {
		if len(byStyle["single lower-case words"]) == 0 {
			return model.Observation{}, false
		}
		best = "single lower-case words"
	}
	total := 0
	for _, fs := range byStyle {
		total += len(fs)
	}
	return observation(fmt.Sprintf("source file names use %s (%d of %d source files)", best, len(byStyle[best]), total),
		"naming", byStyle[best]), true
}

// testKind is how a test file's name marks it, or empty when it does not.
func testKind(base string) string {
	dot := strings.LastIndexByte(base, '.')
	if dot < 0 {
		return ""
	}
	stem, ext := base[:dot], base[dot:]
	switch {
	case strings.HasSuffix(stem, "_test"):
		return "end in _test" + ext
	case strings.HasSuffix(stem, ".test"):
		return "end in .test" + ext
	case strings.HasSuffix(stem, ".spec"):
		return "end in .spec" + ext
	case strings.HasPrefix(stem, "test_"):
		return "start with test_"
	}
	return ""
}

// testNaming reports the most common way test file names are marked.
func testNaming(tests []file) (model.Observation, bool) {
	by := map[string][]file{}
	for _, f := range tests {
		if k := testKind(f.base); k != "" {
			by[k] = append(by[k], f)
		}
	}
	best := ""
	for _, k := range sortedKeys(by) {
		if best == "" || len(by[best]) < len(by[k]) {
			best = k
		}
	}
	if best == "" {
		return model.Observation{}, false
	}
	n := len(by[best])
	noun := "files"
	if n == 1 {
		noun = "file"
	}
	return observation(fmt.Sprintf("test file names %s (%d %s)", best, n, noun), "naming", by[best]), true
}
