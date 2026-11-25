package untrusted

import (
	"bytes"
	"io"
	"strings"
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

// bidiControls is every directional control the fold names, by code point.
var bidiControls = []rune{
	'\u061c', '\u200e', '\u200f',
	'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
	'\u2066', '\u2067', '\u2068', '\u2069',
}

// TestBidiControlsFoldEverywhere proves Flatten, Inert, Line, and the writer
// each turn every bidi control into one space (Trojan Source: U+202E reorders
// what follows it), while a zero-width joiner and the other format
// characters an emoji or an accent needs pass through.
func TestBidiControlsFoldEverywhere(t *testing.T) {
	t.Parallel()
	for _, r := range bidiControls {
		assertBidiFolded(t, r)
	}
	const keep = "👩\u200d💻 e\u0301 \u200b\u2060 日本 \ufe0f"
	for name, fn := range map[string]func(string) string{"Flatten": Flatten, "Inert": Inert} {
		if got := fn(keep); got != keep {
			t.Errorf("%s(%q) = %q; want the emoji and format characters intact", name, keep, got)
		}
	}
}

func assertBidiFolded(t *testing.T, r rune) {
	t.Helper()
	in := "a" + string(r) + "b"
	for name, fn := range map[string]func(string) string{"Flatten": Flatten, "Inert": Inert, "Line": Line} {
		if got := fn(in); got != "a b" {
			t.Errorf("%s(%U) = %q, want %q", name, r, got, "a b")
		}
	}
	var buf bytes.Buffer
	if _, err := NewInertWriter(&buf).Write([]byte(in)); err != nil || buf.String() != "a b" {
		t.Errorf("InertWriter(%U) wrote %q, %v; want %q", r, buf.String(), err, "a b")
	}
}

// TestInertWriterFoldsARuneSplitAcrossWrites proves a control cut in two by
// the caller's write boundaries is still folded: U+202E as `E2 80` + `AE`,
// the C1 CSI as `C2` + `9B`, and a rune split three ways. A printable rune cut
// the same way arrives whole.
func TestInertWriterFoldsARuneSplitAcrossWrites(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		want  string
		parts []string
	}{
		{"bidi override 2+1", "a b", []string{"a\xe2\x80", "\xaeb"}},
		{"bidi override 1+1+1", "a b", []string{"a\xe2", "\x80", "\xaeb"}},
		{"C1 CSI 1+1", "a 2J", []string{"a\xc2", "\x9b2J"}},
		{"arabic mark 1+1", "a b", []string{"a\xd8", "\x9cb"}},
		{"separator", "a b", []string{"a\xe2\x80", "\xa8b"}},
		{"split emoji joiner", "👩‍", []string{"\xf0\x9f\x91", "\xa9\xe2\x80", "\x8d"}},
		{"split accent", "é", []string{"e\xcc", "\x81"}},
	}
	for _, c := range cases {
		if got := writeParts(t, c.name, c.parts); got != c.want {
			t.Errorf("%s: wrote %q, want %q", c.name, got, c.want)
		}
	}
}

// writeParts writes each part through one InertWriter and returns what
// reached the destination, failing the test on a short or failed write.
func writeParts(t *testing.T, name string, parts []string) string {
	t.Helper()
	var buf bytes.Buffer
	w := NewInertWriter(&buf)
	for _, part := range parts {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("%s: Write(%q) = %d, %v; want %d, nil", name, part, n, err, len(part))
		}
	}
	return buf.String()
}

// TestInertWriterFlushWritesAnIncompleteTail proves bytes of a rune that never
// finished are not lost: Flush writes them, and a later write does not repeat
// them.
func TestInertWriterFlushWritesAnIncompleteTail(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewInertWriter(&buf)
	if _, err := w.Write([]byte("ok\xe2\x80")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "ok" {
		t.Fatalf("before Flush wrote %q, want %q", buf.String(), "ok")
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("!")); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "ok\xe2\x80!"; got != want {
		t.Fatalf("wrote %q, want %q", got, want)
	}
}

// TestInertWriterReleasesATailWhenOutputGoesMachine proves a held rune head
// is written ahead of the next bytes when the gate opens, so a mode flip
// cannot drop or reorder it.
func TestInertWriterReleasesATailWhenOutputGoesMachine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	machine := false
	w := NewInertWriterUnless(&buf, func() bool { return machine })
	if _, err := w.Write([]byte("\xe2\x80")); err != nil {
		t.Fatal(err)
	}
	machine = true
	if _, err := w.Write([]byte("\xaex")); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\u202ex"; got != want {
		t.Fatalf("wrote %q, want %q", got, want)
	}
}

// FuzzFlatten holds the sanitizer's one promise for any input: no control,
// line-separator, or bidi rune survives except the newline, and Inert keeps
// only the newline and tab. A stranger's text can then never start a terminal
// sequence.
func FuzzFlatten(f *testing.F) {
	for _, seed := range []string{"plain", "a\x1b]52;c;eA==\x07b", "\u009b2J \r\n\t", "\xc2", "\xe2\x80"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		checkNoControls(t, Flatten(s), "\n")
		checkNoControls(t, Inert(s), "\n\t")
	})
}

func checkNoControls(t *testing.T, got, kept string) {
	t.Helper()
	for _, r := range got {
		if folds(r) && !strings.ContainsRune(kept, r) {
			t.Fatalf("%q kept control %U", got, r)
		}
	}
}
