//go:build !unix

package readcommon

import "io/fs"

// HasHardLinks reports one name: FileInfo carries no link count off unix, so
// the check never decides anything there. The symlink and non-regular checks
// beside it still hold on every platform.
func HasHardLinks(fs.FileInfo) bool {
	return false
}
