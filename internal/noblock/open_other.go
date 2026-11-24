//go:build !unix

package noblock

import "os"

// Open opens path read-only. A non-unix filesystem carries no FIFO namespace
// (Windows named pipes live under \\.\pipe), so the plain open cannot wait for
// a writer; the caller still fstats the descriptor.
func Open(path string) (*os.File, error) {
	return os.Open(path) //nolint:wrapcheck // *fs.PathError keeps fs.ErrNotExist visible
}
