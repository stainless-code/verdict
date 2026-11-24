//go:build !unix

package verifycore

import "io/fs"

// sandboxLinkCount reports one name: neither sandbox mechanism runs on a
// non-unix host, so the check never decides anything there.
func sandboxLinkCount(fs.FileInfo) uint64 {
	return 1
}
