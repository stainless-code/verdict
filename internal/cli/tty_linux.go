//go:build linux

package cli

// termiosSize is the Linux termios buffer size for the TCGETS probe; the
// bytes are never read, so only the size matters.
const termiosSize = 60

// ioctlGetTermios is the termios-fetch ioctl on Linux.
const ioctlGetTermios = 0x5401 // TCGETS
