//go:build !unix && !windows

package readcommon

import (
	"io/fs"
	"os"
)

// HasHardLinks reports one name: this platform exposes no link count verdict
// can read, so the check never decides anything there. The symlink and
// non-regular checks beside it still hold.
func HasHardLinks(string, fs.FileInfo) bool {
	return false
}

// OpenedHasHardLinks reports one name, as [HasHardLinks] does here.
func OpenedHasHardLinks(*os.File, fs.FileInfo) bool {
	return false
}
