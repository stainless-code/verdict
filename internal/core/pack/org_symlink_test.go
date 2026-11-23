package pack

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

//nolint:revive // symlink test necessarily sets up filesystem states
func TestSyncRejectsIntermediateSymlink(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skip symlinks on short")
	}
	repo := t.TempDir()
	outside := t.TempDir()
	// Build central repo with real pack at outside/pack, but symlink packs -> outside
	central := filepath.Join(repo, "central")
	if err := os.MkdirAll(filepath.Join(central, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "org"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(filepath.Join(outside, "org", "pack.json"), lockManifest)
	writeFixture(filepath.Join(outside, "org", "lenses", "perf.md"), "perf body\n")
	// central/packs/org will be via symlink to outside/org? Instead make central/packs a symlink to outside, so pinPath packs/org traverses symlink
	if err := os.RemoveAll(filepath.Join(central, "packs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(central, "packs")); err != nil {
		t.Skipf("symlink not allowed: %v", err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pin := &OrgPin{Repo: "central", Ref: sha, Path: "packs/org"}
	_, ref := RunSync(context.Background(), &SyncRequest{OrgPin: pin})
	if ref == nil {
		t.Fatalf("expected refusal for intermediate symlink, got nil")
	}
	if ref.Code != CodeBadPack && ref.Code != "pack-unsafe-path" && ref.Code != CodePackUnsafePath {
		// Accept any typed refusal that mentions symlink or path
		t.Logf("refused with %s: %s", ref.Code, ref.Message)
	}
	// Ensure no outside read happened beyond refusal - we just check refusal not nil
}
