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
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, jq.Sealed("stat regular input", err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotRegular //nolint:wrapcheck // local sentinel for a non-regular descriptor
	}
	raw, err := jq.Capped(f, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read regular input: %w", err) //nolint:wrapcheck // %w keeps ErrTooLarge visible
	}
	return raw, nil
}
