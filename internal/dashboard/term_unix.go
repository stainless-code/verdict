//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package dashboard

import (
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
	winch   chan os.Signal
	orig    syscall.Termios
	in, out uintptr
}

// openTTY puts in into raw mode. Either side not being a terminal is
// errNoTTY: a pipe has no termios and no window size.
func openTTY(in, out *os.File) (tty, error) {
	t := &unixTTY{in: in.Fd(), out: out.Fd()}
	if err := ioctl(t.in, ioctlGetTermios, unsafe.Pointer(&t.orig)); err != nil {
		return nil, errNoTTY
	}
	if _, _, ok := winsizeOf(t.out); !ok {
		return nil, errNoTTY
	}
	raw := t.orig
	raw.Iflag &^= syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = readTickDeciseconds
	if err := ioctl(t.in, ioctlSetTermios, unsafe.Pointer(&raw)); err != nil {
		return nil, errNoTTY
	}
	t.winch = make(chan os.Signal, 1)
	signal.Notify(t.winch, syscall.SIGWINCH)
	return t, nil
}

func (t *unixTTY) size() (cols, rows int, err error) {
	cols, rows, ok := winsizeOf(t.out)
	if !ok {
		return 0, 0, errNoTTY
	}
	return cols, rows, nil
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
	return ioctl(t.in, ioctlSetTermios, unsafe.Pointer(&t.orig))
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
