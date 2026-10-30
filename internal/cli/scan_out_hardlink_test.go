package cli

// No output write lands in an existing file. A hard link at an output name is a regular file to Lstat, and it shares
// its inode with another file: writing into it would replace that other file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const victimBody = "alpha\n"

func writeVictim(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(victimBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Function level, same directory, so it runs on every host: the output name is a second name of the victim file.
func TestWriteOutputsDoesNotWriteIntoAHardLinkedFile(t *testing.T) {
	for _, name := range []string{manifestFile, attestationFile} {
		t.Run(name, func(t *testing.T) {
			dir := temp(t)
			victim := filepath.Join(dir, "victim.txt")
			writeVictim(t, victim)
			if err := os.Link(victim, filepath.Join(dir, name)); err != nil {
				t.Skipf("hard links are not available here: %v", err)
			}
			if err := writeOutputsIn(t, dir, map[string][]byte{manifestFile: []byte("{}\n"), attestationFile: []byte("{}\n")}); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, victim); got != victimBody {
				t.Errorf("the write went into the hard-linked file: %q", got)
			}
			if got := readFile(t, filepath.Join(dir, name)); got != "{}\n" {
				t.Errorf("%s was not written: %q", name, got)
			}
		})
	}
}

// A hard link that appears after the checks, before the write.
func TestHardLinkAppearingAfterTheChecksIsNotWrittenThrough(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	victim := filepath.Join(x, "victim.txt")
	writeVictim(t, victim)
	setHook(t, func(string) { os.Link(victim, filepath.Join(x, attestationFile)) })
	code, _, stderr := run(t, "--out", x, a)
	if got := readFile(t, victim); got != victimBody {
		t.Errorf("the file behind the hard link was overwritten: %q (exit %d, stderr %q)", got, code, stderr)
	}
	if got := readFile(t, filepath.Join(x, attestationFile)); !strings.HasPrefix(got, "{") {
		t.Errorf("the attestation was not written: %q", got)
	}
}

// End to end, the reviewer's layout: the output name is a second name of a file of the target. It needs link(2)
// across two directories, which some sandboxes refuse.
func TestHardLinkToTargetFileAtOutputNameIsRefused(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	victim := filepath.Join(a, "a.txt")
	if err := os.Link(victim, filepath.Join(x, attestationFile)); err != nil {
		t.Skipf("SKIPPED: link(2) across directories is refused on this host: %v", err)
	}
	passes := countingHook(t)
	code, _, stderr := run(t, "--out", x, a)
	if code != ExitUsage || !strings.Contains(stderr, attestationFile) || !strings.Contains(stderr, "hard link") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if *passes != 0 {
		t.Errorf("%d passes were read before the refusal", *passes)
	}
	if got := readFile(t, victim); got != victimBody {
		t.Errorf("a file of the target was overwritten: %q", got)
	}
	if strings.Contains(stderr, x) {
		t.Errorf("the refusal names an absolute path: %q", stderr)
	}
}
