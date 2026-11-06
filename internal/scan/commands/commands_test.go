package commands_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/commands"
)

// update rewrites the golden files: go test ./internal/scan/commands -update. Read the diff before you commit it.
var update = flag.Bool("update", false, "rewrite the golden files")

const testdata = "../../../testdata"

// mem is a repository held in memory. It records every path the scanner asks to read.
type mem struct {
	files map[string]string
	read  []string
}

func (m *mem) paths() []string {
	var p []string
	for k := range m.files {
		p = append(p, k)
	}
	sort.Strings(p)
	return p
}

func (m *mem) Read(p string) ([]byte, error) {
	m.read = append(m.read, p)
	s, ok := m.files[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(s), nil
}

func scanMem(files map[string]string) (commands.Result, *mem) {
	m := &mem{files: files}
	return commands.Scan(m.paths(), m.Read), m
}

func cmdsOf(r commands.Result, file string) []string {
	var out []string
	for _, c := range r.Commands {
		if c.Source.File == file {
			out = append(out, fmt.Sprintf("%s|%s|%d", c.Kind, c.Text, c.Source.Line))
		}
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n  %q\nwant\n  %q", got, want)
	}
}

func TestMakefileTargetsAreCitedByLine(t *testing.T) {
	r, _ := scanMem(map[string]string{"Makefile": ".PHONY: a\nV := 1\nbuild:\n\techo x\ntest: build\n\techo y\n%.o: %.c\n\tz\nfoo = bar\nrun::\n"})
	eq(t, cmdsOf(r, "Makefile"), []string{"build|make build|3", "test|make test|5", "run|make run|10"})
}

func TestPackageJSONScriptsAndBinCarryTheirLine(t *testing.T) {
	r, _ := scanMem(map[string]string{"package.json": "{\n  \"name\": \"pk\",\n  \"bin\": {\"pk\": \"x.js\"},\n  \"scripts\": {\n    \"build\": \"tsc\",\n    \"lint\": \"eslint .\",\n    \"dev\": \"vite\"\n  }\n}\n"})
	eq(t, cmdsOf(r, "package.json"), []string{"build|npm run build|5", "lint|npm run lint|6", "run|npm run dev|7"})
	if len(r.EntryPoints) != 1 || r.EntryPoints[0].Name != "pk" || r.EntryPoints[0].Kind != "cli" || r.EntryPoints[0].Source.Line != 3 {
		t.Fatalf("entry points = %+v", r.EntryPoints)
	}
}

func TestJustfileRecipesSkipAssignmentsAndSettings(t *testing.T) {
	r, _ := scanMem(map[string]string{"justfile": "set shell := [\"sh\"]\nv := \"1\"\nbuild target=\"all\":\n    cargo build\n_private:\n    x\ntest:\n    y\n"})
	eq(t, cmdsOf(r, "justfile"), []string{"build|just build|3", "test|just test|7"})
}

func TestTaskfileTasksAreTheKeysUnderTasks(t *testing.T) {
	r, _ := scanMem(map[string]string{"Taskfile.yml": "version: '3'\nvars:\n  A: b\ntasks:\n  build:\n    cmds:\n      - x\n  test:\n    cmds:\n      - y\n"})
	eq(t, cmdsOf(r, "Taskfile.yml"), []string{"build|task build|5", "test|task test|8"})
}

func TestPyprojectScriptsAreEntryPointsAndCommands(t *testing.T) {
	r, _ := scanMem(map[string]string{"pyproject.toml": "[project.scripts]\nab = \"p.m:f\"\n\n[tool.poetry.scripts]\ncd = \"q:g\"\n\n[tool.x]\nzz = \"a:b\"\n"})
	var names []string
	for _, e := range r.EntryPoints {
		names = append(names, fmt.Sprintf("%s|%s|%d", e.Kind, e.Name, e.Source.Line))
	}
	eq(t, names, []string{"cli|ab|2", "cli|cd|5"})
	eq(t, cmdsOf(r, "pyproject.toml"), []string{"run|ab|2", "run|cd|5"})
}

func TestCargoBinTablesNameTheirBinary(t *testing.T) {
	r, _ := scanMem(map[string]string{"Cargo.toml": "[package]\nname = \"pk\"\n\n[[bin]]\nname = \"one\"\npath = \"a.rs\"\n\n[[bin]]\npath = \"b.rs\"\nname = \"two\"\n"})
	var names []string
	for _, e := range r.EntryPoints {
		names = append(names, fmt.Sprintf("%s|%s|%d", e.Kind, e.Name, e.Source.Line))
	}
	eq(t, names, []string{"cli|one|4", "cli|two|8"})
}

func TestGoMainNeedsPackageMainAndFuncMainOutsideTests(t *testing.T) {
	r, _ := scanMem(map[string]string{
		"cmd/a/main.go":      "package main\n\nfunc main() {\n}\n",
		"cmd/b/lib.go":       "package b\n\nfunc main() {}\n",
		"cmd/c/x_test.go":    "package main\n\nfunc main() {}\n",
		"cmd/d/helper.go":    "package main\n\nfunc helper() {}\n",
		"testdata/e/main.go": "package main\n\nfunc main() {}\n",
		"vendor/f/main.go":   "package main\n\nfunc main() {}\n",
		"main.go":            "// c\npackage main\n\nfunc main() {}\n",
	})
	var got []string
	for _, e := range r.EntryPoints {
		got = append(got, fmt.Sprintf("%s|%s|%s:%d", e.Kind, e.Name, e.Source.File, e.Source.Line))
	}
	eq(t, got, []string{"main|a|cmd/a/main.go:3", "main|main|main.go:4"})
}

func TestPythonMainModuleAndGuard(t *testing.T) {
	r, _ := scanMem(map[string]string{
		"pkg/__main__.py": "print(1)\n",
		"tool.py":         "def f():\n    pass\n\nif __name__ == '__main__':\n    f()\n",
		"lib.py":          "x = 1\n",
	})
	var got []string
	for _, e := range r.EntryPoints {
		got = append(got, fmt.Sprintf("%s|%s|%s:%d", e.Kind, e.Name, e.Source.File, e.Source.Line))
	}
	eq(t, got, []string{"main|pkg|pkg/__main__.py:1", "script|tool|tool.py:4"})
}

func TestGitHubAndGiteaRunStepsAreCommandsAndTheFileIsCI(t *testing.T) {
	wf := "name: x\njobs:\n  j:\n    steps:\n      - uses: a/b@v1\n      - run: make build\n      - run: |\n          go vet ./...\n\n          go test ./...\n      - name: n\n        run: \"npm run lint\"\n"
	r, _ := scanMem(map[string]string{".github/workflows/ci.yml": wf, ".gitea/workflows/b.yaml": "jobs:\n  j:\n    steps:\n      - run: go build ./...\n"})
	eq(t, cmdsOf(r, ".github/workflows/ci.yml"), []string{"build|make build|6", "lint|go vet ./...|8", "test|go test ./...|10", "lint|npm run lint|12"})
	eq(t, cmdsOf(r, ".gitea/workflows/b.yaml"), []string{"build|go build ./...|4"})
	var systems []string
	for _, c := range r.CI {
		systems = append(systems, c.System+"|"+c.Source.File)
	}
	eq(t, systems, []string{"gitea-actions|.gitea/workflows/b.yaml", "github-actions|.github/workflows/ci.yml"})
}

func TestGitLabScriptListsAreCommands(t *testing.T) {
	r, _ := scanMem(map[string]string{".gitlab-ci.yml": "before_script:\n  - make deps\nj:\n  script:\n    - make build\n    - |\n      npm test\n      npm run lint\nk:\n  script: [ \"go test ./...\" ]\n  image: x\n"})
	eq(t, cmdsOf(r, ".gitlab-ci.yml"), []string{"run|make deps|2", "build|make build|5", "test|npm test|7", "lint|npm run lint|8", "test|go test ./...|10"})
	if len(r.CI) != 1 || r.CI[0].System != "gitlab-ci" {
		t.Fatalf("ci = %+v", r.CI)
	}
}

func TestDockerfileCitesFinalImageCmdAndEntrypoint(t *testing.T) {
	r, _ := scanMem(map[string]string{"api/Dockerfile": "FROM a:1 AS b\nRUN x\nFROM --platform=linux/amd64 c:2\nENTRYPOINT [\"/app\"]\nCMD [\"--a\", \\\n  \"--b\"]\n"})
	if len(r.Containers) != 1 {
		t.Fatalf("containers = %+v", r.Containers)
	}
	c := r.Containers[0]
	if c.Kind != "dockerfile" || c.Image != "c:2" || c.Source.File != "api/Dockerfile" || c.Source.Line != 3 {
		t.Fatalf("container = %+v", c)
	}
	eq(t, cmdsOf(r, "api/Dockerfile"), []string{"run|ENTRYPOINT [\"/app\"]|4", "run|CMD [\"--a\", \"--b\"]|5"})
}

func TestDockerfileVariantsAreFound(t *testing.T) {
	r, _ := scanMem(map[string]string{"Dockerfile.dev": "FROM x\n", "app.Dockerfile": "FROM y\n", "docs/Dockerfile.md": "FROM z\n"})
	if len(r.Containers) != 2 {
		t.Fatalf("containers = %+v", r.Containers)
	}
}

func TestComposeServicesAreContainers(t *testing.T) {
	r, _ := scanMem(map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx:1\n  w:\n    build: .\n    command: [\"./w\", \"-q\"]\n  db:\n    command: pg -c x\n    image: pg:16\nvolumes:\n  v: {}\n"})
	var got []string
	for _, c := range r.Containers {
		got = append(got, fmt.Sprintf("%s|%s|%d", c.Kind, c.Image, c.Source.Line))
	}
	eq(t, got, []string{"compose|nginx:1|2", "compose||4", "compose|pg:16|7"})
	eq(t, cmdsOf(r, "compose.yaml"), []string{"run|[\"./w\", \"-q\"]|6", "run|pg -c x|8"})
}

func TestEveryCommandIsNotExecutedAndCitesALine(t *testing.T) {
	for _, name := range []string{"cmd-polyglot", "cmd-ci", "cmd-container", "go-cli", "node-web"} {
		files, read := dirRepo(t, name)
		r := commands.Scan(files, read)
		if len(r.Commands) == 0 {
			t.Fatalf("%s: no command found", name)
		}
		for _, c := range r.Commands {
			if c.Executed {
				t.Fatalf("%s: %+v is marked executed", name, c)
			}
			checkSource(t, name, files, read, c.Source)
		}
		for _, e := range r.EntryPoints {
			checkSource(t, name, files, read, e.Source)
		}
		for _, c := range r.Containers {
			checkSource(t, name, files, read, c.Source)
		}
		for _, c := range r.CI {
			checkSource(t, name, files, read, c.Source)
		}
	}
}

func checkSource(t *testing.T, repo string, files []string, read func(string) ([]byte, error), s model.Source) {
	t.Helper()
	if s.Line < 1 && s.Key == "" {
		t.Fatalf("%s: %+v has neither a line nor a key", repo, s)
	}
	b, err := read(s.File)
	if err != nil {
		t.Fatalf("%s: cited file %s is not readable: %v", repo, s.File, err)
	}
	if n := strings.Count(string(b), "\n") + 1; s.Line > n {
		t.Fatalf("%s: %s cites line %d of %d", repo, s.File, s.Line, n)
	}
}

func dirRepo(t *testing.T, name string) ([]string, func(string) ([]byte, error)) {
	t.Helper()
	root := filepath.Join(testdata, "repos", name)
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files, func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.FromSlash(p))) }
}

