package untrusted

import (
	"io"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
)

// folds is the one rule for what a terminal must never receive from a
// stranger: every control rune (C0, DEL, C1), the line and paragraph
// separators (core.IsLineBreak), and the bidirectional formatting controls
// (Trojan Source: U+202E reorders the text after it on a bidi-capable
// terminal, so a diff line reads as another). Inert and Flatten both ask it,
// so the two cannot drift. Other format characters stay: a zero-width joiner
// holds an emoji sequence together.
func folds(r rune) bool {
	return core.IsLineBreak(r) || isBidiControl(r)
}

// Inert folds every rune [folds] names to a space, except the newline and
// tab a message lays itself out with: an ESC, BEL, CR, C1, or bidi byte a
// stranger put in a path or a spec item cannot start a terminal sequence or
// reorder a line. It is Flatten's rule for a stream a person reads, where a
// tab still separates columns.
func Inert(s string) string {
	out, changed := fold([]byte(s))
	if !changed {
		return s
	}
	return string(out)
}

// InertWriter applies Inert to every write before it reaches w, so a
// diagnostic stream stays plain text whoever writes to it: a warning, a gate
// check line quoting a spec item, a split candidate naming a diff path, or a
// line a future caller adds.
type InertWriter struct {
	w io.Writer
	// passThrough, when set and true, sends a write unfolded: the stream
	// carries a machine document (a JSON envelope) instead of text a person
	// reads, and JSON already escapes every control rune it holds.
	passThrough func() bool
}

// NewInertWriter wraps w. Wrapping an InertWriter returns it unchanged.
func NewInertWriter(w io.Writer) *InertWriter {
	if iw, ok := w.(*InertWriter); ok {
		return iw
	}
	return &InertWriter{w: w}
}

// NewInertWriterUnless wraps w like [NewInertWriter] but consults machine on
// every write and passes the bytes through untouched while it reports true.
// The runner's stdout uses it: the same stream carries human text, which
// must be inert, and one JSON envelope under --json, which must stay
// byte-identical. Asking at write time keeps the answer right however late
// the output mode settles.
func NewInertWriterUnless(w io.Writer, machine func() bool) *InertWriter {
	return &InertWriter{w: w, passThrough: machine}
}

// Write folds p and writes it, reporting len(p) on success: a fold only
// shortens a multi-byte control to one space, and the caller wrote p.
func (iw *InertWriter) Write(p []byte) (int, error) {
	if iw.passThrough != nil && iw.passThrough() {
		return iw.w.Write(p) //nolint:wrapcheck // a pass-through writer reports the destination's error as is
	}
	out, changed := fold(p)
	if !changed {
		return iw.w.Write(p) //nolint:wrapcheck // a pass-through writer reports the destination's error as is
	}
	if _, err := iw.w.Write(out); err != nil {
		return 0, err //nolint:wrapcheck // a pass-through writer reports the destination's error as is
	}
	return len(p), nil
}

// Raw is the writer under an InertWriter, for bytes verdict composed itself
// (its own color codes) or a trusted child streams (the operator's package
// manager during upgrade). Any other writer is returned as is.
func Raw(w io.Writer) io.Writer {
	if iw, ok := w.(*InertWriter); ok {
		return iw.w
	}
	return w
}

// fold works on bytes, never decoding, so a multi-byte rune split across two
// writes passes through intact; it copies only when something changes.
func fold(p []byte) ([]byte, bool) {
	i := firstControl(p)
	if i < 0 {
		return p, false
	}
	out := append(make([]byte, 0, len(p)), p[:i]...)
	for i < len(p) {
		if width := controlWidth(p, i); width > 0 {
			out = append(out, ' ')
			i += width
			continue
		}
		out = append(out, p[i])
		i++
	}
	return out, true
}

// firstControl is the index of the first control rune in p, or -1.
func firstControl(p []byte) int {
	for i := range p {
		if controlWidth(p, i) > 0 {
			return i
		}
	}
	return -1
}

// controlWidth is the byte length of the control rune starting at p[i], or 0
// when p[i] starts printable text (or is the newline or tab a message keeps).
func controlWidth(p []byte, i int) int {
	if b := p[i]; b < utf8.RuneSelf {
		return asciiControlWidth(b)
	}
	return multiByteControlWidth(p[i:])
}

func asciiControlWidth(b byte) int {
	if b != '\n' && b != '\t' && folds(rune(b)) {
		return 1
	}
	return 0
}

// multiByteControlWidth recognizes a C1 control, a separator, or a bidi
// control at the head of p. A rune cut short at the end of one write is not
// decoded, so it passes intact and a later write finishes it.
func multiByteControlWidth(p []byte) int {
	r, size := utf8.DecodeRune(p)
	if r == utf8.RuneError || !folds(r) {
		return 0
	}
	return size
}
