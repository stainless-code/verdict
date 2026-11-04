//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package cli

// termiosSize is the BSD termios buffer size for the TIOCGETA probe; the
// bytes are never read, so only the size matters.
const termiosSize = 256

// ioctlGetTermios is the termios-fetch ioctl on BSDs (TIOCGETA).
const ioctlGetTermios = 0x40487413
