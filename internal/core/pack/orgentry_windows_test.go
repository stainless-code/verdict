//go:build windows

package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckOrgEntryAllowsAnotherVolume proves a legal absolute org entry on a
// different drive from the repo is outside it: [filepath.Rel] cannot relate the
// two volumes, and that failure must not refuse the entry as unresolvable.
func TestCheckOrgEntryAllowsAnotherVolume(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	other := secondVolume(t, filepath.VolumeName(root))
	listed := filepath.Join(other+`\`, "packs", "team")
	if ref := CheckOrgEntry(root, listed); ref != nil {
		t.Fatalf("%s = %s: %s, want no refusal", listed, ref.Code, ref.Message)
	}
}

// secondVolume returns a drive other than vol, skipping when the host has one.
func secondVolume(t *testing.T, vol string) string {
	t.Helper()
	for letter := 'D'; letter <= 'Z'; letter++ {
		candidate := string(letter) + ":"
		if strings.EqualFold(candidate, vol) {
			continue
		}
		if info, err := os.Stat(candidate + `\`); err == nil && info.IsDir() {
			return candidate
		}
	}
	t.Skip("this host has a single volume; a cross-volume Rel error needs two")
	return ""
}
