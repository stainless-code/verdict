package controls

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestResolvePackNamesSkipStrayFile proves a regular file directly under the
// pack root is not a pack directory: the snapshot still carries its bytes as a
// control row, but it names no pack, so fetch never loads it and never refuses
// the run.
func TestResolvePackNamesSkipStrayFile(t *testing.T) {
	base := baseRepo(t, map[string]string{
		".verdict/config.json":               `{"schema_version":"1"}`,
		".verdict/packs/README.md":           "pack docs\n",
		".verdict/packs/acme/pack.json":      packManifest,
		".verdict/packs/acme/lenses/perf.md": "perf body\n",
	})
	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/15", BaseSHA: base})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if got, want := snap.PackNames(), []string{"acme"}; !slices.Equal(got, want) {
		t.Fatalf("pack dirs = %v, want %v", got, want)
	}
	if !snap.Has(".verdict/packs/README.md") {
		t.Error("the stray file is a control row and must stay snapshotted")
	}
}

// colonPackBase commits one repo pack whose child directory name holds a
// colon, which Windows forbids as a filename: the base tree is built from git
// objects, so the fixture needs no such name on disk.
func colonPackBase(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	gitRun(t, "init", "-q")
	writeAll(t, map[string]string{".verdict/config.json": `{"schema_version":"1"}`, "manifest.json": packManifest})
	gitRun(t, "add", "-f", "-A")
	gitRun(t, "commit", "-qm", "base")
	blob := gitRun(t, "rev-parse", "HEAD:manifest.json")
	gitRun(t, "update-index", "--add", "--cacheinfo", "100644,"+blob+",.verdict/packs/team:qa/pack.json")
	tree := gitRun(t, "write-tree")
	return gitRun(t, "commit-tree", tree, "-p", gitRun(t, "rev-parse", "HEAD"), "-m", "colon pack")
}

// TestResolveRefusesUnrepresentablePackPath proves a pack file at base whose
// path the snapshot cannot carry refuses naming that path, instead of dropping
// the pack from the inventory with no control row and no refusal.
func TestResolveRefusesUnrepresentablePackPath(t *testing.T) {
	base := colonPackBase(t)
	_, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/16", BaseSHA: base})
	if ref == nil || ref.Code != CodeUnavailable {
		t.Fatalf("colon pack path = %v, want controls-unavailable", ref)
	}
	want := "control .verdict/packs/team:qa/pack.json is not a path the snapshot can carry"
	if !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
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
