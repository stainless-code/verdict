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
	unshareTool            = "unshare"
	sandboxExecTool        = "sandbox-exec"
	sandboxExecPath        = "/usr/bin/sandbox-exec"
	sandboxTmpfs           = "--tmpfs"
	sandboxProbeTimeout    = 15 * time.Second
	sandboxProbeCap        = 4 << 10
	sandboxShellArgs       = 3

	// sandboxMechanismBwrap, sandboxMechanismUnshareNet, and
	// sandboxMechanismSandboxExec are the stable names the records carry, so a
	// reader can tell the full mount shape from the network-only opt-in.
	sandboxMechanismBwrap       = "bwrap"
	sandboxMechanismUnshareNet  = "unshare-net"
	sandboxMechanismSandboxExec = "sandbox-exec"

	// sandboxPrivateDirPrefix names the per-run temporary directory the macOS
	// child gets as TMPDIR/TMP/TEMP; the profile allows it and denies the
	// shared host temp roots around it.
	sandboxPrivateDirPrefix = "verdict-sandbox-"

	// sandboxHostTmpDir is the shared host temp root the bwrap shape replaces
	// and the macOS profile denies.
	sandboxHostTmpDir = "/tmp"

	// seatbeltFirstPrintable and seatbeltDelete bound the characters a
	// Seatbelt string literal can represent; anything below or DEL refuses.
	seatbeltFirstPrintable = ' '
	seatbeltDelete         = 0x7f

	// sandboxProbeScript is the macOS probe body: it proves Seatbelt permits
	// bash, mktemp, a write and read inside the private TMPDIR, and a read of
	// the working directory (the reviewed root in group mode, the repo cwd in
	// patch mode) before the real command runs.
	sandboxProbeScript = `p=$(mktemp "$TMPDIR/verdict-probe.XXXXXX") && printf ok > "$p" && test -s "$p" && rm -f "$p" && test -r .`

	// msgSandboxNetworkOnly is the warning every network-only run carries: the
	// opt-in exists because bwrap may be absent, and it must never read as the
	// full sandbox.
	msgSandboxNetworkOnly = "sandbox network-only: unshare --net isolates the network alone — " +
		"no mount, filesystem, or PID isolation; credential paths and /tmp stay visible. " +
		"Use --sandbox with bubblewrap for the full shape."
)

// sandboxCredentialPaths are every host credential path the wrapper hides:
// directories behind an empty tmpfs, files behind a read-only /dev/null bind
// on Linux, and denied reads and writes under Seatbelt on macOS. Only entries
// that exist are mounted on Linux; macOS denies the whole table.
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

// sandboxHostTempDirs are the shared host temp roots macOS denies outside the
// per-run private directory. /tmp and /var/tmp are symlinks into /private on
// macOS; both spellings are denied because Seatbelt matches the resolved
// vnode path.
var sandboxHostTempDirs = []string{sandboxHostTmpDir, "/private/tmp", "/var/tmp", "/private/var/tmp"}

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

// sandbox is one prepared mechanism: the argv prefix that wraps the command,
// the child-environment overrides the mechanism needs, the stable mechanism
// name the records carry, and the cleanup its private state requires.
type sandbox struct {
	cleanup     func()
	tool        string
	mechanism   string
	prefix      []string
	env         []string
	networkOnly bool
}

// argv wraps the verified command: the runner's own exit status is the
// command's, so the watchdog records the same proof an unwrapped run would.
func (sb *sandbox) argv(vcmd string) []string {
	argv := make([]string, 0, len(sb.prefix)+sandboxShellArgs)
	argv = append(argv, sb.prefix...)
	return append(argv, "bash", "-c", vcmd)
}

// probeArgv is the no-op the mechanism proves itself with. Linux runs `true`;
// macOS runs the smoke script, because a Seatbelt profile that compiles can
// still deny bash its own working directory or private TMPDIR.
func (sb *sandbox) probeArgv() []string {
	if sb.tool == sandboxExecTool {
		return append(slices.Clone(sb.prefix), "bash", "-c", sandboxProbeScript)
	}
	return append(slices.Clone(sb.prefix), "true")
}

