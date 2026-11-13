package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
)

func TestRootAttributeInventoryRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeTrustedConfig(t, root, `{"schema_version":"1"}`)
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.go linguist-generated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".verdict/state/7")
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: dir, Root: root, BaseSHA: "base", AllowHead: true})
	if ref != nil {
		t.Fatal(ref)
	}
	rs := &RunState{Inventory: InventoryHashes(snap)}
	if ref := verifyInventory(dir, rs, snap); ref != nil {
		t.Fatalf("unchanged root attribute refused: %v", ref)
	}
}
