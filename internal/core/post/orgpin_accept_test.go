package postcore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
)

// postOrgBase is the base commit postBundle records, so a re-seeded controls
// snapshot verifies against the same base.
const postOrgBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// TestPostAcceptsSyncedOrgPin proves post's controls gate accepts a correctly
// synced org pin: the dry run reaches the payload instead of refusing.
func TestPostAcceptsSyncedOrgPin(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	syncPostOrgPin(t, "v1.0.0")
	seedPostControls(t, ".verdict/state/7", postOrgBase)
	var warnings, human []string
	req := postTestReq(postCommander(t), Flags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("post = %s: %s, want the synced org pin accepted", ref.Code, ref.Message)
	}
	if resp.Data == nil || !resp.Data.DryRun {
		t.Fatalf("data = %+v, want the dry-run payload", resp.Data)
	}
}

// TestPostRefusesBumpedOrgPinRef proves the same gate refuses a pin whose ref
// moved without a re-sync.
func TestPostRefusesBumpedOrgPinRef(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	syncPostOrgPin(t, "v1.0.0")
	if err := writeTestFile(filepath.Join(".verdict", "config.json"),
		`{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"v1.1.0","path":"packs/org"}}}`); err != nil {
		t.Fatal(err)
	}
	seedPostControls(t, ".verdict/state/7", postOrgBase)
	var warnings, human []string
	_, ref := Run(context.Background(), postTestReq(nil, Flags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
	}, &warnings, &human))
	if ref == nil || ref.Code != packcore.CodeLockMissing {
		t.Fatalf("post = %v, want %s", ref, packcore.CodeLockMissing)
	}
}

// syncPostOrgPin vendors one central org pack into the fixture worktree, the
// way `verdict pack sync` leaves a repository.
func syncPostOrgPin(t *testing.T, ref string) {
	t.Helper()
	for path, body := range map[string]string{
		"central/packs/org/pack.json":      postLockManifest,
		"central/packs/org/lenses/perf.md": "perf body\n",
		".verdict/config.json": `{"schema_version":"1","packs":{"org_pack":{"repo":"central","ref":"` +
			ref + `","path":"packs/org"}}}`,
	} {
		if err := writeTestFile(filepath.FromSlash(path), body); err != nil {
			t.Fatal(err)
		}
	}
	read := core.ReadRequest{RepoRoot: "."}
	if _, r := packcore.RunSync(context.Background(), &packcore.SyncRequest{
		ReadRequest: read,
		OrgPin:      &packcore.OrgPin{Repo: "central", Ref: ref, Path: "packs/org"},
	}); r != nil {
		t.Fatalf("pack sync: %s: %s", r.Code, r.Message)
	}
}
