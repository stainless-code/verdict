package jq

import (
	"errors"
	"fmt"
	"io"

	"github.com/stainless-code/verdict/internal/noblock"
)

// ErrTooLarge reports input that exceeded the caller's size cap. The cap is
// checked while reading, so an oversized file is never fully allocated; the
// caller maps the error to its own typed refusal (`corrupt-store`,
// `missing-*`, ...).
var ErrTooLarge = errors.New("input exceeds its size cap")

type tooLargeError struct {
	limit int64
}

func (e *tooLargeError) Error() string {
	return fmt.Sprintf("input exceeds its %d-byte cap", e.limit)
}

func (e *tooLargeError) Unwrap() error {
	//nolint:wrapcheck // returning the sentinel is the matching contract.
	return ErrTooLarge
}

// Store size caps (bytes). Real review bundles stay far below these values;
// the caps are defense-in-depth against a hostile or corrupted store and are
// generous enough that a valid bundle never refuses. Over-cap input is
// rejected before decoding, never after.
const (
	// MaxFindingsBytes caps a findings.json store (the review bundle's
	// derived findings document).
	MaxFindingsBytes int64 = 8 << 20
	MaxPRBytes       int64 = 8 << 20
	MaxKBBytes       int64 = 8 << 20
	MaxCostBytes     int64 = 8 << 20
	// MaxAuthorBytes caps author.json at 64 KiB.
	MaxAuthorBytes int64 = 64 << 10
)

// Capped reads at most maxBytes bytes from r, returning [ErrTooLarge] when
// the input is larger. At most maxBytes+1 bytes are ever read, so the cap
// bounds the allocation even when the source keeps growing. A negative
// maxBytes reads as zero, so any non-empty input refuses.
func Capped(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, Sealed("read capped input", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, &tooLargeError{limit: maxBytes}
	}
	return raw, nil
}

// CappedFile opens path and reads it through [Capped], closing the handle
// before returning. Missing or unreadable files report the read error (never
// [ErrTooLarge]), so callers keep their existing missing-store posture. The
// open never waits for a writer and refuses a non-regular descriptor, so a
// bundle file swapped for a FIFO reads as unreadable instead of blocking the
// store lock; a symlink to a regular file is followed.
func CappedFile(path string, maxBytes int64) ([]byte, error) {
	f, err := noblock.OpenRegular(path)
	if errors.Is(err, noblock.ErrNotRegular) {
		return nil, Sealed("read capped input", err)
	}
	if err != nil {
		//nolint:wrapcheck // %w keeps os.IsNotExist/fs.ErrNotExist visible to callers.
		return nil, fmt.Errorf("open capped input: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only handle; the read error rules
	return Capped(f, maxBytes)
}
