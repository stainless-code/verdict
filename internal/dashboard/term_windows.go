//go:build windows

package dashboard

import (
	"context"
	"os"
	"syscall"
	"unsafe"
)

// Console mode bits stdlib syscall does not name.
const (
	enableProcessedInput            = 0x0001
	enableLineInput                 = 0x0002
	enableEchoInput                 = 0x0004
	enableProcessedOutput           = 0x0001
	enableVirtualTerminalProcessing = 0x0004
	enableVirtualTerminalInput      = 0x0200
	readTickMillis                  = readTickDeciseconds * 100
	waitTimeout                     = 0x00000102
	// peekRecords bounds one look at the pending input records.
	peekRecords = 64
)

// stdlib syscall exports GetConsoleMode but neither SetConsoleMode nor
// GetConsoleScreenBufferInfo, so both come from kernel32 by name; no
// golang.org/x/sys.
var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procPeekConsoleInputW          = kernel32.NewProc("PeekConsoleInputW")
	procReadConsoleInputW          = kernel32.NewProc("ReadConsoleInputW")
)

// inputRecord is INPUT_RECORD with its union read as KEY_EVENT_RECORD: 20
// bytes, the event type padded to 4, then the key fields. Only a key event's
// fields are meaningful; the other kinds are discarded unread.
type inputRecord struct {
	EventType       uint16
	_               uint16
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	Char            uint16
	ControlKeyState uint32
}

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type screenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

// winTTY is a console switched to virtual-terminal input and output. Windows
// has no SIGWINCH, so resized polls the window once per read tick.
type winTTY struct {
	in, out         syscall.Handle
	inMode, outMode uint32
	cols, rows      int
}

// openTTY switches both console handles to VT mode. A handle that is not a
// console is errNoTTY; a console that refuses VT (pre-Windows 10, or a legacy
// conhost) is errNoVT, so the dashboard refuses instead of half-working.
func openTTY(in, out *os.File) (tty, error) {
	t := &winTTY{in: syscall.Handle(in.Fd()), out: syscall.Handle(out.Fd())}
	if syscall.GetConsoleMode(t.in, &t.inMode) != nil || syscall.GetConsoleMode(t.out, &t.outMode) != nil {
		return nil, errNoTTY
	}
	if !setConsoleMode(t.out, t.outMode|enableProcessedOutput|enableVirtualTerminalProcessing) {
		return nil, errNoVT
	}
	inRaw := (t.inMode &^ (enableLineInput | enableEchoInput)) | enableProcessedInput | enableVirtualTerminalInput
	if !setConsoleMode(t.in, inRaw) {
		setConsoleMode(t.out, t.outMode)
		return nil, errNoVT
	}
	t.cols, t.rows, _ = consoleSize(t.out)
	return t, nil
}

func (t *winTTY) size() (cols, rows int, err error) {
	cols, rows, ok := consoleSize(t.out)
	if !ok {
		return 0, 0, errNoTTY
	}
	return cols, rows, nil
}

func (t *winTTY) read(p []byte) (int, error) {
	event, err := syscall.WaitForSingleObject(t.in, readTickMillis)
	if err != nil {
		return 0, err //nolint:wrapcheck // the loop reports the console read as is
	}
	if event == waitTimeout {
		return 0, nil
	}
	pending, err := t.keyPending()
	if err != nil || !pending {
		return 0, err
	}
	var n uint32
	if err := syscall.ReadFile(t.in, p, &n, nil); err != nil {
		return 0, err //nolint:wrapcheck // the loop reports the console read as is
	}
	return int(n), nil
}

// keyPending reports whether the signaled input holds a character a read
// returns. Focus, mouse, and key-up records also signal the handle but give
// ReadFile nothing, so it would block the loop until the next key; those
// records are consumed here and the tick ends idle instead.
func (t *winTTY) keyPending() (bool, error) {
	var recs [peekRecords]inputRecord
	var n uint32
	r, _, err := procPeekConsoleInputW.Call(
		uintptr(t.in), uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return false, err //nolint:wrapcheck // the loop reports the console error as is
	}
	events := make([]consoleEvent, n)
	for i, rec := range recs[:n] {
		events[i] = consoleEvent{kind: rec.EventType, char: rec.Char, keyDown: rec.KeyDown != 0}
	}
	if keyInput(events) {
		return true, nil
	}
	r, _, err = procReadConsoleInputW.Call(
		uintptr(t.in), uintptr(unsafe.Pointer(&recs[0])), uintptr(n), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return false, err //nolint:wrapcheck // the loop reports the console error as is
	}
	return false, nil
}

// stopRequested is always false: a Windows console has no job control.
func (t *winTTY) stopRequested() bool { return false }

// suspend has nothing to do without job control.
func (t *winTTY) suspend(context.Context) error { return nil }

func (t *winTTY) resized() bool {
	cols, rows, ok := consoleSize(t.out)
	if !ok || (cols == t.cols && rows == t.rows) {
		return false
	}
	t.cols, t.rows = cols, rows
	return true
}

func (t *winTTY) restore() error {
	inOK := setConsoleMode(t.in, t.inMode)
	if !setConsoleMode(t.out, t.outMode) || !inOK {
		return errRestore
	}
	return nil
}

// termSize reports f's visible window size when f is a console.
func termSize(f *os.File) (cols, rows int, ok bool) {
	return consoleSize(syscall.Handle(f.Fd()))
}

func consoleSize(h syscall.Handle) (cols, rows int, ok bool) {
	var info screenBufferInfo
	// The pointer conversion stays inside the call expression, where the
	// runtime keeps info alive and in place.
	r, _, _ := procGetConsoleScreenBufferInfo.Call( //nolint:errcheck // GetLastError, ruled by the result word
		uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0, 0, false
	}
	cols = int(info.Window.Right-info.Window.Left) + 1
	rows = int(info.Window.Bottom-info.Window.Top) + 1
	return cols, rows, cols > 0 && rows > 0
}

func setConsoleMode(h syscall.Handle, mode uint32) bool {
	r, _, _ := procSetConsoleMode.Call(uintptr(h), uintptr(mode)) //nolint:errcheck // GetLastError, ruled by the result word
	return r != 0
}
