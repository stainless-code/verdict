//go:build unix

package readcommon

import (
	"os"
	"syscall"
)

// OpenNoBlock opens path without waiting for a writer. A path the operator
// controls can become a FIFO after any earlier stat, and a blocking open would
// wait for a writer that never comes. The caller must fstat the descriptor and
// refuse anything that is not a regular file before reading.
func OpenNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
