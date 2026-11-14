package readgolden_test

import (
	"testing"
)

// storeHomeFile plants a regular file where the bundle home belongs: every
// listing verb must refuse it as a corrupt store naming the path, not read it
// as a home with no bundles.
func storeHomeFile(t *testing.T, dir, _ string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{".verdict/state": "not a bundle home\n"})
}

// TestStoreHomeRefusals pins the one sentence a file-shaped bundle home earns
// from each verb that lists it, hint included: trend's rollup, the risk queue,
// and adopt (which refuses before resolving any bundle under the home). The
// message must be the "not a directory" sentence StoreEntries decides with
// Stat, not the "unreadable" one a ReadDir-only check falls into.
func TestStoreHomeRefusals(t *testing.T) {
	t.Parallel()

	goldenRead(t, "store/trend-file-home-json", storeHomeFile, nil, nil, splitRepoOnly("--json")...)
	goldenRead(t, "store/risk-file-home-json", storeHomeFile, nil, nil, "risk", "acme/app", "--json")
	goldenRead(t, "store/adopt-file-home-json", storeHomeFile, nil, nil,
		"adopt", "acme/app", "1648", "--from", "local-x", "--json")
}
