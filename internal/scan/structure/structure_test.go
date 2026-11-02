package structure_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/structure"
)

var goCLI = []string{
	".gitignore", "Makefile", "README.md", "assets/blob.bin", "cmd/gocli/main.go", "docs/usage.md", "go.mod",
	"internal/greet/greet.go", "internal/greet/greet_test.go",
}

var nodeWeb = []string{
	".gitignore", "README.md", "package.json", "src/index.js", "src/routes/order-detail.js", "src/routes/user-list.js",
	"src/server.js", "test/server.test.js", "web/.gitignore", "web/app.css", "web/index.html",
}

func texts(obs []model.Observation) []string {
	var out []string
	for _, o := range obs {
		out = append(out, o.Text)
	}
	return out
}

func has(obs []model.Observation, text string) bool {
	for _, o := range obs {
		if o.Text == text {
			return true
		}
	}
	return false
}

func TestGoCommandLayout(t *testing.T) {
	s := structure.Scan(goCLI)
	for _, want := range []string{
		"layout: Go command layout (cmd/ and internal/)",
		"source root: cmd",
		"source root: internal",
		"tests sit beside the code they test",
	} {
		if !has(s.Patterns, want) {
			t.Errorf("patterns lack %q: %q", want, texts(s.Patterns))
		}
	}
	for _, want := range []string{
		"source file names use single lower-case words (2 of 2 source files)",
		"test file names end in _test.go (1 file)",
	} {
		if !has(s.Naming, want) {
			t.Errorf("naming lacks %q: %q", want, texts(s.Naming))
		}
	}
	for _, o := range s.Patterns {
		if strings.HasPrefix(o.Text, "test root:") {
			t.Errorf("go-cli has no test directory but reports %q", o.Text)
		}
	}
}

func TestNodeSrcLayoutWithATestRoot(t *testing.T) {
	s := structure.Scan(nodeWeb)
	for _, want := range []string{
		"layout: src layout",
		"source root: src",
		"test root: test",
	} {
		if !has(s.Patterns, want) {
			t.Errorf("patterns lack %q: %q", want, texts(s.Patterns))
		}
	}
	if has(s.Patterns, "source root: test") || has(s.Patterns, "source root: web") {
		t.Errorf("a test directory or a directory of markup is a source root: %q", texts(s.Patterns))
	}
	for _, want := range []string{
		"source file names use kebab-case (2 of 4 source files)",
		"test file names end in .test.js (1 file)",
	} {
		if !has(s.Naming, want) {
			t.Errorf("naming lacks %q: %q", want, texts(s.Naming))
		}
	}
}

func TestFlatLayout(t *testing.T) {
	s := structure.Scan([]string{"main.py", "util.py", "test_util.py", "README.md"})
	if !has(s.Patterns, "layout: flat (source files at the root)") {
		t.Errorf("patterns = %q", texts(s.Patterns))
	}
	if !has(s.Patterns, "tests sit beside the code they test") {
		t.Errorf("patterns = %q", texts(s.Patterns))
	}
	if !has(s.Naming, "test file names start with test_ (1 file)") {
		t.Errorf("naming = %q", texts(s.Naming))
	}
}

func TestEveryObservationCitesAFileOfTheRepositoryWithALocator(t *testing.T) {
	for name, files := range map[string][]string{"go-cli": goCLI, "node-web": nodeWeb} {
		s := structure.Scan(files)
		known := map[string]bool{}
		for _, f := range files {
			known[f] = true
		}
		all := append(append(append([]model.Observation{}, s.Patterns...), s.Naming...), s.Architecture...)
		if len(all) == 0 {
			t.Fatalf("%s: no observation at all", name)
		}
		for _, o := range all {
			if len(o.Examples) == 0 {
				t.Errorf("%s: %q has no example", name, o.Text)
			}
			for _, e := range o.Examples {
				if !known[e.File] {
					t.Errorf("%s: %q cites %q, which is not a file of the repository", name, o.Text, e.File)
				}
				if e.Key == "" && e.Line == 0 {
					t.Errorf("%s: %q cites %q with neither key nor line", name, o.Text, e.File)
				}
			}
		}
	}
}

func TestNothingToObserveGivesEmptySlicesNotNull(t *testing.T) {
	s := structure.Scan(nil)
	if s.Patterns == nil || s.Naming == nil || s.Architecture == nil {
		t.Fatalf("a nil slice would be written as null: %+v", s)
	}
	if len(s.Patterns)+len(s.Naming)+len(s.Architecture) != 0 {
		t.Fatalf("observations from no files: %+v", s)
	}
}

func TestTheSameFilesInAnyOrderGiveTheSameStructure(t *testing.T) {
	rev := make([]string, len(nodeWeb))
	for i, f := range nodeWeb {
		rev[len(nodeWeb)-1-i] = f
	}
	if a, b := structure.Scan(nodeWeb), structure.Scan(rev); !reflect.DeepEqual(a, b) {
		t.Fatalf("order of the input changes the result:\n%+v\n%+v", a, b)
	}
}
