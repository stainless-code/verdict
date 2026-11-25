package jq_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// esc spells the lowercase JSON escape of r the way encoding/json writes
// U+2028; tests build it from code points so no tool rewrites the source into
// the raw rune.
func esc(r rune) string { return fmt.Sprintf("%cu%04x", '\\', r) }

func TestEscapeControlsRewritesExactlyTheTargetRunes(t *testing.T) {
	t.Parallel()
	var targets []rune
	for r := rune(0x80); r <= 0x9F; r++ {
		targets = append(targets, r)
	}
	targets = append(targets, 0x202A, 0x202B, 0x202C, 0x202D, 0x202E, 0x2066, 0x2067, 0x2068, 0x2069)
	for _, r := range targets {
		got := string(jq.EscapeControls([]byte(`{"k":"a` + string(r) + `b"}`)))
		if want := `{"k":"a` + esc(r) + `b"}`; got != want {
			t.Errorf("U+%04X: got %q, want %q", r, got, want)
		}
	}
	// Neighbors of each range stay raw: the rewrite is exact, not a prefix match.
	for _, r := range []rune{0x7F, 0xA0, 0xC2, 0x2029, 0x2028, 0x2029, 0x202F, 0x2065, 0x206A, 0x200E, 0x00E9, 0x20AC, 0x1F600} {
		in := `["x` + string(r) + `y"]`
		if got := string(jq.EscapeControls([]byte(in))); got != in {
			t.Errorf("U+%04X was rewritten: %q", r, got)
		}
	}
}

func TestEscapeControlsKeepsTheDecodedString(t *testing.T) {
	t.Parallel()
	in := "a" + string([]rune{0x85, 0x9B, 0x202E, 0x2066, 0x2069, 0x9F, 0x80}) + "é€😀z"
	raw, err := json.Marshal(map[string]string{"k": in})
	if err != nil {
		t.Fatal(err)
	}
	wire := jq.EscapeControls(raw)
	for _, r := range []rune{0x85, 0x9B, 0x202E, 0x2066, 0x2069, 0x9F, 0x80} {
		if bytes.ContainsRune(wire, r) {
			t.Errorf("U+%04X still raw in %q", r, wire)
		}
	}
	var out map[string]string
	if err := json.Unmarshal(wire, &out); err != nil {
		t.Fatal(err)
	}
	if out["k"] != in {
		t.Errorf("decoded %q; want %q", out["k"], in)
	}
}

// TestEscapeControlsHandlesInvalidUTF8TheWayTheEncoderDoes pins that the
// post-pass leaves what encoding/json already did to invalid bytes alone
// (U+FFFD) and never turns a stray lead byte into a rewrite.
func TestEscapeControlsHandlesInvalidUTF8TheWayTheEncoderDoes(t *testing.T) {
	t.Parallel()
	bad := "x\xc2y\xe2\x80z\xe2A&q\xe2\x81" + string(rune(0x9B)) + "\xff"
	raw, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	want := string(raw)
	want = strings.ReplaceAll(want, string(rune(0x9B)), esc(0x9B))
	if got := string(jq.EscapeControls(raw)); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A truncated lead byte at the very end of the buffer must not read past it.
	for _, tail := range []string{"\xc2", "\xe2", "\xe2\x80", "\xe2\x81"} {
		in := []byte(`"ok` + tail)
		if got := jq.EscapeControls(in); !bytes.Equal(got, in) {
			t.Errorf("truncated %q rewritten to %q", tail, got)
		}
	}
}

func TestEscapeControlsAllocatesNothingWhenThereIsNothingToRewrite(t *testing.T) {
	in := []byte(`{"title":"plain é€😀 text   and \u0007","n":1}`)
	allocs := testing.AllocsPerRun(100, func() { _ = jq.EscapeControls(in) })
	if allocs != 0 {
		t.Errorf("EscapeControls allocated %v times on clean input", allocs)
	}
	if got := jq.EscapeControls(in); &got[0] != &in[0] {
		t.Error("EscapeControls copied clean input")
	}
}

// renderedControls renders one string carrying a C1 and a bidi control
// through every jq renderer and encoder wrapper.
func renderedControls(t *testing.T) map[string]string {
	t.Helper()
	in := "t" + string([]rune{0x85, 0x202E}) + "t"
	value := map[string]any{"k": in}
	ordered := &jq.Obj{Keys: []string{"k"}, Fields: map[string]any{"k": in}}
	marshaled, err := jq.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	indented, err := jq.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"Compact":        jq.Compact(value),
		"Pretty":         jq.Pretty(value),
		"PrettyOrdered":  jq.PrettyOrdered(ordered),
		"MarshalCompact": ordered.MarshalCompact(),
		"MarshalPretty":  ordered.MarshalPretty(),
		"Marshal":        string(marshaled),
		"MarshalIndent":  string(indented),
	}
}

// TestRenderersEscapeControls proves every jq renderer, the shared tail of the
// envelope and artifact writers, applies the rule.
func TestRenderersEscapeControls(t *testing.T) {
	t.Parallel()
	for name, got := range renderedControls(t) {
		for _, r := range []rune{0x85, 0x202E} {
			if strings.ContainsRune(got, r) || !strings.Contains(got, esc(r)) {
				t.Errorf("%s did not escape U+%04X: %q", name, r, got)
			}
		}
	}
}
