package verifycore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	codeSandboxUnavailable = "sandbox-unavailable"
	bwrapTool              = "bwrap"
	sandboxExecTool        = "sandbox-exec"
	sandboxExecPath        = "/usr/bin/sandbox-exec"
	sandboxTmpfs           = "--tmpfs"
	sandboxProbeTimeout    = 15 * time.Second
	sandboxProbeCap        = 4 << 10
	sandboxShellArgs       = 3
)

// macOS has no filesystem namespace; sandbox-exec enforces network only.
const sandboxExecProfile = "(version 1)(allow default)(deny network*)"

// sandboxCredentialPaths are every host credential path the wrapper hides:
// directories behind an empty tmpfs, files behind a read-only /dev/null bind.
// Only entries that exist are mounted.
var sandboxCredentialPaths = []string{
	".aws",
	".ssh",
	filepath.Join(".config", "gh"),
	".docker",
	".kube",
	filepath.Join(".config", "gcloud"),
	".azure",
	".gnupg",
	".netrc",
	".git-credentials",
	".npmrc",
	".pypirc",
}

// sandboxRuntimeDir is mounted empty because connect() on a unix socket there
// ignores --unshare-net: /run/docker.sock reaches a daemon with its own
// network and root, and /run/user/<uid> holds the session and agent sockets.
const sandboxRuntimeDir = "/run"

// sandboxVarRunDir is /var/run, a symlink to /run on most distros.
const sandboxVarRunDir = "/var/run"

// sandboxMount is one path the wrapper replaces; dir mounts an empty tmpfs,
// a file binds /dev/null read-only over it.
type sandboxMount struct {
	dest string
	dir  bool
}

func (m sandboxMount) argv() []string {
	if m.dir {
		return []string{sandboxTmpfs, m.dest}
	}
	return []string{"--ro-bind", "/dev/null", m.dest}
}

type sandbox struct {
	tool   string
	prefix []string
}

// argv wraps the verified command: the runner's own exit status is the
// command's, so the watchdog records the same proof an unwrapped run would.
func (sb *sandbox) argv(vcmd string) []string {
	argv := make([]string, 0, len(sb.prefix)+sandboxShellArgs)
	argv = append(argv, sb.prefix...)
	return append(argv, "bash", "-c", vcmd)
}

// prepareSandbox builds the wrapper and probes it with a no-op command using
// the same flags before anything executes. A nil sandbox with a nil refusal
// means the run is unsandboxed; every enforcement failure is
// sandbox-unavailable.
func prepareSandbox(ctx context.Context, commander core.Commander, workDir, root string, env []string) (*sandbox, *core.RefusalError) {
	sb, ref := sandboxMechanism(runtime.GOOS, root, env)
	if ref != nil {
		return nil, ref
	}
	if ref := sb.probe(ctx, commander, workDir, env); ref != nil {
		return nil, ref
	}
	return sb, nil
}

func sandboxMechanism(goos, root string, env []string) (*sandbox, *core.RefusalError) {
	switch goos {
	case "linux":
		prefix, ref := bwrapArgv(root, env)
		if ref != nil {
			return nil, ref
		}
		return &sandbox{tool: bwrapTool, prefix: prefix}, nil
	case "darwin":
		return &sandbox{tool: sandboxExecTool, prefix: []string{sandboxExecPath, "-p", sandboxExecProfile}}, nil
	}
	return nil, sandboxUnavailable(goos +
		" has no enforced sandbox — --sandbox needs Linux with bubblewrap, or macOS with sandbox-exec")
}

// bwrapArgv is the enforced shape of the manual recipe: a read-only host, a
// writable reviewed root, a private /tmp, an empty /run, a fresh /dev and
// /proc under a private PID namespace, empty credential paths, and no
// network. The repo bind is last so a checkout under /tmp or /run survives
// the tmpfs that replaces its parent.
func bwrapArgv(root string, env []string) ([]string, *core.RefusalError) {
	repo, err := filepath.Abs(root)
	if err != nil {
		return nil, sandboxUnavailable(fmt.Sprintf("could not resolve the reviewed root %s: %v", root, err))
	}
	if filepath.Dir(repo) == repo {
		return nil, sandboxUnavailable(
			fmt.Sprintf("the reviewed root is the filesystem root %s; refusing to bind the whole host writable", repo))
	}
	home, ok := sandboxHome(env)
	if !ok {
		return nil, sandboxUnavailable(
			"HOME is unset in the child environment, so the credential paths under it cannot be hidden")
	}
	argv := []string{
		bwrapTool,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-pid",
		sandboxTmpfs, "/tmp",
		sandboxTmpfs, sandboxRuntimeDir,
	}
	argv = append(argv, sandboxVarRunMount(lstatMode)...)
	argv = append(argv, "--bind", repo, repo)
	for _, mount := range sandboxCredentialMounts(home) {
		argv = append(argv, mount.argv()...)
	}
	return append(argv, "--unshare-net", "--new-session", "--die-with-parent", "--"), nil
}

