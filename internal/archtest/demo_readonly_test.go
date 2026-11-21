package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// demoPackages are the two packages the read-only promise covers: the demo
// domain service and its CLI adapter.
var demoPackages = []string{"./internal/core/demo", "./internal/demo"}

// demoPublishDeps are the packages a demo must never import directly: the
// publish path and the provider write client it drives. A demo that imported
// post could publish a review, which is the one thing the verb promises it
// cannot do.
var demoPublishDeps = []string{"internal/core/post", "internal/post", "internal/forgejo"}

// demoPostSeam is the one transitive door through which the publish package
// already reaches a demo: `internal/core/fetch` imports it for
// `threads --resolve` (a host write the fetch verb performs), which predates
// this verb and is not the demo's to remove. Pinning the exact importer set is
// what keeps the promise checkable: a second door fails here.
const demoPostSeam = "internal/core/fetch"

// TestDemoReadOnlyDeps proves the structural half of the read-only promise:
// neither demo package imports the publish path, and the only package in their
// transitive graph that does is the pre-existing fetch thread-resolution seam.
func TestDemoReadOnlyDeps(t *testing.T) {
	t.Parallel()
	graph := demoGraph(t, moduleRoot(t))
	for _, rel := range demoGraphPackages() {
		assertDemoDirectImports(t, graph, rel)
	}
	importers := demoPostImporters(graph)
	if !slices.Equal(importers, []string{demoPostSeam}) {
		t.Errorf("packages importing internal/core/post inside the demo graph = %v, want only %s "+
			"(a new door to the publish path is a design change)", importers, demoPostSeam)
	}
}

// demoGraphPackages are the demo packages, module-relative.
func demoGraphPackages() []string { return []string{"internal/core/demo", "internal/demo"} }

// assertDemoDirectImports proves one demo package imports neither the publish
// path nor anything under it, and still reaches the services it composes: a
// graph that lost those dependencies is not the shipped graph.
func assertDemoDirectImports(t *testing.T, graph map[string]demoPkg, rel string) {
	t.Helper()
	pkg, ok := graph[rel]
	if !ok {
		t.Fatalf("%s is not in the dependency graph", rel)
	}
	for _, imp := range pkg.Imports {
		if path := demoPublishDep(imp); path != "" {
			t.Errorf("%s imports %s (a demo may never publish)", rel, path)
		}
	}
	for _, want := range []string{"internal/core/fetch", "internal/core/record", "internal/core/run"} {
		if !slices.Contains(pkg.Deps, modulePath+want) {
			t.Fatalf("%s does not depend on %s; the graph read is not the shipped one", rel, want)
		}
	}
}

// demoPostImporters lists the demo-graph packages that import the publish path.
func demoPostImporters(graph map[string]demoPkg) []string {
	var out []string
	for rel, pkg := range graph {
		if slices.Contains(pkg.Imports, modulePath+"internal/core/post") {
			out = append(out, rel)
		}
	}
	slices.Sort(out)
	return out
}

// demoPublishDep reports the forbidden package a direct import names, matching
// the package and every subpackage under it.
func demoPublishDep(dep string) string {
	rel, ok := strings.CutPrefix(dep, modulePath)
	if !ok {
		return ""
	}
	for _, banned := range demoPublishDeps {
		if rel == banned || strings.HasPrefix(rel, banned+"/") {
			return rel
		}
	}
	return ""
}

// TestDemoPublishDepMatcher proves the matcher itself: the package, its
// subpackages, and the sibling write client all match, while the reading
// packages the demo legitimately imports do not. A guard whose matcher cannot
// match is a guard that always passes.
func TestDemoPublishDepMatcher(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dep  string
		want string
	}{
		{modulePath + "internal/core/post", "internal/core/post"},
		{modulePath + "internal/core/post/payload", "internal/core/post/payload"},
		{modulePath + "internal/forgejo", "internal/forgejo"},
		{modulePath + "internal/core/record", ""},
		{modulePath + "internal/core/fetch", ""},
		{"net/http", ""},
	} {
		if got := demoPublishDep(tc.dep); got != tc.want {
			t.Errorf("demoPublishDep(%q) = %q, want %q", tc.dep, got, tc.want)
		}
	}
}

