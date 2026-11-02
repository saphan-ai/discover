//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An existing output file with more than one name is refused by name before anything is read.
func TestOutputFileWithMoreThanOneLinkIsRefused(t *testing.T) {
	for _, f := range []string{manifestFile, attestationFile} {
		t.Run(f, func(t *testing.T) {
			x := temp(t)
			a := tree(t, filepath.Join(temp(t), "a"))
			victim := filepath.Join(x, "victim.txt")
			writeVictim(t, victim)
			if err := os.Link(victim, filepath.Join(x, f)); err != nil {
				t.Skipf("hard links are not available here: %v", err)
			}
			passes := countingHook(t)
			code, stdout, stderr := run(t, "--out", x, a)
			if code != ExitUsage || !strings.Contains(stderr, f) || !strings.Contains(stderr, `"a"`) || !strings.Contains(stderr, "hard link") {
				t.Errorf("exit %d, stderr %q", code, stderr)
			}
			if stdout != "" || *passes != 0 {
				t.Errorf("something was read: %d passes, stdout %q", *passes, stdout)
			}
			if got := readFile(t, victim); got != victimBody {
				t.Errorf("the hard-linked file was overwritten: %q", got)
			}
			if strings.Contains(stderr, x) {
				t.Errorf("the refusal names an absolute path: %q", stderr)
			}
		})
	}
}

// A FIFO that appears at an output name after the checks must not block the scan: the write never opens that name.
func TestFifoAppearingAfterTheChecksDoesNotHangTheWrite(t *testing.T) {
	x := temp(t)
	a := tree(t, filepath.Join(temp(t), "a"))
	fifo := filepath.Join(x, attestationFile)
	setHook(t, func(string) {
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Error(err)
		}
	})
	done := make(chan int, 1)
	go func() {
		code, _, _ := run(t, "--out", x, a)
		done <- code
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		// Free the blocked writer, so the test process can finish.
		if fd, err := syscall.Open(fifo, syscall.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			defer syscall.Close(fd)
		}
		t.Fatal("the write hung on a FIFO that appeared at an output name")
	}
	fi, err := os.Lstat(fifo)
	if err != nil || !fi.Mode().IsRegular() {
		t.Errorf("the output name is not a regular file: %v, %v", fi, err)
	}
}
