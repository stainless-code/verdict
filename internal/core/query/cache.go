package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// bundleKeySep separates the root id from the bundle name in a cache key.
	bundleKeySep = "\x00"
	// decimalBase is the base every integer stamp component formats in.
	decimalBase = 10
	// modeBase is the base one entry's permission bits format in.
	modeBase = 8
	// overrideStampCap bounds one override fragment's content digest: a
	// fragment is a paragraph, not an artifact.
	overrideStampCap int64 = 64 << 10
	// unreadableMarker stamps an input whose bytes the stamp could not read.
	unreadableMarker = "!unreadable\n"
	// missingMarker stamps an absent input, which every reader treats as none.
	missingMarker = "!missing\n"
)

// stamp is the SHA-256 digest of every composition input: each bundle-directory
// entry, the chunks directory, and the root override fragments. A 32-byte
// digest keeps a cache row constant-size no matter how many files a bundle
// holds. An atomic write moves a directory entry; an in-place rewrite moves the
// target file's own size and mtime. A symlink is fingerprinted through to its
// target, the same file the readers open. An override fragment is fingerprinted
// by the bytes the capped reader returns, so a rewrite moves the stamp whatever
// it does to size and mtime. The stamp is a stat fingerprint for the bundle: a
// same-size, same-mtime in-place rewrite of a bundle artifact does not move it.
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

// bundleStamp reads one bundle's cache fingerprint, the root policy inputs the
// composed report validates, and whether the cache may use it. A fragment the
// stamp could not read gives no fingerprint worth trusting, so the request
// bypasses the cache and the reader's own refusal shows.
func bundleStamp(root Root, dir string) (stamp, bool) {
	var out strings.Builder
	appendDirStamp(&out, dir, "")
	cacheable := true
	for _, fragment := range overrideFragments {
		if !appendOverrideStamp(&out, filepath.Join(root.Path, filepath.FromSlash(fragment))) {
			cacheable = false
		}
	}
	return sha256.Sum256([]byte(out.String())), cacheable
}

// appendDirStamp fingerprints one directory's entries and recurses into the
// chunks directory.
func appendDirStamp(out *strings.Builder, dir, prefix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		out.WriteString(prefix + unreadableMarker)
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
		out.WriteString(name + unreadableMarker)
		return
	}
	appendStatLine(out, name, info)
	if prefix == "" && info.IsDir() && entry.Name() == chunksDirName {
		appendDirStamp(out, path, chunksDirName+"/")
	}
}

// appendOverrideStamp fingerprints one override fragment by the bytes the
// capped, non-blocking reader returns, so a content edit moves the stamp
// whatever it leaves size and mtime at. An absent fragment is a stable input.
// It reports false when the reader refused the fragment: the single marker
// every refusal writes cannot tell one unreadable state from another, so
// nothing built from it is cacheable.
func appendOverrideStamp(out *strings.Builder, path string) bool {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		out.WriteString(path + missingMarker)
		return true
	}
	if err != nil {
		out.WriteString(path + unreadableMarker)
		return false
	}
	raw, err := readcommon.ReadRegularCapped(path, overrideStampCap)
	if err != nil {
		out.WriteString(path + unreadableMarker)
		return false
	}
	sum := sha256.Sum256(raw)
	out.WriteString(path)
	out.WriteByte(' ')
	appendMode(out, info)
	out.WriteString(hex.EncodeToString(sum[:]))
	out.WriteByte('\n')
	return true
}

// appendStatLine writes one name + mode + size + mtime fingerprint row.
func appendStatLine(out *strings.Builder, name string, info os.FileInfo) {
	out.WriteString(name)
	out.WriteByte(' ')
	appendMode(out, info)
	out.WriteString(strconv.FormatInt(info.Size(), decimalBase))
	out.WriteByte(' ')
	out.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), decimalBase))
	out.WriteByte('\n')
}

// appendMode writes one entry's mode bits, so a permission-only change moves
// the stamp.
func appendMode(out *strings.Builder, info os.FileInfo) {
	out.WriteString(strconv.FormatUint(uint64(info.Mode()), modeBase))
	out.WriteByte(' ')
}

// cached composes one bundle's report, reusing the in-memory entry while its
// stamp holds. A request whose stamp could not be built reads and writes no
// entry, so the next request still sees the reader's refusal. The cache is
// bounded by the bundle cap and never written anywhere: a stale entry is a
// bug, not data loss.
func (l *Layer) cached(
	ctx context.Context, root Root, bundle, dir string, flags *core.ReportFlags,
) (*core.ReportResponse, *core.RefusalError, inventoryReport) {
	key := keyFor(root, bundle, flags)
	current, cacheable := bundleStamp(root, dir)
	l.mu.Lock()
	hit, ok := l.cache[key]
	if cacheable && ok && hit.stamp == current {
		l.touchLocked(key, &hit)
		l.mu.Unlock()
		return hit.resp, hit.ref, hit.inv
	}
	l.mu.Unlock()

	resp, ref := l.compose(ctx, root, bundle, dir, flags)
	inv := l.inventory(root, bundle, dir)
	if ref != nil || !cacheable {
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
