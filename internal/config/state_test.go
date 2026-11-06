package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureStateIgnore pins the ignore-first rule: the ignore file gains
// the single `/state/` line before any state is written, and a second
// call never duplicates it.
func TestEnsureStateIgnore(t *testing.T) {
	root := t.TempDir()
	layout := NewLayout(root)
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", ".gitignore"))
	if err != nil {
		t.Fatalf("read ignore: %v", err)
	}
	if string(raw) != "/state/\n" {
		t.Fatalf("ignore = %q", raw)
	}
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("second EnsureStateIgnore: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(root, ".verdict", ".gitignore"))
	if err != nil || string(raw) != "/state/\n" {
		t.Fatalf("ignore after second call = %q, err = %v", raw, err)
	}
}

// stateFixture builds a worktree with a tracked config and an existing
// ignore file, returning the layout and the shared config bytes.
func stateFixture(t *testing.T) (Layout, string) {
	t.Helper()
	root := t.TempDir()
	verdictDir := filepath.Join(root, ".verdict")
	if err := os.MkdirAll(verdictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := "{\"schema_version\":\"1\"}\n"
	if err := os.WriteFile(filepath.Join(verdictDir, "config.json"), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(verdictDir, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewLayout(root), shared
}

// readIgnore reads back the nested ignore file.
func readIgnore(t *testing.T, layout Layout) string {
	t.Helper()
	raw, err := os.ReadFile(layout.GitignoreFile())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestEnsureStateIgnoreRepairs pins non-destructive repair: an existing
// ignore file keeps its lines and gains `/state/` exactly once.
func TestEnsureStateIgnoreRepairs(t *testing.T) {
	layout, _ := stateFixture(t)
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	if got := readIgnore(t, layout); got != "dist/\n/state/\n" {
		t.Fatalf("ignore = %q", got)
	}
}

// TestEnsureStateIgnorePreserves pins the shared-config contract:
// ensuring the ignore never mutates the tracked configuration beside it.
func TestEnsureStateIgnorePreserves(t *testing.T) {
	layout, shared := stateFixture(t)
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	kept, err := os.ReadFile(layout.ConfigFile())
	if err != nil || string(kept) != shared {
		t.Fatalf("shared config mutated: %q, err = %v", kept, err)
	}
}

// TestLayoutPaths pins the state inventory: the configuration, knowledge
// corpus, learnings, and overrides live beside the ignored state dir.
func TestLayoutPaths(t *testing.T) {
	layout := NewLayout("/repo")
	if layout.ConfigFile() != filepath.Join("/repo", ConfigPath) {
		t.Fatalf("config = %q", layout.ConfigFile())
	}
	if layout.StateDir() != filepath.Join("/repo", VerdictDir, StateDirName) {
		t.Fatalf("state = %q", layout.StateDir())
	}
	if layout.GitignoreFile() != filepath.Join("/repo", GitignorePath) {
		t.Fatalf("ignore = %q", layout.GitignoreFile())
	}
}
