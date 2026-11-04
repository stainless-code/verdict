//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package cli

const termiosSize = 256

// ioctlGetTermios is the termios-fetch ioctl on BSDs (TIOCGETA).
const ioctlGetTermios = 0x40487413
