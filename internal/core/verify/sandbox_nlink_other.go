//go:build !unix

package verifycore

import "io/fs"

// sandboxHasLinks reports one name: neither sandbox mechanism runs on a
// non-unix host, so the check never decides anything there.
func sandboxHasLinks(fs.FileInfo) bool {
	return false
}
