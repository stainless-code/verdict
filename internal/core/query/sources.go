package query

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
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
	maxInventoryBytes int64 = maxInventoryMiB << mibShift
	// mibShift converts a byte count to MiB for one refusal line.
	mibShift = 20
	// chunksDirName is the one bundle subdirectory the inventory descends into.
	chunksDirName = "chunks"
	// dirReadSlack reads one entry past the ledger cap, so a directory with
	// more entries is detectable without scanning it whole.
	dirReadSlack = 1
)

// inventoryReport is one bundle's ledger walk: the rows, the artifact-specific
// reasons a read was skipped, and whether either budget cut coverage.
type inventoryReport struct {
	sources []Source
	notes   []string
	cut     bool
}

// inventory collects the artifacts under one bundle and the serving root's
// override fragments. It reports truncation when either budget stops the walk;
// every file it does read goes through the store cap.
func (l *Layer) inventory(root Root, bundle, dir string) inventoryReport {
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
	return inventoryReport{
		sources: slices.CompactFunc(collector.sources, func(a, b Source) bool {
			return a.Path == b.Path
		}),
		cut:   collector.truncated,
		notes: collector.notes,
	}
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
	notes      []string
	// beforeOpen runs between the size check and the descriptor open, the
	// window a path can be swapped or grown in; production leaves it nil.
	beforeOpen func(string)
}

// full reports whether either budget is spent.
func (c *sourceCollector) full() bool {
	return (c.maxEntries > 0 && len(c.sources) >= c.maxEntries) ||
		(c.maxBytes > 0 && c.bytes >= c.maxBytes)
}

// add reads and hashes one artifact. A symlink is followed, because the
// reader follows it and the row must name the bytes the view consumed. A
// missing or special file contributes no row. A file past either budget sets
// truncated.
func (c *sourceCollector) add(bundle, abs string) {
	if c.full() {
		c.truncated = true
		return
	}
	info, ok := regularFile(abs)
	if !ok {
		return
	}
	rel, err := filepath.Rel(c.rootPath, abs)
	if err != nil || rel == parentDir || strings.HasPrefix(rel, parentDir+string(filepath.Separator)) {
		return
	}
	raw, ok := c.read(abs, rel, info)
	if !ok {
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

// read hashes one artifact unless its size would cross a cap. The read is
// capped by what the budget has left, so a file that grows after the size
// check cannot overrun it, and a failed read is charged the cap it asked for.
func (c *sourceCollector) read(abs, rel string, info os.FileInfo) ([]byte, bool) {
	if info.Size() > sourceCap {
		c.charge(info.Size())
		c.skip(rel, fmt.Sprintf("%d bytes is over the %d MiB read cap", info.Size(), sourceCap>>mibShift))
		return nil, false
	}
	if c.maxBytes > 0 && c.bytes+info.Size() > c.maxBytes {
		c.truncated = true
		return nil, false
	}
	limit := c.readLimit()
	raw, err := c.readArtifact(abs, limit)
	if err != nil {
		c.charge(limit)
		if errors.Is(err, jq.ErrTooLarge) {
			c.skip(rel, fmt.Sprintf("over the %d-byte read cap", limit))
		} else {
			c.skip(rel, err.Error())
		}
		return nil, false
	}
	return raw, true
}

// readLimit is how many bytes one read may consume: the per-artifact cap,
// bounded by the budget left.
func (c *sourceCollector) readLimit() int64 {
	if c.maxBytes <= 0 || c.maxBytes-c.bytes >= sourceCap {
		return sourceCap
	}
	return c.maxBytes - c.bytes
}

// readArtifact opens one artifact through the race-free reader: the open is
// non-blocking and the descriptor is fstat'd, so a path swapped for a FIFO
// after the pre-stat cannot block the request. The last size check has run, so
// the test seam fires here, at the one production open.
func (c *sourceCollector) readArtifact(abs string, maxBytes int64) ([]byte, error) {
	if c.beforeOpen != nil {
		c.beforeOpen(abs)
	}
	return readcommon.ReadRegularCapped(abs, maxBytes) //nolint:wrapcheck // %w inside keeps ErrTooLarge visible
}

// charge folds an attempted read into the byte budget.
func (c *sourceCollector) charge(size int64) {
	if size > 0 {
		if c.maxBytes > 0 {
			size = min(size, max(0, c.maxBytes-c.bytes))
		}
		c.bytes += size
	}
}

// skip marks coverage cut and names the artifact and reason.
func (c *sourceCollector) skip(rel, reason string) {
	c.truncated = true
	c.notes = append(c.notes, fmt.Sprintf("serve: source inventory skipped %s: %s",
		filepath.ToSlash(rel), reason))
}

// walkBundle inventories one bundle directory: the supported artifacts at the
// top level first, then the chunk files one directory down, so a chunk-heavy
// bundle cannot starve the store row.
func walkBundle(c *sourceCollector, bundle, dir, rel string) {
	entries, more := c.readDir(dir)
	if more {
		c.truncated = true
	}
	if !walkFiles(c, bundle, dir, rel, entries) || rel != "" {
		return
	}
	walkChunks(c, bundle, dir, entries)
}

// readDir reads at most one entry past the ledger cap. Directory order is the
// filesystem's, so the batch is sorted by name to keep the emitted rows
// deterministic; a directory past the bound is cut without being read whole.
// more reports that the batch filled the bound and entries may remain.
func (c *sourceCollector) readDir(dir string) ([]os.DirEntry, bool) {
	limit := maxInventoryEntries + dirReadSlack
	if c.maxEntries > 0 && c.maxEntries+dirReadSlack < limit {
		limit = c.maxEntries + dirReadSlack
	}
	entries, more, err := readcommon.ReadDirLimit(dir, limit)
	if err != nil {
		c.truncated = true
		c.notes = append(c.notes, fmt.Sprintf("serve: source inventory could not list %s: %s",
			c.relPath(dir), err))
		return nil, false
	}
	return entries, more
}

// relPath names one inventory path the way a source row does.
func (c *sourceCollector) relPath(abs string) string {
	rel, err := filepath.Rel(c.rootPath, abs)
	if err != nil || rel == parentDir || strings.HasPrefix(rel, parentDir+string(filepath.Separator)) {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
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
	return name == "truncation.json" || slices.Contains(artifactNames, name)
}
