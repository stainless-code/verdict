package pack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
)

// orgPinFixture syncs one central pack and writes the pin into the worktree's
// config, so both enforcement paths — the review path's worktree lock and the
// trusted-base snapshot — see the same pin.
func orgPinFixture(t *testing.T, ref string) (*OrgPin, string) {
	t.Helper()
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central/packs/org", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pin := &OrgPin{Repo: "central", Ref: ref, Path: "packs/org"}
	if _, ref2 := RunSync(context.Background(), &SyncRequest{OrgPin: pin}); ref2 != nil {
		t.Fatalf("RunSync: %s: %s", ref2.Code, ref2.Message)
	}
	writeOrgPinConfig(t, repo, pin)
	return pin, repo
}

// writeOrgPinConfig writes the base-owned pin into the fixture's config.
func writeOrgPinConfig(t *testing.T, repo string, pin *OrgPin) {
	t.Helper()
	body := fmt.Sprintf(`{"schema_version":"1","packs":{"org_pack":{"repo":%q,"ref":%q,"path":%q}}}`,
		pin.Repo, pin.Ref, pin.Path)
	writeFixture(t, repo, map[string]string{".verdict/config.json": body})
}

// TestCheckLockAcceptsSyncedOrgPin proves the accepting path: a correctly
// synced and locked org pin passes the shared gate over the trusted-base
// snapshot. The snapshot spells the vendored tree packs/<slug>, so a check that
// reads .verdict/packs/<slug> there refuses every synced pin.
func TestCheckLockAcceptsSyncedOrgPin(t *testing.T) {
	pin, repo := orgPinFixture(t, orgSha)
	if ref := CheckLock(lockSnapshot(t, repo)); ref != nil {
		t.Fatalf("CheckLock = %s: %s, want the synced org pin accepted", ref.Code, ref.Message)
	}
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin = %s: %s, want the synced org pin accepted", ref.Code, ref.Message)
	}
}

// TestCheckLockRefusesTamperedSyncedOrgPin proves the accepting path is not a
// bypass: editing the vendored tree breaks the very same gate.
func TestCheckLockRefusesTamperedSyncedOrgPin(t *testing.T) {
	_, repo := orgPinFixture(t, orgSha)
	writeFixture(t, repo, map[string]string{
		".verdict/packs/lenses/lenses/perf.md": "edited after sync\n",
	})
	ref := CheckLock(lockSnapshot(t, repo))
	if ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("CheckLock = %v, want %s", ref, CodeLockDrift)
	}
	if !strings.Contains(ref.Message, "acme/lenses") {
		t.Fatalf("message = %q, want the pack named", ref.Message)
	}
}

// TestOrgPinTagBumpRefusesUntilResynced proves a tag bump alone never keeps
// verifying the old tree: the lock records the ref it was synced at, so
// v1.0.0 -> v1.1.0 refuses on both the worktree and snapshot paths until the
// operator re-syncs, and then the old pin refuses in its turn.
func TestOrgPinTagBumpRefusesUntilResynced(t *testing.T) {
	pinV1, repo := orgPinFixture(t, "v1.0.0")
	assertOrgPinAccepted(t, repo, pinV1)
	pinV2 := &OrgPin{Repo: pinV1.Repo, Ref: "v1.1.0", Path: pinV1.Path}
	assertOrgPinRefused(t, repo, pinV2, "verdict pack sync")
	writeOrgPinConfig(t, repo, pinV2)
	if ref := CheckLock(lockSnapshot(t, repo)); ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("CheckLock(bumped pin) = %v, want %s", ref, CodeLockMissing)
	}

	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pinV2}); ref != nil {
		t.Fatalf("RunSync(v1.1.0): %s: %s", ref.Code, ref.Message)
	}
	assertOrgPinAccepted(t, repo, pinV2)
	assertOrgPinRefused(t, repo, pinV1)
}

// assertOrgPinAccepted proves both enforcement paths accept one pin: the
// worktree lock the review reads and the trusted-base snapshot.
func assertOrgPinAccepted(t *testing.T, repo string, pin *OrgPin) {
	t.Helper()
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin(%s) = %s: %s, want accepted", pin.Ref, ref.Code, ref.Message)
	}
	if ref := CheckLock(lockSnapshot(t, repo)); ref != nil {
		t.Fatalf("CheckLock(%s) = %s: %s, want accepted", pin.Ref, ref.Code, ref.Message)
	}
}

// assertOrgPinRefused proves the worktree path refuses one pin as
// pack-lock-missing, and that the message names every wanted fragment.
func assertOrgPinRefused(t *testing.T, repo string, pin *OrgPin, wants ...string) {
	t.Helper()
	ref := VerifyOrgPin(repo, pin)
	if ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("VerifyOrgPin(%s) = %v, want %s", pin.Ref, ref, CodeLockMissing)
	}
	for _, want := range wants {
		if !strings.Contains(ref.Message, want) {
			t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
		}
	}
}

