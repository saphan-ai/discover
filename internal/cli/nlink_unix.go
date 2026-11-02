//go:build unix

package cli

import (
	"io/fs"
	"syscall"
)

// linkCount reports how many names the file has, where the platform tells.
func linkCount(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Nlink), true
}
