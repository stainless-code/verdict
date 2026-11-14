package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/schemagen"
)

// TestCheckRefusesAStaleBaseline reproduces what this check exists for: a
// baseline that lags the generator by additions alone. Main's committed
// envelope carried one of the four provenanceReceipt bindings, and nothing
// failed, because the additivity test only reports what a baseline still
// promises — an added field is allowed by definition.
func TestCheckRefusesAStaleBaseline(t *testing.T) {
	root := freshTree(t)
	t.Chdir(root)
	assertRun(t, 0, "")
	dropReceiptBinding(t, filepath.Join(root, baselineDir, "envelope.schema.json"))
	assertRun(t, 1, "schemagen: internal/schemagen/testdata/baseline is stale; "+
		"run `make schemas-baseline` from the repo root")
}

// TestCheckAcceptsTheCommittedSchemas runs the same check `make schemas-check`
// runs against the committed tree, so a baseline left behind fails here too.
func TestCheckAcceptsTheCommittedSchemas(t *testing.T) {
	assertRun(t, 0, "")
}

// freshTree writes the generated schemas, unmodified, into both committed
// locations under a throwaway repository root.
func freshTree(t *testing.T) string {
	t.Helper()
	files, err := schemagen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/stale\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, rel := range []string{shippedDir, baselineDir} {
		if err := writeAll(filepath.Join(root, rel), files); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// dropReceiptBinding removes one additive binding from a baseline document,
// the shape the report carried: the additivity comparison cannot see the loss.
func dropReceiptBinding(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	binding := []byte("        \"receipt\": { \"$ref\": \"#/$defs/provenanceReceipt\" },\n")
	if !bytes.Contains(raw, binding) {
		t.Fatalf("baseline envelope carries no provenanceReceipt binding:\n%s", raw)
	}
	if err := os.WriteFile(path, bytes.Replace(raw, binding, nil, 1), 0o644); err != nil {
		t.Fatalf("rewrite baseline: %v", err)
	}
}

// assertRun runs the checker and requires one exit code plus one stderr line.
func assertRun(t *testing.T, wantCode int, wantStderr string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != wantCode {
		t.Errorf("exit = %d; want %d (stdout %q, stderr %q)", code, wantCode, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(stderr.String()); got != wantStderr {
		t.Errorf("stderr = %q; want %q", got, wantStderr)
	}
}
