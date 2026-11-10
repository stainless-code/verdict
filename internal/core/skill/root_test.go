package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWritePayloadRefusesEscapingDir pins the write layer itself: a root whose
// payload directory links outside cannot be written through, whatever the plan
// already refused.
//
//nolint:revive // the test walks the write refusal
func TestWritePayloadRefusesEscapingDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "verdict")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "prompts")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ref := writePayload(root, target, "prompts/review.md")
	if ref == nil || ref.Code != codeInstall {
		t.Fatalf("writePayload refusal = %v, want skill-install-failed", ref)
	}
	if !strings.Contains(ref.Message, "path escapes from parent") {
		t.Fatalf("message = %q, want the root escape named", ref.Message)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside dir = %v; want no files", entries)
	}
}
