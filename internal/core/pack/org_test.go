package pack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// orgSha is the 40-hex commit the local-dir sync pins record.
var orgSha = strings.Repeat("a", 40)

// syncReadFor returns the read runtime the scripted git commander needs.
func syncReadFor(cmdr *gitCommander) core.ReadRequest {
	return core.ReadRequest{Runtime: &core.Runtime{Commander: cmdr}}
}

// TestSyncDirVendorsAndLocks proves the happy path with a central directory:
// sync vendors the pack tree under its slug, writes a dir: lock row, and
// the review-time verifier then accepts the vendored tree with no fetch.
func TestSyncDirVendorsAndLocks(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central/packs/org", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "packs/org"}
	resp, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin})
	if ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	want := "synced acme/lenses@1.0.0 repo=central ref=" + orgSha + " path=packs/org sha256:" + lockHash
	if got := resp.Human; !slices.Equal(got, []string{want}) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	if resp.Data.SHA != nil || resp.Data.Hash != lockHash || resp.Data.Version != "1.0.0" {
		t.Errorf("data = %+v, want hash %s version 1.0.0, no sha", resp.Data, lockHash)
	}
	assertFile(t, ".verdict/packs/lenses/pack.json", lockManifest)
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body\n")
	entry := LockEntry{Name: "acme/lenses", Source: "dir:central", Version: "1.0.0", Hash: lockHash}
	entry.Sig = LockSig(&entry)
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, "dir:central", lockHash, entry.Sig))
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin after sync: %s: %s", ref.Code, ref.Message)
	}
}

// TestSyncDirRootPath proves a pin at the central repository root vendors
// the pack at that root.
func TestSyncDirRootPath(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "."}
	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin}); ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body\n")
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin: %s: %s", ref.Code, ref.Message)
	}
}

// TestSyncGitResolvesAndLocks proves a git pin clones once, records the
// resolved commit in the lock, and verifies offline afterwards.
func TestSyncGitResolvesAndLocks(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	const url = "https://example.invalid/acme/packs.git"
	cmdr := &gitCommander{t: t, files: map[string]string{
		"packs/org/pack.json":      lockManifest,
		"packs/org/lenses/perf.md": "perf body\n",
	}}
	pin := &OrgPin{Repo: url, Ref: "v1.2.3", Path: "packs/org"}
	req := &SyncRequest{
		ReadRequest: syncReadFor(cmdr),
		OrgPin:      pin,
	}
	resp, ref := RunSync(context.Background(), req)
	if ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	if len(cmdr.calls) != 3 {
		t.Fatalf("git calls = %d, want clone, rev-parse, checkout", len(cmdr.calls))
	}
	source := "git:" + url + "@" + addGitSha
	if got, want := resp.Human, []string{"synced acme/lenses@1.0.0 repo=" + url + " ref=v1.2.3 path=packs/org sha256:" + lockHash}; !slices.Equal(got, want) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	if resp.Data.SHA == nil || *resp.Data.SHA != addGitSha {
		t.Errorf("sha = %v, want %s", resp.Data.SHA, addGitSha)
	}
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, source, lockHash, addGitSig))
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin: %s: %s", ref.Code, ref.Message)
	}
}

// TestVerifyOrgPinRefusesMissing proves an un-synced pin refuses the review
// with pack-lock-missing instead of running without the central packs.
func TestVerifyOrgPinRefusesMissing(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "."}
	if ref := VerifyOrgPin(repo, pin); ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("refusal = %v, want %s", ref, CodeLockMissing)
	}
}

// TestVerifyOrgPinRefusesDrift proves a vendored central pack edited after
// sync refuses pack-lock-drift: the drift never silently joins the review.
func TestVerifyOrgPinRefusesDrift(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "."}
	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin}); ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin before drift: %s: %s", ref.Code, ref.Message)
	}
	if err := os.WriteFile(filepath.Join(repo, ".verdict", "packs", "lenses", "lenses", "perf.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ref := VerifyOrgPin(repo, pin); ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("refusal = %v, want %s", ref, CodeLockDrift)
	}
}

// TestVerifyOrgPinRefusesDeletedVendoredPack proves a deleted vendored tree
// refuses pack-lock-drift rather than dropping the central layer.
func TestVerifyOrgPinRefusesDeletedVendoredPack(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "."}
	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin}); ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".verdict", "packs", "lenses")); err != nil {
		t.Fatal(err)
	}
	if ref := VerifyOrgPin(repo, pin); ref == nil || ref.Code != CodeLockDrift {
		t.Fatalf("refusal = %v, want %s", ref, CodeLockDrift)
	}
}

