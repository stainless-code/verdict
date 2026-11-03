package jq

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrTooLarge reports input that exceeded the caller's size cap. The cap is
// checked while reading, so an oversized file is never fully allocated; the
// caller maps the error to its own typed refusal (`corrupt-store`,
// `missing-*`, ...).
var ErrTooLarge = errors.New("input exceeds its size cap")

// tooLargeError carries the cap that refused the input, and matches
// [ErrTooLarge] through [errors.Is].
type tooLargeError struct {
	limit int64
}

// Error implements error.
func (e *tooLargeError) Error() string {
	return fmt.Sprintf("input exceeds its %d-byte cap", e.limit)
}

// Unwrap exposes [ErrTooLarge] to [errors.Is].
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
	// MaxPRBytes caps a PR.json provider record.
	MaxPRBytes int64 = 8 << 20
	// MaxKBBytes caps a kb.jsonl learning store.
	MaxKBBytes int64 = 8 << 20
	// MaxCostBytes caps a cost.json (or budget.json) accounting store.
	MaxCostBytes int64 = 8 << 20
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
// [ErrTooLarge]), so callers keep their existing missing-store posture.
func CappedFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		//nolint:wrapcheck // %w keeps os.IsNotExist/fs.ErrNotExist visible to callers.
		return nil, fmt.Errorf("open capped input: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only handle; the read error rules
	return Capped(f, maxBytes)
}
