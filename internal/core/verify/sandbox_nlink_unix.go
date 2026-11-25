//go:build unix

package verifycore

import (
	"io/fs"
	"syscall"
)

// sandboxHasLinks reports whether the file has more than one hard link. The
// comparison is against an untyped constant, so it holds for every width
// Stat_t.Nlink has (uint16 on darwin, uint32 or uint64 on linux by arch).
func sandboxHasLinks(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}
