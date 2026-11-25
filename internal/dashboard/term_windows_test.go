//go:build windows

package dashboard

import (
	"errors"
	"syscall"
	"testing"
	"unsafe"
)

// TestInputRecordLayout pins INPUT_RECORD's size: PeekConsoleInputW writes
// 20 bytes per record, so any other size reads the wrong fields.
func TestInputRecordLayout(t *testing.T) {
	t.Parallel()
	if got := unsafe.Sizeof(inputRecord{}); got != 20 {
		t.Fatalf("inputRecord is %d bytes, want 20", got)
	}
}

// TestRestoreFailureIsNotNoVT proves a console mode that cannot be put back
// reports a restore error, not dashboard-no-vt's "does not accept VT": the
// session already ran, so VT support is not what failed.
func TestRestoreFailureIsNotNoVT(t *testing.T) {
	t.Parallel()
	bad := &winTTY{in: syscall.InvalidHandle, out: syscall.InvalidHandle}
	if err := bad.restore(); !errors.Is(err, errRestore) {
		t.Fatalf("restore = %v, want errRestore", err)
	}
}
