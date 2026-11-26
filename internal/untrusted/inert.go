package untrusted

import (
	"bytes"
	"io"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
)

// Lead bytes of the multi-byte runes [folds] names.
const (
	c1Lead          = 0xc2
	arabicLead      = 0xd8
	punctuationLead = 0xe2
)

// folds is the one rule for what a terminal must never receive from a
// stranger: every control rune (C0, DEL, C1), the line and paragraph
// separators (core.IsLineBreak), and the bidirectional formatting controls
// (Trojan Source: U+202E reorders the text after it on a bidi-capable
// terminal, so a diff line reads as another). Inert and Flatten both ask it,
// so the two cannot drift. Other format characters stay: a zero-width joiner
// holds an emoji sequence together.
func folds(r rune) bool {
	return core.IsLineBreak(r) || core.IsBidiControl(r)
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
	// tail holds the head of a multi-byte rune a write cut short, so the
	// fold sees the whole rune once the next write finishes it.
	tail []byte
	mu   sync.Mutex
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
// shortens a multi-byte control to one space, and the caller wrote p. A rune
// whose first bytes end p waits in the writer until the next write (or
// [InertWriter.Flush]) completes it, because U+202E cut in two would pass
// both halves unfolded.
func (iw *InertWriter) Write(p []byte) (int, error) {
	iw.mu.Lock()
	defer iw.mu.Unlock()
	data := p
	if len(iw.tail) > 0 {
		data = slices.Concat(iw.tail, p)
		iw.tail = nil
	}
	if iw.passThrough != nil && iw.passThrough() {
		return iw.send(data, len(p))
	}
	if cut := incompleteTail(data); cut < len(data) {
		iw.tail = bytes.Clone(data[cut:])
		data = data[:cut]
	}
	out, _ := fold(data)
	return iw.send(out, len(p))
}

// send writes out and reports n, the caller's byte count, on success.
func (iw *InertWriter) send(out []byte, n int) (int, error) {
	if len(out) == 0 {
		return n, nil
	}
	if _, err := iw.w.Write(out); err != nil {
		return 0, err //nolint:wrapcheck // a pass-through writer reports the destination's error as is
	}
	return n, nil
}

// Flush writes the bytes a cut-short rune left waiting. They are an
// incomplete UTF-8 sequence, so no fold applies; call it when the stream ends.
func (iw *InertWriter) Flush() error {
	iw.mu.Lock()
	defer iw.mu.Unlock()
	tail := iw.tail
	iw.tail = nil
	if len(tail) == 0 {
		return nil
	}
	_, err := iw.w.Write(tail)
	return err //nolint:wrapcheck // a pass-through writer reports the destination's error as is
}

// incompleteTail is the index where a trailing partial rune starts, or
// len(p) when p ends on a whole rune. Only a prefix of a rune the fold can
// name is held: the C1 lead (0xC2), the Arabic letter mark (0xD8), and the
// general-punctuation lead (0xE2) that U+2028 and the bidi controls share.
func incompleteTail(p []byte) int {
	i := lastRuneStart(p)
	if i < len(p) && heldLead(p[i]) && !utf8.FullRune(p[i:]) {
		return i
	}
	return len(p)
}

// lastRuneStart is the index of the last rune-start byte within the final
// [utf8.UTFMax]-1 bytes of p, or len(p) when there is none.
func lastRuneStart(p []byte) int {
	for back := 1; back < utf8.UTFMax && back <= len(p); back++ {
		if i := len(p) - back; utf8.RuneStart(p[i]) {
			return i
		}
	}
	return len(p)
}

// heldLead reports whether b starts a multi-byte rune the fold can name.
func heldLead(b byte) bool {
	return b == c1Lead || b == arabicLead || b == punctuationLead
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

// fold replaces every rune [folds] names with a space; it copies only when
// something changes.
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
// control at the head of p. A rune cut short is not decoded; the writer holds
// it back until the next write, so it never reaches here in pieces.
func multiByteControlWidth(p []byte) int {
	r, size := utf8.DecodeRune(p)
	if r == utf8.RuneError || !folds(r) {
		return 0
	}
	return size
}
