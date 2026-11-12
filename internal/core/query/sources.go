package query

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
)

// sourceCap bounds every inventory read: the store cap the readers share.
const sourceCap = jq.MaxFindingsBytes

// inventory collects the artifacts under one bundle and the serving root's
// override fragments. Every file is read through the store cap, so a hostile
// or corrupt bundle can never make a listing allocate past it.
func (l *Layer) inventory(root Root, bundle, dir string) []Source {
	collector := &sourceCollector{root: root, rootPath: root.Path}
	walkBundle(collector, bundle, dir, "")
	for _, fragment := range overrideFragments {
		collector.add("", filepath.Join(root.Path, filepath.FromSlash(fragment)))
	}
	slices.SortFunc(collector.sources, func(a, b Source) int {
		return strings.Compare(a.Path, b.Path)
	})
	return slices.CompactFunc(collector.sources, func(a, b Source) bool {
		return a.Path == b.Path
	})
}

// overrideFragments are the root-level reader inputs outside a bundle
// directory.
var overrideFragments = []string{
	filepath.ToSlash(overrides.Preamble),
	filepath.ToSlash(overrides.FooterOrderPath),
	filepath.ToSlash(overrides.Threshold),
	filepath.ToSlash(overrides.Attribution),
}

// sourceCollector accumulates deduped inventory rows.
type sourceCollector struct {
	root     Root
	rootPath string
	sources  []Source
}

// add reads and hashes one artifact. A missing or over-cap file contributes
// no row: no view consumed its bytes.
func (c *sourceCollector) add(bundle, abs string) {
	raw, err := jq.CappedFile(abs, sourceCap)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(c.rootPath, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	c.sources = append(c.sources, Source{
		Root:   c.root.ID,
		Bundle: bundle,
		Path:   filepath.ToSlash(rel),
		SHA256: digestBytes(raw),
		Bytes:  int64(len(raw)),
	})
}

// walkBundle inventories one bundle directory: the store's regular files at
// the top level and the chunk cost files one directory down.
func walkBundle(c *sourceCollector, bundle, dir, rel string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		walkEntry(c, bundle, dir, rel, entry)
	}
}

// walkEntry inventories one directory entry: a regular file, or the chunks
// subdirectory one level down.
func walkEntry(c *sourceCollector, bundle, dir, rel string, entry fs.DirEntry) {
	if entry.Type()&fs.ModeSymlink != 0 {
		return
	}
	childRel := path.Join(rel, entry.Name())
	if !entry.IsDir() {
		c.add(bundle, filepath.Join(dir, entry.Name()))
		return
	}
	if rel == "" && entry.Name() == "chunks" {
		walkBundle(c, bundle, filepath.Join(dir, entry.Name()), childRel)
	}
}
