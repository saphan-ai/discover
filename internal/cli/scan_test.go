package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/fixture"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(append([]string{"scan"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func temp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func digestOf(t *testing.T, root string) string {
	t.Helper()
	a, err := attest.Attest(root, attest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return a.Tree.Digest + "/" + func() string {
		if a.Untracked != nil {
			return a.Untracked.Digest
		}
		return ""
	}()
}

func setHook(t *testing.T, f func(root string)) {
	t.Helper()
	old := betweenPasses
	betweenPasses = f
	t.Cleanup(func() { betweenPasses = old })
}

func plainTarget(t *testing.T) string {
	t.Helper()
	root := filepath.Join(temp(t), "plain")
	os.Mkdir(root, 0o755)
	fixture.TreeA(t, root)
	return root
}

func gitTarget(t *testing.T) string {
	t.Helper()
	root := filepath.Join(temp(t), "repo")
	os.Mkdir(root, 0o755)
	fixture.GitRepo(t, root)
	return root
}

// Acceptance 4: the field mutated is ONE byte of ONE file in the target, between the two digest passes.
func TestProofEqualWithoutChangeNotEqualWithOneByte(t *testing.T) {
	root := plainTarget(t)
	out := filepath.Join(temp(t), "out")

	code, stdout, stderr := run(t, "--out", out, root)
	if code != ExitOK || !strings.Contains(stdout, "read-only proof: EQUAL ") {
		t.Fatalf("no change: exit %d\n%s\n%s", code, stdout, stderr)
	}
	t.Log(strings.TrimSpace(lastLine(stdout)))

	setHook(t, func(r string) {
		p := filepath.Join(r, "sub", "c.txt")
		b, _ := os.ReadFile(p)
		b[0] ^= 1 // one byte
		os.WriteFile(p, b, 0o644)
	})
	code, stdout, _ = run(t, "--out", out, root)
	if code != ExitNotEqual || !strings.Contains(stdout, "read-only proof: NOT-EQUAL ") {
		t.Fatalf("one byte changed: exit %d\n%s", code, stdout)
	}
	t.Log(strings.TrimSpace(lastLine(stdout)))
	f := strings.Fields(lastLine(stdout))
	if len(f) != 5 || f[3] == f[4] || len(f[3]) != 64 || len(f[4]) != 64 {
		t.Fatalf("NOT-EQUAL line %q must carry two different digests", lastLine(stdout))
	}
	var doc attestationDoc
	b, _ := os.ReadFile(filepath.Join(out, "ATTESTATION.json"))
	if err := json.Unmarshal(b, &doc); err != nil || doc.Proof.Status != "NOT-EQUAL" {
		t.Fatalf("attestation file: %v %+v", err, doc.Proof)
	}
}

func TestUntrackedChangeBetweenPassesIsNotEqual(t *testing.T) {
	root := gitTarget(t)
	os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("u1\n"), 0o644)
	setHook(t, func(r string) { os.WriteFile(filepath.Join(r, "untracked.txt"), []byte("u2\n"), 0o644) })
	code, stdout, _ := run(t, "--out", filepath.Join(temp(t), "out"), root)
	if code != ExitNotEqual || !strings.Contains(stdout, "(untracked)") {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
}

// Acceptance 5: a link to a file outside the tree is listed as a link; the content behind it is not read.
func TestSymlinkTargetContentIsInNoOutputFile(t *testing.T) {
	base := temp(t)
	secret := "content-behind-the-link-" + strings.Repeat("z", 20) + "\n"
	os.WriteFile(filepath.Join(base, "outside.txt"), []byte(secret), 0o644)
	root := filepath.Join(base, "tree")
	os.Mkdir(root, 0o755)
	fixture.TreeA(t, root)
	os.Symlink(filepath.Join(base, "outside.txt"), filepath.Join(root, "escape"))

	out := filepath.Join(temp(t), "out")
	code, stdout, stderr := run(t, "--out", out, root)
	if code != ExitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	h := sha256.Sum256([]byte(secret))
	needles := []string{hex.EncodeToString(h[:]), secret, "content-behind"}
	assertNoneContain(t, out, stdout, needles)
	// Negative control: the same search finds a digest that IS in the outputs.
	a, _ := attest.Attest(root, attest.Options{})
	b, _ := os.ReadFile(filepath.Join(out, "ATTESTATION.json"))
	if !strings.Contains(string(b), a.Tree.Digest) {
		t.Fatal("control: the tree digest is not in ATTESTATION.json, so the search proves nothing")
	}
	var doc attestationDoc
	json.Unmarshal(b, &doc)
	if doc.Attestation.Tree.Links != 3 {
		t.Errorf("links %d, want 3 (two fixture links and the escape)", doc.Attestation.Tree.Links)
	}
}

