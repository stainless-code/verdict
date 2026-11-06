//go:build !windows

package verdict_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/pkg/verdict"
)

func sdkGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupSDKGroupRepo builds a committed repository and the verified bundle
// used by both group-validation entrypoint tests.
func setupSDKGroupRepo(t *testing.T) (root, head, dir string) {
	t.Helper()
	root = t.TempDir()
	sdkGit(t, root, "init", "-q")
	sdkGit(t, root, "config", "user.email", "t@example.com")
	sdkGit(t, root, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sdkGit(t, root, "add", "marker.txt")
	sdkGit(t, root, "commit", "-q", "-m", "c")
	head = sdkGit(t, root, "rev-parse", "HEAD")
	dir = filepath.Join(root, ".verdict", "state", "5201")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"controls.json": `{"schema_version":"1","policy":"head","base_sha":"` + head + `","sha":"","files":[]}`,
		"PR.json":       `{"headRefOid":"` + head + `","state":"OPEN"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	maxLenses := 1
	rs := &config.RunState{BaseSHA: head, Trusted: true, Resolved: &config.Resolved{Plan: config.Plan{
		Status: config.PendingStatus,
		Groups: []config.PlanGroup{{ID: "group-a1", Label: "deep", Profile: "deep", Depth: "deep", MaxLenses: &maxLenses}},
	}}}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	t.Chdir(root)
	return root, head, dir
}

// TestSDKVerifyGroupRunsWithoutPatch proves the public SDK entrypoint: the
// optional Group/Command pair executes the explicit command at the reviewed
// root with the real watchdog, records a receipt bound to the real head, and
// never fabricates patch.json.
func TestSDKVerifyGroupRunsWithoutPatch(t *testing.T) {
	dir, head, bundle := setupSDKGroupRepo(t)
	_ = dir
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }})
	defer c.Close()
	resp, err := c.Verify(context.Background(), &verdict.VerifyRequest{
		Repo: "acme/app", PR: "5201",
		Timeout: "30", Group: "group-a1", Command: "cat marker.txt",
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if resp.Exit != 0 || resp.TimedOut || resp.Group != "group-a1" || resp.Head != head || resp.Receipt == "" {
		t.Fatalf("resp = %+v", resp)
	}
	requireSDKReceipt(t, bundle, resp.Receipt)
}

// requireSDKReceipt asserts no patch was fabricated and the receipt records a
// pass bound to the group.
func requireSDKReceipt(t *testing.T, bundle, receiptPath string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(bundle, "patch.json")); !os.IsNotExist(err) {
		t.Fatalf("SDK group mode fabricated patch.json")
	}
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if !strings.Contains(string(raw), `"status": "passed"`) || !strings.Contains(string(raw), `"group": "group-a1"`) {
		t.Fatalf("receipt = %s", raw)
	}
}

// TestSDKVerifyGroupBothOrNeither proves the SDK surface cannot half-specify
// deep validation: group alone refuses before anything executes.
func TestSDKVerifyGroupBothOrNeither(t *testing.T) {
	t.Chdir(t.TempDir())
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }})
	defer c.Close()
	_, err := c.Verify(context.Background(), &verdict.VerifyRequest{
		Repo: "acme/app", PR: "5201",
		Timeout: "10", Group: "group-a1",
	})
	if err == nil {
		t.Fatal("group without command was accepted")
	}
	refusal, ok := errors.AsType[*verdict.RefusalError](err)
	if !ok || refusal.Code != "bad-group-validation" {
		t.Fatalf("err = %v, want bad-group-validation", err)
	}
}

// TestVerifyGroupForwardsDirToInjectedCommander proves the public Commander
// seam keeps the working directory: a custom runner must see Dir set to the
// canonical reviewed root, or a deep command would silently run in the
// caller's directory while the receipt certifies the root.
func TestVerifyGroupForwardsDirToInjectedCommander(t *testing.T) {
	root, head, _ := setupSDKGroupRepo(t)
	cmdr := &dirCommander{head: head}
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }, Commander: cmdr})
	defer c.Close()
	if _, err := c.Verify(context.Background(), &verdict.VerifyRequest{
		Repo: "acme/app", PR: "5201",
		Timeout: "30", Group: "group-a1", Command: "true",
	}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	requireCommanderDir(t, root, cmdr)
}

// requireCommanderDir asserts a custom runner saw the canonical reviewed root,
// not the caller's directory or an empty Dir.
func requireCommanderDir(t *testing.T, root string, cmdr *dirCommander) {
	t.Helper()
	if cmdr.commandDir == "" {
		t.Fatalf("injected Commander saw an empty Dir; deep command would run in the caller's cwd")
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if cmdr.commandDir != want {
		t.Fatalf("command Dir = %q, want canonical root %q", cmdr.commandDir, want)
	}
}

// dirCommander answers the head probe and records the command Dir.
type dirCommander struct {
	head       string
	commandDir string
}

func (c *dirCommander) Run(_ context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	if len(cmd.Argv) > 3 && cmd.Argv[0] == "git" {
		return verdict.CommandResult{Stdout: c.head + "\n"}, nil
	}
	c.commandDir = cmd.Dir
	return verdict.CommandResult{}, nil
}
