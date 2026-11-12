//go:build unix

package run

import (
	"os"
	"syscall"
)

// openEvidence opens a crash-evidence path without blocking. The child owns
// the path, so it can be a FIFO whose writer never comes: a blocking open
// would wait for one, while a non-blocking open returns at once and the mode
// check rejects the FIFO.
func openEvidence(path string) (*os.File, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	return f, err == nil
}
