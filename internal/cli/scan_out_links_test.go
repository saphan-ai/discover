package cli

// The scan never follows a symbolic link in what it writes: not the per-target output directory, not an output
// file. A link is refused by name before anything is read, whether it points into a target or anywhere else.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

func mustLink(t *testing.T, to, at string) {
	t.Helper()
	if err := os.Symlink(to, at); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustNotExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); err == nil {
		t.Errorf("%s was written", filepath.Base(p))
	}
}

// Layout 1: several targets, and <out>/c already is a link.
func TestPerTargetOutputDirectoryThatIsALinkIsRefused(t *testing.T) {
	for _, into := range []string{"into a target", "outside every target"} {
		t.Run(into, func(t *testing.T) {
			x := temp(t)
			a := tree(t, filepath.Join(temp(t), "a"))
			c := tree(t, filepath.Join(temp(t), "c"))
			dest := filepath.Join(a, "sub")
			if into != "into a target" {
				dest = tree(t, filepath.Join(temp(t), "elsewhere"))
			}
			mustLink(t, dest, filepath.Join(x, "c"))
			snapA, snapDest := fixture.Snapshot(t, a), fixture.Snapshot(t, dest)
			passes := countingHook(t)
			code, stdout, stderr := run(t, "--out", x, a, c)
			if code != ExitUsage || !strings.Contains(stderr, `"c"`) || !strings.Contains(stderr, "symbolic link") {
				t.Errorf("exit %d, stderr %q", code, stderr)
			}
			if stdout != "" || *passes != 0 {
				t.Errorf("something was read: %d passes, stdout %q", *passes, stdout)
			}
			if strings.Contains(stderr, x) {
				t.Errorf("the refusal names an absolute path: %q", stderr)
			}
			for _, f := range []string{manifestFile, attestationFile} {
				mustNotExist(t, filepath.Join(dest, f))
			}
			if !reflect.DeepEqual(snapA, fixture.Snapshot(t, a)) || !reflect.DeepEqual(snapDest, fixture.Snapshot(t, dest)) {
				t.Error("the refusal changed a directory")
			}
		})
	}
}

