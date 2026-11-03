package jq_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// endlessReader produces filler bytes forever and counts the bytes read, so
// a cap can be proven not to read past its limit.
type endlessReader struct{ n int64 }

// Read fills p and records the requested length.
func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.n += int64(len(p))
	return len(p), nil
}

// TestCappedAtCap proves an input at exactly the cap passes through
// byte-for-byte.
func TestCappedAtCap(t *testing.T) {
	data := []byte(`["a","b"]`)
	got, err := jq.Capped(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Capped: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("Capped = %q, want %q", got, data)
	}
}

// TestCappedOverCapReadsOnlyCapPlusOne proves the reader stops at max+1
// bytes instead of materializing the source.
func TestCappedOverCapReadsOnlyCapPlusOne(t *testing.T) {
	const limit = 16
	r := &endlessReader{}
	_, err := jq.Capped(r, limit)
	if !errors.Is(err, jq.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if r.n > limit+1 {
		t.Errorf("read %d bytes, want at most %d", r.n, limit+1)
	}
}

// TestCappedFileAtAndOverCap proves the file helper accepts the exact cap and
// refuses one byte more.
func TestCappedFileAtAndOverCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := os.WriteFile(path, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := jq.CappedFile(path, 2)
	if err != nil || string(got) != "[]" {
		t.Fatalf("CappedFile at cap = %q, %v; want [] nil", got, err)
	}
	if err := os.WriteFile(path, []byte(`[] `), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := jq.CappedFile(path, 2); !errors.Is(err, jq.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// TestCappedFileMissingIsNotTooLarge proves a missing file keeps its normal
// read error, so callers retain their missing-store posture.
func TestCappedFileMissingIsNotTooLarge(t *testing.T) {
	_, err := jq.CappedFile(filepath.Join(t.TempDir(), "absent.json"), 1<<20)
	if err == nil {
		t.Fatal("err = nil, want a read error")
	}
	if errors.Is(err, jq.ErrTooLarge) {
		t.Fatalf("err = %v, want a missing-file error, not ErrTooLarge", err)
	}
}
