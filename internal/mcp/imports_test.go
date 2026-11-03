package mcp_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// forbiddenMCPDeps are the outward packages the transport must never
// reach: the CLI adapter, the binary, the schema generator, the parity
// harness, and every verb package. mcp depends on internal/core (services
// and the injected runtime) only.
var forbiddenMCPDeps = []string{
	"/internal/cli",
	"/internal/schemagen",
	"/internal/parity",
	"/internal/readparity",
	"/cmd/verdict",
	"/internal/report",
	"/internal/record",
	"/internal/fetch",
	"/internal/verify",
	"/internal/triage",
	"/internal/adopt",
	"/internal/fix",
	"/internal/learn",
	"/internal/ready",
	"/internal/trend",
	"/internal/calibrate",
	"/internal/doctor",
	"/internal/checktree",
	"/internal/scaffold",
}

// TestImportDirection proves internal/mcp is a thin inbound adapter: its
// dependency graph never reaches a CLI/verb package or the binary. The core
// guard (internal/core/imports_test.go) proves the reverse: core never
// imports mcp.
func TestImportDirection(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		for _, forbidden := range forbiddenMCPDeps {
			if strings.HasSuffix(dep, forbidden) {
				t.Errorf("internal/mcp imports forbidden package %q", dep)
			}
		}
	}
}
