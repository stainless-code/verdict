package dashboard

import (
	"slices"
	"testing"
)

// TestDecoder pins the byte-to-key table, including the two cases a naive
// decoder gets wrong: an ESC split from its sequence across reads, and a
// sequence whose parameter bytes are digits.
func TestDecoder(t *testing.T) {
	t.Parallel()
	ev := func(k key) keyEvent { return keyEvent{k: k} }
	r := func(c rune) keyEvent { return keyEvent{k: keyRune, r: c} }
	cases := []struct {
		name  string
		reads []string
		want  []keyEvent
	}{
		{"runes", []string{"jk2"}, []keyEvent{r('j'), r('k'), r('2')}},
		{"utf-8 rune", []string{"é"}, []keyEvent{r('é')}},
		{"arrows", []string{"\x1b[A\x1b[B\x1bOA"}, []keyEvent{ev(keyUp), ev(keyDown), ev(keyUp)}},
		{"paging", []string{"\x1b[5~\x1b[6~\x04\x15"}, []keyEvent{ev(keyPgUp), ev(keyPgDn), ev(keyPgDn), ev(keyPgUp)}},
		{"home and end", []string{"\x1b[H\x1b[F\x1b[1~\x1b[4~"}, []keyEvent{ev(keyHome), ev(keyEnd), ev(keyHome), ev(keyEnd)}},
		{"enter tab backspace", []string{"\r\n\t\x7f"}, []keyEvent{ev(keyEnter), ev(keyEnter), ev(keyTab), ev(keyBackspace)}},
		{"Home's 1 is not a digit", []string{"\x1b[1~"}, []keyEvent{ev(keyHome)}},
		{"unknown sequence is dropped", []string{"\x1b[15~x"}, []keyEvent{r('x')}},
		{"lone Esc waits a tick", []string{"\x1b", ""}, []keyEvent{ev(keyEscape)}},
		{"split arrow is an arrow", []string{"\x1b", "[A"}, []keyEvent{ev(keyUp)}},
		{"Esc then a key", []string{"\x1b", "q"}, []keyEvent{ev(keyEscape), r('q')}},
		{"Alt-key is Esc then the key", []string{"\x1bq"}, []keyEvent{ev(keyEscape), r('q')}},
		{"control bytes are dropped", []string{"\x01\x02a"}, []keyEvent{r('a')}},
	}
	for _, c := range cases {
		var d decoder
		var got []keyEvent
		for _, read := range c.reads {
			got = append(got, d.decode([]byte(read))...)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: decoded %v, want %v", c.name, got, c.want)
		}
	}
}

// FuzzDecoder holds the decoder's safety property: any bytes, split across
// any two reads, decode without a panic into known keys only.
func FuzzDecoder(f *testing.F) {
	for _, seed := range []string{"jk", "\x1b[A", "\x1b", "\x1b[1;5~", "\xff\xfe", "\x1bO"} {
		f.Add(seed, 1)
	}
	f.Fuzz(checkDecode)
}

func checkDecode(t *testing.T, s string, cut int) {
	var d decoder
	cut = max(0, min(cut, len(s)))
	var events []keyEvent
	for _, part := range []string{s[:cut], s[cut:], ""} {
		events = append(events, d.decode([]byte(part))...)
	}
	for _, ev := range events {
		if ev.k == keyNone || ev.k > keyBackspace {
			t.Fatalf("decode(%q) produced %v", s, ev)
		}
	}
}
