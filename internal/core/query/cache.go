package query

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	// bundleKeySep separates the root id from the bundle name in a cache key.
	bundleKeySep = "\x00"
	// decimalBase is the base every integer stamp component formats in.
	decimalBase = 10
)

// stamp is the SHA-256 digest of every composition input: each bundle-directory
// entry, the chunks directory, and the root override fragments. A 32-byte
// digest keeps a cache row constant-size no matter how many files a bundle
// holds. An atomic write moves a directory entry; an in-place rewrite moves the
// target file's own size and mtime. A symlink is fingerprinted through to its
// target, the same file the readers open. The stamp is a stat fingerprint, not
// a content hash: a same-size, same-mtime in-place rewrite does not move it.
type stamp [sha256.Size]byte

// cacheKey names one composed view: the bundle plus the flags that change
// the composition. Check separates an inbox row (no gate) from a detail.
type cacheKey struct {
	root   string
	bundle string
	check  string
	live   string
	cost   bool
}

// keyFor derives the cache key for one composition.
func keyFor(root Root, bundle string, flags *core.ReportFlags) cacheKey {
	return cacheKey{
		root:   root.ID,
		bundle: bundle,
		check:  flags.Check,
		live:   flags.Live,
		cost:   flags.Cost,
	}
}

// entry is one cached composition: the response or refusal plus the artifact
// inventory it was built from.
type entry struct {
	resp  *core.ReportResponse
	ref   *core.RefusalError
	inv   inventoryReport
	stamp stamp
	used  uint64
}

// bundleStamp reads one bundle's cache fingerprint and the root policy inputs
// the composed report validates.
func bundleStamp(root Root, dir string) stamp {
	var out strings.Builder
	appendDirStamp(&out, dir, "")
	for _, fragment := range overrideFragments {
		appendFileStamp(&out, filepath.Join(root.Path, filepath.FromSlash(fragment)))
	}
	return sha256.Sum256([]byte(out.String()))
}

// appendDirStamp fingerprints one directory's entries and recurses into the
// chunks directory.
func appendDirStamp(out *strings.Builder, dir, prefix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		out.WriteString(prefix + "!unreadable\n")
		return
	}
	for _, entry := range entries {
		appendEntryStamp(out, dir, prefix, entry)
	}
}

// appendEntryStamp fingerprints one directory entry. The stat follows a
// symlink, so a target edit moves the stamp; the readers follow the same link.
func appendEntryStamp(out *strings.Builder, dir, prefix string, entry os.DirEntry) {
	name := prefix + entry.Name()
	path := filepath.Join(dir, entry.Name())
	info, err := os.Stat(path)
	if err != nil {
		out.WriteString(name + "!unreadable\n")
		return
	}
	appendStatLine(out, name, info)
	if prefix == "" && info.IsDir() && entry.Name() == chunksDirName {
		appendDirStamp(out, path, chunksDirName+"/")
	}
}

// appendFileStamp fingerprints one root-level reader input, following a
// symlink to the bytes the reader opens.
func appendFileStamp(out *strings.Builder, path string) {
	info, err := os.Stat(path)
	if err != nil {
		out.WriteString(path + "!missing\n")
		return
	}
	appendStatLine(out, path, info)
}

// appendStatLine writes one name + size + mtime fingerprint row.
func appendStatLine(out *strings.Builder, name string, info os.FileInfo) {
	out.WriteString(name)
	out.WriteByte(' ')
	out.WriteString(strconv.FormatInt(info.Size(), decimalBase))
	out.WriteByte(' ')
	out.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), decimalBase))
	out.WriteByte('\n')
}

// cached composes one bundle's report, reusing the in-memory entry while its
// stamp holds. The cache is bounded by the bundle cap and never written
// anywhere: a stale entry is a bug, not data loss.
func (l *Layer) cached(
	ctx context.Context, root Root, bundle, dir string, flags *core.ReportFlags,
) (*core.ReportResponse, *core.RefusalError, inventoryReport) {
	key := keyFor(root, bundle, flags)
	current := bundleStamp(root, dir)
	l.mu.Lock()
	if hit, ok := l.cache[key]; ok && hit.stamp == current {
		l.touchLocked(key, &hit)
		l.mu.Unlock()
		return hit.resp, hit.ref, hit.inv
	}
	l.mu.Unlock()

	resp, ref := l.compose(ctx, root, bundle, dir, flags)
	inv := l.inventory(root, bundle, dir)
	if ref != nil {
		return resp, ref, inv
	}

	l.mu.Lock()
	bundleID := key.root + bundleKeySep + key.bundle
	if !bundlePresentLocked(l.cache, bundleID) && bundleCountLocked(l.cache) >= l.maxBundles {
		evictOldestBundleLocked(l.cache)
	}
	l.touchLocked(key, &entry{resp: resp, ref: ref, inv: inv, stamp: current})
	l.mu.Unlock()
	return resp, ref, inv
}

// touchLocked stores one entry as the most recently used composition.
func (l *Layer) touchLocked(key cacheKey, item *entry) {
	l.seq++
	item.used = l.seq
	l.cache[key] = *item
}

// bundlePresentLocked reports whether the cache already holds a composition of
// one bundle.
func bundlePresentLocked(cache map[cacheKey]entry, bundle string) bool {
	for key := range cache {
		if key.root+bundleKeySep+key.bundle == bundle {
			return true
		}
	}
	return false
}

// bundleCountLocked counts the distinct bundles the cache holds, the unit the
// bundle cap is written in.
func bundleCountLocked(cache map[cacheKey]entry) int {
	bundles := map[string]bool{}
	for key := range cache {
		bundles[key.root+bundleKeySep+key.bundle] = true
	}
	return len(bundles)
}

// evictOldestBundleLocked drops every composition of the least recently used
// bundle, so a new bundle never clears unrelated hot entries.
func evictOldestBundleLocked(cache map[cacheKey]entry) {
	oldest := leastRecentlyUsedBundle(cache)
	for key := range cache {
		if key.root+bundleKeySep+key.bundle == oldest {
			delete(cache, key)
		}
	}
}

// leastRecentlyUsedBundle names the bundle whose newest composition is oldest.
func leastRecentlyUsedBundle(cache map[cacheKey]entry) string {
	last := map[string]uint64{}
	for key, item := range cache {
		bundle := key.root + bundleKeySep + key.bundle
		if item.used > last[bundle] {
			last[bundle] = item.used
		}
	}
	oldest := ""
	var oldestUsed uint64
	for bundle, used := range last {
		if oldest == "" || used < oldestUsed {
			oldest, oldestUsed = bundle, used
		}
	}
	return oldest
}

// cacheEntries reports the cache size a health row publishes.
func (l *Layer) cacheEntries() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.cache)
}
