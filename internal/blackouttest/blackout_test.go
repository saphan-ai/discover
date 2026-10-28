// Package blackouttest runs scripts/blackout-check.sh against throwaway git repositories.
// Every fixture that must hold a trailer, a home path or a private name is built here at
// run time from parts, so no committed file of this repository trips the check.
package blackouttest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "scripts", "blackout-check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("script missing: %v", err)
	}
	return p
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "blackout", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo makes a temporary git repository holding the given tracked files.
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	writeAll(t, dir, files)
	return dir
}

func writeAll(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
}

func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

// run executes the script in dir and returns the exit code and the combined output.
func run(t *testing.T, dir string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{scriptPath(t)}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatal(err)
	return -1, ""
}

func TestAttributionTrailer(t *testing.T) {
	clean := fixture(t, "clean.txt")
	trailer := "Co-Authored" + "-By: some" + "one <x@y>"
	dirty := "line one\nline two\n" + trailer + "\n"

	dir := newRepo(t, map[string]string{"note.txt": dirty})
	code, out := run(t, dir, nil)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "note.txt:3: attribution-trailer") {
		t.Errorf("refusal line missing:\n%s", out)
	}
	if strings.Contains(out, "some"+"one") {
		t.Errorf("output printed the matched text:\n%s", out)
	}

	// Negative control: the same file with that one line removed passes.
	writeAll(t, dir, map[string]string{"note.txt": "line one\nline two\n"})
	if code, out := run(t, dir, nil); code != 0 || !strings.Contains(out, "blackout: clean (1 files)") {
		t.Fatalf("clean file: exit = %d\n%s", code, out)
	}

	// A mid-line mention, and an indented trailer in another case, are told apart.
	writeAll(t, dir, map[string]string{"note.txt": clean})
	if code, out := run(t, dir, nil); code != 0 {
		t.Fatalf("mid-line mention refused: exit = %d\n%s", code, out)
	}
	writeAll(t, dir, map[string]string{"note.txt": "x\n   assisted" + "-BY: a\n"})
	if code, out := run(t, dir, nil); code != 2 || !strings.Contains(out, "note.txt:2: attribution-trailer") {
		t.Fatalf("indented lower-case trailer: exit = %d\n%s", code, out)
	}
}

func TestGeneratedWithLine(t *testing.T) {
	line := "Generated " + "with [Tool](https://example.invalid)"
	dir := newRepo(t, map[string]string{"a.txt": "ok\n" + line + "\n"})
	code, out := run(t, dir, nil)
	if code != 2 || !strings.Contains(out, "a.txt:2: attribution-generated") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	writeAll(t, dir, map[string]string{"a.txt": "text about how reports are generated with care\n"})
	if code, out := run(t, dir, nil); code != 0 {
		t.Fatalf("plain prose refused: exit = %d\n%s", code, out)
	}
}

func TestHomePath(t *testing.T) {
	dir := newRepo(t, map[string]string{"n.txt": "a\nb\nsee /Us" + "ers/ali" + "ce/notes\n"})
	code, out := run(t, dir, nil)
	if code != 2 || !strings.Contains(out, "n.txt:3: home-path") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "ali"+"ce") {
		t.Errorf("output printed the matched text:\n%s", out)
	}
	writeAll(t, dir, map[string]string{"n.txt": "a\nb\nsee /ho" + "me/bo" + "b/x\n"})
	if code, out := run(t, dir, nil); code != 2 || !strings.Contains(out, "n.txt:3: home-path") {
		t.Fatalf("linux home path: exit = %d\n%s", code, out)
	}
	writeAll(t, dir, map[string]string{"n.txt": "a\nb\nsee /usr/local/notes\n"})
	if code, out := run(t, dir, nil); code != 0 {
		t.Fatalf("non-home path refused: exit = %d\n%s", code, out)
	}
}

func TestPrivateList(t *testing.T) {
	secret := "secret-host" + "-42"
	dir := newRepo(t, map[string]string{"s.txt": "one\nreach " + secret + " now\n"})

	// Unplug: a list that is set but missing refuses; it never passes quietly.
	missing := filepath.Join(t.TempDir(), "absent")
	code, out := run(t, dir, []string{"DISCOVER_BLACKOUT_LIST=" + missing})
	if code != 2 || !strings.Contains(out, "blackout list not found") {
		t.Fatalf("missing list: exit = %d\n%s", code, out)
	}

	list := filepath.Join(t.TempDir(), "list")
	body := "# private names\n\n  secret-host" + "-[0-9]+  # inline comment\n"
	if err := os.WriteFile(list, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, dir, []string{"DISCOVER_BLACKOUT_LIST=" + list})
	if code != 2 || !strings.Contains(out, "s.txt:2: private-pattern") {
		t.Fatalf("private pattern: exit = %d\n%s", code, out)
	}
	if strings.Contains(out, secret) || strings.Contains(out, "secret-host") {
		t.Errorf("output printed the matched text:\n%s", out)
	}

	// Without the list the same tree is clean: the list is the only thing that refuses it.
	if code, out := run(t, dir, nil); code != 0 {
		t.Fatalf("no list: exit = %d\n%s", code, out)
	}

	// An invalid pattern refuses rather than matching nothing.
	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("(unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, dir, []string{"DISCOVER_BLACKOUT_LIST=" + bad}); code != 2 {
		t.Fatalf("invalid list: exit = %d\n%s", code, out)
	}
}

func TestCommitRange(t *testing.T) {
	dir := newRepo(t, map[string]string{"a.txt": "one\n"})
	base := commit(t, dir, "first")
	writeAll(t, dir, map[string]string{"a.txt": "two\n"})
	good := commit(t, dir, "second")
	writeAll(t, dir, map[string]string{"a.txt": "three\n"})
	bad := commit(t, dir, "third\n\nCo-Authored"+"-By: some"+"one <x@y>")

	code, out := run(t, dir, nil, "--commits", base+".."+good)
	if code != 0 {
		t.Fatalf("clean range: exit = %d\n%s", code, out)
	}
	code, out = run(t, dir, nil, "--commits", base+"..HEAD")
	if code != 2 || !strings.Contains(out, "commit "+bad+": attribution-trailer") {
		t.Fatalf("dirty range: exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "commit "+good) || strings.Contains(out, "some"+"one") {
		t.Errorf("wrong commit named or text printed:\n%s", out)
	}
	if code, out := run(t, dir, nil, "--commits", "no-such-ref..HEAD"); code != 2 {
		t.Fatalf("bad range: exit = %d\n%s", code, out)
	}
}

func TestUsage(t *testing.T) {
	dir := newRepo(t, map[string]string{"a.txt": "one\n"})
	if code, out := run(t, dir, nil, "--bogus"); code != 2 {
		t.Fatalf("unknown flag: exit = %d\n%s", code, out)
	}
	if code, out := run(t, t.TempDir(), nil); code != 2 {
		t.Fatalf("outside a repository: exit = %d\n%s", code, out)
	}
}
