package fetchcore

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
)

// writeErrFile plants an owner-only .err sidecar with the call's stderr,
// creating it empty on success: a sidecar is truncated either way.
func writeErrFile(path, stderr string) error {
	if err := atomic.WriteErrSidecar(path, []byte(stderr)); err != nil {
		return &fetchWriteError{path: path, err: err}
	}
	return nil
}

type fetchWriteError struct {
	err  error
	path string
}

// Error implements error.
func (e *fetchWriteError) Error() string {
	return "could not write " + e.path + ": " + e.err.Error()
}

// Unwrap exposes the underlying write error.
func (e *fetchWriteError) Unwrap() error {
	return e.err
}

// internalError seals a fetch-internal failure at a disk or decoder edge:
// the message keeps the host's text and the verb maps it to a typed
// fetch-failed refusal at the boundary.
type internalError struct{ msg string }

// Error implements error.
func (e *internalError) Error() string { return e.msg }

// internalErrorf builds an internalError with formatting.
func internalErrorf(format string, args ...any) *internalError {
	return &internalError{msg: fmt.Sprintf(format, args...)}
}

func headBytes(s string, n int) string {
	b := []byte(s)
	if len(b) > n {
		b = b[:n]
	}
	return strings.TrimRight(string(b), newline)
}

// readOptional reads a file, returning "" when it is missing or unreadable.
func readOptional(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

const compareErrHeadBytes = 300
