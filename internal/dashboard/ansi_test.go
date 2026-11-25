package dashboard

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// screen is a test-only terminal: it replays exactly the escape vocabulary
// the dashboard may emit and fails on anything else, so a new sequence cannot
// reach a user's terminal without a test knowing about it.
type screen struct {
	cur          string
	runes        [][]rune
	sgr          [][]string
	w, h, cx, cy int
	cups         int
	alt, sync    bool
	cursorHidden bool
}

func newScreen(w, h int) *screen {
	s := &screen{w: w, h: h}
	s.clear()
	return s
}

func (s *screen) clear() {
	s.runes = make([][]rune, s.h)
	s.sgr = make([][]string, s.h)
	for y := range s.h {
		s.runes[y] = []rune(strings.Repeat(" ", s.w))
		s.sgr[y] = make([]string, s.w)
	}
}

// replay feeds b through the interpreter.
func (s *screen) replay(t *testing.T, b []byte) {
	t.Helper()
	for len(b) > 0 {
		if b[0] == keyEsc {
			b = b[s.escape(t, b):]
			continue
		}
		r, size := utf8.DecodeRune(b)
		b = b[size:]
		s.put(t, r)
	}
}

// put draws one printable rune at the cursor and advances it.
func (s *screen) put(t *testing.T, r rune) {
	t.Helper()
	if r < ' ' || s.cx >= s.w || s.cy >= s.h {
		t.Fatalf("rune %q at %d,%d: a raw control byte or a write outside the %dx%d screen", r, s.cx, s.cy, s.w, s.h)
	}
	s.runes[s.cy][s.cx], s.sgr[s.cy][s.cx] = r, s.cur
	if runeWidth(r) == 2 {
		s.runes[s.cy][s.cx+1], s.sgr[s.cy][s.cx+1] = 0, s.cur
	}
	s.cx += runeWidth(r)
}

// escape applies the one CSI sequence at the head of b and returns its length.
func (s *screen) escape(t *testing.T, b []byte) int {
	t.Helper()
	end := 2
	for end < len(b) && strings.IndexByte("?;0123456789", b[end]) >= 0 {
		end++
	}
	if len(b) < 3 || b[1] != '[' || end == len(b) {
		t.Fatalf("malformed escape %q", b[:min(len(b), 8)])
	}
	seq := string(b[:end+1])
	switch b[end] {
	case 'm':
		s.cur = strings.TrimPrefix(seq, sgrReset)
	case 'H':
		s.move(t, seq, string(b[2:end]))
	default:
		s.mode(t, seq)
	}
	return end + 1
}

func (s *screen) move(t *testing.T, seq, params string) {
	t.Helper()
	row, col, ok := strings.Cut(params, ";")
	y, yerr := strconv.Atoi(row)
	x, xerr := strconv.Atoi(col)
	if !ok || yerr != nil || xerr != nil {
		t.Fatalf("bad cursor position %q", seq)
	}
	s.cy, s.cx = y-1, x-1
	s.cups++
}

func (s *screen) mode(t *testing.T, seq string) {
	t.Helper()
	switch seq {
	case escClear:
		s.clear()
	case escEnterAlt, escLeaveAlt:
		s.alt = seq == escEnterAlt
	case escSyncBegin, escSyncEnd:
		s.sync = seq == escSyncBegin
	case escHideCursor, escShowCursor:
		s.cursorHidden = seq == escHideCursor
	default:
		t.Fatalf("escape %q is outside the dashboard's vocabulary", seq)
	}
}

// matches fails unless the screen shows exactly what g composed, glyph and
// style, cell by cell.
func (s *screen) matches(t *testing.T, g *grid, pal *palette) {
	t.Helper()
	for i, c := range g.cells {
		x, y := i%g.w, i/g.w
		want := strings.TrimPrefix(pal[c.role], sgrReset)
		if s.runes[y][x] != c.r || (c.r != 0 && s.sgr[y][x] != want) {
			t.Fatalf("cell %d,%d = %q %q, want %q %q", x, y, s.runes[y][x], s.sgr[y][x], c.r, want)
		}
	}
}
