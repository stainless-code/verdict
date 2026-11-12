package query

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestBundleStampContentBudget bounds hashes across the bundle and chunks.
func TestBundleStampContentBudget(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(strconv.FormatBool(nested), func(t *testing.T) { checkStampBudget(t, nested) })
	}
}

func checkStampBudget(t *testing.T, nested bool) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "bundle")
	chunks := filepath.Join(dir, "chunks")
	if err := os.MkdirAll(chunks, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 65536)
	for i := range 256 {
		parent := dir
		if nested && i >= 128 {
			parent = chunks
		}
		writeStampArtifact(t, filepath.Join(parent, strconv.Itoa(i)), raw)
	}
	if _, ok := bundleStamp(Root{Path: root}, dir); !ok {
		t.Fatal("exactly 16 MiB should be cacheable")
	}
	writeStampArtifact(t, filepath.Join(chunks, "extra"), raw)
	if _, ok := bundleStamp(Root{Path: root}, dir); ok {
		t.Fatal("more than 16 MiB of content remained cacheable")
	}
}

func writeStampArtifact(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
