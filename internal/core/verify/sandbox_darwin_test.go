//go:build darwin

package verifycore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// requireSandboxExec keeps the real-macOS legs honest: they run the shipped
// mechanism against the host, never a stub.
func requireSandboxExec(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(sandboxExecPath); err != nil {
		t.Skipf("%s is not present: %v", sandboxExecPath, err)
	}
}

// sandboxTestEnv is the child environment the real legs run with: the host
// PATH (so bash, mktemp, and go resolve) and the fake HOME under test.
func sandboxTestEnv(home string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
}

// runWrapped runs one command under the shipped sandbox and returns its
// captured result.
func runWrapped(t *testing.T, sb *sandbox, env []string, root, vcmd string) core.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := (coreexec.Watchdog{}).Run(ctx, &core.Command{
		Argv:      sb.argv(vcmd),
		Env:       sb.childEnv(env),
		Dir:       root,
		OutputCap: sandboxProbeCap,
	})
	if err != nil {
		t.Fatalf("run %q: %v", vcmd, err)
	}
	return res
}

// TestSandboxExecHidesCredentialsAndHostTemp proves the macOS profile against
// the real Seatbelt engine: credential reads and host /tmp reads/writes fail,
// while the reviewed root and the per-run private TMPDIR remain usable.
func TestSandboxExecHidesCredentialsAndHostTemp(t *testing.T) {
	requireSandboxExec(t)
	root, home, hostTmp, sibling := sandboxExecHostFixture(t)
	env := sandboxTestEnv(home)
	sb, private := prepareHostSandbox(t, root, env)
	defer sb.close()
	checkSandboxedCommands(t, sb, env, root, hostTmp, sibling)
	requireDeniedWithErrno(t, sb, env, root, `cat "$HOME/.ssh/id_rsa"`)
	requireDeniedWithErrno(t, sb, env, root, `cat "$HOME/.aws/credentials"`)
	requireDeniedWithErrno(t, sb, env, root, `cat "`+sibling+`"`)
	requireDeniedWithErrno(t, sb, env, root, `( printf x > "`+hostTmp+`/errno.txt" )`)
	requireHostTmpUntouched(t, hostTmp)
	assertGoRunsUnderSeatbelt(t, sb, env, root)
	sb.close()
	assertPathGone(t, private)
}

// sandboxExecHostFixture builds the fake HOME under the reviewed root (so the
// credential denials, not the temp deny, are what hides them) plus one real
// sibling directory in the shared host /tmp.
func sandboxExecHostFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeSandboxFile(t, filepath.Join(home, ".ssh", "id_rsa"), "id-rsa-secret")
	writeSandboxFile(t, filepath.Join(home, ".aws", "credentials"), "aws-secret")
	writeSandboxFile(t, filepath.Join(home, "visible.txt"), "visible")
	writeSandboxFile(t, filepath.Join(root, "readable.txt"), "readable")
	//nolint:usetesting // the leg must deny the real shared /tmp, not t.TempDir()'s per-test dir.
	hostTmp, err := os.MkdirTemp("/private/tmp", "verdict-sandbox-test-")
	if err != nil {
		t.Fatalf("host /private/tmp is required by this leg: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostTmp) })
	sibling := filepath.Join(hostTmp, "sibling.txt")
	writeSandboxFile(t, sibling, "sibling-secret")
	return root, home, hostTmp, sibling
}

// prepareHostSandbox prepares the shipped mechanism against the real host and
// returns the private TMPDIR it created.
func prepareHostSandbox(t *testing.T, root string, env []string) (*sandbox, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sb, ref := prepareSandbox(ctx, coreexec.Watchdog{}, root, root, env, false)
	if ref != nil {
		t.Fatalf("prepareSandbox: %v", ref)
	}
	return sb, strings.TrimPrefix(sb.env[0], "TMPDIR=")
}

// checkSandboxedCommands runs the denial and allowance legs under Seatbelt.
func checkSandboxedCommands(t *testing.T, sb *sandbox, env []string, root, hostTmp, sibling string) {
	t.Helper()
	for _, tc := range []struct {
		name string
		cmd  string
		want string
	}{
		{"ssh key read is denied", `cat "$HOME/.ssh/id_rsa" 2>/dev/null && printf LEAK || printf DENIED`, "DENIED"},
		{"aws credential read is denied", `cat "$HOME/.aws/credentials" 2>/dev/null && printf LEAK || printf DENIED`, "DENIED"},
		{"host /tmp sibling read is denied", `cat "` + sibling + `" 2>/dev/null && printf LEAK || printf DENIED`, "DENIED"},
		{"host /tmp write is denied", `( printf x > "` + hostTmp + `/written.txt" ) 2>/dev/null && printf WROTE || printf DENIED`, "DENIED"},
		{"private TMPDIR write and read succeed", `printf ok > "$TMPDIR/private.txt" && cat "$TMPDIR/private.txt"`, "ok"},
		{"mktemp in the private TMPDIR succeeds", `p=$(mktemp "$TMPDIR/m.XXXXXX") && printf ok > "$p" && test -s "$p" && printf MKTEMP`, "MKTEMP"},
		{"the reviewed root is readable", `cat readable.txt`, "readable"},
		{"the fake HOME stays readable outside credentials", `cat "$HOME/visible.txt"`, "visible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runWrapped(t, sb, env, root, tc.cmd)
			if got := strings.TrimSpace(res.Stdout + res.Stderr); got != tc.want {
				t.Errorf("%q = %q (exit %d), want %q", tc.cmd, got, res.ExitCode, tc.want)
			}
		})
	}
}

