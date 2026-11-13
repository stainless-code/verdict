package learncore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/kb"
)

// TestPackImport pins metadata, reproducibility, and duplicate refusal.
func TestPackImport(t *testing.T) {
	dir := importFixture(t)
	run := func(root, id string) *core.RefusalError {
		t.Helper()
		req := &Request{Pack: dir, ID: id, Rigor: "pointed", JSONMode: true}
		req.RepoRoot = root
		_, ref := Run(t.Context(), req)
		return ref
	}
	root := t.TempDir()
	if ref := run(root, "safe-rule"); ref != nil {
		t.Fatal(ref)
	}
	raw, err := os.ReadFile(filepath.Join(root, kb.File))
	if err != nil {
		t.Fatal(err)
	}
	checkImportedMetadata(t, raw)
	other := t.TempDir()
	if ref := run(other, "safe-rule"); ref != nil {
		t.Fatal(ref)
	}
	again, err := os.ReadFile(filepath.Join(other, kb.File))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, again) {
		t.Fatalf("import not reproducible: %s != %s", raw, again)
	}
	if ref := run(root, "safe-rule"); ref == nil || ref.Code != "already-learned" {
		t.Fatalf("duplicate: %v", ref)
	}
}

func importFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"pack.json":          `{"schema_version":"1","name":"test/rules","version":"1.0.0","severity":{"must":"important"},"rules":[{"id":"safe-rule","severity":"must","globs":["*.go"],"category":"safety","description":"Check errors","examples":["return err"]}]}`,
		"rules/safe-rule.md": "Check errors",
	} {
		file := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func checkImportedMetadata(t *testing.T, raw []byte) {
	t.Helper()
	for _, literal := range []string{`"category":"safety"`, `"description":"Check errors"`, `"examples":["return err"]`, `"severity":"important"`, `"provenance":"pack:test/rules@1.0.0"`, `"hits":0`, `"pack_hash":`} {
		if !strings.Contains(string(raw), literal) {
			t.Fatalf("missing %s in %s", literal, raw)
		}
	}
}

// TestPackImportRequiresMetadata rejects incomplete objects.
func TestPackImportRequiresMetadata(t *testing.T) {
	st := &state{req: &Request{}}
	ref := st.importRule(&pack.Pack{}, &pack.Rule{})
	if ref == nil || ref.Message != "import requires category, description and examples" {
		t.Fatalf("missing metadata: %v", ref)
	}
}

// TestPackImportOwnsProvenance prevents operator-supplied provenance overrides.
func TestPackImportOwnsProvenance(t *testing.T) {
	st := &state{req: &Request{Provenance: "forged"}}
	ref := st.importRule(&pack.Pack{}, &pack.Rule{Category: "safety", Description: "Check errors", Examples: []string{"return err"}})
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("forged provenance: %v", ref)
	}
}

// TestPackImportUnknownID refuses before any KB write.
func TestPackImportUnknownID(t *testing.T) {
	dir := importFixture(t)
	root := t.TempDir()
	run := func(root, id string) *core.RefusalError {
		req := &Request{Pack: dir, ID: id}
		req.RepoRoot = root
		_, ref := Run(t.Context(), req)
		return ref
	}
	if ref := run(root, "unknown"); ref == nil || ref.Code != "unknown-rule" {
		t.Fatalf("unknown ID: %v", ref)
	}
	if _, err := os.Stat(filepath.Join(root, kb.File)); !os.IsNotExist(err) {
		t.Fatalf("unknown ID wrote store: %v", err)
	}
}
