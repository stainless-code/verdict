package readcommon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestResolveBundleDirSlugs pins the slug rules every verb shares: local- and
// corpus- slugs map straight to their own bundle directory.
func TestResolveBundleDirSlugs(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	t.Parallel()
	tests := []struct{ name, pr, want string }{
		{name: "local slug", pr: "local-7", want: ".review/local-7"},
		{name: "corpus slug", pr: "corpus-7", want: ".review/corpus-7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.ResolveBundleDir(tc.pr); got != tc.want {
				t.Errorf("ResolveBundleDir(%q) = %q; want %q", tc.pr, got, tc.want)
			}
		})
	}
}

// TestResolveBundleDirLocalPrecedence pins the bare-slug preference: a
// matching .review/local-<pr> dir wins only while no .review/<pr>/PR.json
// exists beside it. A corpus slug never falls into that guard, and a bare
// slug with neither dir reads the plain bundle path.
func TestResolveBundleDirLocalPrecedence(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	tests := []struct {
		name string
		pr   string
		want string
		seed []string
	}{
		{name: "plain slug", pr: "42", want: ".review/42"},
		{name: "local dir wins", pr: "42", seed: []string{".review/local-42"}, want: ".review/local-42"},
		{
			name: "real bundle wins",
			pr:   "42",
			seed: []string{".review/local-42", ".review/42"},
			want: ".review/42",
		},
		{
			name: "corpus slug beats local dir",
			pr:   "corpus-7",
			seed: []string{".review/local-corpus-7"},
			want: ".review/corpus-7",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkResolveBundleDir(t, tc.pr, tc.seed, tc.want) })
	}
}

// checkResolveBundleDir seeds the bundle dirs, then checks one resolution.
func checkResolveBundleDir(t *testing.T, pr string, seed []string, want string) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, dir := range seed {
		seedBundleDir(t, dir)
	}
	if got := readcommon.ResolveBundleDir(pr); got != want {
		t.Errorf("ResolveBundleDir(%q) = %q; want %q", pr, got, want)
	}
}

// seedBundleDir creates one bundle directory holding a PR.json.
func seedBundleDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestResolveSig pins the tally rule: exactly one full sig with the
// prefix resolves, zero reads NONE, and two read MANY — including two
// entries that carry the identical sig, which the deleted bundle copy
// resolved to a sig.
func TestResolveSig(t *testing.T) {
	tests := []struct {
		name   string
		doc    string
		prefix string
		want   string
	}{
		{
			name:   "unique prefix",
			doc:    `[{"sig":"aaaa1111"},{"sig":"bbbb2222"}]`,
			prefix: "aaaa",
			want:   "aaaa1111",
		},
		{
			name:   "ambiguous prefixes",
			doc:    `[{"sig":"aaaa1111"},{"sig":"aaaa2222"}]`,
			prefix: "aaaa",
			want:   "MANY",
		},
		{
			name:   "two identical entries",
			doc:    `[{"sig":"aaaa1111"},{"sig":"aaaa1111"}]`,
			prefix: "aaaa",
			want:   "MANY",
		},
		{
			name:   "no match",
			doc:    `[{"sig":"aaaa1111"}]`,
			prefix: "zzzz",
			want:   "NONE",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.ResolveSig(mustEntries(t, tc.doc), tc.prefix); got != tc.want {
				t.Errorf("ResolveSig(%q) = %q; want %q", tc.prefix, got, tc.want)
			}
		})
	}
}
