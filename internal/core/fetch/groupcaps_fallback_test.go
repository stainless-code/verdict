package fetchcore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// TestUnattributedSectionCannotBypassKnownCap checks mixed attribution.
func TestUnattributedSectionCannotBypassKnownCap(t *testing.T) {
	limit := int64(10)
	resolved, ref := config.Resolve(&config.Inputs{
		Repo: &core.Config{SchemaVersion: "1", Scopes: []core.ConfigScope{{
			Name: "small", Include: []string{"small/**"}, Defaults: &core.ConfigDefaults{MaxBytes: &limit},
		}}},
		Files: []config.FileInput{{Path: "small/a.txt"}},
	})
	if ref != nil {
		t.Fatal(ref)
	}
	diff := "diff --git a/small/a.txt b/small/a.txt\n--- a/small/a.txt\n+++ b/small/a.txt\n@@ -1 +1 @@\n-old\n+new\n" +
		"diff --git a/other/b.txt b/other/b.txt\n--- a/other/b.txt\n+++ b/other/b.txt\n@@ -1 +1 @@\n-old\n+new\n"
	path := filepath.Join(t.TempDir(), "full.diff")
	if err := os.WriteFile(path, []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := checkDiffSize(&session{Human: io.Discard, Diag: io.Discard}, resolved, path,
		fetchCap{maxBytes: DefaultDiffCap}, false)
	var refusal *core.RefusalError
	if !errors.As(err, &refusal) || refusal.Code != "diff-too-large" {
		t.Fatalf("known group's 10-byte cap: got %v; want diff-too-large", err)
	}
}
