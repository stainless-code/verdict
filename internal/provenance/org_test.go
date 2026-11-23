package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// seedOrgBundle writes a controls snapshot carrying an org pin and its lock
// row, the way fetch leaves it beside the bundle.
func seedOrgBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	controls := filepath.Join(dir, "controls")
	if err := os.MkdirAll(controls, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"schema_version":"1","packs":{"org_pack":{"repo":"https://example.invalid/acme/packs.git","ref":"v1.2.3","path":"packs/org"}}}`
	if err := os.WriteFile(filepath.Join(controls, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://example.invalid/acme/packs.git@` +
		"cccccccccccccccccccccccccccccccccccccccc" +
		`","version":"1.0.0","hash":"abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd","sig":"efefefefefefefefefefefefefefefefefefefefefefefefefefefefefef` + `"}]}`
	if err := os.WriteFile(filepath.Join(controls, "verdict.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// findOrgPack returns the receipt's org_pack row, failing when absent.
func findOrgPack(t *testing.T, r *core.ProvenanceReceipt) core.ProvenanceInput {
	t.Helper()
	for i := range r.Inputs {
		if r.Inputs[i].Kind == "org_pack" {
			return r.Inputs[i]
		}
	}
	t.Fatal("receipt carries no org_pack row")
	return core.ProvenanceInput{}
}

// TestBuildReceiptRecordsOrgPackFromSnapshot proves the governing central
// pack lands in provenance: the pin beside the locked content hash.
func TestBuildReceiptRecordsOrgPackFromSnapshot(t *testing.T) {
	dir := seedOrgBundle(t)
	r := BuildReceipt(&ReceiptBuildInput{Dir: dir})
	row := findOrgPack(t, r)
	if row.Name != "https://example.invalid/acme/packs.git" || row.ID != "v1.2.3" {
		t.Errorf("org_pack row = %q @ %q, want the pinned repo and ref", row.Name, row.ID)
	}
	if row.Status != "included" {
		t.Errorf("org_pack status = %q, want included", row.Status)
	}
	if row.SHA256 != "abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd" {
		t.Errorf("org_pack sha256 = %q, want the locked content hash", row.SHA256)
	}
	for _, want := range []string{"path=packs/org", "sha=cccccccccccccccccccccccccccccccccccccccc", "pack=acme/lenses", "version=1.0.0"} {
		if !strings.Contains(row.Detail, want) {
			t.Errorf("org_pack detail = %q, want it to contain %q", row.Detail, want)
		}
	}
}

// TestBuildReceiptExplicitOrgPack proves an explicit input wins over the
// snapshot: callers that resolved the pin carry it verbatim.
func TestBuildReceiptExplicitOrgPack(t *testing.T) {
	dir := seedOrgBundle(t)
	r := BuildReceipt(&ReceiptBuildInput{
		Dir: dir,
		OrgPack: &OrgPackInput{
			Repo: "central", Ref: "dddddddddddddddddddddddddddddddddddddddd",
			Path: ".", Hash: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
	})
	row := findOrgPack(t, r)
	if row.Name != "central" || row.ID != "dddddddddddddddddddddddddddddddddddddddd" {
		t.Errorf("org_pack row = %q @ %q, want the explicit input", row.Name, row.ID)
	}
}

// TestBuildReceiptWithoutPinKeepsLegacyShape proves a bundle with no pin
// carries no org_pack row, so legacy receipts stay byte-identical.
func TestBuildReceiptWithoutPinKeepsLegacyShape(t *testing.T) {
	r := BuildReceipt(&ReceiptBuildInput{Dir: t.TempDir()})
	for i := range r.Inputs {
		if r.Inputs[i].Kind == "org_pack" {
			t.Fatal("pin-less bundle carries an org_pack row")
		}
	}
}

// TestOrgPackFromBundleWithoutSnapshot proves a missing snapshot degrades to
// no pin rather than refusing.
func TestOrgPackFromBundleWithoutSnapshot(t *testing.T) {
	if pin := OrgPackFromBundle(t.TempDir()); pin.Repo != "" {
		t.Errorf("pin = %+v, want empty", pin)
	}
}
