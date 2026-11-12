package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
)

const (
	exportManifestName  = "manifest.json"
	exportManifestLimit = 2048
)

func exportOwnedFiles() []string {
	return []string{exportIndex, exportScript, exportStyles, "data.json"}
}

type exportManifest struct {
	Files   map[string]string `json:"files"`
	Version string            `json:"version"`
}

func writeExportManifest(root *os.Root) error {
	manifest := exportManifest{Version: "1", Files: map[string]string{}}
	for _, name := range exportOwnedFiles() {
		digest, err := exportDigest(root, name)
		if err != nil {
			return err
		}
		manifest.Files[name] = digest
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return exportFailure(err)
	}
	return root.WriteFile(exportManifestName, append(body, '\n'), exportPerm)
}

func exportDigest(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return "", exportFailure(err)
	}
	if !info.Mode().IsRegular() {
		return "", notExport()
	}
	file, err := root.Open(name)
	if err != nil {
		return "", exportFailure(err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", exportFailure(err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readExportManifest(root *os.Root) (*exportManifest, error) {
	file, err := root.Open(exportManifestName)
	if err != nil {
		return nil, notExport()
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, exportManifestLimit+1))
	decoder.DisallowUnknownFields()
	var manifest exportManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, notExport()
	}
	if !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return nil, notExport()
	}
	if manifest.Version != "1" || len(manifest.Files) != len(exportOwnedFiles()) {
		return nil, notExport()
	}
	return &manifest, nil
}

func verifyExportManifest(root *os.Root) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return exportFailure(err)
	}
	if !exportEntries(entries) {
		return notExport()
	}
	manifest, err := readExportManifest(root)
	if err != nil {
		return err
	}
	return verifyManifestFiles(root, manifest)
}

func verifyManifestFiles(root *os.Root, manifest *exportManifest) error {
	for _, name := range exportOwnedFiles() {
		digest, err := exportDigest(root, name)
		if err != nil {
			return err
		}
		if digest != manifest.Files[name] {
			return notExport()
		}
	}
	return nil
}

func validateExportManifest(parent *os.Root, name string) error {
	root, err := parent.OpenRoot(name)
	if err != nil {
		return exportFailure(err)
	}
	defer func() { _ = root.Close() }()
	return verifyExportManifest(root)
}

func removeOldExport(parent *os.Root, name string) error {
	root, err := parent.OpenRoot(name)
	if err != nil {
		return exportFailure(err)
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return exportFailure(err)
	}
	if len(entries) == 0 {
		return removeExportDirectory(parent, name)
	}
	if err := verifyExportManifest(root); err != nil {
		return err
	}
	if err := removeExportFiles(root); err != nil {
		return err
	}
	return removeExportDirectory(parent, name)
}

func removeExportFiles(root *os.Root) error {
	for _, name := range append(exportOwnedFiles(), exportManifestName) {
		if err := root.Remove(name); err != nil {
			return exportFailure(err)
		}
	}
	return nil
}

func removeExportDirectory(parent *os.Root, name string) error {
	if err := parent.Remove(name); err != nil {
		return exportFailure(err)
	}
	return nil
}
