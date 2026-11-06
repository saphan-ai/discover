package commands

import (
	"path"
	"regexp"
	"strings"
)

var (
	goPackageMain = regexp.MustCompile(`^package\s+main\b`)
	goFuncMain    = regexp.MustCompile(`^func\s+main\s*\(\s*\)`)
	pyMainGuard   = regexp.MustCompile(`^if\s+__name__\s*==\s*["']__main__["']\s*:`)
)

// goMain cites a Go main package by the line of its main function: one row per directory. Test files and the
// testdata trees are not entry points.
func (s *scanner) goMain(p string, lines []string, _ []byte) bool {
	dir := path.Dir(p)
	if s.goDirs[dir] {
		return true
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "testdata" {
			return true
		}
	}
	isMain, fn := false, 0
	for i, l := range lines {
		switch {
		case goPackageMain.MatchString(l):
			isMain = true
		case goFuncMain.MatchString(l) && fn == 0:
			fn = i + 1
		}
	}
	if !isMain || fn == 0 {
		return true
	}
	s.goDirs[dir] = true
	name := path.Base(dir)
	if dir == "." {
		name = "main"
	}
	s.entry("main", name, p, fn)
	return true
}

// python cites a __main__.py module and a script that guards its main block with __name__ == "__main__".
func (s *scanner) python(p string, lines []string, _ []byte) bool {
	base := path.Base(p)
	if base == "__main__.py" {
		name := path.Base(path.Dir(p))
		if path.Dir(p) == "." {
			name = "__main__"
		}
		s.entry("main", name, p, 1)
		return true
	}
	for i, l := range lines {
		if pyMainGuard.MatchString(l) {
			s.entry("script", strings.TrimSuffix(base, ".py"), p, i+1)
			return true
		}
	}
	return true
}
