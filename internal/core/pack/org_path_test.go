package pack

import (
	"context"
	"testing"
)

func TestVerifyOrgPinDetectsPathChange(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "central/packs/a", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	sourcePack(t, repo, "central/packs/b", lockManifest, map[string]string{"lenses/perf.md": "perf body\n"})
	pinA := &OrgPin{Repo: "central", Ref: orgSha, Path: "packs/a"}
	if _, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pinA}); ref != nil {
		t.Fatalf("RunSync A: %s: %s", ref.Code, ref.Message)
	}
	if ref := VerifyOrgPin(repo, pinA); ref != nil {
		t.Fatalf("Verify A: %s: %s", ref.Code, ref.Message)
	}
	pinB := &OrgPin{Repo: "central", Ref: orgSha, Path: "packs/b"}
	if ref := VerifyOrgPin(repo, pinB); ref == nil {
		t.Fatalf("Verify B should refuse path mismatch, got nil")
	} else if ref.Code != CodeLockMissing {
		t.Fatalf("Verify B = %s, want %s", ref.Code, CodeLockMissing)
	}
}
