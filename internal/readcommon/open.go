package readcommon

import (
	"errors"
	"fmt"

	"github.com/stainless-code/verdict/internal/jq"
)

// ErrNotRegular reports a descriptor that is not a regular file.
var ErrNotRegular = errors.New("not a regular file")

// ReadRegularCapped opens path without blocking, refuses anything that is not
// a regular file once opened, and reads at most maxBytes. The open is the
// check, so a regular file swapped for a FIFO after an earlier stat cannot
// block the request.
func ReadRegularCapped(path string, maxBytes int64) ([]byte, error) {
	f, err := OpenNoBlock(path)
	if err != nil {
		return nil, fmt.Errorf("open regular input: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat regular input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	return jq.Capped(f, maxBytes)
}