// childEnv applies the mechanism's environment overrides, replacing the base
// snapshot's value for the same name and keeping every other entry.
func (sb *sandbox) childEnv(base []string) []string {
	if sb == nil || len(sb.env) == 0 {
		return base
	}
	overridden := make(map[string]bool, len(sb.env))
	for _, kv := range sb.env {
		name, _, _ := strings.Cut(kv, "=")
		overridden[name] = true
	}
	env := make([]string, 0, len(base)+len(sb.env))
	for _, kv := range base {
		if name, _, ok := strings.Cut(kv, "="); ok && overridden[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, sb.env...)
}

// close removes the mechanism's private state. It is safe on nil and runs at
// most once.
func (sb *sandbox) close() {
	if sb == nil || sb.cleanup == nil {
		return
	}
	sb.cleanup()
	sb.cleanup = nil
}

// name is the stable mechanism name the records carry, empty when unsandboxed.
func (sb *sandbox) name() string {
	if sb == nil {
		return ""
	}
	return sb.mechanism
}

// prepareSandbox builds the wrapper and probes it with a no-op command using
// the same flags before anything executes. A nil sandbox with a nil refusal
// means the run is unsandboxed; every enforcement failure is
// sandbox-unavailable.
func prepareSandbox(
	ctx context.Context, commander core.Commander, workDir, root string, env []string, networkOnly bool,
) (*sandbox, *core.RefusalError) {
	sb, ref := sandboxMechanism(runtime.GOOS, root, env, networkOnly)
	if ref != nil {
		return nil, ref
	}
	if ref := sb.probe(ctx, commander, workDir, env); ref != nil {
		sb.close()
		return nil, ref
	}
	return sb, nil
}

// sandboxMechanism picks the enforced shape for the host. networkOnly is the
// explicit Linux unshare opt-in for hosts without bubblewrap; it never turns
// --sandbox into a weaker mechanism, because the two flags are exclusive and
// the full flag still requires bwrap.
func sandboxMechanism(goos, root string, env []string, networkOnly bool) (*sandbox, *core.RefusalError) {
	switch goos {
	case "linux":
		return linuxSandboxMechanism(root, env, networkOnly)
	case "darwin":
		if networkOnly {
			return nil, networkOnlyRefusal(goos)
		}
		return sandboxExecMechanism(root, env, "")
	default:
		return unsupportedMechanism(goos, networkOnly)
	}
}

// linuxSandboxMechanism keeps the full bwrap shape on --sandbox and the
// unshare opt-in on --sandbox-network-only; neither falls back to the other.
func linuxSandboxMechanism(root string, env []string, networkOnly bool) (*sandbox, *core.RefusalError) {
	if networkOnly {
		return unshareNetMechanism(), nil
	}
	prefix, ref := bwrapArgv(root, env)
	if ref != nil {
		return nil, ref
	}
	return &sandbox{tool: bwrapTool, mechanism: sandboxMechanismBwrap, prefix: prefix}, nil
}

// unsupportedMechanism names the right missing mechanism for the requested
// boundary instead of reporting one generic refusal.
func unsupportedMechanism(goos string, networkOnly bool) (*sandbox, *core.RefusalError) {
	if networkOnly {
		return nil, networkOnlyRefusal(goos)
	}
	return nil, sandboxUnavailable(goos +
		" has no enforced sandbox — --sandbox needs Linux with bubblewrap, or macOS with sandbox-exec")
}

// networkOnlyRefusal keeps the Linux-only opt-in typed and explicit on every
// other host instead of silently accepting a mechanism it cannot build.
func networkOnlyRefusal(goos string) *core.RefusalError {
	linuxOnly := " — --sandbox-network-only is a Linux unshare --net opt-in"
	if goos == "darwin" {
		return sandboxUnavailable(goos + " has no unshare network-only sandbox" + linuxOnly +
			"; use --sandbox here for sandbox-exec (no network, credential paths and host /tmp denied)")
	}
	return sandboxUnavailable(goos + " has no unshare network-only sandbox" + linuxOnly)
}

// unshareNetMechanism is the network-only Linux fallback: a user namespace
// with the caller mapped to root inside it, then a network namespace. No
// mount, filesystem, or PID namespace is created.
func unshareNetMechanism() *sandbox {
	return &sandbox{
		tool:        unshareTool,
		mechanism:   sandboxMechanismUnshareNet,
		prefix:      []string{unshareTool, "--user", "--map-root-user", "--net", "--"},
		networkOnly: true,
	}
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
		sandboxTmpfs, sandboxHostTmpDir,
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

// sandboxExecMechanism is the macOS shape: a generated Seatbelt profile that
// keeps the network denial, denies the credential table and the shared host
// temp roots, and allows the reviewed root plus one per-run private TMPDIR.
// Seatbelt has no mount namespace, so this hides paths by policy rather than
// replacing them.
func sandboxExecMechanism(root string, env []string, tmpBase string) (*sandbox, *core.RefusalError) {
	home, ok := sandboxHome(env)
	if !ok {
		return nil, sandboxUnavailable(
			"HOME is unset in the child environment, so the credential paths under it cannot be hidden")
	}
	repo, err := filepath.Abs(root)
	if err != nil {
		return nil, sandboxUnavailable(fmt.Sprintf("could not resolve the reviewed root %s: %v", root, err))
	}
	if filepath.Dir(repo) == repo {
		return nil, sandboxUnavailable(
			fmt.Sprintf("the reviewed root is the filesystem root %s; refusing to allow the whole host writable", repo))
	}
	private, err := os.MkdirTemp(tmpBase, sandboxPrivateDirPrefix)
	if err != nil {
		return nil, sandboxUnavailable(fmt.Sprintf("could not create the private temporary directory: %v", err))
	}
	profile, ref := sandboxExecProfile(home, repo, private, tmpBase)
	if ref != nil {
		_ = os.RemoveAll(private)
		return nil, ref
	}
	return &sandbox{
		tool:      sandboxExecTool,
		mechanism: sandboxMechanismSandboxExec,
		prefix:    []string{sandboxExecPath, "-p", profile},
		env:       []string{"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private},
		cleanup:   func() { _ = os.RemoveAll(private) },
	}, nil
}

// sandboxExecProfile builds the profile in precedence order: network denial,
// the shared host temp roots, the allows for the private directory and the
// reviewed root, then the credential denials last so a checkout under HOME
// cannot re-expose a credential path.
func sandboxExecProfile(home, root, private, tmpBase string) (string, *core.RefusalError) {
	hostTemp := tmpBase
	if hostTemp == "" {
		hostTemp = os.TempDir()
	}
	denyTemp := sandboxExpandRealpaths(append(slices.Clone(sandboxHostTempDirs), hostTemp))
	allow := sandboxExpandRealpaths([]string{private, root})
	denyCredential := sandboxCredentialTargets(home)
	var b strings.Builder
	b.WriteString("(version 1)(allow default)(deny network*)")
	if ref := sandboxWriteRules(&b, "deny", denyTemp); ref != nil {
		return "", ref
	}
	if ref := sandboxWriteRules(&b, "allow", allow); ref != nil {
		return "", ref
	}
	if ref := sandboxWriteRules(&b, "deny", denyCredential); ref != nil {
		return "", ref
	}
	return b.String(), nil
}

// sandboxCredentialTargets resolves the credential table under both the HOME
// the child sees and its realpath, so a symlinked home or credential entry
// cannot slip past the policy.
func sandboxCredentialTargets(home string) []string {
	homes := sandboxExpandRealpaths([]string{home})
	targets := make([]string, 0, len(homes)*len(sandboxCredentialPaths))
	for _, h := range homes {
		for _, rel := range sandboxCredentialPaths {
			targets = append(targets, filepath.Join(h, rel))
		}
	}
	return sandboxExpandRealpaths(targets)
}

// sandboxExpandRealpaths cleans every path, adds its resolved realpath when
// one exists, and removes duplicates while keeping first-seen order. Seatbelt
// matches the resolved vnode path, so the /var -> /private/var spelling of a
// temp root needs both forms.
func sandboxExpandRealpaths(paths []string) []string {
	seen := make(map[string]bool, len(paths)*2)
	expanded := make([]string, 0, len(paths)*2)
	add := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		expanded = append(expanded, path)
	}
	for _, path := range paths {
		add(path)
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			add(resolved)
		}
	}
	return expanded
}

// sandboxWriteRules renders one subpath rule per path, refusing a path whose
// control characters have no safe Seatbelt literal.
func sandboxWriteRules(b *strings.Builder, action string, paths []string) *core.RefusalError {
	for _, path := range paths {
		literal, ref := seatbeltLiteral(path)
		if ref != nil {
			return ref
		}
		fmt.Fprintf(b, "(%s file-read* file-write* (subpath %s))", action, literal)
	}
	return nil
}

// seatbeltLiteral quotes one path for the Seatbelt profile. Backslashes and
// quotes are escaped; a control character (including a newline) refuses,
// because the profile must never be built from a path it cannot represent.
func seatbeltLiteral(path string) (string, *core.RefusalError) {
	if strings.ContainsFunc(path, func(r rune) bool {
		return r < seatbeltFirstPrintable || r == seatbeltDelete
	}) {
		return "", sandboxUnavailable(fmt.Sprintf(
			"the sandbox path %q contains a control character the Seatbelt profile cannot represent", path))
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path)
	return `"` + quoted + `"`, nil
}

func (sb *sandbox) probe(ctx context.Context, commander core.Commander, workDir string, env []string) *core.RefusalError {
	pctx, cancel := context.WithTimeout(ctx, sandboxProbeTimeout)
	defer cancel()
	res, err := commander.Run(pctx, &core.Command{
		Argv: sb.probeArgv(), Env: sb.childEnv(env), Dir: workDir, OutputCap: sandboxProbeCap,
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

// probeRemedy is the cause a failed probe names, per mechanism: only the
// namespace builders depend on unprivileged user namespaces, and only the
// network-only opt-in points back at the full shape.
func (sb *sandbox) probeRemedy() string {
	switch sb.tool {
	case bwrapTool:
		return " — unprivileged user namespaces may be restricted"
	case unshareTool:
		return " — unprivileged user namespaces may be restricted; use --sandbox with bubblewrap for mount isolation"
	}
	return ""
}

// sandboxDetail trims the mechanism's diagnostic to its first line for the
// refusal.
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
