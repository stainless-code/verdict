package serve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestHTMLParentSwapBeforeOpen rejects a redirected parent handle.
func TestHTMLParentSwapBeforeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	parent, outside, saved := exportSwapFixture(t)
	err := exerciseExportParent(t, filepath.Join(parent, "export"), openAfterExportSwap(parent, outside, saved))
	if _, statErr := os.Stat(filepath.Join(outside, "export", "data.json")); statErr == nil {
		t.Fatal("parent swap wrote outside/export/data.json")
	}
	ref, ok := errors.AsType[*cli.RefusalError](err)
	if !ok || ref.Code != "html-symlink" {
		t.Fatalf("opened parent swap refusal = %v; want html-symlink", err)
	}
	assertExportEmpty(t, outside)
	assertExportEmpty(t, saved)
}

// TestHTMLParentSwapAfterOpen keeps all later writes on the opened handle.
func TestHTMLParentSwapAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	parent, outside, saved := exportSwapFixture(t)
	if err := exerciseExportParent(t, filepath.Join(parent, "export"), swapAfterExportOpen(parent, outside, saved)); err != nil {
		t.Fatal(err)
	}
	if got := string(readExport(t, filepath.Join(saved, "export", "data.json"))); got != "{}\n" {
		t.Fatalf("anchored export = %q", got)
	}
	assertExportEmpty(t, outside)
}

func exportSwapFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := exportTempDir(t)
	parent, outside, saved := filepath.Join(dir, "parent"), filepath.Join(dir, "outside"), filepath.Join(dir, "parent-saved")
	for _, name := range []string{parent, outside} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return parent, outside, saved
}

func swapExportParent(parent, outside, saved string) error {
	if err := os.Rename(parent, saved); err != nil {
		return err
	}
	return os.Symlink(outside, parent)
}

func exerciseExportParent(t *testing.T, dest string, open func(string) (*os.Root, error)) error {
	t.Helper()
	root, name, err := openExportParentWith(dest, open)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return stageExport(t.Context(), root, name, []byte("{}\n"), root.Rename)
}

func openAfterExportSwap(parent, outside, saved string) func(string) (*os.Root, error) {
	return func(name string) (*os.Root, error) {
		if err := swapExportParent(parent, outside, saved); err != nil {
			return nil, err
		}
		return os.OpenRoot(name)
	}
}

func swapAfterExportOpen(parent, outside, saved string) func(string) (*os.Root, error) {
	return func(name string) (*os.Root, error) {
		root, err := os.OpenRoot(name)
		if err != nil {
			return nil, err
		}
		if err := swapExportParent(parent, outside, saved); err != nil {
			_ = root.Close()
			return nil, err
		}
		return root, nil
	}
}