// Layout 2: one target, and an output file already is a link.
func TestOutputFileThatIsALinkIsRefused(t *testing.T) {
	for _, f := range []string{manifestFile, attestationFile} {
		for _, into := range []string{"into the target", "outside every target"} {
			t.Run(f+" "+into, func(t *testing.T) {
				x := temp(t)
				a := tree(t, filepath.Join(temp(t), "a"))
				victim := filepath.Join(a, "a.txt")
				if into != "into the target" {
					victim = filepath.Join(temp(t), "victim.txt")
					if err := os.WriteFile(victim, []byte("alpha\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				mustLink(t, victim, filepath.Join(x, f))
				passes := countingHook(t)
				code, _, stderr := run(t, "--out", x, a)
				if code != ExitUsage || !strings.Contains(stderr, f) || !strings.Contains(stderr, `"a"`) {
					t.Errorf("exit %d, stderr %q", code, stderr)
				}
				if *passes != 0 {
					t.Errorf("%d targets were read before the refusal", *passes)
				}
				if got := readFile(t, victim); got != "alpha\n" {
					t.Errorf("the file behind the link was overwritten: %q", got)
				}
				if strings.Contains(stderr, x) {
					t.Errorf("the refusal names an absolute path: %q", stderr)
				}
			})
		}
	}
}

// A dangling link and a directory in the place of a file are not regular files either.
func TestOutputNameThatIsNotARegularFileIsRefused(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	mustLink(t, filepath.Join(temp(t), "nowhere"), filepath.Join(x, manifestFile))
	if err := os.Mkdir(filepath.Join(x, attestationFile), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "--out", x, a)
	if code != ExitUsage || !strings.Contains(stderr, manifestFile) || !strings.Contains(stderr, "not a regular file") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	os.Remove(filepath.Join(x, manifestFile))
	code, _, stderr = run(t, "--out", x, a)
	if code != ExitUsage || !strings.Contains(stderr, attestationFile) || !strings.Contains(stderr, "not a regular file") {
		t.Errorf("directory in the place of a file: exit %d, stderr %q", code, stderr)
	}
}

// Positive control: real output directories and regular files that exist are overwritten as before.
func TestExistingRegularOutputIsOverwritten(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	c := tree(t, filepath.Join(temp(t), "c"))
	for _, d := range []string{filepath.Join(x, "a"), filepath.Join(x, "c")} {
		os.MkdirAll(d, 0o755)
		for _, f := range []string{manifestFile, attestationFile} {
			if err := os.WriteFile(filepath.Join(d, f), []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	code, _, stderr := run(t, "--out", x, a, c)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, d := range []string{"a", "c"} {
		for _, f := range []string{manifestFile, attestationFile} {
			if got := readFile(t, filepath.Join(x, d, f)); !strings.HasPrefix(got, "{") {
				t.Errorf("%s/%s was not overwritten: %q", d, f, got)
			}
		}
	}
	// A single target writes into --out itself.
	code, _, stderr = run(t, "--out", filepath.Join(x, "a"), a)
	if code != ExitOK {
		t.Fatalf("single target: exit %d, stderr %q", code, stderr)
	}
}

// Ruling (c): a link that appears after the checks, before the write, is not followed out of the directory.
func TestOutputLinkAppearingAfterTheChecksIsNotWrittenThrough(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	victim := filepath.Join(temp(t), "victim.txt")
	if err := os.WriteFile(victim, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHook(t, func(string) { os.Symlink(victim, filepath.Join(x, attestationFile)) })
	code, _, stderr := run(t, "--out", x, a)
	if got := readFile(t, victim); got != "alpha\n" {
		t.Errorf("the file behind the link was overwritten: %q (exit %d, stderr %q)", got, code, stderr)
	}
	// The write goes to a fresh name and is renamed over the link, so the link is replaced and nothing is followed.
	if fi, err := os.Lstat(filepath.Join(x, attestationFile)); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("the output name is not a regular file: %v, %v", fi, err)
	}
	if code != ExitOK {
		t.Errorf("exit %d, want %d", code, ExitOK)
	}
}

// Ruling (b): a directory link that appears after the checks is caught by the check on the created directory.
func TestOutputDirectoryLinkAppearingAfterTheChecksIsRefused(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	c := tree(t, filepath.Join(temp(t), "c"))
	setHook(t, func(string) { os.Symlink(filepath.Join(a, "sub"), filepath.Join(x, "c")) })
	code, _, stderr := run(t, "--out", x, a, c)
	if code != ExitUsage || !strings.Contains(stderr, "lies inside") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	for _, f := range []string{manifestFile, attestationFile} {
		mustNotExist(t, filepath.Join(a, "sub", f))
	}
}

func TestCreatedDirectoryIdentityIsCheckedAgainstEveryTarget(t *testing.T) {
	base := temp(t)
	a := tree(t, filepath.Join(base, "a"))
	c := tree(t, filepath.Join(base, "c"))
	out := filepath.Join(base, "out")
	os.Mkdir(out, 0o755)
	alias := filepath.Join(out, "alias")
	mustLink(t, filepath.Join(a, "sub"), alias)
	targets := []target{{real: a, label: "a"}, {real: c, label: "c"}}
	judge := func(dir string) (target, bool) {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, bad, err := dirInsideTarget(fi, dir, targets)
		if err != nil {
			t.Fatal(err)
		}
		return got, bad
	}
	if got, bad := judge(alias); !bad || got.label != "a" {
		t.Errorf("a link into target a: got (%q, %v)", got.label, bad)
	}
	if got, bad := judge(filepath.Join(c, "sub")); !bad || got.label != "c" {
		t.Errorf("a directory inside target c: got (%q, %v)", got.label, bad)
	}
	// Negative control: a real directory beside the targets is accepted.
	if got, bad := judge(out); bad {
		t.Errorf("a directory outside every target was refused as inside %q", got.label)
	}
}
