//go:build windows

package skill

import "os"

// writableDir reports whether dir exists as a directory. Windows has no
// portable access(2) probe: a read-only attribute on a directory does not stop
// a create inside it, so the write itself stays the only reliable check.
func writableDir(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