func assertNoneContain(t *testing.T, outDir, stdout string, needles []string) {
	t.Helper()
	texts := map[string]string{"stdout": stdout}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no output files: %v", err)
	}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(outDir, e.Name()))
		texts[e.Name()] = string(b)
	}
	for name, txt := range texts {
		for _, n := range needles {
			if strings.Contains(txt, n) {
				t.Errorf("%s contains %q", name, n)
			}
		}
	}
}

// Acceptance 6: no output holds the absolute root unless --record-root is given.
func TestAbsoluteRootOnlyWithRecordRoot(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) string{"plain": plainTarget, "git": gitTarget} {
		t.Run(name, func(t *testing.T) {
			root := mk(t)
			out := filepath.Join(temp(t), "out")
			code, stdout, stderr := run(t, "--out", out, root)
			if code != ExitOK {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
			assertNoneContain(t, out, stdout, []string{root, filepath.Dir(root)})
			b, _ := os.ReadFile(filepath.Join(out, "ATTESTATION.json"))
			if !strings.Contains(string(b), `"root": "`+filepath.Base(root)+`"`) {
				t.Errorf("root not recorded by base name:\n%s", b)
			}

			// Negative control: with the flag, the same search finds the root.
			out2 := filepath.Join(temp(t), "out")
			if code, _, _ := run(t, "--out", out2, "--record-root", root); code != ExitOK {
				t.Fatalf("exit %d", code)
			}
			b2, _ := os.ReadFile(filepath.Join(out2, "ATTESTATION.json"))
			if !strings.Contains(string(b2), root) {
				t.Fatal("control: --record-root did not record the root, so the search proves nothing")
			}
		})
	}
}

// Acceptance 7: --out inside a target is refused with exit 2 and the target is untouched.
func TestOutInsideTargetIsRefused(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) string{"plain": plainTarget, "git": gitTarget} {
		t.Run(name, func(t *testing.T) {
			root := mk(t)
			before := digestOf(t, root)
			snap := fixture.Snapshot(t, root)
			for _, out := range []string{
				filepath.Join(root, "discover-out"),
				filepath.Join(root, "sub", "deeper", "out"),
				root,
			} {
				code, stdout, stderr := run(t, "--out", out, root)
				if code != ExitUsage || !strings.Contains(stderr, "lies inside the target") || stdout != "" {
					t.Fatalf("--out %s: exit %d stdout %q stderr %q", out, code, stdout, stderr)
				}
			}
			if after := digestOf(t, root); after != before {
				t.Fatalf("digest moved after the refusal: %s -> %s", before, after)
			}
			if !reflect.DeepEqual(snap, fixture.Snapshot(t, root)) {
				t.Fatal("the refusal changed the target")
			}
			// Negative control: a directory beside the target is accepted.
			if code, _, stderr := run(t, "--out", filepath.Join(filepath.Dir(root), "elsewhere"), root); code != ExitOK {
				t.Fatalf("control: exit %d %s", code, stderr)
			}
		})
	}
}

func TestOutThroughASymlinkIntoTargetIsRefused(t *testing.T) {
	root := plainTarget(t)
	via := filepath.Join(temp(t), "via")
	if err := os.Symlink(root, via); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "--out", filepath.Join(via, "out"), root)
	if code != ExitUsage || !strings.Contains(stderr, "lies inside the target") {
		t.Fatalf("exit %d %s", code, stderr)
	}
}

