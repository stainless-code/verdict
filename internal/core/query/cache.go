package query

import (
	"context"
	"os"

	"github.com/stainless-code/verdict/internal/core"
)

// stamp is the directory fingerprint one cache entry was built from: the
// bundle directory's mtime and size, plus the findings store's mtime and
// size. An atomic write moves the directory stamp; a rewrite moves the store
// pair even when a coarse filesystem collapses the directory timestamp.
type stamp struct {
	dirMod      int64
	dirSize     int64
	findingsMod int64
	findingsLen int64
}

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
	resp    *core.ReportResponse
	ref     *core.RefusalError
	sources []Source
	stamp   stamp
}

// bundleStamp reads one bundle's cache fingerprint.
func bundleStamp(dir string) stamp {
	var out stamp
	if info, err := os.Stat(dir); err == nil {
		out.dirMod = info.ModTime().UnixNano()
		out.dirSize = info.Size()
	}
	if info, err := os.Stat(dir + "/findings.json"); err == nil {
		out.findingsMod = info.ModTime().UnixNano()
		out.findingsLen = info.Size()
	}
	return out
}

// cached composes one bundle's report, reusing the in-memory entry while its
// stamp holds. The cache is bounded by the bundle cap and never written
// anywhere: a stale entry is a bug, not data loss.
func (l *Layer) cached(
	ctx context.Context, root Root, bundle, dir string, flags *core.ReportFlags,
) (*core.ReportResponse, *core.RefusalError, []Source) {
	key := keyFor(root, bundle, flags)
	current := bundleStamp(dir)
	l.mu.Lock()
	if hit, ok := l.cache[key]; ok && hit.stamp == current {
		l.mu.Unlock()
		return hit.resp, hit.ref, hit.sources
	}
	l.mu.Unlock()

	resp, ref := l.compose(ctx, root, bundle, dir, flags)
	sources := l.inventory(root, bundle, dir)

	l.mu.Lock()
	if len(l.cache) >= l.maxBundles {
		l.cache = map[cacheKey]entry{}
	}
	l.cache[key] = entry{stamp: current, resp: resp, ref: ref, sources: sources}
	l.mu.Unlock()
	return resp, ref, sources
}

// cacheEntries reports the cache size a health row publishes.
func (l *Layer) cacheEntries() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.cache)
}