// TestCheckLockRefusesEditedOrgRef proves the integrity digest covers the
// recorded ref: a hand edit that keeps the tree hash still refuses drift.
func TestCheckLockRefusesEditedOrgRef(t *testing.T) {
	_, repo := orgPinFixture(t, "v1.0.0")
	path := filepath.Join(repo, filepath.FromSlash(LockFile))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"org_ref": "v1.0.0"`, `"org_ref": "v1.0.1"`, 1)
	if edited == string(raw) {
		t.Fatalf("%s carries no org_ref fixture:\n%s", LockFile, raw)
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := CheckLock(lockSnapshot(t, repo))
	if ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("CheckLock = %v, want %s", ref, CodeLockDrift)
	}
	if !strings.Contains(ref.Message, "sig") {
		t.Fatalf("message = %q, want the sig field named", ref.Message)
	}
}

// TestLockSigCoversOrgFields pins the exact digest for an org row and proves a
// non-org row keeps the digest it had before org packs existed.
func TestLockSigCoversOrgFields(t *testing.T) {
	const orgSig = "962ae21863fb64b333a39fc8db613d7aeaa41a34b2cf53609441551cd5f6f78a"
	entry := LockEntry{
		Name: "acme/lenses", Source: "dir:central", Version: "1.0.0",
		Hash: lockHash, OrgPath: "packs/org", OrgRef: "v1.0.0",
	}
	if got := LockSig(&entry); got != orgSig {
		t.Fatalf("LockSig = %s, want %s", got, orgSig)
	}
	plain := LockEntry{
		Name: "acme/lenses", Source: "dir:src-packs/lenses", Version: "1.0.0", Hash: lockHash,
	}
	if got := LockSig(&plain); got != lockSigDir {
		t.Fatalf("non-org LockSig = %s, want the pre-org digest %s", got, lockSigDir)
	}
	//nolint:govet // the mutate matrix reads as name then action
	for _, mutate := range []struct {
		name string
		edit func(*LockEntry)
	}{
		{"org_path", func(e *LockEntry) { e.OrgPath = "packs/other" }},
		{"org_ref", func(e *LockEntry) { e.OrgRef = "v1.0.1" }},
	} {
		edited := entry
		mutate.edit(&edited)
		if got := LockSig(&edited); got == orgSig {
			t.Errorf("LockSig ignores %s: still %s", mutate.name, got)
		}
	}
}

// TestCheckOrgPinSurfacesCorruptConfig proves a base config the parser refuses
// fails closed: a corrupt pin never reads as "no pin" and silently drops
// org-pin enforcement.
func TestCheckOrgPinSurfacesCorruptConfig(t *testing.T) {
	_, repo := orgPinFixture(t, orgSha)
	if ref := CheckLock(lockSnapshot(t, repo)); ref != nil {
		t.Fatalf("CheckLock before the corruption = %s: %s, want accepted", ref.Code, ref.Message)
	}
	writeFixture(t, repo, map[string]string{".verdict/config.json": `{"schema_version":"1","packs":`})
	ref := CheckLock(lockSnapshot(t, repo))
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("CheckLock = %v, want bad-config", ref)
	}
}

// TestOrgPinFromSnapshotRefusesAnUnreadableCapturedConfig proves the snapshot
// gate fails closed: a base config the snapshot recorded as used but cannot
// read (its captured file is gone, or has grown past the control cap) refuses
// instead of reading as "no pin", which would drop org-pin enforcement for the
// whole review. A row recorded missing still reads as no pin.
func TestOrgPinFromSnapshotRefusesAnUnreadableCapturedConfig(t *testing.T) {
	_, repo := orgPinFixture(t, orgSha)
	snap := lockSnapshot(t, repo)
	path := snap.Path(config.ConfigPath)
	if path == "" {
		t.Fatal("the snapshot captured no config.json to remove")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove the captured config: %v", err)
	}
	if _, ref := orgPinFromSnapshot(snap); ref == nil || ref.Code != controls.CodeUnavailable {
		t.Fatalf("orgPinFromSnapshot = %v, want %s", ref, controls.CodeUnavailable)
	}
	if ref := CheckOrgPin(snap); ref == nil || ref.Code != controls.CodeUnavailable {
		t.Fatalf("CheckOrgPin = %v, want %s", ref, controls.CodeUnavailable)
	}
}

// TestOrgPinFromSnapshotRefusesAMutatedCapturedConfig proves the hash gate
// fails closed: a captured config.json edited after the gate ran — bytes or
// hash no longer matching the snapshot row — refuses controls-stale instead
// of reading as whatever the edit says (here, a pin-less document that would
// drop org-pin enforcement for the review).
func TestOrgPinFromSnapshotRefusesAMutatedCapturedConfig(t *testing.T) {
	_, repo := orgPinFixture(t, orgSha)
	snap := lockSnapshot(t, repo)
	path := snap.Path(config.ConfigPath)
	if path == "" {
		t.Fatal("the snapshot captured no config.json to mutate")
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0o644); err != nil {
		t.Fatalf("mutate the captured config: %v", err)
	}
	if _, ref := orgPinFromSnapshot(snap); ref == nil || ref.Code != controls.CodeStale {
		t.Fatalf("orgPinFromSnapshot = %v, want %s", ref, controls.CodeStale)
	}
	if ref := CheckOrgPin(snap); ref == nil || ref.Code != controls.CodeStale {
		t.Fatalf("CheckOrgPin = %v, want %s", ref, controls.CodeStale)
	}
}

// TestOrgPinFromSnapshotReadsAnAbsentRowAsNoPin proves the other arm: a base
// that declares no config.json at all still reads as no pin, so the fail-closed
// read does not turn every pin-less repository into a refusal.
func TestOrgPinFromSnapshotReadsAnAbsentRowAsNoPin(t *testing.T) {
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: t.TempDir(), AllowHead: true, Root: t.TempDir(),
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	if entry := snap.Entry(config.ConfigPath); entry == nil || entry.Status != "missing" {
		t.Fatalf("config.json row = %+v, want a missing row", entry)
	}
	if pin, ref := orgPinFromSnapshot(snap); pin != nil || ref != nil {
		t.Fatalf("orgPinFromSnapshot = %+v / %v, want no pin", pin, ref)
	}
}
