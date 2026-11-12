package serve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestExportDestinationRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := exportDestination(dir)
	ref, ok := errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-not-export" {
		t.Fatalf("non-export refusal = %v; want html-not-export", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	err = exportDestination(link)
	ref, ok = errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-symlink" {
		t.Fatalf("symlink refusal = %v; want html-symlink", err)
	}
}
