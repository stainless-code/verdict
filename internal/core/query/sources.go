package query

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
)

const (
	// sourceCap bounds every inventory read: the store cap the readers share.
	sourceCap = jq.MaxFindingsBytes
	// maxInventoryEntries bounds one bundle's ledger rows, so a hostile store
	// cannot turn one request into tens of thousands of hashes.
	maxInventoryEntries = 64
	// maxInventoryBytes bounds the bytes one bundle's inventory reads. Real
	// bundles carry 12–20 artifacts.
	maxInventoryBytes int64 = 16 << 20
)

// inventory collects the artifacts under one bundle and the serving root's
// override fragments. It reports truncation when either budget stops the walk;
// every file it does read goes through the store cap.
func (l *Layer) inventory(root Root, bundle, dir string) ([]Source, bool) {
	collector := &sourceCollector{
		root: root, rootPath: root.Path,
		maxEntries: maxInventoryEntries, maxBytes: maxInventoryBytes,
	}
	walkBundle(collector, bundle, dir, "")
	for _, fragment := range overrideFragments {
		collector.add("", filepath.Join(root.Path, filepath.FromSlash(fragment)))
	}
	slices.SortFunc(collector.sources, func(a, b Source) int {
		return strings.Compare(a.Path, b.Path)
	})
	return slices.CompactFunc(collector.sources, func(a, b Source) bool {
		return a.Path == b.Path
	}), collector.truncated
}

// overrideFragments are the root-level reader inputs outside a bundle
// directory.
var overrideFragments = []string{
	filepath.ToSlash(overrides.Preamble),
	filepath.ToSlash(overrides.FooterOrderPath),
	filepath.ToSlash(overrides.Threshold),
	filepath.ToSlash(overrides.Attribution),
}

// sourceCollector accumulates deduped inventory rows under one budget.
type sourceCollector struct {
	root       Root
	rootPath   string
	maxEntries int
	maxBytes   int64
	bytes      int64
	sources    []Source
	truncated  bool
}

// full reports whether either budget is spent.
func (c *sourceCollector) full() bool {
	return (c.maxEntries > 0 && len(c.sources) >= c.maxEntries) ||
		(c.maxBytes > 0 && c.bytes >= c.maxBytes)
}

// add reads and hashes one artifact. A missing, symlinked, or special file
// contributes no row: no view consumed its bytes, and opening a FIFO would
// block the request. A file past either budget sets truncated.
func (c *sourceCollector) add(bundle, abs string) {
	if c.full() {
		c.truncated = true
		return
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	if c.maxBytes > 0 && c.bytes+info.Size() > c.maxBytes {
		c.truncated = true
		return
	}
	raw, err := jq.CappedFile(abs, sourceCap)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(c.rootPath, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	c.bytes += int64(len(raw))
	c.sources = append(c.sources, Source{
		Root:   c.root.ID,
		Bundle: bundle,
		Path:   filepath.ToSlash(rel),
		SHA256: digestBytes(raw),
		Bytes:  int64(len(raw)),
	})
}

// walkBundle inventories one bundle directory: the supported artifacts at the
// top level first, then the chunk files one directory down, so a chunk-heavy
// bundle cannot starve the store row.
func walkBundle(c *sourceCollector, bundle, dir, rel string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() {
			continue
		}
		if c.full() {
			c.truncated = true
			return
		}
		if entry.Type().IsRegular() && supportedArtifact(rel, entry.Name()) {
			c.add(bundle, filepath.Join(dir, entry.Name()))
		}
	}
	if rel != "" {
		return
	}
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink == 0 && entry.IsDir() && entry.Name() == "chunks" {
			walkBundle(c, bundle, filepath.Join(dir, entry.Name()), "chunks")
		}
	}
}

// supportedArtifact keeps the walk to the names a reader can consume: the
// documented bundle artifacts at the top level and the chunk cost files.
func supportedArtifact(rel, name string) bool {
	if rel == "chunks" {
		return name == "manifest.json" || strings.HasSuffix(name, ".cost.json")
	}
	return slices.Contains(artifactNames, name)
}
