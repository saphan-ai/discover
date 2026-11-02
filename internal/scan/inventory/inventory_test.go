package inventory_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/fixture"
	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/inventory"
)

const fixtures = "../../../testdata/repos"

// update rewrites the golden files from the git-mode result: go test ./internal/scan/inventory -update. Read the
// diff before committing it.
var update = flag.Bool("update", false, "rewrite the golden files")

// copyTree copies a committed fixture into a fresh directory, so a test may add files to it.
func copyTree(t *testing.T, name string) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join(fixtures, name)
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// asGit turns dir into a committed git repository.
func asGit(t *testing.T, dir string) {
	t.Helper()
	fixture.Git(t, dir, "init", "-q")
	fixture.Git(t, dir, "add", "-A")
	fixture.Git(t, dir, "commit", "-q", "-m", "fixture")
}

func put(t *testing.T, root, rel string, body []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, root string, mode attest.Mode, opts inventory.Options) (inventory.Result, *manifest.Manifest) {
	t.Helper()
	m := manifest.New(root)
	res, err := inventory.Scan(root, mode, m, opts)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return res, m
}

type golden struct {
	Inventory model.Inventory     `json:"inventory"`
	Skipped   []inventory.Skipped `json:"skipped"`
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestInventoryEqualsTheGoldenFileInBothModes(t *testing.T) {
	for _, name := range []string{"go-cli", "node-web"} {
		for _, mode := range []attest.Mode{attest.ModeGit, attest.ModeDir} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				dir := copyTree(t, name)
				if mode == attest.ModeGit {
					asGit(t, dir)
				}
				res, _ := run(t, dir, mode, inventory.Options{})
				got := marshal(t, golden{res.Inventory, res.Skipped})
				path := filepath.Join("..", "..", "..", "testdata", "golden", name+".inventory.json")
				if *update && mode == attest.ModeGit {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, got, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("inventory differs from %s\ngot:\n%s\nwant:\n%s", path, got, want)
				}
			})
		}
	}
}

func TestOverCapFileIsSkippedAndNeverRead(t *testing.T) {
	for _, mode := range []attest.Mode{attest.ModeGit, attest.ModeDir} {
		t.Run(string(mode), func(t *testing.T) {
			dir := copyTree(t, "go-cli")
			put(t, dir, "data/big.txt", bytes.Repeat([]byte("a"), 2<<20))
			if mode == attest.ModeGit {
				asGit(t, dir)
			}
			res, m := run(t, dir, mode, inventory.Options{})
			want := inventory.Skipped{Path: "data/big.txt", Reason: "over size cap"}
			if !contains(res.Skipped, want) {
				t.Fatalf("Skipped = %+v, want it to hold %+v", res.Skipped, want)
			}
			for _, e := range m.Entries() {
				if e.Path == "data/big.txt" {
					t.Fatalf("the over-cap file is in the manifest: %+v", e)
				}
			}
			if res.Inventory.BytesTotal < 2<<20 {
				t.Fatalf("bytes_total = %d: an over-cap file still counts as a file of the repository", res.Inventory.BytesTotal)
			}
		})
	}
}

func TestCapIsTheCallersAndTheEdgeIsInclusive(t *testing.T) {
	dir := copyTree(t, "go-cli")
	put(t, dir, "edge/at.txt", bytes.Repeat([]byte("a"), 100))
	put(t, dir, "edge/over.txt", bytes.Repeat([]byte("a"), 101))
	res, m := run(t, dir, attest.ModeDir, inventory.Options{MaxBytes: 100})
	if !contains(res.Skipped, inventory.Skipped{Path: "edge/over.txt", Reason: "over size cap"}) {
		t.Errorf("a file of cap+1 bytes is not skipped: %+v", res.Skipped)
	}
	if contains(res.Skipped, inventory.Skipped{Path: "edge/at.txt", Reason: "over size cap"}) {
		t.Errorf("a file of exactly cap bytes is skipped")
	}
	if !recorded(m, "edge/at.txt") {
		t.Errorf("a file of exactly cap bytes is not in the manifest")
	}
}

