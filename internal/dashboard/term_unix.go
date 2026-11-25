//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package dashboard

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// winsize is the TIOCGWINSZ result; stdlib syscall does not declare it.
type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// unixTTY is a terminal in raw mode: no echo, no line buffering, no CR
// translation on input, and reads that return after one tick (VMIN=0,
// VTIME=1). ISIG stays on, so Ctrl-C is still the SIGINT every verb cancels
// on, and OPOST stays on, so the output side is untouched.
type unixTTY struct {
	winch, tstp, cont chan os.Signal
	orig, raw         syscall.Termios
	in, out           uintptr
	cols, rows        int
}

// openTTY puts in into raw mode. An input with no termios (a pipe) is
// errNoTTY. A window that reports no size is drawn at 80×24 until the first
// SIGWINCH says otherwise.
func openTTY(in, out *os.File) (tty, error) {
	t := &unixTTY{in: in.Fd(), out: out.Fd(), cols: minCols, rows: minRows}
	if err := ioctl(t.in, ioctlGetTermios, unsafe.Pointer(&t.orig)); err != nil {
		return nil, errNoTTY
	}
	t.raw = t.orig
	t.raw.Iflag &^= syscall.ICRNL | syscall.IXON
	t.raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN
	t.raw.Cc[syscall.VMIN] = 0
	t.raw.Cc[syscall.VTIME] = readTickDeciseconds
	if err := ioctl(t.in, ioctlSetTermios, unsafe.Pointer(&t.raw)); err != nil {
		return nil, errNoTTY
	}
	t.winch = make(chan os.Signal, 1)
	t.tstp = make(chan os.Signal, 1)
	t.cont = make(chan os.Signal, 1)
	signal.Notify(t.winch, syscall.SIGWINCH)
	signal.Notify(t.tstp, syscall.SIGTSTP)
	return t, nil
}

func (t *unixTTY) size() (cols, rows int, err error) {
	c, r, ok := winsizeOf(t.out)
	t.cols, t.rows = keepSize(t.cols, t.rows, c, r, ok)
	return t.cols, t.rows, nil
}

func (t *unixTTY) stopRequested() bool {
	select {
	case <-t.tstp:
		return true
	default:
		return false
	}
}

// suspend does what the default Ctrl-Z action would, with the terminal put
// back first: cooked mode, then a real SIGTSTP with the default action, so
// the shell sees an ordinary stopped job. It waits for SIGCONT before raw
// mode returns, so a stop delivered to another thread cannot race it.
func (t *unixTTY) suspend(ctx context.Context) error {
	if err := ioctl(t.in, ioctlSetTermios, unsafe.Pointer(&t.orig)); err != nil {
		return errRestore
	}
	signal.Notify(t.cont, syscall.SIGCONT)
	defer signal.Stop(t.cont)
	signal.Reset(syscall.SIGTSTP)
	err := syscall.Kill(syscall.Getpid(), syscall.SIGTSTP)
	if err == nil {
		select {
		case <-t.cont:
		case <-ctx.Done():
		}
	}
	signal.Notify(t.tstp, syscall.SIGTSTP)
	if rerr := ioctl(t.in, ioctlSetTermios, unsafe.Pointer(&t.raw)); rerr != nil {
		return errNoTTY
	}
	return err //nolint:wrapcheck // a failed self-signal is reported as is
}

func (t *unixTTY) read(p []byte) (int, error) {
	for {
		n, err := syscall.Read(int(t.in), p)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return 0, err //nolint:wrapcheck // the loop reports the terminal read as is
		}
		return n, nil
	}
}

func (t *unixTTY) resized() bool {
	select {
	case <-t.winch:
		return true
	default:
		return false
	}
}

func (t *unixTTY) restore() error {
	signal.Stop(t.winch)
	signal.Stop(t.tstp)
	if err := ioctl(t.in, ioctlSetTermios, unsafe.Pointer(&t.orig)); err != nil {
		return errRestore
	}
	return nil
}

// termSize reports f's window size when f is a terminal.
func termSize(f *os.File) (cols, rows int, ok bool) {
	return winsizeOf(f.Fd())
}

// winsizeOf reads one window size; a terminal mid-resize can report zeros,
// which count as no answer.
func winsizeOf(fd uintptr) (cols, rows int, ok bool) {
	var ws winsize
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 || ws.Row == 0 {
		return 0, 0, false
	}
	return int(ws.Col), int(ws.Row), true
}

// ioctl issues one terminal ioctl. stdlib syscall has no typed wrapper, so
// the struct's address goes in as the argument, exactly as cli.IsTerminal does.
func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); errno != 0 {
		return errno //nolint:wrapcheck // the errno is the whole answer; every caller only tests for nil
	}
	return nil
}
