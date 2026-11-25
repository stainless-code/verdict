//go:build unix

package spend

import (
	"os"
	"syscall"

	"github.com/stainless-code/verdict/internal/jq"
)

// openNoBlock opens name below root without waiting for a writer: a
// bundle file replaced by a FIFO after any earlier listing cannot hang
// the scan. The caller fstats the descriptor and refuses anything that
// is not regular before reading.
func openNoBlock(root *os.Root, name string) (*os.File, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, jq.Sealed("open without blocking", err)
	}
	return file, nil
}
