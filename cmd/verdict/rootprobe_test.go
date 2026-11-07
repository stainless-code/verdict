package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInitRepo seeds a real checkout with a nested subdirectory to invoke from.
func gitInitRepo(t *testing.T) (root, sub string) {
	t.Helper()
	root = t.TempDir()
	git := exec.CommandContext(t.Context(), "git", "init", "-q")
	git.Dir = root
	git.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sub = filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, sub
}

// assertNoSubdirVerdict checks the resolved root was honored: nothing may be
// written under the invocation directory.
func assertNoSubdirVerdict(t *testing.T, sub string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(sub, ".verdict")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat sub/.verdict = %v, want fs.ErrNotExist", err)
	}
}

// TestCheckedOutSubdirScaffoldsAtRoot pins the resolved-root contract: from a
// real repository subdirectory init writes the scaffold at the Git root and
// nothing under the subdirectory.
func TestCheckedOutSubdirScaffoldsAtRoot(t *testing.T) {
	hermeticEnv(t)
	root, sub := gitInitRepo(t)
	t.Chdir(sub)
	if got := runOne(t, "init"); got.code != 0 {
		t.Fatalf("init from a subdirectory = %+v; want success", got)
	}
	for _, name := range []string{"config.json", "kb.jsonl", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, ".verdict", name)); err != nil {
			t.Fatalf("scaffold missing at the repository root: %s: %v", name, err)
		}
	}
	assertNoSubdirVerdict(t, sub)
}

// TestSubdirInitSeesExistingRootConfig pins that the root scaffold is the one
// inspected: with a config already at the Git root, a subdirectory init must
// ask for consent instead of previewing "create" and overwriting it blind.
func TestSubdirInitSeesExistingRootConfig(t *testing.T) {
	hermeticEnv(t)
	root, sub := gitInitRepo(t)
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, ".verdict", "config.json")
	if err := os.WriteFile(existing, []byte(`{"schema_version":"1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got := runOne(t, "init"); got.code == 0 {
		t.Fatalf("init from a subdirectory = %+v; want a consent refusal for the existing root config", got)
	}
	body, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != `{"schema_version":"1"}` {
		t.Fatalf("root config changed without consent: %s", body)
	}
}
