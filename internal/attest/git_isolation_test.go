package attest

// Properties of the git calls: they never fetch, never obey the caller's GIT_* variables, and never reach a repository
// other than the target.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/discover/internal/fixture"
)

func TestParseGitVersion(t *testing.T) {
	for _, c := range []struct {
		in           string
		ver          string
		major, minor int
		ok           bool
	}{
		{"git version 2.50.1 (Apple Git-155)\n", "2.50.1", 2, 50, true},
		{"git version 2.44.0.windows.1", "2.44.0.windows.1", 2, 44, true},
		{"git version 2.45.0", "2.45.0", 2, 45, true},
		{"git version 2.53.0\n", "2.53.0", 2, 53, true},
		{"git version 3.0.0-rc1", "3.0.0-rc1", 3, 0, true},
		{"git version 2", "", 0, 0, false},
		{"git version x.y", "", 0, 0, false},
		{"not git at all", "", 0, 0, false},
		{"", "", 0, 0, false},
	} {
		ver, major, minor, ok := parseGitVersion(c.in)
		if ok != c.ok || ver != c.ver || major != c.major || minor != c.minor {
			t.Errorf("parseGitVersion(%q) = %q %d %d %v, want %q %d %d %v", c.in, ver, major, minor, ok,
				c.ver, c.major, c.minor, c.ok)
		}
	}
}

func TestGitOlderThanTheLazyFetchSwitchIsRefusedByName(t *testing.T) {
	base := realTemp(t)
	root := filepath.Join(base, "repo")
	os.Mkdir(root, 0o755)
	fixture.GitRepo(t, root)
	fake := filepath.Join(base, "oldgit")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'git version 2.44.0.windows.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Attest(root, Options{GitBin: fake})
	want := "git 2.45 or newer is required to read a repository without letting it fetch; found 2.44.0.windows.1"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err %v, want it to say %q", err, want)
	}
	if strings.Contains(err.Error(), base) {
		t.Errorf("the refusal names an absolute path: %v", err)
	}
	// Negative control: the same repository is read by the git this host has.
	if _, err := Attest(root, Options{}); err != nil {
		t.Errorf("the host's git was refused: %v", err)
	}
}

func hostGitSupportsNoLazyFetch(t *testing.T) {
	t.Helper()
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	_, major, minor, ok := parseGitVersion(string(out))
	if !ok || major < 2 || (major == 2 && minor < 45) {
		t.Skipf("git %q cannot be told not to fetch", strings.TrimSpace(string(out)))
	}
}

// A partial clone whose HEAD tree is missing: git would fetch it from the remote the target's own configuration
// names, and that remote can be a command.
func TestScanNeverRunsTheProgramATargetsRemoteNames(t *testing.T) {
	hostGitSupportsNoLazyFetch(t)
	base := realTemp(t)
	root := filepath.Join(base, "repo")
	os.Mkdir(root, 0o755)
	fixture.GitRepo(t, root)
	marker := filepath.Join(base, "remote-ran")

	tree := strings.TrimSpace(fixture.Git(t, root, "rev-parse", "HEAD^{tree}"))
	if err := os.Remove(filepath.Join(root, ".git", "objects", tree[:2], tree[2:])); err != nil {
		t.Fatal(err)
	}
	fixture.Git(t, root, "config", "core.repositoryformatversion", "1")
	fixture.Git(t, root, "config", "extensions.partialClone", "origin")
	fixture.Git(t, root, "config", "remote.origin.promisor", "true")
	fixture.Git(t, root, "config", "remote.origin.url", "ext::sh -c touch% "+marker)
	fixture.Git(t, root, "config", "protocol.ext.allow", "always")

	// Negative control: a plain `git ls-tree`, as the scan ran it before, does reach the remote.
	exec.Command("git", "-C", root, "ls-tree", "-r", "HEAD").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not make git fetch (%v): the test proves nothing", err)
	}
	os.Remove(marker)

	_, err := Attest(root, Options{})
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("the scan ran the program the target's remote names (Attest returned %v)", err)
	}
	if err == nil {
		t.Error("a repository whose HEAD tree is missing was attested")
	}
}

func twoRepos(t *testing.T) (target, other string) {
	t.Helper()
	base := realTemp(t)
	target = filepath.Join(base, "target")
	other = filepath.Join(base, "other")
	for _, r := range []string{target, other} {
		os.Mkdir(r, 0o755)
		fixture.GitRepo(t, r)
	}
	os.WriteFile(filepath.Join(other, "only-here.txt"), []byte("other\n"), 0o644)
	fixture.Git(t, other, "add", "-A")
	fixture.Git(t, other, "commit", "-q", "-m", "second")
	return target, other
}

func TestInheritedGitVariablesDoNotRedirectTheScan(t *testing.T) {
	target, other := twoRepos(t)
	want, err := Attest(target, Options{})
	if err != nil {
		t.Fatal(err)
	}
	otherHead := strings.TrimSpace(fixture.Git(t, other, "rev-parse", "HEAD"))
	if otherHead == want.Git.Head {
		t.Fatal("the two repositories share a HEAD: the test proves nothing")
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(other, ".git", "objects"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", other)
	got, err := Attest(target, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Git.Head != want.Git.Head || got.Tree.Digest != want.Tree.Digest {
		t.Errorf("the caller's GIT_* variables redirected the scan: head %s digest %s, want head %s digest %s",
			got.Git.Head, got.Tree.Digest, want.Git.Head, want.Tree.Digest)
	}
}

// A `.git` directory that holds no repository must not let git walk up into the repository the target lies in.
func TestEmptyDotGitInsideAnotherRepositoryIsRefusedNotAttestedAsTheParent(t *testing.T) {
	base := realTemp(t)
	outer := filepath.Join(base, "outer")
	os.Mkdir(outer, 0o755)
	fixture.GitRepo(t, outer)
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := Attest(inner, Options{})
	if err == nil {
		t.Fatalf("attested %+v: the parent repository was read instead", a.Git)
	}
	if !strings.Contains(err.Error(), "inner") || strings.Contains(err.Error(), base) {
		t.Errorf("the refusal must name the label and no absolute path: %v", err)
	}
	// Negative control: the outer repository itself is read.
	if _, err := Attest(outer, Options{}); err != nil {
		t.Errorf("the outer repository was refused: %v", err)
	}
}
