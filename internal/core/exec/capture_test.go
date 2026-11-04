package exec

import (
	"strings"
	"testing"
)

// TestBoundedBufferHeadKeepsFirstBytes proves the head-biased capture keeps
// the first max bytes, records truncation, and appends the marker.
func TestBoundedBufferHeadKeepsFirstBytes(t *testing.T) {
	b := newBoundedBuffer(4, captureHead)
	if n, err := b.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write = %d, %v; want 6, nil", n, err)
	}
	if !b.Truncated() {
		t.Fatal("Truncated = false, want true")
	}
	if len(b.buf) != 4 {
		t.Errorf("retained bytes = %d, want 4", len(b.buf))
	}
	want := "abcd\n[verdict: output truncated at 4 bytes; kept the first 4 bytes]\n"
	if got := b.String(); got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

// TestBoundedBufferTailKeepsLastBytes proves the tail-biased capture keeps
// the last max bytes across chunked writes.
func TestBoundedBufferTailKeepsLastBytes(t *testing.T) {
	b := newBoundedBuffer(5, captureTail)
	for _, chunk := range []string{"abc", "def", "ghi"} {
		if _, err := b.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if !b.Truncated() {
		t.Fatal("Truncated = false, want true")
	}
	got := b.String()
	want := "efghi\n[verdict: output truncated at 5 bytes; kept the last 5 bytes]\n"
	if got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

// TestBoundedBufferExactCapIsNotTruncated proves a stream at exactly the cap
// is returned byte-for-byte, with no marker.
func TestBoundedBufferExactCapIsNotTruncated(t *testing.T) {
	b := newBoundedBuffer(4, captureHead)
	if _, err := b.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	if b.Truncated() {
		t.Error("Truncated = true, want false at the exact cap")
	}
	if got := b.String(); got != "abcd" {
		t.Errorf("String = %q, want abcd", got)
	}
}

// TestBoundedBufferHugeStreamStaysBounded proves the capture never grows
// past the cap no matter how much a child writes.
func TestBoundedBufferHugeStreamStaysBounded(t *testing.T) {
	const limit = 1024
	b := newBoundedBuffer(limit, captureTail)
	chunk := []byte(strings.Repeat("x", 32<<10))
	for range 64 { // 2 MiB through a 1 KiB cap
		if _, err := b.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.buf) > limit {
		t.Fatalf("retained bytes = %d, want <= %d", len(b.buf), limit)
	}
	if !b.Truncated() {
		t.Fatal("Truncated = false, want true")
	}
	if len(b.String()) > limit+len(truncationNotice(captureTail, limit)) {
		t.Errorf("String grew past the cap and its marker: %d bytes", len(b.String()))
	}
}

// TestOutputCapFallback proves a zero command cap resolves to the
// commander's documented default.
func TestOutputCapFallback(t *testing.T) {
	if got := outputCap(0, FetchOutputCap); got != FetchOutputCap {
		t.Errorf("outputCap(0, FetchOutputCap) = %d, want %d", got, FetchOutputCap)
	}
	if got := outputCap(7, FetchOutputCap); got != 7 {
		t.Errorf("outputCap(7, FetchOutputCap) = %d, want 7", got)
	}
}
