//go:build !linux && !darwin

package run

import "io/fs"

// identity is empty where verdict reads no inode or change time from a
// stat (Windows): the fingerprint there is path, mode, size, and
// modification time alone, which a same-size rewrite that restores the
// modification time passes.
func identity(fs.FileInfo) string { return "" }
