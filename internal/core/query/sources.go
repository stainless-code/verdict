package query

import (
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
	// maxInventoryMiB bounds the bytes one bundle's inventory reads. Real
	// bundles carry 12–20 artifacts.
	maxInventoryMiB = 16
	// maxInventoryBytes is maxInventoryMiB in the byte unit the collector uses.
	maxInventoryBytes int64 = maxInventoryMiB << 20
	// chunksDirName is the one bundle subdirectory the inventory descends into.
	chunksDirName = "chunks"
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
type sourceCollector struct { //nolint:govet // grouped as one collector
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

// add reads and hashes one artifact. A symlink is followed, because the
// reader follows it and the row must name the bytes the view consumed. A
// missing or special file contributes no row: opening a FIFO would block the
// request. A file past either budget sets truncated.
func (c *sourceCollector) add(bundle, abs string) {
	if c.full() {
		c.truncated = true
		return
	}
	info, ok := regularFile(abs)
	if !ok {
		return
	}
	raw, ok := c.read(abs, info)
	if !ok {
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

// regularFile reports one regular-file answer, following a symlink so the
// inventory agrees with the reader that opens the target.
func regularFile(path string) (os.FileInfo, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	return info, info.Mode().IsRegular()
}

// read hashes one artifact unless its size would cross the byte budget.
func (c *sourceCollector) read(abs string, info os.FileInfo) ([]byte, bool) {
	if c.maxBytes > 0 && c.bytes+info.Size() > c.maxBytes {
		c.truncated = true
		return nil, false
	}
	raw, err := jq.CappedFile(abs, sourceCap)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// walkBundle inventories one bundle directory: the supported artifacts at the
// top level first, then the chunk files one directory down, so a chunk-heavy
// bundle cannot starve the store row.
func walkBundle(c *sourceCollector, bundle, dir, rel string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if !walkFiles(c, bundle, dir, rel, entries) || rel != "" {
		return
	}
	walkChunks(c, bundle, dir, entries)
}

// walkFiles inventories the regular supported files in one directory.
func walkFiles(c *sourceCollector, bundle, dir, rel string, entries []os.DirEntry) bool {
	for _, entry := range entries {
		if !c.addEntry(bundle, dir, rel, entry) {
			return false
		}
	}
	return true
}

// addEntry inventories one supported regular file; false stops the walk. A
// symlink is followed before the regular-file check, so a linked artifact is
// ledgered while a linked FIFO or device is still skipped.
func (c *sourceCollector) addEntry(bundle, dir, rel string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if c.full() {
		c.truncated = true
		return false
	}
	if !supportedArtifact(rel, entry.Name()) {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, entry.Name()))
	if err != nil || !info.Mode().IsRegular() {
		return true
	}
	c.add(bundle, filepath.Join(dir, entry.Name()))
	return true
}

// walkChunks descends into the bundle's one chunks subdirectory, following a
// directory symlink because the cost reader follows it.
func walkChunks(c *sourceCollector, bundle, dir string, entries []os.DirEntry) {
	for _, entry := range entries {
		if entry.Name() != chunksDirName {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			walkBundle(c, bundle, path, chunksDirName)
		}
	}
}

// supportedArtifact keeps the walk to the names a reader can consume: the
// documented bundle artifacts at the top level and the chunk cost files.
func supportedArtifact(rel, name string) bool {
	if rel == chunksDirName {
		return name == "manifest.json" || strings.HasSuffix(name, ".cost.json")
	}
	return slices.Contains(artifactNames, name)
}
