package untrusted

import (
	"bytes"
	"io"
	"testing"
)

// TestInertFoldsControlsKeepsLayout pins the stream fold: every C0, DEL, C1,
// and line-separator rune becomes one space, while newline, tab, and
// printable UTF-8 (accents, CJK, emoji) pass through byte for byte.
func TestInertFoldsControlsKeepsLayout(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"plain text\n", "plain text\n"},
		{"key\tvalue\nnext", "key\tvalue\nnext"},
		{"a\x1b]52;c;eA==\x07b", "a ]52;c;eA== b"},
		{"csi\u009b2J nel\u0085 del\x7f cr\r", "csi 2J nel  del  cr "},
		{"sep line par", "sep line par"},
		{"café 日本 🦀 ‧", "café 日本 🦀 ‧"},
	}
	for _, c := range cases {
		if got := Inert(c.in); got != c.want {
			t.Errorf("Inert(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestInertWriterFoldsEveryWrite proves the writer folds what any caller
// writes, reports the caller's byte count, does not wrap itself twice, and
// hands the raw writer back for verdict's own escapes.
func TestInertWriterFoldsEveryWrite(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewInertWriter(&buf)
	in := []byte("violated spec check: item\x1b]52;c;cGF5bG9hZA==\x07\n")
	if n, err := w.Write(in); err != nil || n != len(in) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(in))
	}
	if got, want := buf.String(), "violated spec check: item ]52;c;cGF5bG9hZA== \n"; got != want {
		t.Fatalf("wrote %q, want %q", got, want)
	}
	if NewInertWriter(w) != w || Raw(w) != &buf || Raw(&buf) != &buf {
		t.Fatal("NewInertWriter rewrapped an InertWriter, or Raw did not unwrap it")
	}
}

// TestInertWriterKeepsASplitRune proves the fold never decodes: a rune split
// across two writes reaches the destination intact.
func TestInertWriterKeepsASplitRune(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewInertWriter(&buf)
	crab := []byte("🦀")
	for _, part := range [][]byte{crab[:2], crab[2:]} {
		if _, err := w.Write(part); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); got != "🦀" {
		t.Fatalf("split rune came out as %q", got)
	}
}

// TestLineFoldsTheNewlineToo pins the value fold: Inert's rule plus the
// newline, so an interpolated value stays on its own line; tab and printable
// text pass through.
func TestLineFoldsTheNewlineToo(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"app/main.go", "app/main.go"},
		{"a\tb", "a\tb"},
		{"x\nverdict: gate passes", "x verdict: gate passes"},
		{"x\r\ny z\u0085", "x  y z "},
		{"a\x1b]52;c;eA==\x07b", "a ]52;c;eA== b"},
	}
	for _, c := range cases {
		if got := Line(c.in); got != c.want {
			t.Errorf("Line(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestInertWriterUnlessPassesMachineOutput proves the gated writer folds
// while the predicate is false, passes bytes through while it is true, and
// that Raw still reaches the destination.
func TestInertWriterUnlessPassesMachineOutput(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	machine := false
	w := NewInertWriterUnless(&buf, func() bool { return machine })
	in := []byte("a\x1b[2Jb\n")
	if _, err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	machine = true
	if _, err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "a [2Jb\na\x1b[2Jb\n"; got != want {
		t.Fatalf("wrote %q, want %q", got, want)
	}
	if Raw(w) != io.Writer(&buf) {
		t.Fatal("Raw did not return the destination")
	}
}
