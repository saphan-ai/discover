package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saphan-ai/discover/internal/fixture"
	"github.com/saphan-ai/discover/internal/safepath"
)

func tree(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture.TreeA(t, d)
	return d
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestRecordHoldsPathSizeMtimeAndDigest(t *testing.T) {
	root := tree(t)
	when := time.Date(2020, 1, 2, 3, 4, 5, 600, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "sub", "c.txt"), when, when); err != nil {
		t.Fatal(err)
	}
	m := New(root)
	e, err := m.Record("sub/c.txt", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := Entry{Path: "sub/c.txt", Size: 8, Mtime: "2020-01-02T03:04:05.0000006Z", SHA256: sum("charlie\n")}
	if e != want {
		t.Fatalf("entry %+v, want %+v", e, want)
	}
}

func TestEntriesSortedAndSerialised(t *testing.T) {
	root := tree(t)
	m := New(root)
	for _, rel := range []string{"with space.txt", "sub/c.txt", "a.txt", "sub/c.txt"} {
		if _, err := m.Record(rel, 0); err != nil {
			t.Fatal(err)
		}
	}
	b, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema  string
		Entries []Entry
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range doc.Entries {
		paths = append(paths, e.Path)
	}
	if doc.Schema != "discover-read-manifest/v1" || strings.Join(paths, ",") != "a.txt,sub/c.txt,with space.txt" {
		t.Fatalf("schema %q paths %v (a repeated path must be one entry)", doc.Schema, paths)
	}
	if _, err := Decode(b); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte(`{"schema":"other/v9","entries":[]}`)); err == nil {
		t.Fatal("a foreign schema was accepted")
	}
	if got := string(b); strings.Contains(got, root) {
		t.Fatal("manifest holds the absolute root")
	}
}

func TestEmptyManifestSerialisesAnEmptyList(t *testing.T) {
	b, err := New(t.TempDir()).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"entries": []`) {
		t.Fatalf("got %s", b)
	}
}

func TestSizeCap(t *testing.T) {
	root := tree(t)
	m := New(root)
	if _, err := m.Record("sub/c.txt", 7); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("8 bytes under a cap of 7: %v", err)
	}
	if len(m.Entries()) != 0 {
		t.Fatal("an over-cap file was recorded")
	}
	if _, err := m.Record("sub/c.txt", 8); err != nil {
		t.Fatalf("8 bytes under a cap of 8: %v", err)
	}
}

func TestEscapesAreRefusedAndNotRead(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	outside := filepath.Join(base, "outside.txt")
	os.WriteFile(outside, []byte("outside"), 0o644)
	root := filepath.Join(base, "tree")
	os.Mkdir(root, 0o755)
	fixture.TreeA(t, root)
	os.Symlink(outside, filepath.Join(root, "file-link"))
	os.Symlink(base, filepath.Join(root, "dir-link"))

	m := New(root)
	for _, rel := range []string{"../outside.txt", "sub/../../outside.txt", "/etc/hosts", "dir-link/outside.txt", "file-link", "link-in", ""} {
		if _, err := m.Record(rel, 0); err == nil {
			t.Errorf("%q was read", rel)
		}
	}
	if len(m.Entries()) != 0 {
		t.Fatalf("entries %+v after refusals", m.Entries())
	}
	// Negative control: a plain path in the same tree reads.
	if _, err := m.Record("a.txt", 0); err != nil {
		t.Fatal(err)
	}
}

func TestReadReturnsBytesAndDetectsChange(t *testing.T) {
	root := tree(t)
	m := New(root)
	b, e, err := m.Read("a.txt", 0)
	if err != nil || string(b) != "alpha\n" || e.Size != 6 {
		t.Fatalf("read %q %+v %v", b, e, err)
	}
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha two\n"), 0o644)
	if _, _, err := m.Read("a.txt", 0); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed file: %v", err)
	}
}

func TestSafepathResolve(t *testing.T) {
	root := tree(t)
	cases := []struct {
		rel string
		ok  bool
	}{
		{"a.txt", true}, {"sub/deep/d.txt", true}, {"sub/./c.txt", true}, {"link-in", true},
		{"..", false}, {"../x", false}, {"sub/../../x", false}, {"/abs", false}, {"", false}, {"a\x00b", false},
	}
	for _, c := range cases {
		_, err := safepath.Resolve(root, c.rel)
		if (err == nil) != c.ok {
			t.Errorf("Resolve(%q) err = %v, want ok=%v", c.rel, err, c.ok)
		}
	}
}
