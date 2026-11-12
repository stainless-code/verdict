//go:build !windows

package skill

import "syscall"

// writableDir reports whether this process may add an entry to dir, the same
// question the installer's own writableDir asks before replacing a binary.
func writableDir(dir string) bool {
	return syscall.Access(dir, 2) == nil
}
