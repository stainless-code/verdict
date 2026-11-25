package verdict_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testkit"
	"github.com/stainless-code/verdict/pkg/verdict"
)

// okCommander answers every spawn with a clean exit, so a sandboxed run's
// probe and command both succeed without touching the host.
type okCommander struct {
	argvs [][]string
	calls int
}

func (c *okCommander) Run(_ context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	c.calls++
	c.argvs = append(c.argvs, cmd.Argv)
	return verdict.CommandResult{Stdout: "ok"}, nil
}

// TestSDKVerifyForwardsBothSandboxFlags proves the facade carries both opt-ins
// to the service: together they refuse bad-flags before anything spawns,
// which only happens when SandboxNetworkOnly reaches the core request.
func TestSDKVerifyForwardsBothSandboxFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	cmdr := &okCommander{}
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }, Commander: cmdr})
	defer c.Close()
	_, err := c.Verify(context.Background(), &verdict.VerifyRequest{
		Repo: "acme/app", PR: "5201", Timeout: "10", Sandbox: true, SandboxNetworkOnly: true,
	})
	refusal, ok := errors.AsType[*verdict.RefusalError](err)
	if !ok || refusal.Code != "bad-flags" || !strings.Contains(refusal.Message, "--sandbox-network-only") {
		t.Fatalf("err = %v, want the bad-flags exclusivity refusal", err)
	}
	if cmdr.calls != 0 {
		t.Errorf("spawns = %d, want none", cmdr.calls)
	}
}

// gitPlayingCommander plays git for verify's snapshot (sdkGitAnswer) and
// answers every other spawn — the sandbox probe, the command — with a clean
// exit, counting them.
type gitPlayingCommander struct{ ran int }

func (c *gitPlayingCommander) Run(_ context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	if res, ok := sdkGitAnswer(cmd); ok {
		return res, nil
	}
	c.ran++
	return verdict.CommandResult{Stdout: "ok"}, nil
}

// sandboxBundle makes a checkout (a .git the facade's root lookup accepts)
// holding a bundle whose patch.json names a passing command.
func sandboxBundle(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, filepath.Join(root, ".verdict", "state", "5201", "patch.json"), `{"verifyCmd":"printf ok"}`)
	testkit.WriteFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	testkit.WriteFile(t, filepath.Join(root, ".git", "objects", ".keep"), "")
	testkit.WriteFile(t, filepath.Join(root, ".git", "refs", ".keep"), "")
}

func sandboxedVerify(c *verdict.Client, home string) (*verdict.VerifyResponse, error) {
	return c.Verify(context.Background(), &verdict.VerifyRequest{
		Repo: "acme/app", PR: "5201", Timeout: "10", Sandbox: true,
		Env: []string{"PATH=/bin", "HOME=" + home},
	})
}

// TestSDKVerifyReportsSandboxMechanism proves the response names the wrapper
// the host enforced, not only the request flag, for a run in a snapshot.
func TestSDKVerifyReportsSandboxMechanism(t *testing.T) {
	want := map[string]string{"linux": "bwrap", "darwin": "sandbox-exec"}[runtime.GOOS]
	if want == "" {
		t.Skipf("%s has no enforced sandbox mechanism", runtime.GOOS)
	}
	sandboxBundle(t)
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }, Commander: &gitPlayingCommander{}})
	defer c.Close()
	resp, err := sandboxedVerify(c, t.TempDir())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if resp.SandboxMechanism != want || resp.Tree != scriptTree {
		t.Errorf("SandboxMechanism = %q tree %q, want %q in the snapshot of %s", resp.SandboxMechanism, resp.Tree,
			want, scriptTree)
	}
}

// TestSDKVerifyRefusesWithoutASnapshot proves a commander that is not git —
// okCommander answers every read with "ok", which names no tree — leaves
// verify no snapshot, so it refuses and runs nothing, instead of running the
// command in the checkout.
func TestSDKVerifyRefusesWithoutASnapshot(t *testing.T) {
	sandboxBundle(t)
	cmdr := &okCommander{}
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }, Commander: cmdr})
	defer c.Close()
	_, err := sandboxedVerify(c, t.TempDir())
	refusal, ok := errors.AsType[*verdict.RefusalError](err)
	if !ok || refusal.Code != "verify-failed" || !strings.Contains(refusal.Message, "could not snapshot the content") {
		t.Fatalf("err = %v, want verify-failed naming the snapshot", err)
	}
	for _, argv := range cmdr.argvs {
		if argv[0] != "git" {
			t.Errorf("a non-git command ran without a snapshot: %q", argv)
		}
	}
}
