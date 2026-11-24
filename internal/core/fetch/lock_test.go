package fetchcore

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
)

const lockTestManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`

// lockFetchSnapshot builds the trusted-base snapshot fetch resolves packs
// from, over the head opt-out a local fixture can supply.
func lockFetchSnapshot(t *testing.T, root, lock string) *controls.Snapshot {
	t.Helper()
	writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(".verdict/packs/lenses/pack.json")), lockTestManifest)
	writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(".verdict/packs/lenses/lenses/perf.md")), "perf body\n")
	if lock != "" {
		writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(pack.LockFile)), lock)
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: t.TempDir(), AllowHead: true, Root: root,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	return snap
}

// TestFetchRefusesMissingLock proves the fetch pack resolver refuses a repo
// pack with no verdict.lock entry before any review spends anything.
func TestFetchRefusesMissingLock(t *testing.T) {
	t.Parallel()
	ctx2 := &fetchRunCtx{runner: &session{controls: lockFetchSnapshot(t, t.TempDir(), "")}}
	ref := resolveLensPacks(ctx2)
	if ref == nil || ref.Code != pack.CodeLockMissing {
		t.Fatalf("resolveLensPacks = %v, want pack-lock-missing", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") {
		t.Fatalf("message = %q, want the pack named", ref.Message)
	}
}

// TestFetchRefusesUnsyncedOrgPin proves fetch refuses when the base-owned
// packs.org_pack pin has no verdict.lock entry — never silently runs
// without the central packs.
func TestFetchRefusesUnsyncedOrgPin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sha := strings.Repeat("a", 40)
	cfg := `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"` + sha + `","path":"packs/org"}}}`
	writeFixtureFile(t, filepath.Join(root, ".verdict", "config.json"), cfg)
	// No repo packs and no lock: the org pin alone must refuse.
	bundleDir := t.TempDir()
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: bundleDir, AllowHead: true, Root: root})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	ctx2 := &fetchRunCtx{runner: &session{controls: snap}}
	ref2 := resolveLensPacks(ctx2)
	if ref2 == nil || ref2.Code != pack.CodeLockMissing {
		t.Fatalf("resolveLensPacks = %v, want pack-lock-missing", ref2)
	}
	if !strings.Contains(ref2.Message, "central") {
		t.Fatalf("message = %q, want org pin repo", ref2.Message)
	}
}

// TestFetchRefusesLockDrift proves the same resolver refuses a lock whose
// recorded hash no longer matches the vendored tree.
func TestFetchRefusesLockDrift(t *testing.T) {
	t.Parallel()
	wrong := strings.Repeat("0", 64)
	lock := `{"schema_version":"1","packs":[{"name":"acme/lenses","source":"dir:src-packs/lenses",` +
		`"version":"1.0.0","hash":"` + wrong + `","sig":"` + pack.LockSig(&pack.LockEntry{
		Name: "acme/lenses", Source: "dir:src-packs/lenses", Version: "1.0.0", Hash: wrong,
	}) + `"}]}`
	ctx2 := &fetchRunCtx{runner: &session{controls: lockFetchSnapshot(t, t.TempDir(), lock)}}
	ref := resolveLensPacks(ctx2)
	if ref == nil || ref.Code != pack.CodeLockDrift {
		t.Fatalf("resolveLensPacks = %v, want pack-lock-drift", ref)
	}
	if !strings.Contains(ref.Message, "acme/lenses") || !strings.Contains(ref.Message, wrong) {
		t.Fatalf("message = %q, want the pack and recorded hash", ref.Message)
	}
}

// syncOrgPinInto vendors one central org pin under root's .verdict/ tree and
// writes the pin into its config, the way `verdict pack sync` leaves a
// repository.
func syncOrgPinInto(t *testing.T, root string, pin *pack.OrgPin) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(root, "central", "packs", "org", "pack.json"), lockTestManifest)
	writeFixtureFile(t, filepath.Join(root, "central", "packs", "org", "lenses", "perf.md"), "perf body\n")
	writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(".verdict/config.json")),
		`{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"`+pin.Ref+`","path":"packs/org"}}}`)
	read := core.ReadRequest{RepoRoot: root}
	if _, ref := pack.RunSync(t.Context(), &pack.SyncRequest{ReadRequest: read, OrgPin: pin}); ref != nil {
		t.Fatalf("pack.RunSync: %s: %s", ref.Code, ref.Message)
	}
}

// TestFetchAcceptsSyncedOrgPin proves fetch's pack resolver accepts a
// correctly synced org pin: the vendored tree verifies against the lock and the
// pin, with no fetch of its own.
func TestFetchAcceptsSyncedOrgPin(t *testing.T) {
	root := t.TempDir()
	syncOrgPinInto(t, root, &pack.OrgPin{Repo: "central", Ref: "v1.0.0", Path: "packs/org"})
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: t.TempDir(), AllowHead: true, Root: root,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	ctx2 := &fetchRunCtx{runner: &session{controls: snap}}
	if ref := resolveLensPacks(ctx2); ref != nil {
		t.Fatalf("resolveLensPacks = %s: %s, want the synced org pin accepted", ref.Code, ref.Message)
	}
}

// TestFetchRefusesBumpedOrgPinRef proves fetch refuses a pin whose ref moved
// without a re-sync: the old vendored tree never stands in for the new ref.
func TestFetchRefusesBumpedOrgPinRef(t *testing.T) {
	root := t.TempDir()
	syncOrgPinInto(t, root, &pack.OrgPin{Repo: "central", Ref: "v1.0.0", Path: "packs/org"})
	writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(".verdict/config.json")),
		`{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.1.0","path":"packs/org"}}}`)
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: t.TempDir(), AllowHead: true, Root: root,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	ctx2 := &fetchRunCtx{runner: &session{controls: snap}}
	if ref := resolveLensPacks(ctx2); ref == nil || ref.Code != pack.CodeLockMissing {
		t.Fatalf("resolveLensPacks = %v, want %s", ref, pack.CodeLockMissing)
	}
}
