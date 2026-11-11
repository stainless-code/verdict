package controls

import (
	"os"
	"path/filepath"
	"testing"
)

const packManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`

// packBase commits one repo pack at base.
func packBase(t *testing.T, body string) string {
	t.Helper()
	return baseRepo(t, map[string]string{
		".verdict/config.json":               `{"schema_version":"1"}`,
		".verdict/packs/acme/pack.json":      packManifest,
		".verdict/packs/acme/lenses/perf.md": body,
	})
}

// TestResolvePackTreeSnapshotsBase proves the pack tree joins the control
// inventory at the base bytes, and a snapshot edit refuses controls-stale at
// the shared post/record gate.
//
//nolint:revive // the resolve/gate/tamper sequence reads as one scenario
func TestResolvePackTreeSnapshotsBase(t *testing.T) {
	base := packBase(t, "base body\n")
	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: base})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if got, want := snap.PackNames(), []string{"acme"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("pack dirs = %v, want %v", got, want)
	}
	rel := ".verdict/packs/acme/lenses/perf.md"
	if got, ok := snap.ReadFile(rel); !ok || string(got) != "base body\n" {
		t.Fatalf("%s = %q (ok=%v), want base bytes", rel, got, ok)
	}
	if _, ref := Gate(".verdict/state/12", base); ref != nil {
		t.Fatalf("Gate: %v", ref)
	}
	snapshot := filepath.Join(snap.Dir, "packs", "acme", "lenses", "perf.md")
	if err := os.WriteFile(snapshot, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ref := Gate(".verdict/state/12", base); ref == nil || ref.Code != CodeStale {
		t.Fatalf("tampered pack Gate = %v, want controls-stale", ref)
	}
}

// TestResolvePackTreeIgnoresHead proves a head edit to the pack leaves the
// snapshot at the base bytes.
func TestResolvePackTreeIgnoresHead(t *testing.T) {
	base := packBase(t, "base body\n")
	writeAll(t, map[string]string{".verdict/packs/acme/lenses/perf.md": "head body\n"})
	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/13", BaseSHA: base})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if got, ok := snap.ReadFile(".verdict/packs/acme/lenses/perf.md"); !ok || string(got) != "base body\n" {
		t.Fatalf("pack body = %q (ok=%v), want base bytes", got, ok)
	}
}

// TestResolvePackTreeWorktreeRefusesSymlink proves the explicit head opt-out
// still refuses a symlink under the pack tree.
func TestResolvePackTreeWorktreeRefusesSymlink(t *testing.T) {
	base := packBase(t, "base body\n")
	target := filepath.Join(t.TempDir(), "outside.md")
	writeAll(t, map[string]string{target: "outside\n"})
	if err := os.Remove(filepath.Join(".verdict", "packs", "acme", "lenses", "perf.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(".verdict", "packs", "acme", "lenses", "perf.md")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	_, ref := Resolve(t.Context(), cmdr, &Request{
		BundleDir: ".verdict/state/14", BaseSHA: base, AllowHead: true,
	})
	if ref == nil || ref.Code != CodeUnavailable {
		t.Fatalf("symlinked pack file = %v, want controls-unavailable", ref)
	}
}
