package record_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextSourcesPersistOutsideSignature(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	review := strings.Replace(kbReview(""), `"rigor":"walked"`, `"sources":["docs/auth.md"],"rigor":"walked"`, 1)
	kbBundle(t, dir, review, nil)
	first := runOne(t, dir, home, "record", "acme/app", "local-kb")
	if first.Status != 0 {
		t.Fatalf("record: %s", first.Stderr)
	}
	if !strings.Contains(first.Stderr, "unknown context source: docs/auth.md") {
		t.Fatalf("missing warning: %s", first.Stderr)
	}
	before := contextStoredFinding(t, dir)
	if strings.Join(before.Sources, ",") != "docs/auth.md" {
		t.Fatalf("sources = %v", before.Sources)
	}
	writeFiles(t, dir, map[string]string{".verdict/state/local-kb/review.json": kbReview("")})
	second := runOne(t, dir, home, "record", "acme/app", "local-kb")
	if second.Status != 0 {
		t.Fatalf("record again: %s", second.Stderr)
	}
	after := contextStoredFinding(t, dir)
	if before.Sig != after.Sig {
		t.Fatalf("sources changed signature: %s != %s", before.Sig, after.Sig)
	}
}

type contextFinding struct {
	Sig     string   `json:"sig"`
	Sources []string `json:"sources"`
}

func contextStoredFinding(t *testing.T, dir string) contextFinding {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict/state/local-kb/findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []contextFinding
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	return entries[0]
}
