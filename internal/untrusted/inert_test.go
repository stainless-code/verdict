package untrusted

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
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

// FuzzFlatten holds the sanitizer's one promise for any input: no control or
// line-separator rune survives except the newline, and Inert keeps only the
// newline and tab. A stranger's text can then never start a terminal sequence.
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
		if core.IsLineBreak(r) && !strings.ContainsRune(kept, r) {
			t.Fatalf("%q kept control %U", got, r)
		}
	}
}