func TestGitModeHonoursTheRootAndTheNestedGitignore(t *testing.T) {
	dir := copyTree(t, "node-web")
	asGit(t, dir)
	put(t, dir, "dist/bundle.js", []byte("ignored by the root file\n"))
	put(t, dir, "web/debug.log", []byte("ignored by the nested file\n"))
	put(t, dir, "src/untracked.js", []byte("untracked and not ignored\n"))

	res, m := run(t, dir, attest.ModeGit, inventory.Options{})
	for _, ignored := range []string{"dist/bundle.js", "web/debug.log"} {
		if recorded(m, ignored) || contains(res.Skipped, inventory.Skipped{Path: ignored, Reason: "binary"}) {
			t.Errorf("%s is ignored by git and was read", ignored)
		}
	}
	if !recorded(m, "src/untracked.js") {
		t.Errorf("an untracked file that is not ignored was not read")
	}

	plain := copyTree(t, "node-web")
	put(t, plain, "dist/bundle.js", []byte("not ignored without git\n"))
	_, pm := run(t, plain, attest.ModeDir, inventory.Options{})
	if !recorded(pm, "dist/bundle.js") {
		t.Errorf("dir mode has no .gitignore: dist/bundle.js should be read")
	}
}

func TestBinaryIsNulInTheFirst8KiB(t *testing.T) {
	dir := t.TempDir()
	last := bytes.Repeat([]byte("a"), 9000)
	last[8191] = 0
	late := bytes.Repeat([]byte("a"), 9000)
	late[8192] = 0
	put(t, dir, "last.dat", last)
	put(t, dir, "late.dat", late)
	put(t, dir, "plain.txt", []byte("text\n"))
	res, m := run(t, dir, attest.ModeDir, inventory.Options{})
	if !contains(res.Skipped, inventory.Skipped{Path: "last.dat", Reason: "binary"}) {
		t.Errorf("NUL at byte 8191 is not binary: %+v", res.Skipped)
	}
	if contains(res.Skipped, inventory.Skipped{Path: "late.dat", Reason: "binary"}) {
		t.Errorf("NUL at byte 8192 is past the first 8 KiB and must not make the file binary")
	}
	if !recorded(m, "late.dat") || !recorded(m, "plain.txt") {
		t.Errorf("a text file is not in the manifest")
	}
	if !recorded(m, "last.dat") {
		t.Errorf("a binary file was read to be told apart, so it is in the manifest")
	}
}

func TestSymlinkIsNeverFollowed(t *testing.T) {
	outside := t.TempDir()
	put(t, outside, "secret.txt", []byte("outside the root\n"))
	for _, mode := range []attest.Mode{attest.ModeGit, attest.ModeDir} {
		t.Run(string(mode), func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, "real.txt", []byte("real\n"))
			if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, "linkdir")); err != nil {
				t.Fatal(err)
			}
			if mode == attest.ModeGit {
				asGit(t, dir)
			}
			res, m := run(t, dir, mode, inventory.Options{})
			if !contains(res.Skipped, inventory.Skipped{Path: "link.txt", Reason: "not a regular file"}) {
				t.Errorf("a symlink is not listed as skipped: %+v", res.Skipped)
			}
			for _, e := range m.Entries() {
				if e.Path != "real.txt" {
					t.Errorf("manifest holds %q, only real.txt may be read", e.Path)
				}
			}
			if res.Inventory.FilesTotal != 1 {
				t.Errorf("files_total = %d, want 1", res.Inventory.FilesTotal)
			}
			kinds := map[string]string{}
			for _, e := range res.Inventory.TopLevel {
				kinds[e.Name] = e.Kind
			}
			if kinds["link.txt"] != "symlink" || kinds["linkdir"] != "symlink" || kinds["real.txt"] != "file" {
				t.Errorf("top-level kinds = %v", kinds)
			}
		})
	}
}

