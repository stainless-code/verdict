package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain scrubs the git environment the launcher exported.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

func gitRun(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required: the trusted-base read proves against a real commit")
	}
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "config",
		"GIT_AUTHOR_EMAIL":    "config@example.invalid",
		"GIT_COMMITTER_NAME":  "config",
		"GIT_COMMITTER_EMAIL": "config@example.invalid",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// baseRepo commits files (force-added) in a fresh repo, chdirs into it, and
// returns the base commit.
func baseRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	gitRun(t, "init", "-q")
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, "add", "-f", "-A")
	gitRun(t, "commit", "-qm", "base")
	return gitRun(t, "rev-parse", "HEAD")
}

var cmdr = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}

// TestReadBaseReadsBaseNotHead pins the trusted read: a head edit to the
// configuration leaves the snapshot at the base bytes.
func TestReadBaseReadsBaseNotHead(t *testing.T) {
	base := baseRepo(t, map[string]string{".verdict/config.json": "{\"schema_version\":\"1\"}\n"})
	if err := os.WriteFile(".verdict/config.json", []byte("{\"schema_version\":\"2\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, ref := ReadBase(t.Context(), cmdr, "", base)
	if ref != nil {
		t.Fatalf("ReadBase: %v", ref)
	}
	if !found.Present || string(found.Raw) != "{\"schema_version\":\"1\"}\n" {
		t.Fatalf("found = %+v", found)
	}
	cfg, ref := ParseBase(found)
	if ref != nil {
		t.Fatalf("ParseBase: %v", ref)
	}
	if cfg == nil || cfg.SchemaVersion != "1" {
		t.Fatalf("config = %+v", cfg)
	}
}

// TestReadBaseAbsent pins safe no-policy handling: a repository without
// base configuration reads as absent, never as an error.
func TestReadBaseAbsent(t *testing.T) {
	base := baseRepo(t, map[string]string{"README.md": "hi\n"})
	found, ref := ReadBase(t.Context(), cmdr, "", base)
	if ref != nil {
		t.Fatalf("ReadBase: %v", ref)
	}
	if found == nil || found.Present {
		t.Fatalf("found = %+v", found)
	}
	cfg, ref := ParseBase(found)
	if ref != nil {
		t.Fatalf("ParseBase: %v", ref)
	}
	if cfg != nil {
		t.Fatalf("config = %+v, want nil without base policy", cfg)
	}
}

// TestReadBaseBadBase pins the trusted-base gate: a non-SHA refuses
// without touching the network or the worktree.
func TestReadBaseBadBase(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, ref := ReadBase(t.Context(), cmdr, "", "not-a-sha"); ref == nil {
		t.Fatal("want a refusal for a non-SHA base")
	} else if ref.Code != "controls-unavailable" && ref.Code != "controls-too-large" {
		t.Fatalf("code = %q", ref.Code)
	}
}
