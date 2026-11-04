//go:build linux

package cli

const termiosSize = 60

// ioctlGetTermios is the termios-fetch ioctl on Linux.
const ioctlGetTermios = 0x5401 // TCGETS
