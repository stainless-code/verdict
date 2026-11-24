//go:build darwin

package verifycore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
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
	defer closeSandbox(t, sb)
	checkSandboxedCommands(t, sb, env, root, hostTmp, sibling)
	requireDeniedWithErrno(t, sb, env, root, `cat "$HOME/.ssh/id_rsa"`)
	requireDeniedWithErrno(t, sb, env, root, `cat "$HOME/.aws/credentials"`)
	requireDeniedWithErrno(t, sb, env, root, `cat "`+sibling+`"`)
	requireDeniedWithErrno(t, sb, env, root, `( printf x > "`+hostTmp+`/errno.txt" )`)
	requireHostTmpUntouched(t, hostTmp)
	assertGoRunsUnderSeatbelt(t, sb, env, root)
	closeSandbox(t, sb)
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
	sb, ref := prepareSandbox(ctx, coreexec.Watchdog{}, root, root, sandboxInputs{childEnv: env}, false)
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
// reviewed repo runs: with every write outside the root and the private tree
// denied, `go build` must still succeed through the GOCACHE and GOTMPDIR the
// wrapper points into the private directory.
func assertGoRunsUnderSeatbelt(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Log("go is not installed; skipping the toolchain leg")
		return
	}
	writeSandboxFile(t, filepath.Join(root, "gomod", "go.mod"), "module sandboxprobe\n\ngo 1.21\n")
	writeSandboxFile(t, filepath.Join(root, "gomod", "main.go"), "package main\n\nfunc main() { println(\"built\") }\n")
	res := runWrapped(t, sb, env, root, `cd gomod && go build -o probe-bin . && ./probe-bin`)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout+res.Stderr, "built") {
		t.Errorf("go build under the sandbox = exit %d %q, want a built binary", res.ExitCode, res.Stdout+res.Stderr)
	}
}

// TestSandboxExecPrivateTmpInsideDeniedHostTmp proves precedence: a private
// directory under the denied /private/tmp is allowed by the later rule.
func TestSandboxExecPrivateTmpInsideDeniedHostTmp(t *testing.T) {
	requireSandboxExec(t)
	home := t.TempDir()
	root := t.TempDir()
	env := sandboxTestEnv(home)
	sb, ref := sandboxExecMechanism(root, sandboxInputs{childEnv: env}, "/private/tmp")
	if ref != nil {
		t.Fatalf("sandboxExecMechanism: %v", ref)
	}
	defer closeSandbox(t, sb)
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
	sb, ref := prepareSandbox(ctx, coreexec.Watchdog{}, root, root, sandboxInputs{childEnv: env}, false)
	if ref != nil {
		t.Fatalf("prepareSandbox with quoted paths: %v", ref)
	}
	defer closeSandbox(t, sb)
	for _, tc := range []struct{ cmd, want string }{
		{`cat "$HOME/.ssh/id_rsa" 2>/dev/null && printf LEAK || printf DENIED`, "DENIED"},
		{`cat readable.txt`, "readable"},
	} {
		res := runWrapped(t, sb, env, root, tc.cmd)
		if got := strings.TrimSpace(res.Stdout + res.Stderr); got != tc.want {
			t.Errorf("%q = %q (exit %d), want %q", tc.cmd, got, res.ExitCode, tc.want)
		}
	}

	if _, ref := sandboxExecMechanism(root, homeInputs(home+"\n"), ""); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "control character") {
		t.Errorf("newline HOME ref = %v, want a control-character refusal", ref)
	}
}

