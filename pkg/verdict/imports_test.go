package verdict_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDeps are the transport and verb-adapter packages the public
// facade must never reach. pkg/verdict may depend on the core domain
// services and the order-preserving JSON layer only; adapters own argv,
// streams, and exit mapping.
var forbiddenDeps = []string{
	"/internal/cli",
	"/internal/mcp",
	"/internal/schemagen",
	"/internal/parity",
	"/internal/readparity",
	"/cmd/verdict",
	"/cmd/schemagen",
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
	"/internal/install",
}

// TestFacadeImportDirection proves `go list -deps ./pkg/verdict` never
// reaches a transport, verb adapter, or the schema generator.
func TestFacadeImportDirection(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		for _, forbidden := range forbiddenDeps {
			if strings.HasSuffix(dep, forbidden) {
				t.Errorf("pkg/verdict imports forbidden package %q", dep)
			}
		}
	}
}
