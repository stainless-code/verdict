package fetch

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp writes content to a temp file, failing the test on error.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
