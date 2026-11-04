//go:build !linux && !darwin && !windows && !freebsd && !netbsd && !openbsd && !dragonfly

package cli

import (
	"os"
)

func IsTerminal(f *os.File) bool {
	return false
}
