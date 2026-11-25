package dashboard

import (
	"io"
	"sync"
)

// The escape vocabulary the session writes around the frames. paint.go owns
// the in-frame sequences; these only enter and leave the alternate screen.
const (
	escEnterAlt   = "\x1b[?1049h"
	escLeaveAlt   = "\x1b[?1049l"
	escHideCursor = "\x1b[?25l"
	escShowCursor = "\x1b[?25h"
	escSyncBegin  = "\x1b[?2026h"
	escSyncEnd    = "\x1b[?2026l"
)

// readTickDeciseconds is how long one idle terminal read waits before it
// reports no input (VTIME, in tenths of a second), so the single loop
// goroutine notices a resize or a cancel within 100 ms.
const readTickDeciseconds = 1

// startError is one terminal sentinel; Run maps it to a typed refusal.
type startError string

// Error renders the sentinel.
func (e startError) Error() string { return string(e) }

// Terminal sentinels.
var (
	// errNoTTY means the input or output is not a terminal the session can
	// put in raw mode.
	errNoTTY error = startError("dashboard: stdin and stdout must both be a terminal")
	// errNoVT means a Windows console refused virtual-terminal processing.
	errNoVT error = startError("dashboard: this console does not accept virtual-terminal sequences")
)

// tty is the raw-mode terminal a live session drives. The platform files
// implement it over stdlib syscall; tests substitute a scripted fake, so no
// test ever opens a real terminal.
type tty interface {
	// size reports the terminal's columns and rows.
	size() (cols, rows int, err error)
	// read returns the pending input bytes, or 0 and no error once a short
	// tick passes without any.
	read(p []byte) (int, error)
	// resized reports, once, that the window changed size since the last call.
	resized() bool
	// restore puts the terminal back in the mode it had before raw mode.
	restore() error
}

// session owns one raw-mode, alternate-screen lifetime. restore is the only
// way out and runs at most once, whichever exit path reaches it first: a
// clean quit, a cancel, a write error, or a panic unwinding through Run.
type session struct {
	term tty
	out  io.Writer
	err  error
	once sync.Once
}

// enter switches to the alternate screen and hides the cursor.
func (s *session) enter() error {
	_, err := io.WriteString(s.out, escEnterAlt+escHideCursor)
	return err //nolint:wrapcheck // the loop reports the terminal write as is
}

// restore shows the cursor, ends any open synchronized update, leaves the
// alternate screen, then restores the terminal mode. The escapes go first, so
// a failed mode restore still leaves the shell's screen readable.
func (s *session) restore() error {
	s.once.Do(func() {
		_, werr := io.WriteString(s.out, escShowCursor+escSyncEnd+escLeaveAlt)
		s.err = s.term.restore()
		if s.err == nil {
			s.err = werr
		}
	})
	return s.err
}
