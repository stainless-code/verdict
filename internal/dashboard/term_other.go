//go:build !linux && !darwin && !windows && !freebsd && !netbsd && !openbsd && !dragonfly

package dashboard

import "os"

// openTTY has no raw mode to offer on this platform, so the live dashboard
// refuses and --once stays the way in.
func openTTY(_, _ *os.File) (tty, error) {
	return nil, errNoTTY
}

// termSize has no window size to read on this platform.
func termSize(_ *os.File) (cols, rows int, ok bool) {
	return 0, 0, false
}
