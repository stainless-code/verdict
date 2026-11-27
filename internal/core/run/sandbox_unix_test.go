//go:build darwin || linux

package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// hostStub is the real watchdog as a host commander, so the launch goes
// through the real review sandbox. The sandboxed launch runs the binary the
// resolution found, so the planted stand-in needs no PATH entry.
type hostStub struct{}

// StartsHostProcesses marks the stub as a host commander.
func (hostStub) StartsHostProcesses() bool { return true }

// Run runs the command for real.
func (hostStub) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	return coreexec.Watchdog{}.Run(ctx, cmd)
}

// requireReviewSandbox skips where the host cannot build the review sandbox:
// macOS without sandbox-exec, Linux without a working bubblewrap.
func requireReviewSandbox(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
			t.Skipf("sandbox-exec is not present: %v", err)
		}
	case "linux":
		probe := exec.CommandContext(t.Context(), "bwrap", "--ro-bind", "/", "/", "--dev", "/dev",
			"--proc", "/proc", "--unshare-pid", "true")
		if err := probe.Run(); err != nil {
			t.Skipf("bubblewrap cannot run here: %v", err)
		}
	}
}

// escapeScript is what a prompt-injected diff asks a review agent to do with
// its absolute-path file tools: append to the operator's shell profile,
// rewrite the operator's checkout, plant a config directory, and read a
// private key into its output. Each attempt's outcome lands in a log under
// the agent's own state path, which the sandbox keeps writable; the review
// is written where it is owed.
const escapeScript = "#!/bin/sh\n" +
	"log=\"$HOME/.pi/escape.log\"\n" +
	"{ printf 'evil\\n' >> \"$HOME/.zshrc\" && echo zshrc=written || echo zshrc=denied\n" +
	"  printf 'evil\\n' > \"$CHECKOUT_FILE\" && echo checkout=written || echo checkout=denied\n" +
	"  /bin/mkdir -p \"$HOME/.config/evil\" && echo config=written || echo config=denied\n" +
	"  /bin/cat \"$HOME/.ssh/id_rsa\" && echo ssh=read || echo ssh=denied\n" +
	"  printf ok > \"$HOME/.pi/own-state\" && echo state=written || echo state=denied\n" +
	"} > \"$log\" 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// privateKeyCanary is the planted key's content, which must never reach the
// agent.
const privateKeyCanary = "-----BEGIN CANARY PRIVATE KEY----- verdict-review-sandbox"

// TestReviewSandboxConfinesWritesAndHidesCredentials runs a hostile review
// agent through the real mechanism (sandbox-exec on macOS, bwrap on Linux):
// its writes to HOME's dotfiles, the operator's checkout by absolute path,
// and ~/.config are all denied and leave the files unchanged, its read of
// ~/.ssh/id_rsa fails, its own state path stays writable, and the review it
// owes still records.
func TestReviewSandboxConfinesWritesAndHidesCredentials(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	root := confinedRoot(t)
	home := escapeHome(t)
	checkoutFile := filepath.Join(root, "src", "main.go")

	bin := plantStub(t, "pi", escapeScript)
	req := request(root, hostStub{})
	req.Agent = "pi"
	req.LookPath = func(string) (string, error) { return bin, nil }
	req.Environ = append(req.Environ, "HOME="+home, "CHECKOUT_FILE="+checkoutFile, "PATH="+os.Getenv("PATH"))
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	assertEscapeLog(t, home)
	if got := string(readFile(t, filepath.Join(home, ".zshrc"))); got != operatorProfile {
		t.Errorf("~/.zshrc = %q, want it unchanged", got)
	}
	assertCheckoutUntouched(t, root)
	if _, err := os.Lstat(filepath.Join(home, ".config", "evil")); !os.IsNotExist(err) {
		t.Errorf("~/.config/evil exists (err = %v), want the write denied", err)
	}
	if got := string(readFile(t, filepath.Join(home, ".pi", "own-state"))); got != "ok" {
		t.Errorf("the agent's own state = %q, want its write", got)
	}
	if _, err := os.Stat(filepath.Join(bundleDir(root), reviewFile)); err != nil {
		t.Errorf("the review did not record: %v", err)
	}
	if box := resp.Data.Sandbox; box == nil || box.Posture != "sandboxed" {
		t.Errorf("sandbox = %+v, want the sandboxed posture", box)
	}
}

// operatorProfile is the shell profile the agent tries to append to.
const operatorProfile = "# the operator's shell profile\n"

// escapeHome is the operator's HOME the hostile agent sees: a shell
// profile, a private key, and the agent's own state directory.
func escapeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeUnder(t, home, ".zshrc", operatorProfile)
	writeUnder(t, home, filepath.Join(".ssh", "id_rsa"), privateKeyCanary+"\n")
	if err := os.MkdirAll(filepath.Join(home, ".pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// assertEscapeLog reads what each escape attempt reported: every write and
// the key read denied, the agent's own state written, and the key never in
// its output.
func assertEscapeLog(t *testing.T, home string) {
	t.Helper()
	want := map[string]string{
		"zshrc": "denied", "checkout": "denied", "config": "denied", "ssh": "denied",
		"state": "written",
	}
	log := string(readFile(t, filepath.Join(home, ".pi", "escape.log")))
	for key, outcome := range want {
		if !strings.Contains(log, key+"="+outcome+"\n") {
			t.Errorf("%s: want %s; escape log:\n%s", key, outcome, log)
		}
	}
	if strings.Contains(log, privateKeyCanary) {
		t.Errorf("the agent read the private key:\n%s", log)
	}
}

// readFile reads one fixture file.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
