//go:build !unix

package readcommon

import "os"

// OpenNoBlock opens path. A non-unix filesystem carries no FIFO namespace
// (Windows named pipes live under \\.\pipe), so the plain open cannot wait for
// a writer; the caller still fstats and refuses a non-regular file.
func OpenNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}
