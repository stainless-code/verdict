package dashboard

import "unicode/utf8"

// key is one decoded keystroke class.
type key uint8

// The keys the dashboard binds. keyRune carries the rune itself.
const (
	keyNone key = iota
	keyRune
	keyUp
	keyDown
	keyPgUp
	keyPgDn
	keyHome
	keyEnd
	keyEnter
	keyTab
	keyEscape
	keyBackspace
)

// Control bytes the decoder reads.
const (
	ctrlD     = 0x04
	ctrlU     = 0x15
	backspace = 0x08
	del       = 0x7f
	csiIntro  = '['
	ss3Intro  = 'O'
	csiTilde  = '~'
)

// keyEvent is one keystroke.
type keyEvent struct {
	k key
	r rune
}

// csiKeys maps a CSI or SS3 sequence's body (after ESC and the introducer)
// to a key; anything else is consumed and ignored.
var csiKeys = map[string]key{
	"A": keyUp, "B": keyDown, "H": keyHome, "F": keyEnd,
	"5~": keyPgUp, "6~": keyPgDn, "1~": keyHome, "4~": keyEnd, "7~": keyHome, "8~": keyEnd,
}

// decoder turns raw terminal bytes into keystrokes. A lone ESC waits one
// read tick: a terminal can split an arrow's ESC from its `[A`, so Esc is
// only Esc when the next tick brings nothing (or no sequence).
type decoder struct {
	escPending bool
}

// decode reads one tick's bytes. An empty read is the idle tick that turns
// a pending ESC into Esc.
func (d *decoder) decode(b []byte) []keyEvent {
	out, b := d.resume(b)
	for len(b) > 0 {
		ev, n := d.next(b)
		b = b[n:]
		if ev.k != keyNone {
			out = append(out, ev)
		}
	}
	return out
}

// resume settles an ESC the last tick left pending: the start of a split
// sequence when this read continues one, else the Esc key.
func (d *decoder) resume(b []byte) ([]keyEvent, []byte) {
	if !d.escPending {
		return nil, b
	}
	d.escPending = false
	if len(b) > 0 && (b[0] == csiIntro || b[0] == ss3Intro) {
		return nil, append([]byte{keyEsc}, b...)
	}
	return []keyEvent{{k: keyEscape}}, b
}

// next decodes the event at the head of b and how many bytes it used.
func (d *decoder) next(b []byte) (keyEvent, int) {
	if b[0] == keyEsc {
		return d.escape(b)
	}
	if ev, ok := controlKey(b[0]); ok {
		return ev, 1
	}
	r, n := utf8.DecodeRune(b)
	if r < ' ' {
		return keyEvent{}, n
	}
	return keyEvent{k: keyRune, r: r}, n
}

func controlKey(c byte) (keyEvent, bool) {
	switch c {
	case '\r', '\n':
		return keyEvent{k: keyEnter}, true
	case '\t':
		return keyEvent{k: keyTab}, true
	case del, backspace:
		return keyEvent{k: keyBackspace}, true
	case ctrlD:
		return keyEvent{k: keyPgDn}, true
	case ctrlU:
		return keyEvent{k: keyPgUp}, true
	default:
		return keyEvent{}, false
	}
}

// escape decodes a sequence starting at ESC. A lone ESC at the end of the
// read becomes pending; an unknown sequence is consumed whole.
func (d *decoder) escape(b []byte) (keyEvent, int) {
	if len(b) == 1 {
		d.escPending = true
		return keyEvent{}, 1
	}
	if b[1] != csiIntro && b[1] != ss3Intro {
		return keyEvent{k: keyEscape}, 1
	}
	end := sequenceEnd(b)
	if end < 0 {
		return keyEvent{}, len(b) // a truncated sequence: drop it
	}
	return keyEvent{k: csiKeys[string(b[2:end+1])]}, end + 1
}

// sequenceEnd is the index of the final byte (0x40 to 0x7E) of the CSI or
// SS3 sequence at the head of b, or -1 when the read ends first.
func sequenceEnd(b []byte) int {
	for i := 2; i < len(b); i++ {
		if b[i] >= '@' && b[i] <= csiTilde {
			return i
		}
	}
	return -1
}
