package recordcore_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/pack"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

const recordLockManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`

// writeRecordLockPack writes the repo pack record resolves from its controls
// snapshot, plus the lock body when non-empty.
func writeRecordLockPack(t *testing.T, lock string) {
	t.Helper()
	writeFile(t, ".verdict/packs/lenses/pack.json", recordLockManifest)
	writeFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body\n")
	if lock != "" {
		writeFile(t, pack.LockFile, lock)
	}
}

// lockBodyFor renders one well-formed entry for the record fixture.
func lockBodyFor(hash string) string {
	entry := pack.LockEntry{
		Name: "acme/lenses", Source: "dir:src-packs/lenses", Version: "1.0.0", Hash: hash,
	}
	return `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"dir:src-packs/lenses",` +
		`"version":"1.0.0","hash":"` + hash + `","sig":"` + pack.LockSig(&entry) + `"}]}`
}

// TestRecordRefusesMissingLock proves record runs the shared lock check: a
// repo pack with no verdict.lock entry refuses pack-lock-missing.
func TestRecordRefusesMissingLock(t *testing.T) {
	bundle(t, "local-lock-missing", reviewJSON(""))
	writeRecordLockPack(t, "")
	_, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-lock-missing"))
	if ref == nil || ref.Code != pack.CodeLockMissing {
		t.Fatalf("record = %v, want pack-lock-missing", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") {
		t.Fatalf("message = %q, want the pack named", ref.Message)
	}
}

// TestRecordRefusesLockDrift proves a lock whose recorded tree hash differs
// from the snapshot's pack refuses pack-lock-drift at record.
func TestRecordRefusesLockDrift(t *testing.T) {
	bundle(t, "local-lock-drift", reviewJSON(""))
	wrong := strings.Repeat("0", 64)
	writeRecordLockPack(t, lockBodyFor(wrong))
	_, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-lock-drift"))
	if ref == nil || ref.Code != pack.CodeLockDrift {
		t.Fatalf("record = %v, want pack-lock-drift", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") || !strings.Contains(ref.Message, wrong) {
		t.Fatalf("message = %q, want the pack and recorded hash", ref.Message)
	}
}

// TestRecordRefusesEditedLockSnapshot proves verdict.lock joins the controls
// inventory: editing the captured lock refuses controls-stale before the lock
// check can read it, exactly like an edited pack body.
func TestRecordRefusesEditedLockSnapshot(t *testing.T) {
	bundle(t, "local-lock-tamper", reviewJSON(""))
	writeRecordLockPack(t, lockBodyFor(
		"24e651328246cb30fd8e65893deb92d3136036b466daa9615f92a7110c592b0c"))
	req := recordRequest(t, &collector{}, "local-lock-tamper")
	writeFile(t, filepath.Join(".verdict", "state", "local-lock-tamper", "controls", "verdict.lock"),
		`{"schema_version":"1","packs":[]}`)
	_, ref := recordcore.Run(context.Background(), req)
	if ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("edited lock record = %v, want controls-stale", ref)
	}
}
