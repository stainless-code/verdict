package verifycore

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSandboxArgvRefusesTheFilesystemRoot keeps the writable bind from ever
// covering the whole host.
func TestSandboxArgvRefusesTheFilesystemRoot(t *testing.T) {
	_, ref := sandboxArgv(string(filepath.Separator), []string{"HOME=" + t.TempDir()})
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, "filesystem root") {
		t.Errorf("message = %q, want the root cause", ref.Message)
	}
}
