//go:build !windows

package readcommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// restoreFragmentMode re-enables a fragment so the temp dir always cleans up.
func restoreFragmentMode(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Logf("restore fragment mode: %v", err)
	}
}

// TestValidateOverridesAtUnreadableFragment pins the read-error posture:
// a present but unreadable fragment refuses bad-override with the frozen
// "is unreadable" message, the same rule the record-local copy carried.
func TestValidateOverridesAtUnreadableFragment(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission gate does not fail for root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "preamble.md")
	writeFragment(t, dir, "preamble.md", "Reviewed with verdict.\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreFragmentMode(t, path) })

	err := ValidateOverridesAt(dir)
	if got := refusalCode(err); got != "bad-override" {
		t.Fatalf("code = %q (%v), want bad-override", got, err)
	}
	if !strings.Contains(err.Error(), "is unreadable") {
		t.Errorf("refusal = %q, want the unreadable-fragment message", err)
	}
}
