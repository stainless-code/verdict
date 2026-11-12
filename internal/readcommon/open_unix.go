//go:build unix

package readcommon

import (
	"os"
	"syscall"

	"github.com/stainless-code/verdict/internal/jq"
)

// OpenNoBlock opens path without waiting for a writer. A path the operator
// controls can become a FIFO after any earlier stat, and a blocking open would
// wait for a writer that never comes. The caller must fstat the descriptor and
// refuse anything that is not a regular file before reading.
func OpenNoBlock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, jq.Sealed("open without blocking", err)
	}
	return f, nil
}
