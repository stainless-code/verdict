package serve

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/webui"
)

const (
	exportIndex     = "index.html"
	exportScript    = "app.js"
	exportStyles    = "app.css"
	exportNotExport = "html-not-export"
	exportPerm      = 0o600
	exportDirPerm   = 0o700
)

func exportHTML(ctx context.Context, dest string, layer Layer, now time.Time) error {
	return exportWithRename(ctx, dest, layer, now, nil)
}

func exportWithRename(ctx context.Context, dest string, layer Layer, now time.Time, rename func(string, string) error) error {
	parent, dest, err := openExportParent(dest)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if err := parentRemnants(parent, dest); err != nil {
		return err
	}
	if err := parentDestination(parent, dest); err != nil {
		return err
	}
	body, err := snapshotData(ctx, layer, now)
	if err != nil {
		return exportFailure(err)
	}
	return stageExport(ctx, parent, dest, body, exportRename(parent, rename))
}

func exportRename(parent *os.Root, rename func(string, string) error) func(string, string) error {
	publishRename := parent.Rename
	if rename != nil {
		publishRename = func(from, to string) error {
			return rename(filepath.Join(parent.Name(), from), filepath.Join(parent.Name(), to))
		}
	}
	return publishRename
}

func stageExport(ctx context.Context, parent *os.Root, dest string, body []byte, rename func(string, string) error) (result error) {
	stage := dest + ".tmp-" + rand.Text()
	err := parent.Mkdir(stage, exportDirPerm)
	if err != nil {
		return exportFailure(err)
	}
	defer func() {
		if err := parent.RemoveAll(stage); err != nil {
			result = errors.Join(result, exportFailure(err))
		}
	}()
	if err := writeExport(parent, stage, body); err != nil {
		return exportFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return exportFailure(err)
	}
	return publishExport(parent, stage, dest, rename)
}

func writeExport(parent *os.Root, stage string, body []byte) error {
	root, err := parent.OpenRoot(stage)
	if err != nil {
		return exportFailure(err)
	}
	defer func() { _ = root.Close() }()
	for _, name := range webui.Names() {
		asset, err := exportAsset(name)
		if err != nil {
			return err
		}
		if err := root.WriteFile(name, asset, exportPerm); err != nil {
			return err
		}
	}
	if err := root.WriteFile("data.json", body, exportPerm); err != nil {
		return exportFailure(err)
	}
	return writeExportManifest(root)
}

func publishExport(parent *os.Root, stage, dest string, rename func(string, string) error) error {
	_, err := parent.Lstat(dest)
	if os.IsNotExist(err) {
		return publishNew(stage, dest, rename)
	}
	if err != nil {
		return exportFailure(err)
	}
	old := dest + ".old-" + strings.TrimPrefix(filepath.Base(stage), filepath.Base(dest)+".tmp-")
	if err := rename(dest, old); err != nil {
		return exportFailure(err)
	}
	if err := validateExport(parent, old); err != nil {
		return rollbackExport(old, dest, err, rename)
	}
	if err := rename(stage, dest); err != nil {
		return rollbackExport(old, dest, err, rename)
	}
	return removeOldExport(parent, old)
}

func publishNew(stage, dest string, rename func(string, string) error) error {
	if err := rename(stage, dest); err != nil {
		return exportFailure(err)
	}
	return nil
}

func rollbackExport(old, dest string, publish error, rename func(string, string) error) error {
	if err := rename(old, dest); err != nil {
		return exportFailure(fmt.Errorf("publish: %w; rollback: %w; restore %s manually", publish, err, old))
	}
	return exportFailure(publish)
}

func exportDestination(dest string) error {
	parent, name, err := openExportParent(dest)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return parentDestination(parent, name)
}

func parentDestination(parent *os.Root, dest string) error {
	info, err := parent.Lstat(dest)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return exportFailure(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &cli.RefusalError{Code: "html-symlink", Message: "HTML export destination must not be a symlink"}
	}
	if !info.IsDir() {
		return notExport()
	}
	return validateExport(parent, dest)
}

func validateExport(parent *os.Root, dest string) error {
	entries, err := fs.ReadDir(parent.FS(), dest)
	if err != nil {
		return exportFailure(err)
	}
	if len(entries) == 0 {
		return nil
	}
	if !exportEntries(entries) {
		return notExport()
	}
	page, err := parent.ReadFile(filepath.Join(dest, exportIndex))
	if err != nil || !strings.Contains(string(page), `<meta name="verdict-mode" content="static" />`) {
		return notExport()
	}
	return validateExportManifest(parent, dest)
}

func exportEntries(entries []os.DirEntry) bool {
	expected := map[string]bool{exportIndex: true, exportScript: true, exportStyles: true, "data.json": true, exportManifestName: true}
	if len(entries) != len(expected) {
		return false
	}
	for _, entry := range entries {
		if !expected[entry.Name()] || !entry.Type().IsRegular() {
			return false
		}
	}
	return true
}

func notExport() error {
	return &cli.RefusalError{Code: exportNotExport, Message: "non-empty destination is not a previous HTML export"}
}

func exportFailure(err error) error {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
		return ref
	}
	return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
}

func exportRemnants(dest string) error {
	parent, name, err := openExportParent(dest)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return parentRemnants(parent, name)
}

func parentRemnants(parent *os.Root, dest string) error {
	entries, err := fs.ReadDir(parent.FS(), ".")
	if err != nil {
		return exportFailure(err)
	}
	base := filepath.Base(dest)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), base+".old-") || strings.HasPrefix(entry.Name(), base+".tmp-") {
			return exportFailure(errors.New("interrupted HTML export: inspect sibling " + entry.Name() +
				"; restore the .old-* directory if the destination is absent, then move stale " +
				".old-* and .tmp-* siblings away before retrying"))
		}
	}
	return nil
}

func openExportParent(dest string) (*os.Root, string, error) {
	absolute, err := checkedExportPath(dest)
	if err != nil {
		return nil, "", err
	}
	parent, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return nil, "", exportFailure(err)
	}
	return parent, filepath.Base(absolute), nil
}

func checkedExportPath(dest string) (string, error) {
	if !filepath.IsAbs(dest) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", exportFailure(err)
		}
		dest = cwd + string(filepath.Separator) + dest
	}
	volume := filepath.VolumeName(dest)
	current := volume + string(filepath.Separator)
	parts := strings.Split(strings.TrimRight(filepath.ToSlash(dest[len(volume):]), "/"), "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		if err := exportPathComponent(current, i == len(parts)-1); err != nil {
			return "", err
		}
	}
	return current, nil
}

func exportPathComponent(name string, last bool) error {
	info, err := os.Lstat(name)
	if os.IsNotExist(err) && last {
		return nil
	}
	if err != nil {
		return exportFailure(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &cli.RefusalError{Code: "html-symlink", Message: "HTML export path must not contain a symlink"}
	}
	return nil
}

func exportAsset(name string) ([]byte, error) {
	asset, err := webui.Asset(name)
	if err != nil {
		return nil, exportFailure(err)
	}
	if name == exportIndex {
		asset = []byte(strings.Replace(string(asset), `content="live"`, `content="static"`, 1))
	}
	return asset, nil
}
