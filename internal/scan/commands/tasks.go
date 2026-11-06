package commands

import (
	"regexp"
	"strings"
)

var makeWord = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./+-]*$`)

// makefile cites each explicit rule target: a line at the left margin, a name, a colon. Variable assignments, pattern
// rules, special targets (.PHONY) and recipe lines are not targets.
func (s *scanner) makefile(p string, lines []string, _ []byte) bool {
	seen := map[string]bool{}
	for i, l := range lines {
		if l == "" || l[0] == '\t' || l[0] == ' ' || l[0] == '#' {
			continue
		}
		colon := strings.IndexByte(l, ':')
		if colon <= 0 {
			continue
		}
		if rest := l[colon+1:]; strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":=") {
			continue
		}
		for _, w := range strings.Fields(l[:colon]) {
			if !makeWord.MatchString(w) || seen[w] || w == "export" || w == "include" || w == "ifeq" || w == "ifneq" {
				continue
			}
			seen[w] = true
			s.command(classify(w), "make "+w, p, i+1)
		}
	}
	return true
}

var (
	justAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*\s*:?=`)
	justRecipe = regexp.MustCompile(`^@?([A-Za-z][A-Za-z0-9_-]*)(?:\s[^:]*)?:(?:\s|$)`)
)

// justfile cites each public recipe. A recipe whose name starts with an underscore is private to the file.
func (s *scanner) justfile(p string, lines []string, _ []byte) bool {
	for i, l := range lines {
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#' || l[0] == '[' || justAssign.MatchString(l) {
			continue
		}
		if m := justRecipe.FindStringSubmatch(l); m != nil {
			switch m[1] {
			case "set", "import", "mod", "alias", "export":
				continue
			}
			s.command(classify(m[1]), "just "+m[1], p, i+1)
		}
	}
	return true
}

var yamlKey = regexp.MustCompile(`^("[^"]+"|'[^']+'|[A-Za-z0-9_.:-]+?):(?:\s|$)`)

// taskfile cites each task: a key one level below a top-level tasks mapping.
func (s *scanner) taskfile(p string, lines []string, _ []byte) bool {
	in, child := false, -1
	for i, l := range lines {
		ind, text := indentOf(l)
		if !text || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if ind == 0 {
			in, child = strings.HasPrefix(l, "tasks:") && strings.TrimSpace(strings.TrimPrefix(l, "tasks:")) == "", -1
			continue
		}
		if !in {
			continue
		}
		if child < 0 {
			child = ind
		}
		if ind != child {
			continue
		}
		if m := yamlKey.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			name := strings.Trim(m[1], `"'`)
			s.command(classify(name), "task "+name, p, i+1)
		}
	}
	return true
}