// sandboxCredentialMounts resolves the table against the host, so a missing
// entry adds no mount and a file never gets a directory's tmpfs.
func sandboxCredentialMounts(home string) []sandboxMount {
	mounts := make([]sandboxMount, 0, len(sandboxCredentialPaths))
	for _, rel := range sandboxCredentialPaths {
		dest := filepath.Join(home, rel)
		info, err := os.Stat(dest)
		if err != nil {
			continue
		}
		mounts = append(mounts, sandboxMount{dest: dest, dir: info.IsDir()})
	}
	return mounts
}

// sandboxVarRunMount hides /var/run only when it is a real directory: a
// symlink to /run is already covered by the runtime mount, and bwrap refuses
// to mount over a symlink.
func sandboxVarRunMount(stat func(string) (os.FileMode, bool)) []string {
	if mode, ok := stat(sandboxVarRunDir); ok && mode.IsDir() {
		return []string{sandboxTmpfs, sandboxVarRunDir}
	}
	return nil
}

func lstatMode(path string) (os.FileMode, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	return info.Mode(), true
}

// sandboxHome reads the home the child will see: the environment snapshot
// when it carries one, else the process home an inherited child environment
// resolves to. An explicit snapshot without HOME cannot be hidden.
func sandboxHome(env []string) (string, bool) {
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if ok && name == "HOME" {
			return value, value != ""
		}
	}
	if len(env) == 0 {
		home, err := os.UserHomeDir()
		return home, err == nil && home != ""
	}
	return "", false
}

func (sb *sandbox) probe(ctx context.Context, commander core.Commander, workDir string, env []string) *core.RefusalError {
	pctx, cancel := context.WithTimeout(ctx, sandboxProbeTimeout)
	defer cancel()
	res, err := commander.Run(pctx, &core.Command{
		Argv: append(slices.Clone(sb.prefix), "true"), Env: env, Dir: workDir, OutputCap: sandboxProbeCap,
	})
	if ctx.Err() != nil {
		return canceledRefusal()
	}
	if err != nil {
		return sandboxUnavailable(fmt.Sprintf(
			"%s could not start (%v) — --sandbox needs Linux with bubblewrap, or macOS with sandbox-exec; "+
				"install it, or use the manual recipe on another OS", sb.tool, err))
	}
	if res.TimedOut {
		return sandboxUnavailable(fmt.Sprintf("%s did not finish its sandbox probe within %s", sb.tool, sandboxProbeTimeout))
	}
	if res.ExitCode != 0 {
		return sandboxUnavailable(fmt.Sprintf(
			"%s could not create the sandbox (exit %d: %s)%s",
			sb.tool, res.ExitCode, sandboxDetail(res), sb.probeRemedy()))
	}
	return nil
}

// probeRemedy is the cause a failed probe names, per mechanism: only bwrap
// builds namespaces, so only its refusal points at the namespace sysctl.
func (sb *sandbox) probeRemedy() string {
	if sb.tool == bwrapTool {
		return " — unprivileged user namespaces may be restricted"
	}
	return ""
}

// sandboxDetail trims bwrap's diagnostic to its first line for the refusal.
func sandboxDetail(res core.Result) string {
	detail := firstLine(strings.TrimSpace(res.Stdout + res.Stderr))
	if detail == "" {
		return "no diagnostic output"
	}
	return detail
}

// canceledRefusal keeps a canceled wait on the service's existing contract
// instead of reporting the sandbox as unavailable.
func canceledRefusal() *core.RefusalError {
	return &core.RefusalError{Code: codeCanceled, Message: msgCanceled}
}

func sandboxUnavailable(message string) *core.RefusalError {
	return &core.RefusalError{Code: codeSandboxUnavailable, Message: "sandbox unavailable: " + message}
}
