//go:build unix

package readcommon

import (
	"io/fs"
	"os"
	"syscall"
)

// HasHardLinks reports whether the file at path has more than one hard link.
// On unix the link count rides info, so path is unused. The comparison is
// against an untyped constant, so it holds for every width Stat_t.Nlink has
// (uint16 on darwin, uint32 or uint64 on linux by arch).
func HasHardLinks(_ string, info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}

// OpenedHasHardLinks reports whether the opened file f, whose fstat is info,
// has more than one hard link. On unix the fstat carries the count.
func OpenedHasHardLinks(_ *os.File, info fs.FileInfo) bool {
	return HasHardLinks("", info)
}
