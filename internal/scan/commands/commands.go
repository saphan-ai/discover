package commands

import (
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
)

// The reasons a file is listed in Result.Skipped. The first two are the words the inventory uses.
const (
	ReasonOverCap    = "over size cap"
	ReasonUnreadable = "unreadable"
	ReasonUnparsed   = "not readable as its format"
)

// Skipped is a file that was listed but could not be used, with the reason.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Result is what the files say about how the repository is started, built and tested. Every slice is non-nil.
type Result struct {
	EntryPoints []model.EntryPoint
	Commands    []model.Command
	CI          []model.CI
	Containers  []model.Container
	Skipped     []Skipped
}

// scanner collects one run. It reads files only through read and starts nothing.
type scanner struct {
	read func(string) ([]byte, error)
	res  Result
	// goDirs holds the directories that already have a Go main entry point: one row per directory.
	goDirs map[string]bool
}

// Scan reads the files of a repository that it knows by name and cites what they hold. files are slash-separated paths
// relative to the root; read returns the content of one of them. Scan reads no path outside files, reads each at most
// once, and never runs anything: it has no way to.
func Scan(files []string, read func(string) ([]byte, error)) Result {
	s := &scanner{read: read, goDirs: map[string]bool{}, res: Result{
		EntryPoints: []model.EntryPoint{}, Commands: []model.Command{}, CI: []model.CI{},
		Containers: []model.Container{}, Skipped: []Skipped{},
	}}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, p := range sorted {
		if ignored(p) {
			continue
		}
		s.file(p)
	}
	return s.res
}

// ignored is true for the trees that hold other people's code.
func ignored(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "node_modules" || seg == "vendor" || seg == ".git" {
			return true
		}
	}
	return false
}

var docSuffix = map[string]bool{"md": true, "txt": true, "rst": true, "json": true, "yml": true, "yaml": true, "toml": true, "html": true}

func isDockerfile(base string) bool {
	if base == "Dockerfile" || strings.HasSuffix(base, ".Dockerfile") {
		return true
	}
	rest, ok := strings.CutPrefix(base, "Dockerfile.")
	return ok && !docSuffix[strings.ToLower(rest)]
}

func isYAML(base string) bool {
	return strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")
}

func isCompose(base string) bool {
	for _, pre := range []string{"docker-compose", "compose"} {
		if rest, ok := strings.CutPrefix(base, pre); ok && isYAML(base) {
			if rest == ".yml" || rest == ".yaml" || strings.HasPrefix(rest, ".") {
				return true
			}
		}
	}
	return false
}

func (s *scanner) file(p string) {
	base, dir := path.Base(p), path.Dir(p)
	switch {
	case base == "Makefile" || base == "makefile" || base == "GNUmakefile":
		s.with(p, s.makefile)
	case base == "justfile" || base == "Justfile" || base == ".justfile":
		s.with(p, s.justfile)
	case base == "Taskfile.yml" || base == "Taskfile.yaml":
		s.with(p, s.taskfile)
	case base == "package.json":
		s.with(p, s.packageJSON)
	case base == "pyproject.toml":
		s.with(p, s.pyproject)
	case base == "Cargo.toml":
		s.with(p, s.cargo)
	case p == ".gitlab-ci.yml":
		s.with(p, s.gitlab)
	case dir == ".github/workflows" && isYAML(base):
		s.with(p, func(p string, lines []string, data []byte) bool { return s.workflow(p, "github-actions", lines) })
	case dir == ".gitea/workflows" && isYAML(base):
		s.with(p, func(p string, lines []string, data []byte) bool { return s.workflow(p, "gitea-actions", lines) })
	case isDockerfile(base):
		s.with(p, s.dockerfile)
	case isCompose(base):
		s.with(p, s.compose)
	case strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go"):
		s.with(p, s.goMain)
	case strings.HasSuffix(base, ".py"):
		s.with(p, s.python)
	}
}

// with reads p and hands it to f. A read that fails, or a parse f refuses, names the file in Skipped.
func (s *scanner) with(p string, f func(p string, lines []string, data []byte) bool) {
	data, err := s.read(p)
	if err != nil {
		reason := ReasonUnreadable
		if errors.Is(err, manifest.ErrTooLarge) {
			reason = ReasonOverCap
		}
		s.res.Skipped = append(s.res.Skipped, Skipped{p, reason})
		return
	}
	if !f(p, splitLines(data), data) {
		s.res.Skipped = append(s.res.Skipped, Skipped{p, ReasonUnparsed})
	}
}

func splitLines(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	return lines
}

func (s *scanner) command(kind, text, file string, line int) {
	text = redact(strings.TrimSpace(text))
	if text == "" {
		return
	}
	s.res.Commands = append(s.res.Commands, model.Command{Kind: kind, Text: text, Source: model.Source{File: file, Line: line}})
}

func (s *scanner) entry(kind, name, file string, line int) {
	s.res.EntryPoints = append(s.res.EntryPoints, model.EntryPoint{Kind: kind, Name: name, Source: model.Source{File: file, Line: line}})
}

// classify says what a name or a command line is for, by the first word that tells. A line that tells nothing is run.
func classify(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, w := range words {
		switch w {
		case "test", "tests", "check", "spec", "e2e", "pytest", "jest":
			return "test"
		case "lint", "vet", "fmt", "format", "clippy", "eslint", "prettier", "staticcheck":
			return "lint"
		case "build", "compile", "install", "all", "dist", "package", "release":
			return "build"
		}
	}
	return "run"
}

// A command line may carry a credential. The report names the command and never quotes the value.
var (
	secretAssign = regexp.MustCompile(`(?i)((?:token|secret|passw(?:or)?d|api[_-]?key|credentials?|auth)[A-Za-z0-9_-]*\s*[=:]\s*)(["']?)([^\s"'$][^\s"']*)`)
	urlUser      = regexp.MustCompile(`(://[^/\s:@]+:)[^/\s@]+@`)
)

const withheld = "<withheld>"

func redact(text string) string {
	text = secretAssign.ReplaceAllString(text, "${1}${2}"+withheld)
	return urlUser.ReplaceAllString(text, "${1}"+withheld+"@")
}

// lineAt is the 1-based line number of byte offset off.
func lineAt(data []byte, off int) int {
	if off > len(data) {
		off = len(data)
	}
	return 1 + strings.Count(string(data[:off]), "\n")
}

// indentOf is the number of leading spaces of a line, and whether the line has anything else on it.
func indentOf(l string) (int, bool) {
	t := strings.TrimLeft(l, " ")
	return len(l) - len(t), strings.TrimSpace(t) != ""
}

// scalar reads one YAML value written on a line: it drops a trailing comment and one pair of quotes.
func scalar(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
		return v[1:]
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
