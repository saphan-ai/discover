// Package imagepinstest runs scripts/image-pins.sh, the check that refuses a container base image not pinned by
// version and digest, against this repository's Dockerfile and against copies of it with one thing changed.
package imagepinstest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// run executes the check with extra environment and arguments, and returns the exit code and combined output.
func run(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("sh", append([]string{filepath.Join(root, "scripts", "image-pins.sh")}, args...)...)
	cmd.Dir = root
	var clean []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOLANG_IMAGE=") && !strings.HasPrefix(kv, "RUNTIME_IMAGE=") && !strings.HasPrefix(kv, "ROOT=") {
			clean = append(clean, kv)
		}
	}
	cmd.Env = append(clean, env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("run: %v\n%s", err, out)
	return 0, ""
}

// mutatedDockerfile writes a copy of the Dockerfile with exactly one line replaced.
func mutatedDockerfile(t *testing.T, oldLine, newLine string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), oldLine+"\n") != 1 {
		t.Fatalf("the Dockerfile no longer holds exactly one line %q", oldLine)
	}
	p := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), oldLine+"\n", newLine+"\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRepositoryBasesArePinned(t *testing.T) {
	code, out := run(t, nil)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
}

// Field mutated: the RUNTIME_IMAGE setting loses its digest.
func TestSettingWithoutDigestIsRefused(t *testing.T) {
	code, out := run(t, []string{"RUNTIME_IMAGE=alpine:3.24.3"})
	if code != 2 || !strings.Contains(out, "RUNTIME_IMAGE=alpine:3.24.3 is not pinned") {
		t.Fatalf("exit %d, want 2 naming RUNTIME_IMAGE\n%s", code, out)
	}
}

// Field mutated: the runtime stage's FROM names a tag instead of the pinned setting.
func TestUnpinnedFromLineIsRefused(t *testing.T) {
	p := mutatedDockerfile(t, "FROM ${RUNTIME_IMAGE}", "FROM alpine:3.24")
	code, out := run(t, nil, p)
	if code != 2 || !strings.Contains(out, "FROM alpine:3.24 is not pinned") {
		t.Fatalf("exit %d, want 2 naming the FROM line\n%s", code, out)
	}
}

// Field mutated: the Dockerfile's default for GOLANG_IMAGE drifts from versions.env while still pinned.
func TestDefaultDriftingFromVersionsIsRefused(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "ARG GOLANG_IMAGE=") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the Dockerfile has no ARG GOLANG_IMAGE= line")
	}
	drifted := "ARG GOLANG_IMAGE=golang:1.26.9@sha256:" + strings.Repeat("0", 64)
	p := mutatedDockerfile(t, line, drifted)
	code, out := run(t, nil, p)
	if code != 2 || !strings.Contains(out, "the default of GOLANG_IMAGE differs from versions.env") {
		t.Fatalf("exit %d, want 2 naming the drift\n%s", code, out)
	}
}
