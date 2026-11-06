package commands_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan"
)

var repos = []string{"cmd-polyglot", "cmd-ci", "cmd-container", "go-cli", "node-web"}

// fakeTools puts a fake make, npm, go, just, task and docker first on PATH. Each one writes a marker file named after
// itself into the returned directory when it runs, and prints nothing.
func fakeTools(t *testing.T) (bin, markers string) {
	t.Helper()
	bin, markers = t.TempDir(), t.TempDir()
	for _, name := range []string{"make", "npm", "go", "just", "task", "docker", "python", "cargo", "sh"} {
		body := "#!/bin/sh\n: > '" + filepath.Join(markers, name) + "'\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin, markers
}

func markerCount(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

func TestAScanNeverRunsTheCommandsItCites(t *testing.T) {
	_, markers := fakeTools(t)
	for _, name := range repos {
		res, err := scan.Run(filepath.Join(testdata, "repos", name), attest.ModeDir, scan.Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(res.Repo.Commands) == 0 {
			t.Fatalf("%s: the scan cited no command, so the marker check proves nothing", name)
		}
	}
	if n := markerCount(t, markers); n != 0 {
		es, _ := os.ReadDir(markers)
		t.Fatalf("%d fake tool(s) ran during the scan: %v", n, es)
	}
}

// The negative control: the instrument must see an execution when there is one.
func TestTheMarkerInstrumentSeesAnExecution(t *testing.T) {
	_, markers := fakeTools(t)
	for _, name := range []string{"make", "npm", "go"} {
		if err := exec.Command(name).Run(); err != nil {
			t.Fatalf("running the fake %s: %v", name, err)
		}
	}
	if n := markerCount(t, markers); n != 3 {
		t.Fatalf("the fakes left %d markers after three direct runs, want 3", n)
	}
}

func TestThePackageDoesNotImportOsExec(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 10 {
		t.Fatalf("go list returned %d packages; the check cannot see the tree", len(deps))
	}
	for _, d := range deps {
		if d == "os/exec" {
			t.Fatal("os/exec is in the dependency tree of the commands package")
		}
	}
}

func TestOutputValidatesOverEachFixture(t *testing.T) {
	for _, name := range repos {
		t.Run(name, func(t *testing.T) {
			res, err := scan.Run(filepath.Join(testdata, "repos", name), attest.ModeDir, scan.Options{})
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(testdata, "schema", "valid", "two-repos.json"))
			if err != nil {
				t.Fatal(err)
			}
			var rep model.Report
			if err := json.Unmarshal(b, &rep); err != nil {
				t.Fatal(err)
			}
			rep.Repos = rep.Repos[:1]
			r := &rep.Repos[0]
			r.Inventory, r.Structure = res.Repo.Inventory, res.Repo.Structure
			r.EntryPoints, r.Commands, r.CI, r.Containers = res.Repo.EntryPoints, res.Repo.Commands, res.Repo.CI, res.Repo.Containers
			r.Attestation.ReadManifest = res.ReadManifest
			out, err := model.Marshal(&rep)
			if err != nil {
				t.Fatalf("model.Marshal: %v", err)
			}
			if err := model.Validate(out); err != nil {
				t.Fatalf("model.Validate: %v", err)
			}
		})
	}
}

func TestRunFillsTheFourSections(t *testing.T) {
	res, err := scan.Run(filepath.Join(testdata, "repos", "cmd-polyglot"), attest.ModeDir, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repo.Commands) == 0 || len(res.Repo.EntryPoints) == 0 {
		t.Fatalf("commands %d, entry points %d", len(res.Repo.Commands), len(res.Repo.EntryPoints))
	}
	res, err = scan.Run(filepath.Join(testdata, "repos", "cmd-ci"), attest.ModeDir, scan.Options{})
	if err != nil || len(res.Repo.CI) != 3 {
		t.Fatalf("ci = %+v, err %v", res.Repo.CI, err)
	}
	res, err = scan.Run(filepath.Join(testdata, "repos", "cmd-container"), attest.ModeDir, scan.Options{})
	if err != nil || len(res.Repo.Containers) != 5 {
		t.Fatalf("containers = %+v, err %v", res.Repo.Containers, err)
	}
}

func TestAnOverCapFileIsSkippedByNameThroughRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("build:\n\tx\n"+strings.Repeat("#", 4096)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := scan.Run(root, attest.ModeDir, scan.Options{MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range res.Skipped {
		if s.Path == "Makefile" {
			n++
		}
	}
	if n != 1 || len(res.Repo.Commands) != 0 {
		t.Fatalf("skipped %+v, commands %+v: want the Makefile named once and not read", res.Skipped, res.Repo.Commands)
	}
}
