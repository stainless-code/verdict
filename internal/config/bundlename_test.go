package config

import (
	"path/filepath"
	"testing"
)

// bundleNameMsg is the one line every bundle verb renders for a positional
// that is not a single path element.
const bundleNameMsg = "bundle name must be one path element (a PR number or fetch-local name), not a path"

// TestBundleDirRefusesNonElementNames proves the shared guard admits exactly
// one path element: every spelling that would leave the bundle home, land on
// the home itself, or break a path round-trip refuses with the one refusal the
// bundle verbs render.
func TestBundleDirRefusesNonElementNames(t *testing.T) {
	for _, name := range []string{
		"",
		".",
		"..",
		"../escaped",
		"a/b",
		`a\b`,
		"7/",
		"./7",
		"7/.",
		"a\x00b",
		"a\nb",
		"a\x7fb",
	} {
		t.Run(name, func(t *testing.T) { assertBundleDirRefused(t, name) })
	}
}

// assertBundleDirRefused pins the refusal for one non-element name.
func assertBundleDirRefused(t *testing.T, name string) {
	t.Helper()
	dir, ref := BundleDir("", name)
	if dir != "" {
		t.Fatalf("BundleDir(%q) = %q; want no directory beside a refusal", name, dir)
	}
	if ref == nil {
		t.Fatalf("BundleDir(%q) answered; want a refusal", name)
	}
	if ref.Code != CodeBadBundleName || ref.Message != bundleNameMsg {
		t.Fatalf("BundleDir(%q) refused %s: %q; want %s: %q",
			name, ref.Code, ref.Message, CodeBadBundleName, bundleNameMsg)
	}
}

// TestBundleDirResolvesElementNames proves the names a bundle really carries
// resolve to the bundle home, anchored at the given root.
func TestBundleDirResolvesElementNames(t *testing.T) {
	for _, tc := range []struct{ name, bundle, root, want string }{
		{name: "pr number", bundle: "7", want: ".verdict/state/7"},
		{name: "fetch-local slug", bundle: "local-tidy", want: ".verdict/state/local-tidy"},
		{name: "corpus slug", bundle: "corpus-1", want: ".verdict/state/corpus-1"},
		{name: "rooted", bundle: "7", root: "/repo", want: "/repo/.verdict/state/7"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBundleDirResolves(t, tc.root, tc.bundle, tc.want) })
	}
}

// assertBundleDirResolves pins the path one element name resolves to, in slash
// form so the expectation reads the same on every platform.
func assertBundleDirResolves(t *testing.T, root, bundle, want string) {
	t.Helper()
	dir, ref := BundleDir(root, bundle)
	if ref != nil {
		t.Fatalf("BundleDir(%q, %q) refused: %v", root, bundle, ref)
	}
	if got := filepath.ToSlash(dir); got != want {
		t.Fatalf("BundleDir(%q, %q) = %q; want %q", root, bundle, got, want)
	}
}
