//go:build !unix

package spend

import (
	"os"

	"github.com/stainless-code/verdict/internal/jq"
)

// openNoBlock opens name below root. A non-unix filesystem carries no
// FIFO namespace, so the plain open cannot wait for a writer; the caller
// still fstats and refuses anything that is not regular.
func openNoBlock(root *os.Root, name string) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, jq.Sealed("open without blocking", err)
	}
	return file, nil
}
