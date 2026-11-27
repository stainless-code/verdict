//go:build unix

package loopcore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoopFixRefusesAHardLinkedPatchFile proves the fix phase applies the
// review walk's hard-link check to patch.json: a patch.json hard-linked to a
// file outside the bundle would carry verdict's read, and the agent's write,
// to that file. Only unix reports the link count.
func TestLoopFixRefusesAHardLinkedPatchFile(t *testing.T) {
	t.Parallel()
	assertFixRefusesPlanted(t, func(dir string) error {
		source := filepath.Join(t.TempDir(), "main.go")
		if err := os.WriteFile(source, []byte("package main\n"), 0o600); err != nil {
			return err
		}
		return os.Link(source, filepath.Join(dir, patchFile))
	})
}
