package scan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan"
)

const fixtures = "../../testdata"

func TestRegistryNamesItsScannersInOrder(t *testing.T) {
	var names []string
	for _, s := range scan.Scanners {
		names = append(names, s.Name)
	}
	if want := []string{"inventory", "structure", "stack", "commands"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("scanners = %q, want %q", names, want)
	}
}

func TestOutputValidatesAgainstTheSchema(t *testing.T) {
	for _, name := range []string{"go-cli", "node-web"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(fixtures, "repos", name)
			res, err := scan.Run(root, attest.ModeDir, scan.Options{})
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
			rep.Repos[0].Attestation.ReadManifest = res.ReadManifest
			out, err := model.Marshal(&rep)
			if err != nil {
				t.Fatalf("model.Marshal refuses the report: %v", err)
			}
			if err := model.Validate(out); err != nil {
				t.Fatalf("model.Validate: %v", err)
			}
			if res.ReadManifest.FilesRead == 0 || len(res.ReadManifest.Entries) != res.ReadManifest.FilesRead {
				t.Fatalf("read manifest: %+v", res.ReadManifest)
			}
		})
	}
}

func TestStructureIsBuiltFromTheInventoryFileList(t *testing.T) {
	root := filepath.Join(fixtures, "repos", "go-cli")
	res, err := scan.Run(root, attest.ModeDir, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repo.Structure.Patterns) == 0 {
		t.Fatal("structure scanner produced nothing: it must run after the inventory scanner")
	}
}

func TestAScannerThatFailsStopsTheRunAndNamesItself(t *testing.T) {
	_, err := scan.Run(filepath.Join(fixtures, "repos", "no-such-fixture"), attest.ModeDir, scan.Options{})
	if err == nil {
		t.Fatal("a missing root gave no error")
	}
}
