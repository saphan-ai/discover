//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/saphan-ai/discover/internal/safepath"
)

// A regular file is swapped for a FIFO between the check and the open. An open of a FIFO with no writer blocks for
// ever unless it is non-blocking, so a scan of a hostile tree could hang. The swapper races the reader: the reader
// must finish, whatever it opened.
func TestFifoSwappedInDoesNotHangTheRead(t *testing.T) {
	b := scratch(t)
	r := filepath.Join(b, "root")
	os.Mkdir(r, 0o755)
	f := filepath.Join(r, "f")
	os.WriteFile(f, []byte("inside"), 0o644)

	stop := make(chan struct{})
	swapperDone := make(chan struct{})
	go func() {
		defer close(swapperDone)
		tmp := filepath.Join(r, ".swap")
		for {
			select {
			case <-stop:
				return
			default:
			}
			os.Remove(tmp)
			if syscall.Mkfifo(tmp, 0o644) == nil {
				os.Rename(tmp, f)
			}
			os.WriteFile(tmp, []byte("inside"), 0o644)
			os.Rename(tmp, f)
		}
	}()

	done := make(chan int)
	go func() {
		n := 0
		root, err := safepath.OpenRoot(r)
		if err != nil {
			t.Error(err)
			done <- 0
			return
		}
		defer root.Close()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if fh, _, err := root.OpenRegular("f"); err == nil {
				fh.Close()
				n++
			}
		}
		done <- n
	}()
	select {
	case n := <-done:
		t.Logf("%d regular opens while the path flipped between a file and a FIFO", n)
	case <-time.After(10 * time.Second):
		t.Errorf("the read hung: a FIFO swapped in after the check blocked the open")
	}
	close(stop)
	<-swapperDone
}
