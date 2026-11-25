//go:build !windows

package verify_test

// End-to-end proof of what a patch-path receipt treats as a clean tree: the
// real watchdog, the real git probes, and only verdict's own bundle state and
// generated .verdict/.gitignore left out of the status read.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/verify"
)

// TestVerifyPatchReceiptTreeScope pins the clean-tree rule against real git:
// the bundle's state and a first run's untracked .verdict/.gitignore leave the
// head bound, while reviewed input under .verdict/ (a regression test, the KB)
// marks the tree dirty and binds no head.
func TestVerifyPatchReceiptTreeScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		extra    string // a .verdict-relative file written untracked before verify
		wantHead bool
	}{
		{name: "state-and-generated-gitignore", extra: ".gitignore", wantHead: true},
		{name: "untracked-regression-test", extra: "tests/regress_test.go"},
		{name: "untracked-kb", extra: "kb.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkTreeScope(t, tc.extra, tc.wantHead) })
	}
}

// checkTreeScope verifies a committed repo carrying one untracked
// .verdict-relative file and checks whether the receipt bound the head.
func checkTreeScope(t *testing.T, extra string, wantHead bool) {
	t.Helper()
	root := t.TempDir()
	gitOrFatal(t, root, "init", "-q")
	gitOrFatal(t, root, "config", "user.email", "t@example.com")
	gitOrFatal(t, root, "config", "user.name", "t")
	writeTreeFile(t, root, "marker.txt", "root\n")
	gitOrFatal(t, root, "add", "marker.txt")
	gitOrFatal(t, root, "commit", "-q", "-m", "c")
	head := gitOrFatal(t, root, "rev-parse", "HEAD")
	writeTreeFile(t, root, ".verdict/state/5201/patch.json", `{"verifyCmd":"true"}`)
	writeTreeFile(t, root, ".verdict/"+extra, "x\n")
	receipt, warnings := runPatchVerify(t, root)
	want := ""
	if wantHead {
		want = head
	}
	if receipt.Head != want {
		t.Fatalf("receipt head = %q, want %q (warnings %q)", receipt.Head, want, warnings)
	}
	if !wantHead && !strings.Contains(warnings, "has uncommitted changes") {
		t.Errorf("warnings = %q, want the uncommitted-changes warning", warnings)
	}
}

func writeTreeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runPatchVerify runs the patch-path CLI at root and returns the receipt it
// wrote and the diagnostic stream.
func runPatchVerify(t *testing.T, root string) (core.VerifyReceipt, string) {
	t.Helper()
	t.Chdir(root)
	var out, diag strings.Builder
	r := &cli.Runner{
		Cmd: "verify", Repo: "acme/app", Pr: "5201",
		Out: &out, Diag: &diag, Human: &out,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	code := verify.Run(context.Background(), r, nil, verify.Request{
		Env:  map[string]string{"HOME": t.TempDir(), "PATH": os.Getenv("PATH")},
		Repo: "acme/app", Pr: "5201", Root: root, RepoRoot: root,
	})
	if code != 0 {
		t.Fatalf("verify exit = %d\nstdout: %s\nstderr: %s", code, out.String(), diag.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "state", "5201", "verify-receipt.json"))
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	var receipt core.VerifyReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt, diag.String()
}
