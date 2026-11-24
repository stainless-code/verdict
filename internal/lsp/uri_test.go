package lsp

import (
	"errors"
	"testing"
)

type uriCase struct {
	err  error
	name string
	uri  string
	rel  string
}

// TestRelFromURIPOSIX pins URI confinement against a POSIX root: only an
// absolute local file URI strictly under the root maps, and every traversal
// spelling refuses after exactly one percent-decode.
func TestRelFromURIPOSIX(t *testing.T) {
	t.Parallel()
	root := newServedRoot("/work/repo", false)
	runURICases(t, &root, []uriCase{
		{nil, "plain", "file:///work/repo/a.go", "a.go"},
		{nil, "nested", "file:///work/repo/internal/x/y.go", "internal/x/y.go"},
		{nil, "localhost authority", "file://localhost/work/repo/a.go", "a.go"},
		{nil, "scheme case", "FILE:///work/repo/a.go", "a.go"},
		{nil, "space and umlaut", "file:///work/repo/dir%20one/%C3%BC.go", "dir one/ü.go"},
		{nil, "dot segment cleans inside", "file:///work/repo/x/../a.go", "a.go"},
		{errOutsideRoot, "parent escape", "file:///work/repo/../etc/passwd", ""},
		{errOutsideRoot, "encoded parent escape", "file:///work/repo/%2e%2e%2fetc/passwd", ""},
		{nil, "double-encoded stays a name", "file:///work/repo/%252e%252e/a.go", "%2e%2e/a.go"},
		{errOutsideRoot, "sibling prefix", "file:///work/repo2/a.go", ""},
		{errOutsideRoot, "root itself", "file:///work/repo", ""},
		{errOutsideRoot, "root with slash", "file:///work/repo/", ""},
		{errOutsideRoot, "remote host", "file://server/work/repo/a.go", ""},
		{errOutsideRoot, "ipv6 host", "file://[::1]/work/repo/a.go", ""},
		{errUnsupportedURI, "encoded NUL", "file:///work/repo/a%00.go", ""},
		{errUnsupportedURI, "raw control", "file:///work/repo/a\x01.go", ""},
		{errUnsupportedURI, "invalid utf-8", "file:///work/repo/a%ff.go", ""},
		{errUnsupportedURI, "leading space", " file:///work/repo/a.go", ""},
		{errUnsupportedURI, "other scheme", "untitled:Untitled-1", ""},
		{errUnsupportedURI, "http scheme", "https://example.com/work/repo/a.go", ""},
		{errUnsupportedURI, "opaque", "file:a.go", ""},
		{errUnsupportedURI, "query", "file:///work/repo/a.go?x=1", ""},
		{errUnsupportedURI, "fragment", "file:///work/repo/a.go#L1", ""},
		{errOutsideRoot, "backslash is a name on POSIX", "file:///work/repo/a%5Cb.go", ""},
	})
}

// TestRelFromURIWindows pins the drive-letter rules: the volume and the root
// prefix fold case, an encoded colon decodes, a drive-relative path refuses,
// and backslashes normalize before confinement.
func TestRelFromURIWindows(t *testing.T) {
	t.Parallel()
	root := newServedRoot(`C:\work\repo`, true)
	runURICases(t, &root, []uriCase{
		{nil, "plain", "file:///C:/work/repo/a.go", "a.go"},
		{nil, "lower drive", "file:///c:/work/repo/a.go", "a.go"},
		{nil, "encoded colon (VS Code)", "file:///c%3A/work/repo/a.go", "a.go"},
		{nil, "prefix case folds", "file:///C:/Work/Repo/a.go", "a.go"},
		{nil, "rel keeps client case", "file:///C:/work/repo/Dir/A.go", "Dir/A.go"},
		{errOutsideRoot, "encoded backslash", "file:///C:/work/repo/x%5C..%5C..%5Cetc", ""},
		{errOutsideRoot, "parent escape", "file:///C:/work/repo/../../Windows/x", ""},
		{errOutsideRoot, "other drive", "file:///D:/work/repo/a.go", ""},
		{errUnsupportedURI, "drive-relative", "file:///C:a.go", ""},
		{errUnsupportedURI, "no drive", "file:///work/repo/a.go", ""},
		{errOutsideRoot, "unc host against a drive root", "file://server/share/a.go", ""},
	})
}

