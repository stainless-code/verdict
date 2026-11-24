package postcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
)

func TestPostRefusesUnsyncedOrgPin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sha := strings.Repeat("a", 40)
	cfg := `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"` + sha + `"}}}`
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".verdict", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: t.TempDir(), AllowHead: true, Root: root})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	ref2 := packcore.CheckLock(snap)
	if ref2 == nil || ref2.Code != packcore.CodeLockMissing {
		t.Fatalf("CheckLock = %v, want pack-lock-missing", ref2)
	}
}
