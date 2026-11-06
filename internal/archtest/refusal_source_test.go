package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestRefusalScanIgnoresRunEvidence preserves live emission checks.
func TestRefusalScanIgnoresRunEvidence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, fixture := range []struct{ path, code string }{
		{"internal/live/source.go", "live-source-code"},
		{".review/before/source.go", "archived-source-code"},
	} {
		path := filepath.Join(root, fixture.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		source := fmt.Sprintf("package fixture\nvar result = struct{ Code string }{Code: %q}\n", fixture.code)
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	emitted := scanRefusalCodes(t, root)
	if len(emitted["live-source-code"]) != 1 {
		t.Fatalf("live refusal emission missing: %v", emitted)
	}
	if len(emitted["archived-source-code"]) != 0 {
		t.Fatalf("run evidence treated as live source: %v", emitted)
	}
}
