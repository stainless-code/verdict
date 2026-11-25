package recordcore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

// TestRecordAcceptsSyncedOrgPin proves record's shared lock gate accepts a
// correctly synced org pin and records the review.
func TestRecordAcceptsSyncedOrgPin(t *testing.T) {
	bundle(t, "local-org-accept", reviewJSON(""))
	syncRecordOrgPin(t, "v1.0.0")
	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-org-accept"))
	if ref != nil {
		t.Fatalf("record = %s: %s, want the synced org pin accepted", ref.Code, ref.Message)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Data.Items))
	}
}

// TestRecordRefusesBumpedOrgPinRef proves the same gate refuses a pin whose ref
// moved without a re-sync.
func TestRecordRefusesBumpedOrgPinRef(t *testing.T) {
	bundle(t, "local-org-bump", reviewJSON(""))
	syncRecordOrgPin(t, "v1.0.0")
	testkit.WriteFile(t, filepath.Join(".verdict", "config.json"),
		`{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.1.0","path":"packs/org"}}}`)
	_, ref := recordcore.Run(context.Background(), recordRequest(t, &testkit.Collector{}, "local-org-bump"))
	if ref == nil || ref.Code != packcore.CodeLockMissing {
		t.Fatalf("record = %v, want %s", ref, packcore.CodeLockMissing)
	}
}

// syncRecordOrgPin vendors one central org pack into the fixture worktree, the
// way `verdict pack sync` leaves a repository.
func syncRecordOrgPin(t *testing.T, ref string) {
	t.Helper()
	testkit.WriteFile(t, filepath.Join("central", "packs", "org", "pack.json"), recordLockManifest)
	testkit.WriteFile(t, filepath.Join("central", "packs", "org", "lenses", "perf.md"), "perf body\n")
	testkit.WriteFile(t, filepath.Join(".verdict", "config.json"),
		`{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"`+ref+`","path":"packs/org"}}}`)
	read := core.ReadRequest{RepoRoot: "."}
	if _, r := packcore.RunSync(context.Background(), &packcore.SyncRequest{
		ReadRequest: read,
		OrgPin:      &packcore.OrgPin{Repo: "central", Ref: ref, Path: "packs/org"},
	}); r != nil {
		t.Fatalf("pack sync: %s: %s", r.Code, r.Message)
	}
}
