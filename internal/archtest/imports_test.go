// Package archtest holds repository-wide architecture guards. The tests read
// the package graph with `go list` and fail when a layering boundary is
// crossed; they build no production code.
package archtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modulePath prefixes every package in this repository.
const modulePath = "github.com/stainless-code/verdict/"

// forbiddenDeps are the transport, adapter, and verb packages the core domain
// and the MCP transport must never reach, directly or transitively. A
// dependency matches on an exact path, on the historical suffix form, or on a
// subpackage prefix.
var forbiddenDeps = []string{
	"internal/cli",
	"internal/mcp",
	"internal/schemagen",
	"internal/golden",
	"internal/readgolden",
	"cmd/verdict",
	"internal/report",
	"internal/record",
	"internal/fetch",
	"internal/verify",
	"internal/triage",
	"internal/adopt",
	"internal/fix",
	"internal/gate",
	"internal/learn",
	"internal/ready",
	"internal/threads",
	"internal/trend",
	"internal/calibrate",
	"internal/doctor",
	"internal/checktree",
	"internal/scaffold",
}

// facadeAllowedDeps is the exact facade dependency set: the core domain
// services and the pure helpers they share. It is an allowlist, so a new
// import fails the guard until a reviewer adds it deliberately; the
// distribution surfaces (internal/core/upgrade, internal/core/skill) and every
// CLI/transport adapter are absent by design.
var facadeAllowedDeps = map[string]bool{
	"internal/atomic":         true,
	"internal/brief":          true,
	"internal/controls":       true,
	"internal/core":           true,
	"internal/core/adopt":     true,
	"internal/core/calibrate": true,
	"internal/core/checktree": true,
	"internal/core/corpus":    true,
	"internal/core/doctor":    true,
	"internal/core/exec":      true,
	"internal/core/fetch":     true,
	"internal/core/fix":       true,
	"internal/core/init":      true,
	"internal/core/learn":     true,
	"internal/core/post":      true,
	"internal/core/ready":     true,
	"internal/core/record":    true,
	"internal/core/report":    true,
	"internal/core/threads":   true,
	"internal/core/trend":     true,
	"internal/core/triage":    true,
	"internal/core/verify":    true,
	"internal/diagram":        true,
	"internal/envsecret":      true,
	"internal/hunks":          true,
	"internal/identity":       true,
	"internal/jq":             true,
	"internal/kb":             true,
	"internal/overrides":      true,
	"internal/provenance":     true,
	"internal/readcommon":     true,
	"internal/retry":          true,
	"internal/sig":            true,
	"pkg/verdict":             true,
}

// coreSubpackages classifies every package under internal/core. A new package
// must be added here deliberately, so the core boundary is never widened
// unnoticed.
var coreSubpackages = []string{
	"adopt", "calibrate", "checktree", "corpus", "doctor", "exec", "fetch", "fix",
	"init", "learn", "post", "ready", "record", "report", "skill", "threads",
	"trend", "triage", "upgrade", "verify",
}

// readcommonDeniedCoreDeps names every core service package the shared store
// helpers must not import. internal/core/exec is the process-runner primitive
// the helper layer legitimately shares; every classified subpackage other than
// it is a verb service.
var readcommonDeniedCoreDeps = func() []string {
	deny := make([]string, 0, len(coreSubpackages))
	for _, name := range coreSubpackages {
		if name != "exec" {
			deny = append(deny, "internal/core/"+name)
		}
	}
	return deny
}()

// boundary is one row of the import guard: every repository package matching
// pattern is checked against deny (a denylist) or allow (an exact allowlist).
// Exactly one of deny and allow is set.
type boundary struct {
	allow   map[string]bool
	name    string
	pattern string
	deny    []string
}

