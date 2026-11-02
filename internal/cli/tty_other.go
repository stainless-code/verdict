//go:build !linux && !darwin && !windows && !freebsd && !netbsd && !openbsd && !dragonfly

package cli

import (
	"os"
)

// IsTerminal reports false on platforms without a terminal probe: the CLI
// fails closed (refuses needs-confirm) instead of prompting blindly.
func IsTerminal(f *os.File) bool {
	return false
}