// TestSandboxExecDeniesKeychainAndPersistence proves the two macOS escapes a
// read-deny profile leaves open are closed against the real Seatbelt engine:
// the keychain broker is unreachable, and a write anywhere outside the
// reviewed root and the private tree — a LaunchAgent, a shell rc file, a
// sibling directory — fails, while a write inside the root still succeeds.
// The fake HOME and the sibling live outside every denied temp root (the
// mechanism's host temp root is a separate directory), so only the keychain
// and write rules can be what stops them.
func TestSandboxExecDeniesKeychainAndPersistence(t *testing.T) {
	requireSandboxExec(t)
	root, home, sibling := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := sandboxTestEnv(home)
	sb, ref := sandboxExecMechanism(root, sandboxInputs{childEnv: env}, t.TempDir())
	if ref != nil {
		t.Fatalf("sandboxExecMechanism: %v", ref)
	}
	defer closeSandbox(t, sb)
	assertKeychainUnreachable(t, sb, env, root)
	assertPersistenceDenied(t, sb, env, root, home, sibling)
	res := runWrapped(t, sb, env, root, `printf inside > inside.txt && cat inside.txt`)
	if got := strings.TrimSpace(res.Stdout + res.Stderr); res.ExitCode != 0 || got != "inside" {
		t.Errorf("write inside the root = exit %d %q, want inside", res.ExitCode, got)
	}
}

// assertPersistenceDenied proves each write outside the root fails with the
// kernel's EPERM and lands nowhere.
func assertPersistenceDenied(t *testing.T, sb *sandbox, env []string, root, home, sibling string) {
	t.Helper()
	for _, tc := range []struct{ name, cmd, path string }{
		{
			"LaunchAgent plist", `printf x > "$HOME/Library/LaunchAgents/dev.verdict.probe.plist"`,
			filepath.Join(home, "Library", "LaunchAgents", "dev.verdict.probe.plist"),
		},
		{"shell rc file", `printf x >> "$HOME/.zshrc"`, filepath.Join(home, ".zshrc")},
		{"sibling outside the root", `printf x > "` + sibling + `/outside.txt"`, filepath.Join(sibling, "outside.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireDeniedWithErrno(t, sb, env, root, tc.cmd)
			if _, err := os.Stat(tc.path); !os.IsNotExist(err) {
				t.Errorf("%s exists after the denied write (%v)", tc.path, err)
			}
		})
	}
}