// TestDemoHasNoPublishSymbols proves the second half: neither demo package
// names a publish verb. Comments and strings are not identifiers, so a prose
// mention of posting can neither pass nor fail this check.
func TestDemoHasNoPublishSymbols(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	files := 0
	for _, pkg := range demoPackages {
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
		for _, name := range goFileNames(t, dir) {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			files++
			path := filepath.Join(dir, name)
			assertNoPublishSymbols(t, path, parseGo(t, path))
		}
	}
	if files == 0 {
		t.Fatal("no demo sources were read; the guard would pass vacuously")
	}
}

// publishAliases are the package identifiers a publish call would come through.
var publishAliases = map[string]bool{"post": true, "postcore": true, "forgejo": true}

// assertNoPublishSymbols fails on a publish-package selector or a submit/apply
// call in one file.
func assertNoPublishSymbols(t *testing.T, path string, file *ast.File) {
	t.Helper()
	ast.Inspect(file, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok {
			assertNoPublishSelector(t, path, selector)
		}
		if call, ok := n.(*ast.CallExpr); ok {
			assertNoPublishCall(t, path, call)
		}
		return true
	})
}

// assertNoPublishSelector fails on a selector read through a publish alias.
func assertNoPublishSelector(t *testing.T, path string, selector *ast.SelectorExpr) {
	t.Helper()
	ident, ok := selector.X.(*ast.Ident)
	if !ok || !publishAliases[ident.Name] {
		return
	}
	t.Errorf("%s references %s.%s", filepath.Base(path), ident.Name, selector.Sel.Name)
}

// assertNoPublishCall fails on a submit or apply call, the two publish verbs.
func assertNoPublishCall(t *testing.T, path string, call *ast.CallExpr) {
	t.Helper()
	if name := calleeName(call.Fun); name == "Submit" || name == "Apply" {
		t.Errorf("%s calls %s (a publish-path symbol)", filepath.Base(path), name)
	}
}

// demoPkg is the subset of `go list -json` these guards read.
type demoPkg struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
	Deps       []string `json:"Deps"`
}

// demoGraph reads the two demo packages' dependency graph from go list, keyed
// by module-relative path.
func demoGraph(t *testing.T, root string) map[string]demoPkg {
	t.Helper()
	args := append([]string{"list", "-deps", "-json"}, demoPackages...)
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A child that leaks its pipes must not hang the suite: WaitDelay closes
	// them and reports the spawn error instead.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps -json: %v\n%s", err, stderr.String())
	}
	graph := decodeDemoGraph(t, &stdout)
	if len(graph) == 0 {
		t.Fatal("go list returned no repository packages")
	}
	return graph
}

// decodeDemoGraph keyes the go list stream by module-relative path, dropping
// the standard library and third-party packages.
func decodeDemoGraph(t *testing.T, r io.Reader) map[string]demoPkg {
	t.Helper()
	graph := make(map[string]demoPkg)
	dec := json.NewDecoder(r)
	for {
		pkg, ok := nextDemoPkg(t, dec)
		if !ok {
			return graph
		}
		if rel, cut := strings.CutPrefix(pkg.ImportPath, modulePath); cut {
			graph[rel] = pkg
		}
	}
}

// nextDemoPkg decodes one go list object, reporting whether one was read.
func nextDemoPkg(t *testing.T, dec *json.Decoder) (demoPkg, bool) {
	t.Helper()
	var pkg demoPkg
	err := dec.Decode(&pkg)
	if errors.Is(err, io.EOF) {
		return demoPkg{}, false
	}
	if err != nil {
		t.Fatalf("decode go list output: %v", err)
	}
	return pkg, true
}
