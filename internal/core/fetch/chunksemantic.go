package fetchcore

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// semanticOrder reorders file slices so coupled files land in the same chunk
// whenever they fit: the package/module directory first, then implementation
// and test pairs by stem, then path bytes. The order is total and derived only
// from the paths, so the same table packs identically on every platform and
// every run; nothing here reads a file or a clock.
func semanticOrder(table []fileSlice) []fileSlice {
	type entry struct {
		key   semanticKey
		slice fileSlice
	}
	entries := make([]entry, len(table))
	for i := range table {
		entries[i] = entry{key: keyForPath(table[i].path), slice: table[i]}
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return a.key.compare(b.key) })
	out := make([]fileSlice, len(entries))
	for i := range entries {
		out[i] = entries[i].slice
	}
	return out
}

// semanticKey is one file's ordering identity: the normalized directory, the
// stem it shares with the implementation or test file it belongs to, whether it
// is the test side of that pair, and the path bytes that settle the rest.
type semanticKey struct {
	dir  string
	stem string
	path string
	test bool
}

// compare orders two keys: directory subtree, stem, implementation before test,
// then path bytes. Paths are unique in a diff, so the order is total.
func (k semanticKey) compare(other semanticKey) int {
	if c := compareDirPaths(k.dir, other.dir); c != 0 {
		return c
	}
	if c := strings.Compare(k.stem, other.stem); c != 0 {
		return c
	}
	if k.test != other.test {
		if k.test {
			return 1
		}
		return -1
	}
	return strings.Compare(k.path, other.path)
}

// compareDirPaths orders two normalized directories by their segments, so a
// subsystem sorts together: `engine` before `engine/graph`, and both before
// `engine-extras` — a byte comparison would put `engine-extras` first because
// `-` sorts below `/`. The repository root sorts first as the empty prefix.
func compareDirPaths(a, b string) int {
	if a == b {
		return 0
	}
	left, right := dirSegments(a), dirSegments(b)
	for i := 0; i < len(left) && i < len(right); i++ {
		if c := strings.Compare(left[i], right[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(left), len(right))
}

// dirSegments splits a normalized directory into its segments; the root has
// none.
func dirSegments(dir string) []string {
	if dir == "." || dir == "" || dir == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(dir, pathSep), pathSep)
}

// keyForPath derives one file's ordering identity. The directory and the stem
// are read from the normalized spelling — git spells separators as forward
// slashes, while a hand-made or Windows-produced table may not — so `pkg\a.go`
// and `pkg/a.go` pair with the same files. The final tie-break keeps the
// original spelling, so two equivalent paths whose normalized keys are equal
// still order by the bytes the diff carried, never by input order.
func keyForPath(p string) semanticKey {
	normalized := portableOrderPath(p)
	stem, isTest := splitTestName(path.Base(normalized))
	return semanticKey{dir: path.Dir(normalized), stem: stem, test: isTest, path: p}
}

// portableOrderPath normalizes one diff path to forward slashes.
func portableOrderPath(p string) string {
	return path.Clean(strings.ReplaceAll(p, `\`, pathSep))
}

// testMarkers are the file-name fragments each ecosystem uses to mark a test
// file: a suffix on the stem (Go's `_test`, Python's `foo_test`) and the
// infixes TypeScript and JavaScript spell (`foo.test`, `foo.spec`). Stripping
// them leaves the implementation file's stem, which is what pulls the pair into
// one chunk.
var testMarkers = []string{"_test", ".test", ".spec"}

// testPrefix opens Python's `test_` naming, the other half of the pair.
const testPrefix = "test_"

// splitTestName reports the stem one file shares with its counterpart and
// whether this file is the test side. The final extension is dropped first, so
// `foo.test.ts` and `foo.ts` share the stem `foo`, and `bar.py` and
// `test_bar.py` share `bar`.
func splitTestName(name string) (stem string, test bool) {
	root := strings.TrimSuffix(name, path.Ext(name))
	if root == "" {
		return name, false
	}
	if trimmed, ok := cutTestMarker(root); ok {
		return trimmed, true
	}
	if trimmed, ok := strings.CutPrefix(root, testPrefix); ok && trimmed != "" {
		return trimmed, true
	}
	return root, false
}

// cutTestMarker strips one marker suffix, reporting whether a non-empty stem is
// left — an implementation file named exactly `_test.go` pairs with nothing.
func cutTestMarker(root string) (string, bool) {
	for _, marker := range testMarkers {
		if trimmed, ok := strings.CutSuffix(root, marker); ok && trimmed != "" {
			return trimmed, true
		}
	}
	return "", false
}
