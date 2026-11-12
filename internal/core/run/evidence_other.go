//go:build !unix

package run

import "os"

// openEvidence opens a crash-evidence path. A non-unix filesystem carries no
// FIFO namespace (Windows named pipes live under \\.\pipe), so the plain open
// cannot wait for a writer; the mode check after it rejects whatever is not a
// regular file.
func openEvidence(path string) (*os.File, bool) {
	f, err := os.Open(path)
	return f, err == nil
}
