package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

// seedOrgBundle writes a controls snapshot carrying an org pin and its lock
// row, the way fetch leaves it beside the bundle.
func seedOrgBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "controls")
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"schema_version":"1","packs":{"org_pack":{"repo":"https://example.invalid/acme/packs.git","ref":"v1.2.3","path":"packs/org"}}}`
	if err := os.WriteFile(filepath.Join(snapshot, "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://example.invalid/acme/packs.git@` +
		"cccccccccccccccccccccccccccccccccccccccc" +
		`","version":"1.0.0","hash":"abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd",` +
		`"org_path":"packs/org","org_ref":"v1.2.3","sig":"efefefefefefefefefefefefefefefefefefefefefefefefefefefefefef` + `"}]}`
	if err := os.WriteFile(filepath.Join(snapshot, "verdict.lock"), []byte(lock), 0o644); err != nil {
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

// TestLegacyRunIDIsBitIdenticalWithoutAnOrgPack pins the run id a bundle with
// no governing org pack produces: the org binding must add nothing when no pin
// is active, so run ids written before org packs existed still match.
func TestLegacyRunIDIsBitIdenticalWithoutAnOrgPack(t *testing.T) {
	const ctrlV1 = `{"defaults":{"kb_policy":{"max_chars":120}}}`
	_, dir := seedReceiptFixture(t, ctrlV1)
	r := BuildReceipt(baseBuildInput(dir, ctrlV1))
	// The digest folds prompts_hash, so a deliberate prompt edit moves this pin
	// with it: the org binding still adds nothing when no pin is active.
	if got, want := r.RunID, "4a01f4edad42a9c13f737db578ac1662424cfcacd28b5640f3da5433eebb6515"; got != want {
		t.Fatalf("run_id = %s, want the legacy digest %s", got, want)
	}
}

// TestReadBundleLockRowFoldsHostCase proves the repo match folds the URL
// scheme and host, so an upper-case HOST in the pin still finds the canonical
// row, while the repository path stays case-sensitive.
func TestReadBundleLockRowFoldsHostCase(t *testing.T) {
	dir := seedOrgBundle(t)
	path := filepath.Join(dir, "controls", "verdict.lock")
	row := readBundleLockRow(path, &OrgPackInput{
		Repo: "HTTPS://EXAMPLE.INVALID/acme/packs.git", Ref: "v1.2.3", Path: "packs/org",
	})
	if row.Name != "acme/lenses" || row.Hash != "abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd" {
		t.Fatalf("row = %+v, want the locked acme/lenses row", row)
	}
	miss := readBundleLockRow(path, &OrgPackInput{
		Repo: "https://example.invalid/Acme/packs.git", Ref: "v1.2.3", Path: "packs/org",
	})
	if miss.Name != "" {
		t.Fatalf("row = %+v, want a case-sensitive path to miss", miss)
	}
}

// TestReadBundleLockRowMatchesPathAndRef proves one central repository's rows
// are told apart: the row must carry the pin's own org_path and org_ref, so a
// pin at another path or another tag never reports a sibling row's locked
// hash as its own.
func TestReadBundleLockRowMatchesPathAndRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "verdict.lock")
	lock := `{"schema_version":"1","packs":[` +
		`{"name":"acme/other","source":"git:https://example.invalid/acme/packs.git@` + strings.Repeat("1", 40) +
		`","version":"9.9.9","hash":"1111","org_path":"packs/other","org_ref":"v1.2.3"},` +
		`{"name":"acme/older","source":"git:https://example.invalid/acme/packs.git@` + strings.Repeat("2", 40) +
		`","version":"0.9.0","hash":"2222","org_path":"packs/org","org_ref":"v1.0.0"},` +
		`{"name":"acme/root","source":"git:https://example.invalid/acme/packs.git@` + strings.Repeat("3", 40) +
		`","version":"1.0.0","hash":"3333","org_ref":"v1.2.3"},` +
		`{"name":"acme/lenses","source":"git:https://example.invalid/acme/packs.git@` + strings.Repeat("4", 40) +
		`","version":"1.0.0","hash":"4444","org_path":"packs/org","org_ref":"v1.2.3"}]}`
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	subdir := readBundleLockRow(path, &OrgPackInput{
		Repo: "https://example.invalid/acme/packs.git", Ref: "v1.2.3", Path: "packs/org",
	})
	if subdir.Name != "acme/lenses" || subdir.Hash != "4444" {
		t.Fatalf("subdir row = %+v, want the packs/org @ v1.2.3 row", subdir)
	}
	root := readBundleLockRow(path, &OrgPackInput{
		Repo: "https://example.invalid/acme/packs.git", Ref: "v1.2.3", Path: ".",
	})
	if root.Name != "acme/root" || root.Hash != "3333" {
		t.Fatalf("root row = %+v, want the repository-root row: an absent org_path reads as .", root)
	}
	stale := readBundleLockRow(path, &OrgPackInput{
		Repo: "https://example.invalid/acme/packs.git", Ref: "v1.3.0", Path: "packs/org",
	})
	if stale.Name != "" {
		t.Fatalf("row = %+v, want a bumped ref to match no row", stale)
	}
}

// TestOrgPackFromBundleCapsTheSnapshotReads proves the snapshot reads are
// capped like the controls they mirror (GO-JSON-01): an oversized config.json
// or verdict.lock degrades to no pin instead of being read whole.
func TestOrgPackFromBundleCapsTheSnapshotReads(t *testing.T) {
	t.Run("config", assertOversizedConfigReadsAsNoPin)
	t.Run("lock", assertOversizedLockReadsAsNoRow)
}

// assertOversizedConfigReadsAsNoPin proves an oversized captured config.json
// degrades like any other unreadable one: the receipt carries no pin.
func assertOversizedConfigReadsAsNoPin(t *testing.T) {
	dir := seedOrgBundle(t)
	writeOversize(t, filepath.Join(dir, "controls", "config.json"))
	if pin := OrgPackFromBundle(dir); pin.Repo != "" {
		t.Fatalf("pin = %+v, want no pin from an oversized config", pin)
	}
}

// assertOversizedLockReadsAsNoRow proves an oversized captured verdict.lock
// leaves the pin intact with no locked content beside it.
func assertOversizedLockReadsAsNoRow(t *testing.T) {
	dir := seedOrgBundle(t)
	writeOversize(t, filepath.Join(dir, "controls", "verdict.lock"))
	pin := OrgPackFromBundle(dir)
	if pin.Repo == "" || pin.Hash != "" {
		t.Fatalf("pin = %+v, want the pin with no locked row from an oversized lock", pin)
	}
}

// writeOversize replaces path with a document one byte over the control cap.
func writeOversize(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", controls.MaxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
}