// TestVerifyOrgPinRefusesPinMismatch proves a lock row synced from a
// different repository does not satisfy the pin.
func TestVerifyOrgPinRefusesPinMismatch(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "."}
	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin}); ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	other := &OrgPin{Repo: "other-central", Ref: orgSha, Path: "."}
	if ref := VerifyOrgPin(repo, other); ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("refusal = %v, want %s", ref, CodeLockMissing)
	}
}

// TestSyncRefusesBadPins probes defense in depth: a pin built directly
// (past the config parser) still refuses traversal paths, absolute paths,
// and floating branch refs before any vendoring.
//
//nolint:revive // the table rows read as one contract block
func TestSyncRefusesBadPins(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	cases := []struct {
		name string
		pin  *OrgPin
		want string
	}{
		{"traversal", &OrgPin{Repo: "central", Ref: orgSha, Path: "../escape"}, "escapes"},
		{"absolute", &OrgPin{Repo: "central", Ref: orgSha, Path: "/etc"}, "escapes"},
		{"branch ref", &OrgPin{Repo: "central", Ref: "main", Path: "."}, "not immutable"},
		{"empty repo", &OrgPin{Repo: "", Ref: orgSha, Path: "."}, "not a repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ref := RunSync(context.Background(), &SyncRequest{OrgPin: tc.pin})
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("refusal = %v, want %s", ref, CodeBadPack)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Errorf("message = %q, want it to contain %q", ref.Message, tc.want)
			}
		})
	}
}

// TestSyncUnreachableDir proves a pin whose local repository never appears
// refuses typed instead of vendoring nothing.
func TestSyncUnreachableDir(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	pin := &OrgPin{Repo: "no-such-central", Ref: orgSha, Path: "."}
	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin}); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("refusal = %v, want %s", ref, CodeBadPack)
	}
}

// TestSyncGitFailed proves a failing clone refuses pack-git-failed, never a
// silently un-vendored pin.
func TestSyncGitFailed(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	const url = "https://example.invalid/acme/packs.git"
	cmdr := &gitCommander{t: t, cloneExit: 128}
	pin := &OrgPin{Repo: url, Ref: "v1.2.3", Path: "."}
	req := &SyncRequest{
		ReadRequest: syncReadFor(cmdr),
		OrgPin:      pin,
	}
	if _, ref := RunSync(context.Background(), req); ref == nil || ref.Code != CodePackGitFailed {
		t.Fatalf("refusal = %v, want %s", ref, CodePackGitFailed)
	}
}

// TestSyncWithoutPin proves sync refuses when the base config pins nothing.
func TestSyncWithoutPin(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	if _, ref := RunSync(context.Background(), &SyncRequest{}); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("refusal = %v, want %s", ref, CodeBadPack)
	}
}

// TestBaseOrgPinIgnoresTheBranchesOwnPin proves the security property: a
// branch commit that drops or moves the pin never reaches the pack layer,
// because the read is anchored at the merge base with the default branch.
func TestBaseOrgPinIgnoresTheBranchesOwnPin(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "main")
	baseSHA := commitConfig(t,
		`{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"`+orgSha+`","path":"packs/org"}}}`,
		"base")
	baseGit(t, "checkout", "-q", "-b", "feature")
	commitConfig(t, `{"schema_version":"1","packs":{"org_pack":{"repo":"evil","ref":"v9.9.9"}}}`, "head tampering")
	baseGit(t, "checkout", "-q", "main")
	// The branch just moved elsewhere: re-point the checkout at the feature
	// head with a dropped pin, then read from there.
	baseGit(t, "checkout", "-q", "-b", "dropping")
	commitConfig(t, `{"schema_version":"1","packs":{}}`, "pin dropped")

	pin, source, ref := BaseOrgPin(t.Context(), coreexec.Runner{}, ".")
	if ref != nil {
		t.Fatalf("BaseOrgPin: %s: %s", ref.Code, ref.Message)
	}
	if want := BaseConfigPrefix + baseSHA[:12]; source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
	if pin == nil || pin.Repo != "central" || pin.Ref != orgSha || pin.Path != "packs/org" {
		t.Errorf("pin = %+v, want the base's central pin", pin)
	}
}

// TestResolveVerifiesOrgPin proves the review path shares the pin check: a
// resolution carrying the base pin refuses when the central pack is missing.
func TestResolveVerifiesOrgPin(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	pin := &OrgPin{Repo: "central", Ref: orgSha, Path: "."}
	req := &Request{OrgPin: pin, List: true}
	if _, _, ref := Resolve(context.Background(), req); ref == nil || ref.Code != CodeLockMissing {
		t.Fatalf("refusal = %v, want %s", ref, CodeLockMissing)
	}
}
