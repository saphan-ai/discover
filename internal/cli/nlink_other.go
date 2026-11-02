//go:build !unix

package cli

import "io/fs"

// linkCount reports how many names the file has, where the platform tells. Here it does not; the write by rename
// is what keeps a hard link from being written through.
func linkCount(fs.FileInfo) (uint64, bool) { return 0, false }
