//go:build unix

package verifycore

import (
	"io/fs"
	"syscall"
)

// sandboxLinkCount is the file's hard link count; an unknown shape counts as
// one name.
func sandboxLinkCount(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
