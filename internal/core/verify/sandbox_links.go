package verifycore

import (
	"io/fs"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// sandboxHasLinks reports whether the file has more than one hard link,
// the check run's pre-launch bundle walk shares.
func sandboxHasLinks(info fs.FileInfo) bool {
	return readcommon.HasHardLinks(info)
}
