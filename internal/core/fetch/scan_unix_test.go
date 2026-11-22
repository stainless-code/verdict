//go:build unix

package fetchcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScanDiffRefusesUnreadableFile proves an unreadable file refuses the whole
// scan instead of silently shrinking the review. It is unix-only: Windows has
// no permission bit that revokes read access for the owner.
func TestScanDiffRefusesUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file, so the refusal cannot fire")
	}
	root := physicalTempDir(t)
	path := filepath.Join(root, "secret.txt")
	scanFixture(t, root, "secret.txt", "hidden\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Errorf("restore %s permissions: %v", path, err)
		}
	})
	files := []scanFile{{rel: "secret.txt", size: 7}}
	_, _, _, ref := scanDiff(context.Background(), root, files)
	if ref == nil || ref.Code != codeBadScan {
		t.Fatalf("scanDiff = %v; want a bad-scan refusal", ref)
	}
	if !strings.Contains(ref.Message, "could not read secret.txt") {
		t.Errorf("refusal = %q; want it to name the unreadable file", ref.Message)
	}
}
