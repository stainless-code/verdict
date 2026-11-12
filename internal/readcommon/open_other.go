//go:build !unix

package readcommon

import (
	"os"

	"github.com/stainless-code/verdict/internal/jq"
)

// OpenNoBlock opens path. A non-unix filesystem carries no FIFO namespace
// (Windows named pipes live under \\.\pipe), so the plain open cannot wait for
// a writer; the caller still fstats and refuses a non-regular file.
func OpenNoBlock(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, jq.Sealed("open", err)
	}
	return f, nil
}