// requireDeniedWithErrno proves the denial is the kernel's EPERM/EACCES, not a
// missing file or a broken command.
func requireDeniedWithErrno(t *testing.T, sb *sandbox, env []string, root, vcmd string) {
	t.Helper()
	res := runWrapped(t, sb, env, root, vcmd)
	out := strings.ToLower(res.Stdout + res.Stderr)
	if res.ExitCode == 0 {
		t.Errorf("%q exited 0, want the sandbox to deny it", vcmd)
	}
	if !strings.Contains(out, "not permitted") && !strings.Contains(out, "permission denied") {
		t.Errorf("%q = %q, want EPERM/EACCES", vcmd, res.Stdout+res.Stderr)
	}
}

// requireHostTmpUntouched proves the denied write landed nowhere and the host
// sibling survived.
func requireHostTmpUntouched(t *testing.T, hostTmp string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(hostTmp, "written.txt")); !os.IsNotExist(err) {
		t.Error("the sandboxed command wrote into the host /tmp")
	}
	if _, err := os.Stat(filepath.Join(hostTmp, "sibling.txt")); err != nil {
		t.Errorf("the host sibling disappeared: %v", err)
	}
}

// assertPathGone proves a per-run path was removed.
func assertPathGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%q survived close (%v)", path, err)
	}
}

// assertGoRunsUnderSeatbelt keeps the profile from breaking the toolchain the
// reviewed repo runs: `go version` must exit 0 inside the sandbox.
func assertGoRunsUnderSeatbelt(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Log("go is not installed; skipping the toolchain leg")
		return
	}
	res := runWrapped(t, sb, env, root, "go version")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout+res.Stderr, "go version") {
		t.Errorf("go version under the sandbox = exit %d %q, want a version line", res.ExitCode, res.Stdout+res.Stderr)
	}
}

// TestSandboxExecPrivateTmpInsideDeniedHostTmp proves precedence: a private
// directory under the denied /private/tmp is allowed by the later rule.
func TestSandboxExecPrivateTmpInsideDeniedHostTmp(t *testing.T) {
	requireSandboxExec(t)
	home := t.TempDir()
	root := t.TempDir()
	env := sandboxTestEnv(home)
	sb, ref := sandboxExecMechanism(root, env, "/private/tmp")
	if ref != nil {
		t.Fatalf("sandboxExecMechanism: %v", ref)
	}
	defer sb.close()
	res := runWrapped(t, sb, env, root, `printf ok > "$TMPDIR/x" && cat "$TMPDIR/x"`)
	if got := strings.TrimSpace(res.Stdout + res.Stderr); got != "ok" {
		t.Errorf("private TMPDIR under /private/tmp = %q (exit %d), want ok", got, res.ExitCode)
	}
}

// TestSandboxExecEscapesAdversarialPaths proves a quote in HOME or the root
// is escaped into a working profile, while a newline refuses before any
// profile is built.
func TestSandboxExecEscapesAdversarialPaths(t *testing.T) {
	requireSandboxExec(t)
	base := t.TempDir()
	root := filepath.Join(base, `re"po`)
	home := filepath.Join(root, `ho"me`)
	writeSandboxFile(t, filepath.Join(home, ".ssh", "id_rsa"), "id-rsa-secret")
	writeSandboxFile(t, filepath.Join(home, "visible.txt"), "visible")
	writeSandboxFile(t, filepath.Join(root, "readable.txt"), "readable")
	env := sandboxTestEnv(home)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sb, ref := prepareSandbox(ctx, coreexec.Watchdog{}, root, root, env, false)
	if ref != nil {
		t.Fatalf("prepareSandbox with quoted paths: %v", ref)
	}
	defer sb.close()
	for _, tc := range []struct{ cmd, want string }{
		{`cat "$HOME/.ssh/id_rsa" 2>/dev/null && printf LEAK || printf DENIED`, "DENIED"},
		{`cat readable.txt`, "readable"},
	} {
		res := runWrapped(t, sb, env, root, tc.cmd)
		if got := strings.TrimSpace(res.Stdout + res.Stderr); got != tc.want {
			t.Errorf("%q = %q (exit %d), want %q", tc.cmd, got, res.ExitCode, tc.want)
		}
	}

	if _, ref := sandboxExecMechanism(root, []string{"HOME=" + home + "\n"}, ""); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "control character") {
		t.Errorf("newline HOME ref = %v, want a control-character refusal", ref)
	}
}

func writeSandboxFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
