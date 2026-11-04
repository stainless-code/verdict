package verdict

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestConvertErr proves the error conversion seam: nil stays nil, a core
// refusal becomes the public typed refusal, a missing tool keeps its kind,
// and anything else passes through untouched.
func TestConvertErr(t *testing.T) {
	if err := convertErr(nil); err != nil {
		t.Fatalf("convertErr(nil) = %v, want nil", err)
	}
	assertRefusalConverted(t)
	assertMissingToolConverted(t)
	if plain := errors.New("boom"); !errors.Is(convertErr(plain), plain) {
		t.Error("convertErr(plain) = a different error, want the same one")
	}
}

// assertRefusalConverted proves a core refusal maps to the public refusal
// with its hint and an unaliased warning copy.
func assertRefusalConverted(t *testing.T) {
	t.Helper()
	warnings := []string{"w1", "w2"}
	err := convertErr(&core.RefusalError{Code: "unknown-sig", Message: "show: sig not in store", Warnings: warnings})
	got, ok := errors.AsType[*RefusalError](err)
	if !ok {
		t.Fatalf("err = %v, want *RefusalError", err)
	}
	if got.Code != "unknown-sig" || got.Message != "show: sig not in store" {
		t.Errorf("refusal = %+v, want the core code and message", got)
	}
	if got.Hint != core.HintFor("unknown-sig") || got.Hint == "" {
		t.Errorf("hint = %q, want the core hint for unknown-sig", got.Hint)
	}
	warnings[0] = "mutated"
	if got.Warnings[0] != "w1" {
		t.Errorf("warnings = %v, want a copy the core cannot mutate", got.Warnings)
	}
}

// assertMissingToolConverted proves a wrapped missing-tool error keeps its
// kind through the conversion.
func assertMissingToolConverted(t *testing.T) {
	t.Helper()
	err := convertErr(fmt.Errorf("spawn: %w", &core.MissingToolError{Tool: "gh"}))
	toolErr, ok := errors.AsType[*MissingToolError](err)
	if !ok {
		t.Fatalf("err = %v, want *MissingToolError", err)
	}
	if toolErr.Tool != "gh" {
		t.Errorf("tool = %q, want gh", toolErr.Tool)
	}
	if toolErr.Error() != "missing tool: gh" {
		t.Errorf("Error() = %q, want the frozen missing-tool line", toolErr.Error())
	}
}

// TestLineWriterSplitsPartialLines proves the progress writer: it forwards
// only complete lines, buffers a line split across writes, reports the full
// byte count, and drops the trailing newline.
func TestLineWriterSplitsPartialLines(t *testing.T) {
	var got []string
	w := &lineWriter{onLine: func(line string) { got = append(got, line) }}
	for _, chunk := range []string{"one\ntw", "o\n", "three", "\nfour"} {
		n, err := w.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write(%q): %v", chunk, err)
		}
		if n != len(chunk) {
			t.Errorf("Write(%q) = %d, want %d", chunk, n, len(chunk))
		}
	}
	if want := []string{"one", "two", "three"}; !equalLines(got, want) {
		t.Errorf("lines = %v, want %v (the unterminated tail must stay buffered)", got, want)
	}
}

// TestLineWriterLeadingNewline proves a chunk whose first byte is the
// newline forwards the empty line and every line after it: the index of the
// first newline is 0, and treating that as "no newline" would swallow the
// whole chunk.
func TestLineWriterLeadingNewline(t *testing.T) {
	var got []string
	w := &lineWriter{onLine: func(line string) { got = append(got, line) }}
	if n, err := w.Write([]byte("\nfirst\nsecond\n")); err != nil || n != len("\nfirst\nsecond\n") {
		t.Fatalf("Write = %d, %v; want the full count and nil", n, err)
	}
	if want := []string{"", "first", "second"}; !equalLines(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if n, err := w.Write([]byte("\ntail")); err != nil || n != len("\ntail") {
		t.Fatalf("second Write = %d, %v", n, err)
	}
	if want := []string{"", "first", "second", ""}; !equalLines(got, want) {
		t.Errorf("lines after the second write = %q, want %q", got, want)
	}
}

// TestStreamWriterRoutes proves the progress writer seam: human lines route
// to Human, diagnostics route to Diag, and a client without an observer
// discards both without panicking.
func TestStreamWriterRoutes(t *testing.T) {
	progress := &recordingProgress{}
	c := New(Options{Progress: progress})
	if _, err := c.streamWriter(true).Write([]byte("human line\n")); err != nil {
		t.Fatalf("write human: %v", err)
	}
	if _, err := c.streamWriter(false).Write([]byte("diag line\n")); err != nil {
		t.Fatalf("write diag: %v", err)
	}
	if !equalLines(progress.human, []string{"human line"}) {
		t.Errorf("human = %v, want the human line only", progress.human)
	}
	if !equalLines(progress.diag, []string{"diag line"}) {
		t.Errorf("diag = %v, want the diagnostic line only", progress.diag)
	}
	quiet := New(Options{})
	if n, err := quiet.streamWriter(true).Write([]byte("dropped\n")); err != nil || n != len("dropped\n") {
		t.Errorf("discarding Write = %d, %v; want the full count and nil", n, err)
	}
}

// TestWireJSONKeepsStoredOrder proves the data-profile renderer: an ordered
// core profile renders as its JSON object in stored key order, never as the
// Obj's Go fields.
func TestWireJSONKeepsStoredOrder(t *testing.T) {
	raw := wireJSON(jq.NewObj("second", 2, "first", 1))
	if string(raw) != `{"second":2,"first":1}` {
		t.Errorf("wireJSON = %s, want the stored order", raw)
	}
	if bytes.Contains(raw, []byte(`"Fields"`)) {
		t.Errorf("wireJSON = %s, want the profile, not the Obj struct", raw)
	}
	if wireJSON(nil) != nil {
		t.Error("wireJSON(nil) = non-nil, want nil")
	}
}

// recordingProgress collects every observed line for routing assertions.
type recordingProgress struct {
	human []string
	diag  []string
}

// Human implements Progress.
func (p *recordingProgress) Human(line string) { p.human = append(p.human, line) }

// Warn implements Progress.
func (p *recordingProgress) Warn(string) {}

// Diag implements Progress.
func (p *recordingProgress) Diag(line string) { p.diag = append(p.diag, line) }

// equalLines reports whether two line slices match.
func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
