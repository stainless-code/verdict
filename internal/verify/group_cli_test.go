//go:build !windows

// Platform note: these proofs use the product's documented POSIX execution
// environment (bash, pwd/cat, git). Cross-building for Windows is checked
// separately; the behavior itself is not asserted on a Windows runtime.

package verify_test

// End-to-end CLI adapter proof: `verify --group --command` runs a deep
// validation with no patch.json, through the real watchdog commander, at the
// canonical reviewed root, and records a receipt bound to the real git head.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/verify"
)

func gitOrFatal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupCLIGroupRepo builds a committed repository, the verified bundle, and
// returns the root, head, and bundle dir.
func setupCLIGroupRepo(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	gitOrFatal(t, root, "init", "-q")
	gitOrFatal(t, root, "config", "user.email", "t@example.com")
	gitOrFatal(t, root, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOrFatal(t, root, "add", "marker.txt")
	gitOrFatal(t, root, "commit", "-q", "-m", "c")
	head := gitOrFatal(t, root, "rev-parse", "HEAD")
	dir := filepath.Join(root, ".verdict", "state", "5201")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	controls := `{"schema_version":"1","policy":"head","base_sha":"` + head + `","sha":"","files":[]}`
	if err := os.WriteFile(filepath.Join(dir, "controls.json"), []byte(controls), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"headRefOid":"`+head+`","state":"OPEN"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	maxLenses := 1
	rs := &config.RunState{BaseSHA: head, Trusted: true, Resolved: &config.Resolved{Plan: config.Plan{
		Status: config.PendingStatus,
		Groups: []config.PlanGroup{{ID: "group-a1", Label: "deep", Profile: "deep", Depth: "deep", MaxLenses: &maxLenses}},
	}}}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	return root, head, dir
}

// TestVerifyGroupCLIWithoutPatch drives the CLI adapter end to end: no patch
// exists, the real watchdog runs the command at the canonical root, and the
// receipt binds the real head.
func TestVerifyGroupCLIWithoutPatch(t *testing.T) {
	root, head, dir := setupCLIGroupRepo(t)
	// The caller sits in a subdirectory while the flags name the root: the
	// command must still run at the canonical root (it reads marker.txt).
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	var out, diag strings.Builder
	r := &cli.Runner{
		Cmd: "verify", Repo: "acme/app", Pr: "5201",
		Out: &out, Diag: &diag, Human: &out,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	code := verify.Run(context.Background(), r, []string{"--group", "group-a1", "--command", "cat marker.txt"}, verify.Request{
		Env:  map[string]string{"HOME": t.TempDir(), "PATH": os.Getenv("PATH")},
		Repo: "acme/app", Pr: "5201", Root: root, RepoRoot: root,
	})
	if code != 0 {
		t.Fatalf("verify exit = %d\nstdout: %s\nstderr: %s", code, out.String(), diag.String())
	}
	requireGroupReceipt(t, root, dir, head)
}

// requireGroupReceipt asserts the recorded receipt binds the real head, the
// canonical root, and the run, and that no patch.json was fabricated.
func requireGroupReceipt(t *testing.T, root, dir, head string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "patch.json")); !os.IsNotExist(err) {
		t.Fatalf("group mode fabricated patch.json")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "validation-group-a1.json"))
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	var receipt core.ValidationReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "passed" || !receipt.Passed || receipt.Exit != 0 ||
		receipt.ExecutionHead != head || receipt.ReviewedHead != head ||
		receipt.RepoRoot != want || receipt.Run != "5201" {
		t.Fatalf("receipt = %+v", receipt)
	}
}
