package postcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
)

const postLockManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`

// writePostLockPack writes the repo pack post reads from its controls
// snapshot, plus the lock body when non-empty.
func writePostLockPack(t *testing.T, lock string) {
	t.Helper()
	for path, body := range map[string]string{
		".verdict/packs/lenses/pack.json":      postLockManifest,
		".verdict/packs/lenses/lenses/perf.md": "perf body\n",
	} {
		if err := writeTestFile(path, body); err != nil {
			t.Fatal(err)
		}
	}
	if lock != "" {
		if err := writeTestFile(pack.LockFile, lock); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPostRefusesMissingLock proves post's controls gate runs the shared lock
// check: a repo pack with no lock entry refuses pack-lock-missing.
func TestPostRefusesMissingLock(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writePostLockPack(t, "")
	postBundle(t, postReview)
	var warnings, human []string
	req := postTestReq(nil, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
	}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != pack.CodeLockMissing {
		t.Fatalf("Run = %v, want pack-lock-missing", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") {
		t.Fatalf("message = %q, want the pack named", ref.Message)
	}
}

// TestPostRefusesLockDrift proves a lock whose recorded tree hash differs
// from the snapshot's pack refuses pack-lock-drift at post.
func TestPostRefusesLockDrift(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wrong := strings.Repeat("0", 64)
	writePostLockPack(t, `{"schema_version":"1","packs":[{"name":"acme/lenses",`+
		`"source":"dir:src-packs/lenses","version":"1.0.0","hash":"`+wrong+`","sig":"`+
		pack.LockSig(pack.LockEntry{
			Name: "acme/lenses", Source: "dir:src-packs/lenses", Version: "1.0.0", Hash: wrong,
		})+`"}]}`)
	postBundle(t, postReview)
	var warnings, human []string
	req := postTestReq(nil, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
	}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != pack.CodeLockDrift {
		t.Fatalf("Run = %v, want pack-lock-drift", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") || !strings.Contains(ref.Message, wrong) {
		t.Fatalf("message = %q, want the pack and recorded hash", ref.Message)
	}
}
