package verdict_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// modulePath prefixes every package in this repository.
const modulePath = "github.com/stainless-code/verdict/"

// allowedDeps is the exact facade dependency set: the core domain services
// and the pure helpers they share. It is an allowlist, so a new import fails
// the guard until a reviewer adds it deliberately; the distribution surfaces
// (internal/core/upgrade, internal/core/skill) and every CLI/transport
// adapter are absent by design.
var allowedDeps = map[string]bool{
	"internal/atomic":         true,
	"internal/bundle":         true,
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
	"internal/core/trend":     true,
	"internal/core/triage":    true,
	"internal/core/verify":    true,
	"internal/diagram":        true,
	"internal/hunks":          true,
	"internal/jq":             true,
	"internal/kb":             true,
	"internal/overrides":      true,
	"internal/provenance":     true,
	"internal/readcommon":     true,
	"internal/retry":          true,
	"internal/sig":            true,
	"pkg/verdict":             true,
}

// TestFacadeImportDirection proves `go list -deps ./pkg/verdict` stays on
// the allowlist, so the public facade never reaches a transport, CLI adapter,
// schema generator, or the CLI-only distribution surfaces.
func TestFacadeImportDirection(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		rel, ok := strings.CutPrefix(dep, modulePath)
		if !ok {
			continue // standard library and third-party packages
		}
		if !allowedDeps[rel] {
			t.Errorf("pkg/verdict imports non-allowlisted package %q", dep)
		}
	}
}
