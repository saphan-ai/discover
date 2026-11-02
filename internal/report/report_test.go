package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/model"
)

var update = flag.Bool("update", false, "rewrite the golden REPORT.md files under testdata/report")

// fixtureDir holds the hand-built models and their golden reports.
var fixtureDir = filepath.Join("..", "..", "testdata", "report")

// wantHeadings is the contract (per-repo-report-v1): the metadata block, then ten numbered sections, in this order.
var wantHeadings = []string{
	"## Repo metadata (attestation echo)",
	"## 1. Top-level inventory",
	"## 2. Tech stack inventory",
	"## 3. Project structure deep-dive",
	"## 4. Entry points",
	"## 5. Build / run / test",
	"## 6. Cross-repo wiring",
	"## 7. Findings",
	"## 8. UI strings & screens preview",
	"## 9. Open questions",
	"## 10. Recommendations",
}

// load reads testdata/report/<name>.json, refuses it unless model.Validate accepts it, and decodes it.
func load(t *testing.T, name string) model.Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := model.Validate(data); err != nil {
		t.Fatalf("fixture %s is not a valid discover/v1 report: %v", name, err)
	}
	var r model.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func render(t *testing.T, name string) []byte {
	t.Helper()
	out, err := Render(load(t, name), 0)
	if err != nil {
		t.Fatalf("Render(%s): %v", name, err)
	}
	return out
}

func TestRenderMatchesGolden(t *testing.T) {
	for _, name := range []string{"full", "empty", "hostile"} {
		t.Run(name, func(t *testing.T) {
			got := render(t, name)
			path := filepath.Join(fixtureDir, name+".REPORT.md")
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
				t.Errorf("%s differs from its golden; rerun with -update and review the diff\n--- got ---\n%s", path, got)
			}
		})
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	for _, name := range []string{"full", "empty", "hostile"} {
		t.Run(name, func(t *testing.T) {
			if a, b := render(t, name), render(t, name); !bytes.Equal(a, b) {
				t.Errorf("two renders of %s differ", name)
			}
		})
	}
}

// headings returns every level-two heading line of a report, in order.
func headings(report []byte) []string {
	var out []string
	for _, line := range strings.Split(string(report), "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, line)
		}
	}
	return out
}

func TestEveryReportCarriesTheTenSectionsInOrder(t *testing.T) {
	for _, name := range []string{"empty", "full", "hostile"} {
		t.Run(name, func(t *testing.T) {
			got := headings(render(t, name))
			if strings.Join(got, "\n") != strings.Join(wantHeadings, "\n") {
				t.Errorf("headings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantHeadings, "\n"))
			}
		})
	}
}

// sectionBodies splits a report into the non-blank lines under each numbered section heading.
func sectionBodies(report []byte) map[string][]string {
	bodies := map[string][]string{}
	current := ""
	for _, line := range strings.Split(string(report), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			current = line
		case current != "" && strings.TrimSpace(line) != "":
			bodies[current] = append(bodies[current], line)
		}
	}
	return bodies
}

func TestAnEmptySectionIsOneHonestAbsenceLine(t *testing.T) {
	bodies := sectionBodies(render(t, "empty"))
	for _, h := range wantHeadings[1:] {
		body := bodies[h]
		if len(body) != 1 {
			t.Errorf("%s: want exactly one line, got %d: %q", h, len(body), body)
			continue
		}
		if !strings.HasPrefix(body[0], "_Absent._ ") {
			t.Errorf("%s: the line does not declare the absence: %q", h, body[0])
		}
	}
	for _, h := range []string{wantHeadings[3], wantHeadings[9], wantHeadings[10]} {
		if !strings.Contains(bodies[h][0], "--model") || !strings.Contains(bodies[h][0], "a later version") {
			t.Errorf("%s: a model-written section with no model must say how to turn one on: %q", h, bodies[h][0])
		}
	}
}

func TestSecretFindingPrintsLocationAndTypeOnly(t *testing.T) {
	r := load(t, "full")
	r.Repos[0].Findings = []model.Finding{{
		Kind: "secret", Severity: "high", SecretType: "api-token",
		Source: model.Source{File: "config/app.env", Line: 7, Key: "SERVICE_TOKEN"},
	}}
	out, err := Render(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "api-token") {
			line = l
		}
	}
	if !strings.Contains(line, "config/app.env:7") {
		t.Errorf("secret row lacks file:line: %q", line)
	}
	if strings.Contains(string(out), "SERVICE_TOKEN") {
		t.Errorf("secret row prints more than file, line and type: %q", line)
	}
}

func TestUnpinnedVersionPrintsNotPinned(t *testing.T) {
	r := load(t, "empty")
	r.Repos[0].Stack = []model.StackRow{{Layer: "build", Technology: "Make", Source: model.Source{File: "Makefile", Line: 1}}}
	out, err := Render(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "| build | Make | not pinned | `Makefile:1` |") {
		t.Errorf("a null version must print as not pinned:\n%s", out)
	}
}

func TestRenderRefusesARepoIndexOutOfRange(t *testing.T) {
	r := load(t, "empty")
	for _, i := range []int{-1, 1} {
		if _, err := Render(r, i); err == nil {
			t.Errorf("Render(_, %d) on a one-repo report returned no error", i)
		}
	}
}