// assertKeychainUnreachable stores a sentinel in a throwaway keychain file
// inside the reviewed root — readable by path, so only the keychain broker
// denial can stop the read — proves the unsandboxed control reads it, and
// proves the sandboxed read does not.
func assertKeychainUnreachable(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	const sentinel = "verdict-keychain-sentinel"
	keychain := filepath.Join(root, "probe.keychain-db")
	security := func(args ...string) string {
		t.Helper()
		out, err := hostCmd(t.Context(), "/usr/bin/security", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("security %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	security("create-keychain", "-p", "verdict-probe", keychain)
	t.Cleanup(func() {
		// The test context is already canceled when cleanup runs.
		if out, err := hostCmd(context.Background(), "/usr/bin/security", "delete-keychain", keychain).CombinedOutput(); err != nil {
			t.Errorf("security delete-keychain: %v: %s", err, out)
		}
	})
	security("add-generic-password", "-s", "verdict-sandbox-probe", "-a", "probe", "-w", sentinel, keychain)
	if got := security("find-generic-password", "-s", "verdict-sandbox-probe", "-w", keychain); got != sentinel {
		t.Fatalf("unsandboxed control read %q, want the sentinel", got)
	}
	res := runWrapped(t, sb, env, root, `security find-generic-password -s verdict-sandbox-probe -w "`+keychain+`"`)
	if res.ExitCode == 0 || strings.Contains(res.Stdout+res.Stderr, sentinel) {
		t.Errorf("sandboxed keychain read = exit %d %q, want the broker denied", res.ExitCode, res.Stdout+res.Stderr)
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

// TestSandboxExecDeniesGitControlWrites proves the git control denials against
// the real Seatbelt engine in a real repository: the hooks, config, and info
// trees and a new nested `.git` refuse writes, while `git status`, `git
// diff`, and an ordinary write in the working tree still work.
func TestSandboxExecDeniesGitControlWrites(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sandboxGitRepo(t, root)
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	for _, cmd := range []string{
		`printf '#!/bin/sh\n' > .git/hooks/pre-commit`,
		`printf '[core]\n\tfsmonitor = x\n' >> .git/config`,
		`printf x > .git/info/exclude`,
		`mkdir -p nested/.git`,
		`printf 'gitdir: /x\n' > nested.git-pointer && mv nested.git-pointer .GIT`,
	} {
		requireDeniedWithErrno(t, sb, env, root, cmd)
	}
	assertGitStillWorks(t, sb, env, root)
}

// TestSandboxExecDeniesSeparateGitDirWrites proves a `.git` file is followed:
// the git directory it names sits under the root without a `.git` name, and
// its hooks still refuse writes.
func TestSandboxExecDeniesSeparateGitDirWrites(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sandboxGitRepo(t, root, "--separate-git-dir="+filepath.Join(root, "meta"))
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	requireDeniedWithErrno(t, sb, env, root, `printf '#!/bin/sh\n' > meta/hooks/pre-commit`)
	requireDeniedWithErrno(t, sb, env, root, `printf x >> meta/config`)
	assertGitStillWorks(t, sb, env, root)
}

// sandboxGitRepo initializes root as a repository with one committed file and
// one unstaged change.
func sandboxGitRepo(t *testing.T, root string, initArgs ...string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := hostCmd(t.Context(), "git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git(append([]string{"init", "-q"}, initArgs...)...)
	writeSandboxFile(t, filepath.Join(root, "f.txt"), "one\n")
	git("add", "f.txt")
	// Signing is pinned off so a developer's commit.gpgsign cannot fail the
	// fixture before any assertion runs.
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false",
		"commit", "-q", "-m", "c")
	writeSandboxFile(t, filepath.Join(root, "f.txt"), "two\n")
}

// gitSandbox prepares the shipped mechanism for a repository root, with the
// host temp root moved aside so only the git rules can deny a write under it.
func gitSandbox(t *testing.T, root string) (*sandbox, []string) {
	t.Helper()
	env := sandboxTestEnv(t.TempDir())
	sb, ref := sandboxExecMechanism(root, sandboxInputs{childEnv: env}, t.TempDir())
	if ref != nil {
		t.Fatalf("sandboxExecMechanism: %v", ref)
	}
	return sb, env
}

// assertGitStillWorks keeps the denials from breaking the read side of git or
// an ordinary write in the working tree.
func assertGitStillWorks(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	for _, tc := range []struct{ cmd, want string }{
		{`git status --short`, "M f.txt"},
		{`git diff --stat`, "1 file changed"},
		{`printf ok > new.txt && cat new.txt`, "ok"},
	} {
		res := runWrapped(t, sb, env, root, tc.cmd)
		if res.ExitCode != 0 || !strings.Contains(res.Stdout+res.Stderr, tc.want) {
			t.Errorf("%q = exit %d %q, want %q", tc.cmd, res.ExitCode, res.Stdout+res.Stderr, tc.want)
		}
	}
}

// TestSandboxExecDeniesServiceMediatedChannels proves the Mach default-deny
// against the real services: `defaults write` cannot get cfprefsd to persist
// a preference, and the pasteboard can be neither read nor written. The
// pasteboard leg uses the find pasteboard and restores it, so the operator's
// clipboard is never touched.
func TestSandboxExecDeniesServiceMediatedChannels(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	assertPreferencesNotPersisted(t, sb, env, root)
	assertPasteboardUnreachable(t, sb, env, root)
	assertUserLookupAllowed(t, sb, env, root)
}

// assertUserLookupAllowed pins the one Mach allowlist entry: the passwd
// lookup still resolves the account name, as os/user and `whoami` need.
func assertUserLookupAllowed(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	want, code := hostCommand(t, "", "/usr/bin/id", "-un")
	if code != 0 {
		t.Fatalf("host id -un exited %d", code)
	}
	res := runWrapped(t, sb, env, root, `id -un`)
	if got := strings.TrimSpace(res.Stdout + res.Stderr); res.ExitCode != 0 || got != strings.TrimSpace(want) {
		t.Errorf("sandboxed id -un = exit %d %q, want %q", res.ExitCode, got, strings.TrimSpace(want))
	}
}

// assertPreferencesNotPersisted writes a fresh preference domain from inside
// the sandbox and proves the host cannot read it back.
func assertPreferencesNotPersisted(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	// A per-process, per-moment suffix: concurrent runs never share a domain,
	// so one run's cleanup cannot delete another's probe.
	domain := fmt.Sprintf("dev.verdict.sandboxtest.%d.%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { removePreferenceDomain(t, domain) })
	// The per-domain write must itself fail for the right reason: cfprefsd is
	// unreachable. `defaults write -g` exits 0 even when nothing persists, so
	// only its absence on the host is asserted.
	res := runWrapped(t, sb, env, root, `defaults write `+domain+` k v`)
	if res.ExitCode == 0 || !strings.Contains(res.Stdout+res.Stderr, "Could not write domain") {
		t.Errorf("sandboxed defaults write = exit %d %q, want the cfprefsd denial", res.ExitCode, res.Stdout+res.Stderr)
	}
	runWrapped(t, sb, env, root, `defaults write -g `+domain+` v`)
	for _, args := range [][]string{{"read", domain, "k"}, {"read", "-g", domain}} {
		if out, code := hostCommand(t, "", "/usr/bin/defaults", args...); code == 0 {
			t.Errorf("defaults %v on the host = %q, want the sandboxed write absent", args, out)
		}
	}
}

// removePreferenceDomain deletes whatever a failed run persisted.
func removePreferenceDomain(t *testing.T, domain string) {
	t.Helper()
	if _, code := hostCommand(t, "", "/usr/bin/defaults", "read", domain); code == 0 {
		hostCommand(t, "", "/usr/bin/defaults", "delete", domain)
	}
	if _, code := hostCommand(t, "", "/usr/bin/defaults", "read", "-g", domain); code == 0 {
		hostCommand(t, "", "/usr/bin/defaults", "delete", "-g", domain)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if err := os.Remove(filepath.Join(home, "Library", "Preferences", domain+".plist")); err != nil && !os.IsNotExist(err) {
		t.Errorf("remove the probe preference file: %v", err)
	}
}

// assertPasteboardUnreachable puts a sentinel on the find pasteboard from the
// host, proves the sandbox can neither read nor overwrite it, and restores
// the pasteboard's previous text.
func assertPasteboardUnreachable(t *testing.T, sb *sandbox, env []string, root string) {
	t.Helper()
	const sentinel = "verdict-pasteboard-sentinel"
	saved, _ := hostCommand(t, "", "/usr/bin/pbpaste", "-pboard", "find")
	t.Cleanup(func() { hostCommand(t, saved, "/usr/bin/pbcopy", "-pboard", "find") })
	hostCommand(t, sentinel, "/usr/bin/pbcopy", "-pboard", "find")
	if got, _ := hostCommand(t, "", "/usr/bin/pbpaste", "-pboard", "find"); got != sentinel {
		// A headless or SSH session has no usable pasteboard server, so the
		// leg cannot apply; the CI lanes that require the sandbox fail here.
		if os.Getenv("EVAL_REQUIRE_SANDBOX") == "1" {
			t.Fatalf("host control read %q from the find pasteboard, want the sentinel", got)
		}
		t.Logf("WARNING: the pasteboard leg did not run: no usable pasteboard on this host (control read %q); "+
			"the verify-sandbox eval under EVAL_REQUIRE_SANDBOX=1 is the gate that requires it", got)
		t.Skipf("no usable pasteboard on this host (control read %q); the leg cannot apply", got)
	}
	res := runWrapped(t, sb, env, root, `pbpaste -pboard find`)
	if res.ExitCode == 0 || strings.Contains(res.Stdout+res.Stderr, sentinel) {
		t.Errorf("sandboxed pbpaste = exit %d %q, want the pasteboard unreachable", res.ExitCode, res.Stdout+res.Stderr)
	}
	res = runWrapped(t, sb, env, root, `printf overwritten | pbcopy -pboard find`)
	if res.ExitCode == 0 {
		t.Errorf("sandboxed pbcopy exited 0, want the pasteboard unreachable")
	}
	if got, _ := hostCommand(t, "", "/usr/bin/pbpaste", "-pboard", "find"); got != sentinel {
		t.Errorf("the find pasteboard reads %q after the sandboxed pbcopy, want the sentinel", got)
	}
}

// hostCmdWaitDelay bounds the wait for a captured command's pipes to close
// after it exits or its context ends (GO-CONC-06).
const hostCmdWaitDelay = 5 * time.Second

// hostCmd builds one host command whose captured output cannot hold the test
// past its context: WaitDelay is set on every one.
func hostCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = hostCmdWaitDelay
	return cmd
}

// hostCommand runs one unsandboxed host command with stdin and returns its
// combined output and exit code; a spawn failure fails the test.
func hostCommand(t *testing.T, stdin, name string, args ...string) (string, int) {
	t.Helper()
	// Cleanups run after the test context is canceled, so the bound is the
	// helper's own, like runWrapped's.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := hostCmd(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	default:
		t.Fatalf("%s %v: %v", name, args, err)
		return "", -1
	}
}

// TestSandboxExecDeniesLaunchChannels proves the launch channels against the
// real services: neither `open` nor `osascript ... launch` starts an app from
// inside the sandbox, and `launchctl submit` and `bootstrap` load no job. The
// app is a throwaway bundle whose executable only appends its arguments to a
// marker, so a delivered launch is visible and harmless; an unsandboxed
// control launch run afterwards proves the marker works and bounds the wait.
func TestSandboxExecDeniesLaunchChannels(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	app, marker := probeApp(t)
	for _, cmd := range []string{
		`open -n -g "` + app + `" --args sandboxed-open`,
		`osascript -e 'tell application "` + app + `" to launch'`,
	} {
		if res := runWrapped(t, sb, env, root, cmd); res.ExitCode == 0 {
			t.Errorf("%q exited 0 inside the sandbox, want the launch refused", cmd)
		}
	}
	assertLsopenAloneRefuses(t, sb, env, root, app)
	if _, code := hostCommand(t, "", "/usr/bin/open", "-n", "-g", app, "--args", "host-control"); code != 0 {
		t.Fatalf("the unsandboxed control open exited %d", code)
	}
	testenv.Until(t, func() bool { return strings.Contains(readMarker(marker), "host-control") })
	for _, leak := range []string{"sandboxed-open", "noargs", "lsopen-only"} {
		if got := readMarker(marker); strings.Contains(got, leak) {
			t.Errorf("marker = %q, want no %s launch", got, leak)
		}
	}
	assertNoLaunchdJob(t, sb, env, root, marker)
}

// assertLsopenAloneRefuses runs `open` under a variant of the shipped profile
// without the Mach default-deny. The shipped profile blocks launches twice
// over, so only this variant pins lsopen on its own.
func assertLsopenAloneRefuses(t *testing.T, sb *sandbox, env []string, root, app string) {
	t.Helper()
	profile := ""
	for _, arg := range sb.prefix {
		if strings.Contains(arg, "(deny mach-lookup)") && strings.Contains(arg, "(deny lsopen)") {
			profile = arg
		}
	}
	if profile == "" {
		t.Fatalf("no profile argument in %q carries both (deny mach-lookup) and (deny lsopen)", sb.prefix)
	}
	lsopenOnly := strings.Replace(profile, "(deny mach-lookup)", "", 1)
	res := runProfile(t, lsopenOnly, sb.childEnv(env), root, `open -n -g "`+app+`" --args lsopen-only`)
	if res.ExitCode == 0 {
		t.Errorf("open under the lsopen-only variant exited 0, want lsopen to refuse it")
	}
}

// assertNoLaunchdJob proves launchd loads no job for the sandboxed command,
// through `submit` and through `bootstrap` of a plist inside the root.
func assertNoLaunchdJob(t *testing.T, sb *sandbox, env []string, root, marker string) {
	t.Helper()
	label := fmt.Sprintf("dev.verdict.sandboxtest.%d.%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { removeLaunchdJob(t, label) })
	writeSandboxFile(t, filepath.Join(root, "job.plist"), launchdPlist(label, marker))
	for _, cmd := range []string{
		`launchctl submit -l ` + label + ` -- /bin/sh -c 'echo launchd >> "` + marker + `"'`,
		`launchctl bootstrap gui/$(id -u) job.plist`,
	} {
		if res := runWrapped(t, sb, env, root, cmd); res.ExitCode == 0 {
			t.Errorf("%q exited 0 inside the sandbox, want the job refused", cmd)
		}
	}
	if out, code := hostCommand(t, "", "/bin/launchctl", "list", label); code == 0 {
		t.Errorf("launchctl list %s = %q, want no job loaded", label, out)
	}
	if strings.Contains(readMarker(marker), "launchd") {
		t.Error("a launchd job ran the marker command")
	}
}

// removeLaunchdJob unloads a job a failed run left behind.
func removeLaunchdJob(t *testing.T, label string) {
	t.Helper()
	if _, code := hostCommand(t, "", "/bin/launchctl", "list", label); code == 0 {
		hostCommand(t, "", "/bin/launchctl", "remove", label)
	}
}

// launchdPlist is a RunAtLoad job that appends to the marker.
func launchdPlist(label, marker string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + label + `</string>
<key>ProgramArguments</key><array><string>/bin/sh</string><string>-c</string>
<string>echo launchd &gt;&gt; "` + marker + `"</string></array>
<key>RunAtLoad</key><true/></dict></plist>
`
}

// probeApp builds a background-only app bundle whose executable appends its
// arguments (or "noargs") to a marker, and unregisters it from LaunchServices
// when the test ends.
func probeApp(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	app := filepath.Join(dir, "VerdictProbe.app")
	writeSandboxFile(t, filepath.Join(app, "Contents", "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleExecutable</key><string>probe</string>
<key>CFBundleIdentifier</key><string>dev.verdict.sandboxtest.`+filepath.Base(dir)+`</string>
<key>CFBundlePackageType</key><string>APPL</string><key>LSBackgroundOnly</key><true/></dict></plist>
`)
	exe := filepath.Join(app, "Contents", "MacOS", "probe")
	writeSandboxFile(t, exe, "#!/bin/sh\necho \"${*:-noargs}\" >> '"+marker+"'\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		hostCommand(t, "", "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-u", app)
	})
	return app, marker
}

// runProfile runs one command under an arbitrary Seatbelt profile, for the
// variants that prove a single rule on its own.
func runProfile(t *testing.T, profile string, env []string, root, vcmd string) core.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := (coreexec.Watchdog{}).Run(ctx, &core.Command{
		Argv: []string{sandboxExecPath, "-p", profile, "bash", "-c", vcmd}, Env: env, Dir: root, OutputCap: sandboxProbeCap,
	})
	if err != nil {
		t.Fatalf("run %q: %v", vcmd, err)
	}
	return res
}

func readMarker(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// TestSandboxExecDeniesSignalsToHostProcesses proves the command cannot signal
// a process it did not start, while it still controls its own children.
func TestSandboxExecDeniesSignalsToHostProcesses(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	victim := exec.CommandContext(t.Context(), "/bin/sleep", "60")
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = victim.Process.Kill() //nolint:errcheck // the sleeper may already be gone
		_ = victim.Wait()         //nolint:errcheck // reaped for cleanup only
	})
	pid := strconv.Itoa(victim.Process.Pid)
	requireDeniedWithErrno(t, sb, env, root, `kill -TERM `+pid)
	res := runWrapped(t, sb, env, root, `sleep 30 & c=$!; kill $c; wait $c; echo "child=$?"`)
	if got := strings.TrimSpace(res.Stdout + res.Stderr); !strings.Contains(got, "child=143") {
		t.Errorf("killing its own child = %q, want child=143", got)
	}
}

// TestSandboxExecDeniesADanglingGitdirPointer proves macOS needs no refusal
// for a `.git` file whose git directory does not exist yet: the profile denies
// creating it, hooks included, by path.
func TestSandboxExecDeniesADanglingGitdirPointer(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	writeSandboxFile(t, filepath.Join(root, ".git"), "gitdir: meta\n")
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	requireDeniedWithErrno(t, sb, env, root, `mkdir -p meta/hooks`)
}

// TestSandboxExecDeniesSwappingAnInRootGitdirLink proves macOS covers the
// shape Linux refuses: `.git` names `gitdir: link`, and `link` is an in-root
// symlink to a directory outside the root. The command can neither remove or
// rename the link to put a directory of its own in its place, nor write
// hooks through it.
func TestSandboxExecDeniesSwappingAnInRootGitdirLink(t *testing.T) {
	requireSandboxExec(t)
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	writeSandboxFile(t, filepath.Join(root, ".git"), "gitdir: link\n")
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	for _, cmd := range []string{
		`rm link`,
		`mv link moved`,
		`mkdir -p link/hooks`,
		`printf x > link/pre-commit`,
	} {
		requireDeniedWithErrno(t, sb, env, root, cmd)
	}
	if info, err := os.Lstat(filepath.Join(root, "link")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the in-root link was replaced (%v, %v)", info, err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Errorf("the link target gained %v (%v), want it untouched", entries, err)
	}
}

// TestSandboxExecDeniesTheDotDotPointerEscape reproduces the `..` escape
// against the real Seatbelt engine and real git: `.git` names `gitdir:
// a/../g`, which cleans to `g` but resolves `a` first. The command must not
// be able to create `a` — as a link to `x/evil` (sending git to `x/g`, where
// it would plant hooks) or as anything else.
func TestSandboxExecDeniesTheDotDotPointerEscape(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sandboxGitRepo(t, root, "--separate-git-dir="+filepath.Join(root, "g"))
	writeSandboxFile(t, filepath.Join(root, ".git"), "gitdir: a/../g\n")
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	if res := runWrapped(t, sb, env, root, `mkdir -p x/evil x/g/hooks`); res.ExitCode != 0 {
		t.Fatalf("setup inside the root = exit %d %q", res.ExitCode, res.Stdout+res.Stderr)
	}
	requireDeniedWithErrno(t, sb, env, root, `ln -s x/evil a`)
	requireDeniedWithErrno(t, sb, env, root, `mkdir a`)
	if _, err := os.Lstat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Errorf("the walked component a exists after the denied writes (%v)", err)
	}
}

// TestSandboxExecDeniesSwappingAnInRootLinkBehindAnOutsideLink proves the
// hop-by-hop walk against the real Seatbelt engine: `.git` reaches its git
// directory through an outside-root link whose target is the in-root link
// `inlink`. The command must not be able to remove or repoint `inlink` at a
// directory of its own, where git would run its hooks.
func TestSandboxExecDeniesSwappingAnInRootLinkBehindAnOutsideLink(t *testing.T) {
	requireSandboxExec(t)
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "gitdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "inlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "inlink"), filepath.Join(outside, "hop")); err != nil {
		t.Fatal(err)
	}
	writeSandboxFile(t, filepath.Join(root, ".git"), "gitdir: "+filepath.Join(outside, "hop", "gitdir")+"\n")
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	if res := runWrapped(t, sb, env, root, `mkdir -p evil/gitdir/hooks`); res.ExitCode != 0 {
		t.Fatalf("setup inside the root = exit %d %q", res.ExitCode, res.Stdout+res.Stderr)
	}
	requireDeniedWithErrno(t, sb, env, root, `rm inlink`)
	requireDeniedWithErrno(t, sb, env, root, `mv inlink moved`)
	if target, err := os.Readlink(filepath.Join(root, "inlink")); err != nil || target != filepath.Join(root, "real") {
		t.Errorf("inlink = %q (%v), want it still pointing at %s", target, err, filepath.Join(root, "real"))
	}
}

// TestSandboxRefusesAFirmlinkSpelledGitDir pins the identity check against
// the macOS firmlink alias: `/System/Volumes/Data/private/var/…` names the
// same directory as `/private/var/…`, but Seatbelt matches only the
// firmlink-resolved spelling, so a git directory named under the Data
// volume spelling was neither denied nor refused. It refuses on both paths.
func TestSandboxRefusesAFirmlinkSpelledGitDir(t *testing.T) {
	root := t.TempDir()
	mustSandboxFS(t, os.MkdirAll(filepath.Join(root, "meta", "git"), 0o755))
	realRoot, err := filepath.EvalSymlinks(root)
	mustSandboxFS(t, err)
	alias := "/System/Volumes/Data" + realRoot
	if a, errA := os.Stat(alias); errA != nil || !sandboxSameFile(realRoot, a) {
		t.Skipf("no Data-volume firmlink spelling for %s (%v)", realRoot, errA)
	}
	writeSandboxFile(t, filepath.Join(root, ".git"), "gitdir: "+alias+"/meta/git\n")
	cause := "the git pointer " + filepath.Join(root, ".git") + " reaches " + alias + "/meta, which is inside the " +
		"reviewed root " + root + " under another spelling"
	requireGitRefusal(t, root, cause)
	requireGitDirsRefusal(t, root, cause)
}

// TestSandboxExecDeniesGitWritesThroughACaseVariantPointer proves, against
// the real Seatbelt engine, the shape the identity check accepts: a pointer
// spelled in another letter case below the root's own spelling (`gitdir:
// meta/GIT` for `meta/git`). Seatbelt matches path rules case-insensitively
// on a case-insensitive volume, so the rule in the pointer's spelling denies
// hooks and config written through the directory's on-disk spelling.
func TestSandboxExecDeniesGitWritesThroughACaseVariantPointer(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	mustSandboxFS(t, os.Mkdir(filepath.Join(root, "meta"), 0o755))
	sandboxGitRepo(t, root, "--separate-git-dir="+filepath.Join(root, "meta", "git"))
	if _, err := os.Stat(filepath.Join(root, "meta", "GIT")); err != nil {
		t.Skip("the temp volume is case-sensitive")
	}
	writeSandboxFile(t, filepath.Join(root, ".git"), "gitdir: meta/GIT\n")
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	requireDeniedWithErrno(t, sb, env, root, `printf '#!/bin/sh\n' > `+filepath.Join(root, "meta", "git", "hooks", "pre-commit"))
	requireDeniedWithErrno(t, sb, env, root, `printf x >> `+filepath.Join(root, "meta", "git", "config"))
	assertGitStillWorks(t, sb, env, root)
}

// TestSandboxExecDeniesHardlinkingGitControlFiles keeps the hardlink gap a
// setup-time one: inside the real sandbox `ln .git/config cfg` is denied
// (Seatbelt checks a new link against the source's write rules), so the
// command cannot mint a second name for a protected file at run time, and a
// checkout that already carries one refuses before anything runs.
func TestSandboxExecDeniesHardlinkingGitControlFiles(t *testing.T) {
	requireSandboxExec(t)
	root := t.TempDir()
	sandboxGitRepo(t, root)
	config := filepath.Join(root, ".git", "config")
	before, err := os.ReadFile(config)
	mustSandboxFS(t, err)
	sb, env := gitSandbox(t, root)
	defer closeSandbox(t, sb)
	requireDeniedWithErrno(t, sb, env, root, `ln .git/config cfg`)
	if _, err := os.Lstat(filepath.Join(root, "cfg")); !os.IsNotExist(err) {
		t.Errorf("cfg exists after the denied link (%v)", err)
	}
	if after, err := os.ReadFile(config); err != nil || !bytes.Equal(after, before) {
		t.Errorf(".git/config changed (%v)", err)
	}
	mustSandboxFS(t, os.Link(config, filepath.Join(root, "cfg")))
	if _, ref := sandboxExecMechanism(root, sandboxInputs{childEnv: env}, t.TempDir()); ref == nil ||
		!strings.Contains(ref.Message, "the git control file "+config+" has another hard link") {
		t.Errorf("ref = %v, want the hardlink refusal for %s", ref, config)
	}
}