// boundaries declare the repository's layering guarantees.
var boundaries = []boundary{
	{
		name:    "core",
		pattern: "./internal/core/...",
		deny:    forbiddenDeps,
	},
	{
		name:    "mcp",
		pattern: "./internal/mcp",
		deny:    forbiddenDeps,
	},
	{
		// core <- readcommon <- core/<verb>: the root contract package never
		// reaches up into the shared store helpers.
		name:    "core root",
		pattern: "./internal/core",
		deny:    []string{"internal/readcommon"},
	},
	{
		// The helper layer depends on the contract root and the process
		// runner only; reaching into a verb service would invert the DAG.
		name:    "readcommon",
		pattern: "./internal/readcommon",
		deny:    readcommonDeniedCoreDeps,
	},
	{
		name:    "facade",
		pattern: "./pkg/verdict",
		allow:   facadeAllowedDeps,
	},
}

// listedPackage is the subset of `go list -json` the guard reads.
type listedPackage struct {
	ImportPath string   `json:"ImportPath"`
	Deps       []string `json:"Deps"`
}

// TestImportBoundaries is the repository's single import-boundary guard. One
// `go list -deps -json ./...` describes the whole package graph; every row
// checks its packages against that graph, and the coverage checks prove no
// package escapes a row.
func TestImportBoundaries(t *testing.T) {
	repo := loadRepoGraph(t)
	for _, b := range boundaries {
		t.Run(b.name, func(t *testing.T) {
			b.check(t, repo)
		})
	}
	t.Run("core coverage", func(t *testing.T) {
		checkCoreCoverage(t, repo)
	})
	t.Run("verb adapter coverage", func(t *testing.T) {
		checkVerbAdapterCoverage(t, repo)
	})
}

// loadRepoGraph runs exactly one `go list -deps -json ./...` from the module
// root and returns the repository packages keyed by module-relative path.
func loadRepoGraph(t *testing.T) map[string]listedPackage {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-json", "./...")
	cmd.Dir = moduleRoot(t)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps -json ./...: %v\n%s", err, stderr.String())
	}
	repo := decodeRepoPackages(t, &stdout)
	if len(repo) == 0 {
		t.Fatal("go list returned no repository packages")
	}
	return repo
}

// decodeRepoPackages decodes the JSON stream and keeps the repository packages,
// keyed by module-relative path; standard library and third-party packages are
// dropped.
func decodeRepoPackages(t *testing.T, r io.Reader) map[string]listedPackage {
	t.Helper()
	repo := make(map[string]listedPackage)
	dec := json.NewDecoder(r)
	for {
		pkg, ok := nextListedPackage(t, dec)
		if !ok {
			return repo
		}
		rel, ok := strings.CutPrefix(pkg.ImportPath, modulePath)
		if !ok {
			continue
		}
		repo[rel] = pkg
	}
}

// nextListedPackage decodes one JSON object and reports whether one was read.
func nextListedPackage(t *testing.T, dec *json.Decoder) (listedPackage, bool) {
	t.Helper()
	var pkg listedPackage
	err := dec.Decode(&pkg)
	if errors.Is(err, io.EOF) {
		return listedPackage{}, false
	}
	if err != nil {
		t.Fatalf("decode go list output: %v", err)
	}
	return pkg, true
}

// moduleRoot walks up from the test's working directory to the go.mod root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test working directory")
		}
		dir = parent
	}
}

// check verifies every package the row matches against its denylist or
// allowlist.
func (b boundary) check(t *testing.T, repo map[string]listedPackage) {
	t.Helper()
	if (b.deny == nil) == (b.allow == nil) {
		t.Fatalf("boundary %q must set exactly one of deny and allow", b.name)
	}
	matched := 0
	for _, rel := range sortedRepoKeys(repo) {
		if !matchPattern(b.pattern, rel) {
			continue
		}
		matched++
		for _, dep := range repo[rel].Deps {
			b.checkDep(t, rel, dep)
		}
	}
	if matched == 0 {
		t.Fatalf("boundary %q matched no packages", b.name)
	}
}