// TestRelFromURIUNC pins a UNC root: only its own host confines.
func TestRelFromURIUNC(t *testing.T) {
	t.Parallel()
	root := newServedRoot(`\\server\share\repo`, true)
	runURICases(t, &root, []uriCase{
		{nil, "own host", "file://server/share/repo/a.go", "a.go"},
		{nil, "host case folds", "file://SERVER/share/repo/a.go", "a.go"},
		{errOutsideRoot, "other host", "file://other/share/repo/a.go", ""},
		{errOutsideRoot, "local authority", "file:///share/repo/a.go", ""},
		{errOutsideRoot, "escape", "file://server/share/repo/../x", ""},
	})
}

func runURICases(t *testing.T, root *servedRoot, cases []uriCase) {
	t.Helper()
	for _, tc := range cases {
		rel, err := root.relFromURI(tc.uri)
		if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Errorf("%s: relFromURI(%q) error = %v, want %v", tc.name, tc.uri, err, tc.err)
			continue
		}
		if rel != tc.rel {
			t.Errorf("%s: relFromURI(%q) = %q, want %q", tc.name, tc.uri, rel, tc.rel)
		}
	}
}

// TestURIForRoundTrips pins the rendered URI literally per convention and
// proves each maps back to the same root-relative path.
func TestURIForRoundTrips(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		root    string
		rel     string
		want    string
		windows bool
	}{
		{"/work/repo", "a.go", "file:///work/repo/a.go", false},
		{"/work/repo/", "dir one/ü (1).go", "file:///work/repo/dir%20one/%C3%BC%20%281%29.go", false},
		{"/work/re po", "a#b?.go", "file:///work/re%20po/a%23b%3F.go", false},
		{`C:\work\repo`, "a.go", "file:///C:/work/repo/a.go", true},
		{`c:\work\repo`, "x/y z.go", "file:///C:/work/repo/x/y%20z.go", true},
		{`C:\`, "a.go", "file:///C:/a.go", true},
		{`\\server\share\repo`, "a.go", "file://server/share/repo/a.go", true},
	} {
		root := newServedRoot(tc.root, tc.windows)
		got := root.uriFor(tc.rel)
		if got != tc.want {
			t.Errorf("uriFor(%q under %q) = %q, want %q", tc.rel, tc.root, got, tc.want)
			continue
		}
		back, err := root.relFromURI(got)
		if err != nil || back != tc.rel {
			t.Errorf("relFromURI(%q) = %q, %v; want %q", got, back, err, tc.rel)
		}
	}
}

// TestConfineRelRefusesEveryEscape pins the finding-path confinement every
// store path passes before it can become a diagnostic URI.
func TestConfineRelRefusesEveryEscape(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{
		"", ".", "..", "../x", "a/../../x", "a/..", "/etc/passwd", `C:/x`, `C:x`, `a\b`,
		"./a", "a//b", "a/", "a\x00b", "a\nb", " a", "\xff",
	} {
		if confineRel(rel) == nil {
			t.Errorf("confineRel(%q) accepted an unconfined path", rel)
		}
	}
	for _, rel := range []string{"a.go", "x/y.go", "..a/b", "a../b", "dir one/ü.go"} {
		if err := confineRel(rel); err != nil {
			t.Errorf("confineRel(%q) = %v, want accepted", rel, err)
		}
	}
}

// TestPathKeyFoldsOnlyOnWindows pins the lookup key: Windows folds case,
// POSIX never does.
func TestPathKeyFoldsOnlyOnWindows(t *testing.T) {
	t.Parallel()
	win := newServedRoot(`C:\r`, true)
	posix := newServedRoot("/r", false)
	if win.pathKey("Dir/A.go") != "dir/a.go" || posix.pathKey("Dir/A.go") != "Dir/A.go" {
		t.Fatalf("pathKey = %q (windows), %q (posix)", win.pathKey("Dir/A.go"), posix.pathKey("Dir/A.go"))
	}
}
