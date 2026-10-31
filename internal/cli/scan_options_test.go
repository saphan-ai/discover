package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitTimeoutFlagReachesEveryGitCall(t *testing.T) {
	b, r := gitFixture(t)
	fakeDir := filepath.Join(b, "bin")
	os.Mkdir(fakeDir, 0o755)
	if err := os.WriteFile(filepath.Join(fakeDir, "git"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code, _, stderr := run(t, "--git-timeout", "300ms", "--out", filepath.Join(b, "out"), r)
	if code != ExitUsage || !strings.Contains(stderr, "timed out after 300ms") || !strings.Contains(stderr, "git ") {
		t.Errorf("exit %d, stderr %q: the call that timed out is not named", code, stderr)
	}
	if strings.Contains(stderr, filepath.Dir(r)) {
		t.Errorf("the absolute root leaks: %q", stderr)
	}
}

func TestGitTimeoutMustBePositive(t *testing.T) {
	b, r := gitFixture(t)
	for _, v := range []string{"0s", "-1s"} {
		code, _, stderr := run(t, "--git-timeout", v, "--out", filepath.Join(b, "out"), r)
		if code != ExitUsage || !strings.Contains(stderr, "--git-timeout") {
			t.Errorf("--git-timeout %s: exit %d %q", v, code, stderr)
		}
	}
}

func TestOutThroughAHardToSeeAliasOfTheTargetIsRefused(t *testing.T) {
	b, r := plainFixture(t)
	alias := filepath.Join(b, "alias")
	if err := os.Symlink(r, alias); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "--out", filepath.Join(alias, "deeper", "out"), r)
	if code != ExitUsage || !strings.Contains(stderr, "lies inside the target") {
		t.Errorf("exit %d %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(r, "deeper")); err == nil {
		t.Error("something was written into the target")
	}
	// Negative control: a sibling directory is fine.
	if code, _, stderr := run(t, "--out", filepath.Join(b, "elsewhere"), r); code != ExitOK {
		t.Errorf("a directory outside the target was refused: exit %d %q", code, stderr)
	}
}

func TestFreeNameTakesTheNextFreeSuffix(t *testing.T) {
	used := map[string]bool{}
	var got []string
	for _, l := range []string{"plain", "plain", "plain-2", "plain", "/"} {
		got = append(got, freeName(used, l))
	}
	want := []string{"plain", "plain-2", "plain-2-2", "plain-3", "root"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v want %v", got, want)
	}
}
