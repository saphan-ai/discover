package scan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan"
)

var stackFixtures = []string{
	"stack-go", "stack-node", "stack-python", "stack-rust", "stack-jvm", "stack-ruby", "stack-php", "stack-tooling",
}

func TestStackSectionIsFilledByRun(t *testing.T) {
	for _, name := range stackFixtures {
		t.Run(name, func(t *testing.T) {
			res, err := scan.Run(filepath.Join(fixtures, "repos", name), attest.ModeDir, scan.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Repo.Stack) == 0 {
				t.Fatal("the stack scanner produced no rows")
			}
		})
	}
}

func TestStackOutputValidates(t *testing.T) {
	for _, name := range stackFixtures {
		t.Run(name, func(t *testing.T) {
			res, err := scan.Run(filepath.Join(fixtures, "repos", name), attest.ModeDir, scan.Options{})
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(fixtures, "schema", "valid", "two-repos.json"))
			if err != nil {
				t.Fatal(err)
			}
			var rep model.Report
			if err := json.Unmarshal(b, &rep); err != nil {
				t.Fatal(err)
			}
			rep.Repos = rep.Repos[:1]
			rep.Repos[0].Inventory = res.Repo.Inventory
			rep.Repos[0].Structure = res.Repo.Structure
			rep.Repos[0].Stack = res.Repo.Stack
			rep.Repos[0].Attestation.ReadManifest = res.ReadManifest
			out, err := model.Marshal(&rep)
			if err != nil {
				t.Fatalf("model.Marshal refuses the report: %v", err)
			}
			if err := model.Validate(out); err != nil {
				t.Fatalf("model.Validate: %v", err)
			}
		})
	}
}

func TestStackFilesAreInTheReadManifest(t *testing.T) {
	res, err := scan.Run(filepath.Join(fixtures, "repos", "stack-go"), attest.ModeDir, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.ReadManifest.Entries {
		if e.Path == "go.mod" {
			return
		}
	}
	t.Fatalf("go.mod was read but is not in the read manifest: %+v", res.ReadManifest.Entries)
}

func TestStackOverTheCapIsInSkipped(t *testing.T) {
	res, err := scan.Run(filepath.Join(fixtures, "repos", "stack-go"), attest.ModeDir, scan.Options{MaxBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repo.Stack) != 0 {
		t.Errorf("rows from a file over the cap: %+v", res.Repo.Stack)
	}
	for _, s := range res.Skipped {
		if s.Path == "go.mod" {
			return
		}
	}
	t.Fatalf("go.mod over the cap is not named in Skipped: %+v", res.Skipped)
}
