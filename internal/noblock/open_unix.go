//go:build unix

package noblock

import (
	"os"
	"syscall"
)

// Open opens path read-only with O_NONBLOCK, so opening a FIFO returns at once
// instead of waiting for a writer. The caller fstats the descriptor before
// reading; [OpenRegular] does so.
func Open(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:wrapcheck // *fs.PathError keeps fs.ErrNotExist visible
}
