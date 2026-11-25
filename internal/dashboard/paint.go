package dashboard

import (
	"bytes"
	"io"
	"strconv"
)

// escClear erases the whole screen; a full repaint starts with it.
const escClear = "\x1b[2J"

// painter turns composed grids into terminal bytes. It keeps the frame the
// terminal already shows and writes only the cells that changed: a frame
// identical to the last one writes zero bytes, which is the whole flicker and
// idle-cost contract. Each frame that does change goes out in one Write,
// wrapped in a synchronized update, so the terminal never shows half of it.
type painter struct {
	out   io.Writer
	pal   *palette
	front *grid
	buf   bytes.Buffer
}

// invalidate forces the next paint to repaint the whole screen (a resize, or
// a write that failed partway).
func (p *painter) invalidate() { p.front = nil }

// paint brings the terminal from the shown frame to back.
func (p *painter) paint(back *grid) error {
	p.buf.Reset()
	front := p.front
	if front == nil || front.w != back.w || front.h != back.h {
		front = newGrid(back.w, back.h)
		p.buf.WriteString(escSyncBegin + p.pal[rolePlain] + escClear)
	}
	cur := rolePlain
	for y := range back.h {
		first, last, ok := rowSpan(front, back, y)
		if !ok {
			continue
		}
		if p.buf.Len() == 0 {
			p.buf.WriteString(escSyncBegin)
		}
		cur = p.emitRow(back, y, first, last, cur)
	}
	return p.flush(back, cur)
}

// emitRow moves the cursor to the span's start and writes its cells, with an
// SGR only where the role changes; it returns the role left in effect.
func (p *painter) emitRow(back *grid, y, first, last int, cur role) role {
	p.buf.WriteString("\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(first+1) + "H")
	for x := first; x <= last; x++ {
		c := back.at(x, y)
		if c.r == 0 {
			continue // the second half of a wide rune the leader already drew
		}
		if c.role != cur {
			p.buf.WriteString(p.pal[c.role])
			cur = c.role
		}
		p.buf.WriteRune(c.r)
	}
	return cur
}

// flush writes the frame in one call, or nothing at all when no cell moved.
func (p *painter) flush(back *grid, cur role) error {
	if p.buf.Len() == 0 {
		return nil
	}
	if cur != rolePlain {
		p.buf.WriteString(p.pal[rolePlain])
	}
	p.buf.WriteString(escSyncEnd)
	if _, err := p.out.Write(p.buf.Bytes()); err != nil {
		p.front = nil
		return err //nolint:wrapcheck // the loop reports the terminal write as is
	}
	p.front = back.clone()
	return nil
}

// rowSpan is the first and last column of row y that differ between the two
// frames. A span never starts on the second half of a wide rune: grid.text
// writes both halves together, so the leader differs whenever its half does.
func rowSpan(front, back *grid, y int) (first, last int, ok bool) {
	first, last = -1, -1
	for x := range back.w {
		if front.at(x, y) != back.at(x, y) {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	if first < 0 {
		return 0, 0, false
	}
	return first, last, true
}

func (g *grid) clone() *grid {
	c := &grid{w: g.w, h: g.h, cells: make([]cell, len(g.cells))}
	copy(c.cells, g.cells)
	return c
}
