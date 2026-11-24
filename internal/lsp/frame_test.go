package lsp

import (
	"bufio"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

// TestReadFrameAcceptsWellFormedStreams pins the framing reader's accepting
// table: every body is read exactly, header names fold case, unknown headers
// are ignored, bare-LF lines are tolerated, and a body may carry the header
// separator inside a JSON string without splitting.
func TestReadFrameAcceptsWellFormedStreams(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  []string
	}{
		{"single", "Content-Length: 2\r\n\r\n{}", []string{"{}"}},
		{"two frames in one read", "Content-Length: 2\r\n\r\n{}Content-Length: 5\r\n\r\n[1,2]", []string{"{}", "[1,2]"}},
		{"header case", "content-LENGTH: 2\r\n\r\n{}", []string{"{}"}},
		{"extra headers", "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 2\r\nX-Trace: 1\r\n\r\n{}", []string{"{}"}},
		{"bare LF", "Content-Length: 2\n\n{}", []string{"{}"}},
		{"zero length", "Content-Length: 0\r\n\r\n", []string{""}},
		{"separator inside body", "Content-Length: 16\r\n\r\n{\"a\":\"\\r\\n\\r\\n\"}", []string{`{"a":"\r\n\r\n"}`}},
		{"raw separator inside body", "Content-Length: 6\r\n\r\n{\r\n\r\n}", []string{"{\r\n\r\n}"}},
		{"multibyte body", "Content-Length: 12\r\n\r\n{\"a\":\"üü\"}", []string{`{"a":"üü"}`}},
		{"blank line between frames", "Content-Length: 2\r\n\r\n{}\r\nContent-Length: 2\r\n\r\n[]", []string{"{}", "[]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readAllFrames(t, tc.input); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("bodies = %q, want %q", got, tc.want)
			}
		})
	}
}

// readAllFrames reads bodies until a clean EOF, failing on any other error.
func readAllFrames(t *testing.T, input string) []string {
	t.Helper()
	r := bufio.NewReader(strings.NewReader(input))
	var got []string
	for {
		body, err := readFrame(r)
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		got = append(got, string(body))
	}
}

// TestReadFrameRefusesMalformedHeaders pins every refusal: each is a typed
// frame error (the session stops; a desynchronized stream is never guessed
// at), and an EOF inside a frame is unexpected, never a clean end.
func TestReadFrameRefusesMalformedHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		want  error
		name  string
		input string
	}{
		{errFrameHeader, "missing length", "Content-Type: x\r\n\r\n{}"},
		{errFrameHeader, "non-numeric", "Content-Length: 1e3\r\n\r\n{}"},
		{errFrameHeader, "negative", "Content-Length: -2\r\n\r\n{}"},
		{errFrameHeader, "empty value", "Content-Length:\r\n\r\n{}"},
		{errFrameHeader, "duplicate", "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}"},
		{errFrameHeader, "no colon", "Content-Length 2\r\n\r\n{}"},
		{errFrameTooLarge, "oversized", "Content-Length: 16777217\r\n\r\n"},
		{errFrameTooLarge, "absurd digits", "Content-Length: 99999999999999999999\r\n\r\n"},
		{errFrameHeader, "header flood", strings.Repeat("X: y\r\n", 2000)},
		{io.ErrUnexpectedEOF, "truncated body", "Content-Length: 10\r\n\r\n{}"},
		{io.ErrUnexpectedEOF, "truncated header", "Content-Length: 2\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := readFrame(bufio.NewReader(strings.NewReader(tc.input)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("readFrame error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestReadFrameAtCapIsAccepted proves the cap is inclusive: exactly 16 MiB
// reads, one byte more refuses (the oversized row above).
func TestReadFrameAtCapIsAccepted(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", maxFrameBytes)
	got, err := readFrame(bufio.NewReader(strings.NewReader("Content-Length: 16777216\r\n\r\n" + body)))
	if err != nil || len(got) != maxFrameBytes {
		t.Fatalf("readFrame at cap = %d bytes, %v", len(got), err)
	}
}

// TestEncodeFrameIsTheWireShape pins the writer's bytes literally.
func TestEncodeFrameIsTheWireShape(t *testing.T) {
	t.Parallel()
	if got := string(encodeFrame([]byte(`{"a":"ü"}`))); got != "Content-Length: 10\r\n\r\n{\"a\":\"ü\"}" {
		t.Fatalf("encodeFrame = %q", got)
	}
}

// TestReadFrameAllocatesWhatArrives pins that a body is buffered as it
// arrives: a header declaring the 16 MiB cap followed by two bytes and EOF
// allocates far less than the declaration before failing. It runs without
// t.Parallel so no other test's allocations land in the measurement.
func TestReadFrameAllocatesWhatArrives(t *testing.T) {
	input := "Content-Length: 16777216\r\n\r\n{}"
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := readFrame(bufio.NewReader(strings.NewReader(input)))
	runtime.ReadMemStats(&after)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readFrame = %v, want unexpected EOF", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("a 2-byte body of a 16 MiB declaration allocated %d bytes", grew)
	}
}
