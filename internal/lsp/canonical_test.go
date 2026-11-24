package lsp

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWindowsFS resolves a fixed set of existing paths (keyed by their slash
// spelling, matched case-insensitively as Windows does) to OS-native long
// spellings, as [filepath.EvalSymlinks] would there. Anything else does not
// exist.
func fakeWindowsFS(known map[string]string) func(string) (string, error) {
	folded := map[string]string{}
	for k, v := range known {
		folded[strings.ToLower(k)] = v
	}
	return func(p string) (string, error) {
		if resolved, ok := folded[strings.ToLower(p)]; ok {
			return resolved, nil
		}
		return "", errors.New("does not exist")
	}
}

// windowsFS models a runner whose temp directory is reachable through the
// 8.3 short name RUNNER~1 and the long name runneradmin.
var windowsFS = fakeWindowsFS(map[string]string{
	"C:/Users/RUNNER~1/Temp/repo":             `C:\Users\runneradmin\Temp\repo`,
	"C:/Users/runneradmin/Temp/repo":          `C:\Users\runneradmin\Temp\repo`,
	"C:/Users/RUNNER~1/Temp/repo/a.go":        `C:\Users\runneradmin\Temp\repo\a.go`,
	"c:/users/runneradmin/temp/repo/a.go":     `C:\Users\runneradmin\Temp\repo\a.go`,
	"C:/Users/runneradmin/Temp/repo/a.go":     `C:\Users\runneradmin\Temp\repo\a.go`,
	"C:/Users/RUNNER~1/Temp/sibling":          `C:\Users\runneradmin\Temp\sibling`,
	"C:/Users/runneradmin/Temp/sibling/b.go":  `C:\Users\runneradmin\Temp\sibling\b.go`,
	"C:/Users/runneradmin/Temp/repo/sub/x.go": `C:\Users\runneradmin\Temp\repo\sub\x.go`,
})

// TestPhysicalPathWindowsShapes pins the resolver on Windows-shaped input on
// every OS: a short name expands, backslashes and the drive letter
// normalize, and a file that does not exist yet resolves through its
// deepest existing ancestor.
func TestPhysicalPathWindowsShapes(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"C:/Users/RUNNER~1/Temp/repo/a.go":     "C:/Users/runneradmin/Temp/repo/a.go",
		"C:/Users/RUNNER~1/Temp/repo/new/x.go": "C:/Users/runneradmin/Temp/repo/new/x.go",
		"c:/users/runneradmin/temp/repo/a.go":  "C:/Users/runneradmin/Temp/repo/a.go",
		"D:/nowhere/at/all.go":                 "",
	} {
		got, ok := physicalPath(in, true, windowsFS)
		if got != want || ok != (want != "") {
			t.Errorf("physicalPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// windowsServer is a server forced into Windows mode over one root spelled
// with the short name, as a runner's temp directory is.
func windowsServer(t *testing.T) (*Server, *servedRoot) {
	t.Helper()
	s := New(newConfig())
	s.windows = true
	s.cfg.eval = windowsFS
	root := newServedRoot(`C:\Users\RUNNER~1\Temp\repo`, true)
	phys, ok := physicalPath(root.slash, true, windowsFS)
	if !ok {
		t.Fatal("the root did not resolve")
	}
	root.physical = phys
	s.roots = []servedRoot{root}
	return s, &s.roots[0]
}

// TestResolveURIAcrossSpellings pins document confinement on Windows-shaped
// spellings: the long name, a lower-cased drive and path, the short name, and
// an encoded colon all reach the same file under the short-named root and
// share its key; a sibling directory and a remote authority do not.
func TestResolveURIAcrossSpellings(t *testing.T) {
	t.Parallel()
	s, root := windowsServer(t)
	want := root.fileKey("a.go")
	for _, uri := range []string{
		"file:///C:/Users/RUNNER~1/Temp/repo/a.go",
		"file:///C:/Users/runneradmin/Temp/repo/a.go",
		"file:///c:/users/runneradmin/temp/repo/a.go",
		"file:///c%3A/Users/runneradmin/Temp/repo/a.go",
		"file:///C:%5CUsers%5Crunneradmin%5CTemp%5Crepo%5Ca.go",
	} {
		ref, ok := s.resolveURI(uri)
		if !ok || len(ref.roots) != 1 || ref.key != want {
			t.Errorf("resolveURI(%s) = %q, %v; want key %q", uri, ref.key, ok, want)
		}
	}
	for _, uri := range []string{
		"file:///C:/Users/runneradmin/Temp/sibling/b.go",
		"file://C:%5CUsers%5Crunneradmin%5CTemp%5Crepo%5Ca.go",
		"file://server/share/repo/a.go",
	} {
		if ref, _ := s.resolveURI(uri); len(ref.roots) != 0 {
			t.Errorf("resolveURI(%s) confined under the root as %q", uri, ref.key)
		}
	}
}

// TestLongNameDocumentGetsShortRootFindings pins the diagnostics side: a
// document the client spells with the long name receives the findings the
// short-named root holds for it.
func TestLongNameDocumentGetsShortRootFindings(t *testing.T) {
	t.Parallel()
	s, root := windowsServer(t)
	f, _ := joinFinding(&record{Sig: sigCrit, Status: "open", Severity: "critical", Path: "a.go", Line: 1}, nil)
	s.states[root.ID] = &bundleState{
		bundle: "local-1", byPath: map[string][]int{root.pathKey("a.go"): {0}},
		paths: map[string]string{root.pathKey("a.go"): "a.go"}, items: []finding{f},
	}
	uri := "file:///c:/users/runneradmin/temp/repo/a.go"
	doc := &document{uri: uri, physical: s.physicalOfURI(uri)}
	if got := s.fileDiagnostics(uri, doc); len(got) != 1 || got[0].Code != sigCrit {
		t.Fatalf("long-name document diagnostics = %+v", got)
	}
}

// TestDocumentThroughSymlinkedSpelling reproduces the same bug class on the
// host OS: the root is served through a symlinked spelling (as /var is for
// /private/var on macOS), the client opens the file through the physical
// spelling, and it still receives its findings under its own URI, with the
// root's spelling of the file cleared, in both directions.
func TestDocumentThroughSymlinkedSpelling(t *testing.T) {
	t.Parallel()
	target := fixtureRoot(t)
	link := filepath.Join(t.TempDir(), "link")
	symlink(t, target, link)
	physical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	// The third row is macOS's own case with no link of ours: a temp root
	// served as /var/folders/... and opened as /private/var/folders/... (on
	// Linux both spell /tmp and the row is lexical).
	for _, tc := range []struct{ served, opened string }{{link, physical}, {physical, link}, {target, physical}} {
		checkSpelling(t, tc.served, tc.opened)
	}
}

// checkSpelling serves one spelling of a root and opens notes.txt through
// another: the document gets its findings under its own URI, and the root's
// spelling (when different) is cleared.
func checkSpelling(t *testing.T, served, openedRoot string) {
	t.Helper()
	s := startSession(t, newConfig(served))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	opened := fileURI(openedRoot, "notes.txt")
	openDoc(s, opened, 1, notesText)
	got := summarize(publishes(t, s.barrier(3, mark)))
	want := opened + " v1 " + sigCrit
	if rootSpelled := fileURI(served, "notes.txt"); rootSpelled != opened {
		want = rootSpelled + " v- \n" + want
	}
	if got != want {
		t.Fatalf("served %s, opened %s:\n%s\nwant\n%s", served, openedRoot, got, want)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}
