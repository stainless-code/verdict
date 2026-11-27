//go:build darwin

package run

import (
	"fmt"
	"io/fs"
	"syscall"
)

// identity is the part of an entry's lstat no user-space write can restore:
// its inode and change time (ctime), "" when the stat carries neither.
func identity(info fs.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d.%d", st.Ino, st.Ctimespec.Sec, st.Ctimespec.Nsec)
}
