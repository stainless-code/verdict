package verdict_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// okCommander answers every spawn with a clean exit, so a sandboxed run's
// probe and command both succeed without touching the host.
type okCommander struct{ calls int }

func (c *okCommander) Run(context.Context, *verdict.Command) (verdict.CommandResult, error) {
	c.calls++
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

// TestSDKVerifyReportsSandboxMechanism proves the response names the wrapper
// the host enforced, not only the request flag.
func TestSDKVerifyReportsSandboxMechanism(t *testing.T) {
	want := map[string]string{"linux": "bwrap", "darwin": "sandbox-exec"}[runtime.GOOS]
	if want == "" {
		t.Skipf("%s has no enforced sandbox mechanism", runtime.GOOS)
	}
	root := t.TempDir()
	t.Chdir(root)
	dir := filepath.Join(root, ".verdict", "state", "5201")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "patch.json"), []byte(`{"verifyCmd":"printf ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Linux refuses a root with no `.git`; the mechanism is what this pins.
	if out, err := exec.CommandContext(t.Context(), "git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }, Commander: &okCommander{}})
	defer c.Close()
	resp, err := c.Verify(context.Background(), &verdict.VerifyRequest{
		Repo: "acme/app", PR: "5201", Timeout: "10", Sandbox: true,
		Env: []string{"PATH=/bin", "HOME=" + t.TempDir()},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if resp.SandboxMechanism != want {
		t.Errorf("SandboxMechanism = %q, want %q", resp.SandboxMechanism, want)
	}
}
