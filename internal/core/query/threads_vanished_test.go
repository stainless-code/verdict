package query

import (
	"os"
	"path/filepath"
	"testing"
)

// TestThreadFeedVanishes verifies the published refusal or read contract.
func TestThreadFeedVanishes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "threads.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1","threads":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &Layer{beforeThreadRead: func(p string) {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}}
	view, row := l.threadView(Root{}, "42", dir)
	if view != nil || row != nil {
		t.Fatalf("vanished feed = %v, %v; want absent", view, row)
	}
}
