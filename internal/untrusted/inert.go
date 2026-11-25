package untrusted

import "io"

// UTF-8 encodings of the control runes beyond ASCII that core.IsLineBreak
// names: C1 (U+0080..U+009F, lead byte 0xC2) and the two separators U+2028
// and U+2029 (0xE2 0x80 0xA8/0xA9).
const (
	asciiDel     = 0x7f
	utf8RuneSelf = 0x80
	c1Lead       = 0xc2
	c1First      = 0x80
	c1Last       = 0x9f
	sepLead      = 0xe2
	sepMid       = 0x80
	sepLine      = 0xa8
	sepParagraph = 0xa9
	sepLen       = 3
)

// Inert folds every control and line-separator rune to a space, except the
// newline and tab a message lays itself out with: an ESC, BEL, CR, or C1
// byte a stranger put in a path or a spec item cannot start a terminal
// sequence. It is Flatten's rule (core.IsLineBreak) for a stream a person
// reads, where a tab still separates columns.
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
}

// NewInertWriter wraps w. Wrapping an InertWriter returns it unchanged.
func NewInertWriter(w io.Writer) *InertWriter {
	if iw, ok := w.(*InertWriter); ok {
		return iw
	}
	return &InertWriter{w: w}
}

// Write folds p and writes it, reporting len(p) on success: a fold only
// shortens a multi-byte control to one space, and the caller wrote p.
func (iw *InertWriter) Write(p []byte) (int, error) {
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
	if b := p[i]; b < utf8RuneSelf {
		return asciiControlWidth(b)
	}
	return multiByteControlWidth(p[i:])
}

func asciiControlWidth(b byte) int {
	if b != '\n' && b != '\t' && (b < ' ' || b == asciiDel) {
		return 1
	}
	return 0
}

// multiByteControlWidth recognizes a C1 control or a line or paragraph
// separator at the head of p.
func multiByteControlWidth(p []byte) int {
	switch {
	case len(p) >= 2 && p[0] == c1Lead && p[1] >= c1First && p[1] <= c1Last:
		return 2
	case len(p) >= sepLen && p[0] == sepLead && p[1] == sepMid && (p[2] == sepLine || p[2] == sepParagraph):
		return sepLen
	default:
		return 0
	}
}
