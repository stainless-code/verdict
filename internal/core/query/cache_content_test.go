package query

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBundleStampSmallRewrite verifies the published refusal or read contract.
func TestBundleStampSmallRewrite(t *testing.T) {
	root := t.TempDir()
	dir := root
	file := filepath.Join(dir, "review.json")
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	before, ok := bundleStamp(Root{Path: root}, dir)
	if !ok {
		t.Fatal("initial stamp uncacheable")
	}
	if err := os.WriteFile(file, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, ok := bundleStamp(Root{Path: root}, dir)
	if !ok || before == after {
		t.Fatal("same-size same-mtime rewrite did not change stamp")
	}
}