// checkDep checks one dependency against the row's denylist or allowlist.
func (b boundary) checkDep(t *testing.T, rel, dep string) {
	t.Helper()
	if b.deny != nil {
		if forbiddenDep(dep, b.deny) {
			t.Errorf("%s depends on forbidden package %s", rel, dep)
		}
		return
	}
	depRel, ok := strings.CutPrefix(dep, modulePath)
	if !ok {
		return // standard library and third-party packages
	}
	if !b.allow[depRel] {
		t.Errorf("%s depends on non-allowlisted package %s", rel, dep)
	}
}

// sortedRepoKeys returns the repository package paths in a stable order.
func sortedRepoKeys(repo map[string]listedPackage) []string {
	return slices.Sorted(maps.Keys(repo))
}

// matchPattern supports the subset of go list pattern syntax the guard uses:
// "./dir/..." for a subtree and "./dir" for one package.
func matchPattern(pattern, rel string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	if subtree, ok := strings.CutSuffix(pattern, "/..."); ok {
		return rel == subtree || strings.HasPrefix(rel, subtree+"/")
	}
	return rel == pattern
}

// forbiddenDep reports whether dep is denied. The suffix form preserves the
// historical guards; the module-prefix form additionally covers subpackages of
// a denied package, which is stricter than the suffix form alone.
func forbiddenDep(dep string, deny []string) bool {
	for _, prefix := range deny {
		if dep == prefix || strings.HasSuffix(dep, "/"+prefix) {
			return true
		}
		if strings.HasPrefix(dep, modulePath+prefix+"/") {
			return true
		}
	}
	return false
}

// checkCoreCoverage proves every internal/core package is classified: a new
// subpackage fails until it is named, and a stale name fails too.
func checkCoreCoverage(t *testing.T, repo map[string]listedPackage) {
	t.Helper()
	classified := classifyCore(t, repo)
	for _, rel := range sortedRepoKeys(repo) {
		sub, ok := strings.CutPrefix(rel, "internal/core/")
		if ok && !classified[sub] {
			t.Errorf("core package %s is not classified in coreSubpackages", rel)
		}
	}
	if _, ok := repo["internal/core"]; !ok {
		t.Error("internal/core is not listed by go list")
	}
}

// classifyCore checks the classification list against the graph and returns it
// as a set.
func classifyCore(t *testing.T, repo map[string]listedPackage) map[string]bool {
	t.Helper()
	classified := make(map[string]bool, len(coreSubpackages))
	for _, name := range coreSubpackages {
		if classified[name] {
			t.Errorf("core subpackage %q is classified more than once", name)
		}
		classified[name] = true
		if _, ok := repo["internal/core/"+name]; !ok {
			t.Errorf("classified core subpackage internal/core/%s is not listed by go list", name)
		}
	}
	return classified
}

// checkVerbAdapterCoverage keeps the denylist in step with the verb adapters
// under internal/: a thin internal/<name> adapter that wraps a same-named core
// service must be forbidden to core. It names every unclassified adapter
// rather than stopping at the first.
func checkVerbAdapterCoverage(t *testing.T, repo map[string]listedPackage) {
	t.Helper()
	for _, rel := range sortedRepoKeys(repo) {
		name, ok := verbAdapterName(rel, repo)
		if !ok {
			continue
		}
		if !slices.Contains(forbiddenDeps, "internal/"+name) {
			t.Errorf("verb adapter internal/%s is not covered by forbiddenDeps", name)
		}
	}
}

// verbAdapterName reports whether rel is a thin internal/<name> adapter with a
// same-named core service.
func verbAdapterName(rel string, repo map[string]listedPackage) (string, bool) {
	name, ok := strings.CutPrefix(rel, "internal/")
	if !ok || strings.Contains(name, "/") {
		return "", false // only immediate internal/<name> packages
	}
	if _, ok := repo["internal/core/"+name]; !ok {
		return "", false // no same-named core service
	}
	return name, true
}
