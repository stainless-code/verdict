package dashboard

import (
	"strings"

	"github.com/stainless-code/verdict/internal/untrusted"
)

// cell is one terminal cell: the rune it shows and the role it is painted
// with. A wide rune owns the cell after it, which holds rune 0.
type cell struct {
	r    rune
	role role
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
	col := x
	for _, r := range s {
		w := runeWidth(r)
		if w == 0 {
			continue
		}
		row[col] = cell{r: r, role: rl}
		if w == 2 {
			row[col+1] = cell{role: rl}
		}
		col += w
	}
	return col
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
			if r := g.at(x, y).r; r != 0 {
				line.WriteRune(r)
			}
		}
		sb.WriteString(strings.TrimRight(line.String(), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}