// Acceptance 8: the scan leaves the target byte-identical, mtimes included; git mode includes .git/index.
func TestScanLeavesTargetUntouched(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) string{"plain": plainTarget, "git": gitTarget} {
		t.Run(name, func(t *testing.T) {
			root := mk(t)
			if name == "git" { // dirty, with an untracked file: the busiest case
				os.WriteFile(filepath.Join(root, "a.txt"), []byte("edited\n"), 0o644)
				os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("u\n"), 0o644)
			}
			digestBefore := digestOf(t, root)
			snapBefore := fixture.Snapshot(t, root)
			if name == "git" {
				if _, ok := snapBefore[filepath.Join(".git", "index")]; !ok {
					t.Fatal("fixture has no .git/index to watch")
				}
			}
			code, stdout, stderr := run(t, "--out", filepath.Join(temp(t), "out"), root)
			if code != ExitOK {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
			if name == "git" && !strings.Contains(stdout, "dirty: 2 paths") {
				t.Errorf("git output lacks the dirty line:\n%s", stdout)
			}
			if digestAfter := digestOf(t, root); digestAfter != digestBefore {
				t.Errorf("digest %s -> %s", digestBefore, digestAfter)
			}
			snapAfter := fixture.Snapshot(t, root)
			if !reflect.DeepEqual(snapBefore, snapAfter) {
				for p, s := range snapBefore {
					if snapAfter[p] != s {
						t.Errorf("%s: %+v -> %+v", p, s, snapAfter[p])
					}
				}
				for p := range snapAfter {
					if _, ok := snapBefore[p]; !ok {
						t.Errorf("%s: appeared", p)
					}
				}
			}
		})
	}
}

// Negative control for the watcher above: the snapshot does see a one-byte write.
func TestSnapshotSeesAChange(t *testing.T) {
	root := plainTarget(t)
	before := fixture.Snapshot(t, root)
	p := filepath.Join(root, "a.txt")
	os.WriteFile(p, []byte("alphb\n"), 0o644)
	os.Chtimes(p, fileTime(1), fileTime(1))
	if reflect.DeepEqual(before, fixture.Snapshot(t, root)) {
		t.Fatal("snapshot did not notice a rewritten file")
	}
}

func TestDirtyGitTargetIsScannedAndReported(t *testing.T) {
	root := gitTarget(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("SECRET-LOOKING-EDIT\n"), 0o644)
	out := filepath.Join(temp(t), "out")
	code, stdout, _ := run(t, "--out", out, root)
	if code != ExitOK || !strings.Contains(stdout, "dirty: 1 paths") || !strings.Contains(stdout, "branch: main") {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	assertNoneContain(t, out, stdout, []string{"SECRET-LOOKING-EDIT"})
}

func TestManifestFileIsWrittenAndEmptyForNow(t *testing.T) {
	root := plainTarget(t)
	out := filepath.Join(temp(t), "out")
	if code, _, e := run(t, "--out", out, root); code != ExitOK {
		t.Fatal(e)
	}
	b, err := os.ReadFile(filepath.Join(out, "READ_MANIFEST.json"))
	if err != nil || !strings.Contains(string(b), `"schema": "discover-read-manifest/v1"`) {
		t.Fatalf("%v %s", err, b)
	}
}

func TestSeveralTargetsGetTheirOwnDirectories(t *testing.T) {
	a, b := plainTarget(t), plainTarget(t) // same base name "plain"
	out := filepath.Join(temp(t), "out")
	code, stdout, stderr := run(t, "--out", out, a, b)
	if code != ExitOK || strings.Count(stdout, "read-only proof: EQUAL") != 2 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, d := range []string{"plain", "plain-2"} {
		if _, err := os.Stat(filepath.Join(out, d, "ATTESTATION.json")); err != nil {
			t.Error(err)
		}
	}
}

func TestBadTargets(t *testing.T) {
	d := temp(t)
	f := filepath.Join(d, "file")
	os.WriteFile(f, nil, 0o644)
	for _, tc := range [][]string{{filepath.Join(d, "missing")}, {f}, {"--bogus", d}} {
		code, _, _ := run(t, append([]string{"--out", filepath.Join(temp(t), "o")}, tc...)...)
		if code != ExitUsage {
			t.Errorf("%v: exit %d, want %d", tc, code, ExitUsage)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func fileTime(n int64) time.Time { return time.Unix(n, 0) }
