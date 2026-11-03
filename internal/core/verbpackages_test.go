package core_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// coreSubpackages are the service and infrastructure packages the core
// exposes. Each one must stay reachable from the import-direction guard:
// adding a package here makes the guard fail if it ever imports a CLI
// adapter or another transport.
var coreSubpackages = []string{
	"./adopt", "./calibrate", "./checktree", "./corpus", "./doctor", "./exec", "./fetch", "./fix",
	"./init", "./learn", "./post", "./ready", "./record", "./report", "./skill", "./threads",
	"./trend", "./triage", "./upgrade", "./verify",
}

// TestCoreSubpackageImportDirection proves every subpackage under
// internal/core is listed by `go list ./...` (so the package-wide guard
// covers it) and never reaches a transport, adapter, or the schema
// generator.
func TestCoreSubpackageImportDirection(t *testing.T) {
	listed := listedCorePackages(t)
	for _, pkg := range coreSubpackages {
		if !slices.Contains(listed, pkg) {
			t.Errorf("core subpackage %s is missing from `go list ./...`", pkg)
		}
	}
	for _, pkg := range coreSubpackages {
		scanSubpackageDeps(t, pkg)
	}
}

// listedCorePackages returns the relative package paths `go list ./...`
// reports for the core tree.
func listedCorePackages(t *testing.T) []string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "go", "list", "-f", "{{.ImportPath}}", "./...")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var rel []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if _, after, ok := strings.CutLast(line, "/internal/core"); ok {
			rel = append(rel, "."+after)
		}
	}
	return rel
}

// scanSubpackageDeps fails when one core subpackage depends on a forbidden
// transport or adapter package.
func scanSubpackageDeps(t *testing.T, pkg string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{.ImportPath}}", pkg)
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		for _, forbidden := range forbiddenDeps {
			if strings.HasSuffix(dep, forbidden) {
				t.Errorf("%s imports forbidden package %q", pkg, dep)
			}
		}
	}
}

// TestCoreSubpackagesExist guards against the list drifting away from the
// tree: every immediate core subdirectory with Go sources must be named by
// coreSubpackages.
//
//nolint:revive // one pass over the directory tree reads clearer than a helper
func TestCoreSubpackagesExist(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !hasGoSources(t, e.Name()) {
			continue
		}
		if !slices.Contains(coreSubpackages, "./"+e.Name()) {
			t.Errorf("core package %s is not covered by coreSubpackages", e.Name())
		}
	}
}

// hasGoSources reports whether dir holds a non-test Go file.
func hasGoSources(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(".", dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}
