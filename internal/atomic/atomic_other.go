//go:build !windows

package atomic

import "os"

// replaceFile renames the finished temp file over the store. POSIX rename
// is atomic and has no sharing rules to wait out, so a failure is final
// and returned as-is (byte-identical to the pre-port shell behavior).
func replaceFile(from, to string) error {
	//nolint:wrapcheck // the caller seals the rename error in the atomic-write message (GO-ERRORS-03)
	return os.Rename(from, to)
}

func replaceRootFile(root *os.Root, from, to string) error {
	//nolint:wrapcheck // the caller seals the rename error in the atomic-write message (GO-ERRORS-03)
	return root.Rename(from, to)
}