type golden struct {
	EntryPoints []model.EntryPoint `json:"entry_points"`
	Commands    []model.Command    `json:"commands"`
	CI          []model.CI         `json:"ci"`
	Containers  []model.Container  `json:"containers"`
	Skipped     []commands.Skipped `json:"skipped"`
}

func TestResultEqualsTheGoldenFile(t *testing.T) {
	for _, name := range []string{"cmd-polyglot", "cmd-ci", "cmd-container", "go-cli", "node-web"} {
		t.Run(name, func(t *testing.T) {
			files, read := dirRepo(t, name)
			r := commands.Scan(files, read)
			g := golden{r.EntryPoints, r.Commands, r.CI, r.Containers, r.Skipped}
			got, err := json.MarshalIndent(g, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join(testdata, "golden", name+".commands.json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("result differs from %s\ngot:\n%s\nwant:\n%s", path, got, want)
			}
		})
	}
}

func TestOnlyListedFilesAreReadAndEachOnceAtMost(t *testing.T) {
	m := &mem{files: map[string]string{"Makefile": "a:\n", "package.json": "{}", "unlisted/Makefile": "b:\n"}}
	r := commands.Scan([]string{"Makefile", "package.json"}, m.Read)
	for _, p := range m.read {
		if p == "unlisted/Makefile" {
			t.Fatal("a path outside the file list was read")
		}
	}
	if len(r.Commands) != 1 {
		t.Fatalf("commands = %+v", r.Commands)
	}
}

func TestDependencyTreesAreNotScanned(t *testing.T) {
	r, _ := scanMem(map[string]string{"node_modules/x/package.json": "{\"scripts\":{\"a\":\"b\"}}", "vendor/y/Makefile": "z:\n", ".github/workflows/ci.yml": "x: 1\n"})
	if len(r.Commands) != 0 || len(r.EntryPoints) != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestOverCapAndUnreadableAndUnparsableFilesAreNamed(t *testing.T) {
	read := func(p string) ([]byte, error) {
		switch p {
		case "Makefile":
			return nil, fmt.Errorf("%s: %w", p, manifest.ErrTooLarge)
		case "justfile":
			return nil, errors.New("boom")
		}
		return []byte("{not json"), nil
	}
	r := commands.Scan([]string{"Makefile", "justfile", "package.json"}, read)
	var got []string
	for _, s := range r.Skipped {
		got = append(got, s.Path+"|"+s.Reason)
	}
	eq(t, got, []string{"Makefile|" + commands.ReasonOverCap, "justfile|" + commands.ReasonUnreadable, "package.json|" + commands.ReasonUnparsed})
}

func TestASecretShapedValueIsNeverQuoted(t *testing.T) {
	// The value is built at run time from parts, so no secret-shaped string sits in the repository.
	val := strings.Join([]string{"s3", "cr3t", "v4lue"}, "-")
	wf := "jobs:\n  j:\n    steps:\n      - run: deploy --token=" + val + " --env prod\n      - run: echo $TOKEN\n"
	r, _ := scanMem(map[string]string{".github/workflows/d.yml": wf})
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), val) {
		t.Fatalf("the value reached the result: %s", b)
	}
	got := cmdsOf(r, ".github/workflows/d.yml")
	if len(got) != 2 || !strings.Contains(got[0], "--token=") || !strings.Contains(got[1], "echo $TOKEN") {
		t.Fatalf("commands = %q", got)
	}
}

func TestNoUnreadableInputPanics(t *testing.T) {
	for _, body := range []string{"", "\x00\x01", "{", "[[bin]]", "tasks:\n", "FROM", "services:\n  a", "run:", "script:\n  - ", strings.Repeat("a:\n", 1000)} {
		files := map[string]string{}
		for _, n := range []string{"Makefile", "justfile", "Taskfile.yml", "package.json", "pyproject.toml", "Cargo.toml", "Dockerfile", "compose.yml", ".gitlab-ci.yml", ".github/workflows/w.yml", "m.go", "m.py"} {
			files[n] = body
		}
		scanMem(files)
	}
}
