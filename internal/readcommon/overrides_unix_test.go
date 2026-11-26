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

// TestValidateOverridesAtRefusesAFragmentLinkedOutsideTheRepo proves the
// override validation every reader runs (report --output included) refuses a
// fragment linked out of its repository and keeps one linked inside it.
func TestValidateOverridesAtRefusesAFragmentLinkedOutsideTheRepo(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".verdict", "overrides")
	outside := writeFragmentAt(t, filepath.Join(t.TempDir(), "preamble.md"))
	inside := writeFragmentAt(t, filepath.Join(repo, "docs", "preamble.md"))
	linkFragment(t, outside, filepath.Join(dir, "preamble.md"))
	err := ValidateOverridesAt(dir)
	if err == nil || !strings.Contains(err.Error(), "bad-override") || !strings.Contains(err.Error(), "outside its repository") {
		t.Fatalf("ValidateOverridesAt through an outside link = %v, want bad-override", err)
	}
	linkFragment(t, inside, filepath.Join(dir, "preamble.md"))
	if err := ValidateOverridesAt(dir); err != nil {
		t.Fatalf("ValidateOverridesAt through an in-repo link = %v, want nil", err)
	}
}

// writeFragmentAt writes a valid preamble fragment at path.
func writeFragmentAt(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("A preamble.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// linkFragment points link at target, replacing whatever was there.
func linkFragment(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