func TestATrackedFileThatWasRemovedIsListedUnreadable(t *testing.T) {
	dir := copyTree(t, "go-cli")
	asGit(t, dir)
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	res, m := run(t, dir, attest.ModeGit, inventory.Options{})
	if !contains(res.Skipped, inventory.Skipped{Path: "README.md", Reason: "unreadable"}) {
		t.Fatalf("Skipped = %+v", res.Skipped)
	}
	if recorded(m, "README.md") {
		t.Fatal("a file that could not be read is in the manifest")
	}
}

func TestEveryReadFileIsInTheManifestAndNothingElse(t *testing.T) {
	dir := copyTree(t, "node-web")
	put(t, dir, "data/big.txt", bytes.Repeat([]byte("a"), 2<<20))
	asGit(t, dir)
	res, m := run(t, dir, attest.ModeGit, inventory.Options{})
	skipped := map[string]bool{}
	for _, s := range res.Skipped {
		if s.Reason != "binary" {
			skipped[s.Path] = true
		}
	}
	var want []string
	for _, p := range res.Files {
		if !skipped[p] {
			want = append(want, p)
		}
	}
	var got []string
	for _, e := range m.Entries() {
		got = append(got, e.Path)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %q\nlisted files that were not skipped = %q", got, want)
	}
}

func TestTotalsAgreeWithTheAttestation(t *testing.T) {
	for _, mode := range []attest.Mode{attest.ModeGit, attest.ModeDir} {
		t.Run(string(mode), func(t *testing.T) {
			dir := copyTree(t, "go-cli")
			if mode == attest.ModeGit {
				asGit(t, dir)
				put(t, dir, "extra.txt", []byte("untracked\n"))
			}
			res, _ := run(t, dir, mode, inventory.Options{})
			a, err := attest.Attest(dir, attest.Options{})
			if err != nil {
				t.Fatal(err)
			}
			files, bytes := a.Tree.Files, a.Tree.Bytes
			if a.Untracked != nil {
				files += a.Untracked.Files
				bytes += a.Untracked.Bytes
			}
			if res.Inventory.FilesTotal != files || int64(res.Inventory.BytesTotal) != bytes {
				t.Fatalf("inventory %d files %d bytes; attestation %d files %d bytes",
					res.Inventory.FilesTotal, res.Inventory.BytesTotal, files, bytes)
			}
		})
	}
}

func TestLanguagesAreCountedByExtensionAndSorted(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.go", "b.go", "c.GO", "x.js", "y.ts", "z.ts", "w.ts", "README.md", "noext", "Makefile"} {
		put(t, dir, p, []byte("x\n"))
	}
	res, _ := run(t, dir, attest.ModeDir, inventory.Options{})
	want := []model.LanguageShare{{Name: "Go", Files: 3}, {Name: "TypeScript", Files: 3}, {Name: "JavaScript", Files: 1}, {Name: "Markdown", Files: 1}}
	if !reflect.DeepEqual(res.Inventory.Languages, want) {
		t.Fatalf("languages = %+v, want %+v", res.Inventory.Languages, want)
	}
}

func TestAnEmptyDirectoryGivesAnEmptyButValidInventory(t *testing.T) {
	res, _ := run(t, t.TempDir(), attest.ModeDir, inventory.Options{})
	if res.Inventory.FilesTotal != 0 || res.Inventory.TopLevel == nil || res.Inventory.Languages == nil || res.Skipped == nil {
		t.Fatalf("empty root: %+v (slices must be empty, never null)", res)
	}
}

func TestGitModeRefusesAPlainDirectory(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a.txt", []byte("a\n"))
	_, err := inventory.Scan(dir, attest.ModeGit, manifest.New(dir), inventory.Options{})
	if err == nil {
		t.Fatal("git mode on a plain directory returned no error")
	}
}

func contains(list []inventory.Skipped, s inventory.Skipped) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func recorded(m *manifest.Manifest, path string) bool {
	for _, e := range m.Entries() {
		if e.Path == path {
			return true
		}
	}
	return false
}
