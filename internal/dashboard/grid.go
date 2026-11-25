package dashboard

import (
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/untrusted"
)

// maxMarks is how many zero-width runes (combining marks, a variation
// selector, a joiner) one cell carries after its base rune; more are dropped.
const maxMarks = 2

// cell is one terminal cell: the rune it shows, the zero-width runes that
// combine with it ("e" + U+0301 is "é"), and the role it is painted with. A
// wide rune owns the cell after it, which holds rune 0. The struct stays
// comparable, so the painter's diff is still `==`.
type cell struct {
	marks [maxMarks]rune
	r     rune
	role  role
}

// cellBytes fits a base rune and its marks, UTF-8 encoded.
const cellBytes = utf8.UTFMax * (1 + maxMarks)

// appendTo appends the cell's base rune and its marks, UTF-8 encoded.
func (c *cell) appendTo(b []byte) []byte {
	b = utf8.AppendRune(b, c.r)
	for _, m := range c.marks {
		if m != 0 {
			b = utf8.AppendRune(b, m)
		}
	}
	return b
}

// blank is an empty plain cell.
var blank = cell{r: ' '}

// grid is one composed frame. Views write text into it; only paint.go turns
// it into bytes, so escape sequences never exist inside cell text.
type grid struct {
	cells []cell
	w, h  int
}

func newGrid(w, h int) *grid {
	g := &grid{w: w, h: h, cells: make([]cell, w*h)}
	for i := range g.cells {
		g.cells[i] = blank
	}
	return g
}

func (g *grid) at(x, y int) cell { return g.cells[y*g.w+x] }

// text writes s at (x, y) in at most limit cells and returns the cells used.
// Every string goes through untrusted.Flatten first: a finding title or a
// path a stranger wrote cannot carry an ESC or a line break into the frame.
// A newline Flatten keeps becomes a space, because a cell row is one line.
func (g *grid) text(x, y, limit int, s string, rl role, cs charset) int {
	if y < 0 || y >= g.h || x < 0 || x >= g.w {
		return 0
	}
	s = strings.ReplaceAll(untrusted.Flatten(s), "\n", " ")
	row := g.cells[y*g.w : (y+1)*g.w]
	col := place(row, x, truncate(s, min(limit, g.w-x), cs.ellipsis), rl)
	mendWide(row, x, col)
	return col - x
}

// place writes already-fitted text from x and returns the column after it.
// Zero-width runes have no cell of their own and are dropped.
func place(row []cell, x int, s string, rl role) int {
	col, base := x, -1
	for _, r := range s {
		w := runeWidth(r)
		if w == 0 {
			attachMark(row, base, r)
			continue
		}
		base = col
		row[col] = cell{r: r, role: rl}
		if w == 2 {
			row[col+1] = cell{role: rl}
		}
		col += w
	}
	return col
}

// attachMark adds a zero-width rune to the cell at base, the last base rune
// this write placed. A mark with no base in the write has nothing to combine
// with and is dropped.
func attachMark(row []cell, base int, r rune) {
	if base < 0 {
		return
	}
	for i, m := range row[base].marks {
		if m == 0 {
			row[base].marks[i] = r
			return
		}
	}
}

// mendWide blanks the half of a wide rune a write over [x, end) left behind,
// so no row ever holds a leader without its second cell or the reverse.
func mendWide(row []cell, x, end int) {
	if end == x {
		return
	}
	if x > 0 && row[x-1].r != 0 && runeWidth(row[x-1].r) == 2 {
		row[x-1] = blank
	}
	if end < len(row) && row[end].r == 0 {
		row[end] = blank
	}
}

// right writes s so it ends at the grid's last column but one, leaving the
// bottom-right cell unwritten so no terminal scrolls on the last row.
func (g *grid) right(y int, s string, rl role, cs charset) {
	w := min(stringWidth(s), g.w-1)
	g.text(g.w-1-w, y, w, s, rl, cs)
}

// rule fills row y with the horizontal rule glyph.
func (g *grid) rule(y int, cs charset) {
	for x := range g.w {
		g.cells[y*g.w+x] = cell{r: cs.rule, role: roleBorder}
	}
}

// lines renders the grid as plain text, one line per row with trailing
// blanks trimmed: the --once frame and the golden form.
func (g *grid) lines() string {
	var sb strings.Builder
	for y := range g.h {
		var line strings.Builder
		for x := range g.w {
			if c := g.at(x, y); c.r != 0 {
				var tmp [cellBytes]byte
				line.Write(c.appendTo(tmp[:0]))
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}
