package coverage

import (
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// writeBundle lays out the one file the evaluator reads, in a per-test
// directory: no shared scratch path, so parallel packages cannot collide.
func writeBundle(t *testing.T, rows []hunks.Hunk) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir+"/hunks.tsv", hunks.Format(rows))
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o750) }
