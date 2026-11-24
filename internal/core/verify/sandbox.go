package verifycore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
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

	// sandboxPrivateDirMode restores owner access to every directory under
	// the private tree before removal, so a child that dropped its own
	// permissions cannot strand the tree on the host.
	sandboxPrivateDirMode = 0o700

	// sandboxGoCacheDir and sandboxCacheDir are the toolchain caches the
	// macOS child is pointed at inside its private directory: the profile
	// denies writes everywhere else, so the default caches under HOME fail.
	sandboxGoCacheDir = "go-build"
	sandboxCacheDir   = "cache"

	// sandboxHostTmpDir is the shared host temp root the bwrap shape replaces
	// and the macOS profile denies.
	sandboxHostTmpDir = "/tmp"

	// seatbeltFirstPrintable and seatbeltDelete bound the characters a
	// Seatbelt string literal can represent; anything below or DEL refuses.
	seatbeltFirstPrintable = ' '
	seatbeltDelete         = 0x7f

	// seatbeltHeader keeps the network denial, denies every Mach service
	// lookup but the allowlist after it, and denies every write outside the
	// paths allowed later. A path policy alone leaves service-mediated
	// channels open: cfprefsd writes ~/Library/Preferences for `defaults
	// write`, the pasteboard server reads and writes the operator's clipboard,
	// the keychain broker hands out stored tokens, LaunchServices and
	// AppleEvents start other apps outside the sandbox. Every one of them is
	// reached by a bootstrap lookup of a global name, so default-deny closes
	// the whole class, including services nobody has listed yet. Seatbelt
	// applies the last matching rule, so the allows that follow re-open only
	// what they name.
	seatbeltHeader = `(version 1)(allow default)(deny network*)` +
		`(deny mach-lookup)` + seatbeltMachAllow + seatbeltServiceDenials +
		`(deny file-write* (subpath "/"))`

	// seatbeltServiceDenials closes the remaining operations `(allow
	// default)` grants that reach outside the run without a file write:
	// LaunchServices (lsopen, which alone stops `open` and `osascript ...
	// launch`), AppleEvents (appleevent-send), launchd job submission
	// (job-creation), distributed notifications, and signals to any process
	// the command did not start. The first four also need a Mach lookup the
	// default-deny refuses, so they are a second layer; the signal rule is
	// the only guard against the command killing the operator's own
	// processes, since Seatbelt has no PID namespace.
	seatbeltServiceDenials = `(deny lsopen)(deny appleevent-send)(deny job-creation)` +
		`(deny distributed-notification-post)(deny signal)(allow signal (target same-sandbox))`

	// seatbeltMachAllow is the whole Mach allowlist, found by running git,
	// go build/test, node/npm, python3, make, cc, perl, ruby, and a shell
	// under a blanket deny: they all work with no service at all except the
	// passwd and group lookups (getpwuid, getgrgid), which libinfo answers
	// through opendirectoryd. Without it `whoami` prints the uid and Go's
	// [os/user.Current] fails, which test suites call. The endpoint is
	// lookup-only: record writes go through opendirectoryd's separate API
	// service, which stays denied.
	seatbeltMachAllow = `(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo"))`

	// seatbeltDeviceWrites re-allows the device writes a shell needs after the
	// blanket write denial: the bit bucket, the controlling terminal, and the
	// descriptor aliases (/dev/stdout and /dev/stderr resolve under /dev/fd).
	seatbeltDeviceWrites = `(allow file-write* (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/fd/"))`

	// sandboxProbeScript is the macOS probe body: it proves Seatbelt permits
	// bash, mktemp, a write and read inside the private TMPDIR, and a read of
	// the working directory (the reviewed root in group mode, the repo cwd in
	// patch mode) before the real command runs.
	sandboxProbeScript = `p=$(mktemp "$TMPDIR/verdict-probe.XXXXXX") && printf ok > "$p" && test -s "$p" && rm -f "$p" && test -r .`

	// msgSandboxNetworkOnly is the warning every network-only run carries: the
	// opt-in exists because bwrap may be absent, and it must never read as the
	// full sandbox.
	msgSandboxNetworkOnly = "sandbox network-only: unshare --net isolates the network alone — " +
		"no mount, filesystem, or PID isolation; the host filesystem (credential paths, /tmp), " +
		"the host /proc (other processes' environments), and filesystem unix sockets " +
		"(docker.sock, the ssh-agent socket) stay reachable, and a unix socket bypasses the " +
		"network namespace. Use --sandbox with bubblewrap for the full shape."

	// sandboxGitName is the one path component git refuses to check out, so
	// it is the only thing under the reviewed root the PR itself cannot plant.
	sandboxGitName = ".git"

	// sandboxGitdirPrefix is the exact prefix git requires on a `.git` file.
	sandboxGitdirPrefix = "gitdir: "

	// sandboxGitPointerCap bounds a `.git` file or a commondir file read.
	sandboxGitPointerCap = 4 << 10

	// seatbeltGitComponent matches a `.git` path component in any letter
	// case (APFS is case-insensitive by default, and git finds `.GIT` there),
	// followed by a slash or the end of the path, so `.gitignore`, `.github`,
	// and `.git-blame-ignore-revs` stay writable.
	seatbeltGitComponent = `/(.*/)?\.[Gg][Ii][Tt](/|$)`

	// envSeparator splits one environment entry into its name and value.
	envSeparator = "="

	msgSandboxHomeUnset = "HOME is unset in the child environment, so the credential paths under it cannot be hidden"
)

// sandboxCredentialPaths is the one credential table both platforms hide,
// relative to every home the account answers to: directories behind an empty
// tmpfs, files behind a read-only /dev/null bind on Linux, and denied reads
// and writes under Seatbelt on macOS. Only entries that exist are mounted on
// Linux; macOS denies the whole table. New entries go at the end so the
// mount order of the existing ones never moves.
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
	".terraform.d",
	filepath.Join(".cargo", "credentials.toml"),
	filepath.Join(".cargo", "credentials"),
	".vault-token",
	filepath.Join("Library", "Keychains"),
}

// sandboxCredentialVar is one environment variable that relocates a
// credential store away from its home-relative default: the path it names
// (joined with suffix), or every entry of a path list.
type sandboxCredentialVar struct {
	name   string
	suffix string
	list   bool
}

// sandboxCredentialEnv is the relocation half of the table, read from both
// the child environment and the verdict process environment, so a store
// moved by the operator's shell is hidden even when the child never sees the
// variable. Relative values are skipped: they resolve against the reviewed
// root the command may already read.
var sandboxCredentialEnv = []sandboxCredentialVar{
	{name: "XDG_CONFIG_HOME", suffix: "gh"},
	{name: "GH_CONFIG_DIR"},
	{name: "AWS_SHARED_CREDENTIALS_FILE"},
	{name: "AWS_CONFIG_FILE"},
	{name: "DOCKER_CONFIG"},
	{name: "KUBECONFIG", list: true},
	{name: "CLOUDSDK_CONFIG"},
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

// sandboxInputs is what a mechanism resolves the credential table against:
// the environment the child runs with, the verdict process environment the
// request snapshotted, and the account home from the passwd database. A
// child HOME that differs from the account home still leaves the account's
// real credential stores on disk, so both are hidden.
type sandboxInputs struct {
	accountHome string
	childEnv    []string
	hostEnv     []string
}

// homes lists every home the credential table resolves under, the child's
// first. An unset child HOME is the one refusal: its paths cannot be named.
func (in sandboxInputs) homes() ([]string, *core.RefusalError) {
	home, ok := sandboxHome(in.childEnv)
	if !ok {
		return nil, sandboxUnavailable(msgSandboxHomeUnset)
	}
	if !filepath.IsAbs(home) {
		return nil, sandboxUnavailable(fmt.Sprintf(
			"HOME %q in the child environment is not an absolute path, so the credential paths under it cannot be hidden", home))
	}
	homes := []string{home}
	if host := envLookup(in.hostEnv, "HOME"); filepath.IsAbs(host) {
		homes = append(homes, host)
	}
	if filepath.IsAbs(in.accountHome) {
		homes = append(homes, in.accountHome)
	}
	return sandboxCleanUnique(homes), nil
}

// credentialLocations resolves the shared table: every home-relative entry
// under every home, then every relocation variable from the child and host
// environments. A reviewed root inside a credential location refuses, since
// hiding the location would hide the code under review and allowing the root
// would expose the credential. expandHomes adds each home's realpath first
// (Seatbelt matches resolved vnodes; bwrap follows the literal path itself).
func (in sandboxInputs) credentialLocations(root string, expandHomes bool) ([]string, *core.RefusalError) {
	homes, ref := in.homes()
	if ref != nil {
		return nil, ref
	}
	if expandHomes {
		homes = sandboxExpandRealpaths(homes)
	}
	locations := sandboxHomeCredentials(homes)
	for _, env := range [][]string{in.childEnv, in.hostEnv} {
		for _, v := range sandboxCredentialEnv {
			locations = append(locations, v.paths(env)...)
		}
	}
	locations = sandboxCleanUnique(locations)
	if location, ok := sandboxEnclosing(locations, root); ok {
		return nil, sandboxUnavailable(fmt.Sprintf(
			"the reviewed root %s is inside the credential path %s; refusing to expose or hide it", root, location))
	}
	return locations, nil
}

// sandboxHomeCredentials joins the home-relative table under every home.
func sandboxHomeCredentials(homes []string) []string {
	locations := make([]string, 0, len(homes)*len(sandboxCredentialPaths)+len(sandboxCredentialEnv))
	for _, home := range homes {
		for _, rel := range sandboxCredentialPaths {
			locations = append(locations, filepath.Join(home, rel))
		}
	}
	return locations
}

// sandboxEnclosing returns the first location that is root or one of its
// ancestors.
func sandboxEnclosing(locations []string, root string) (string, bool) {
	for _, location := range locations {
		if root == location || strings.HasPrefix(root, location+string(filepath.Separator)) {
			return location, true
		}
	}
	return "", false
}

// paths returns the absolute credential paths one environment's value of the
// variable names; an unset, empty, or relative value names nothing.
func (v sandboxCredentialVar) paths(env []string) []string {
	value := envLookup(env, v.name)
	values := []string{value}
	if v.list {
		values = filepath.SplitList(value)
	}
	var paths []string
	for _, path := range values {
		if filepath.IsAbs(path) {
			paths = append(paths, filepath.Join(path, v.suffix))
		}
	}
	return paths
}

// envLookup reads one name from an environment snapshot; the last entry wins,
// as it does for a spawned child.
func envLookup(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if key, v, ok := strings.Cut(kv, envSeparator); ok && key == name {
			value = v
		}
	}
	return value
}

// sandboxCleanUnique cleans every path and drops empties and duplicates while
// keeping first-seen order.
func sandboxCleanUnique(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// sandboxAccountHome is the passwd-database home of the current uid, or
// empty when the lookup fails; HOME alone can point anywhere.
func sandboxAccountHome() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

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
	cleanup     func() error
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
		name, _, _ := strings.Cut(kv, envSeparator)
		overridden[name] = true
	}
	env := make([]string, 0, len(base)+len(sb.env))
	for _, kv := range base {
		if name, _, ok := strings.Cut(kv, envSeparator); ok && overridden[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, sb.env...)
}

// close removes the mechanism's private state and reports what survived. It
// is safe on nil and runs at most once.
func (sb *sandbox) close() error {
	if sb == nil || sb.cleanup == nil {
		return nil
	}
	cleanup := sb.cleanup
	sb.cleanup = nil
	return cleanup()
}

// name is the stable mechanism name the records carry, empty when unsandboxed.
func (sb *sandbox) name() string {
	if sb == nil {
		return ""
	}
	return sb.mechanism
}

// full reports whether the run had the full sandbox shape — bwrap or
// sandbox-exec — which is what a recorded `sandboxed: true` means. The
// network-only opt-in names its mechanism but is never `sandboxed`.
func (sb *sandbox) full() bool {
	return sb != nil && !sb.networkOnly
}

// prepareSandbox builds the wrapper and probes it with a no-op command using
// the same flags before anything executes. A nil sandbox with a nil refusal
// means the run is unsandboxed; every enforcement failure is
// sandbox-unavailable. A failed probe returns the prepared sandbox beside the
// refusal so the caller releases its private state and reports the cleanup.
func prepareSandbox(
	ctx context.Context, commander core.Commander, workDir, root string, in sandboxInputs, networkOnly bool,
) (*sandbox, *core.RefusalError) {
	sb, ref := sandboxMechanism(runtime.GOOS, root, in, networkOnly)
	if ref != nil {
		return nil, ref
	}
	if ref := sb.probe(ctx, commander, workDir, in.childEnv); ref != nil {
		return sb, ref
	}
	return sb, nil
}

// sandboxMechanism picks the enforced shape for the host. networkOnly is the
// explicit Linux unshare opt-in for hosts without bubblewrap; it never turns
// --sandbox into a weaker mechanism, because the two flags are exclusive and
// the full flag still requires bwrap.
func sandboxMechanism(goos, root string, in sandboxInputs, networkOnly bool) (*sandbox, *core.RefusalError) {
	switch goos {
	case "linux":
		return linuxSandboxMechanism(root, in, networkOnly)
	case "darwin":
		if networkOnly {
			return nil, networkOnlyRefusal(goos)
		}
		return sandboxExecMechanism(root, in, "")
	default:
		return unsupportedMechanism(goos, networkOnly)
	}
}

// linuxSandboxMechanism keeps the full bwrap shape on --sandbox and the
// unshare opt-in on --sandbox-network-only; neither falls back to the other.
func linuxSandboxMechanism(root string, in sandboxInputs, networkOnly bool) (*sandbox, *core.RefusalError) {
	if networkOnly {
		return unshareNetMechanism(), nil
	}
	prefix, ref := bwrapArgv(root, in)
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
			"; use --sandbox here for sandbox-exec (no network, no Mach services, writes only to the reviewed root " +
			"and a private TMPDIR, credential paths and host /tmp denied)")
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
func bwrapArgv(root string, in sandboxInputs) ([]string, *core.RefusalError) {
	repo, err := filepath.Abs(root)
	if err != nil {
		return nil, sandboxUnavailable(fmt.Sprintf("could not resolve the reviewed root %s: %v", root, err))
	}
	if ref := sandboxFilesystemRootRefusal(repo, "bind"); ref != nil {
		return nil, ref
	}
	locations, ref := in.credentialLocations(repo, false)
	if ref != nil {
		return nil, ref
	}
	gitBinds, ref := sandboxGitBindTargets(repo)
	if ref != nil {
		return nil, ref
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
	for _, dir := range gitBinds {
		argv = append(argv, "--ro-bind", dir, dir)
	}
	for _, mount := range sandboxCredentialMounts(locations) {
		argv = append(argv, mount.argv()...)
	}
	return append(argv, "--unshare-net", "--new-session", "--die-with-parent", "--"), nil
}

// sandboxGitBindTargets validates the reviewed root's git control locations
// for bwrap and returns what to bind read-only over the writable root: the
// top-level entry of the root that holds each in-root path git would walk.
// Binding only a git directory deep in the tree would leave its in-root
// ancestors renamable — the command could move `meta` aside and put a link
// of its own at `meta` — while a top-level bind is a mount point (rename
// fails EBUSY) and pins its whole subtree. bwrap can protect only paths that
// exist, so every in-root component on the walk must exist and be a real
// directory; a missing one or a symlink refuses. A location outside the root
// is already read-only under `--ro-bind / /` (or private under the /tmp and
// /run tmpfs mounts).
func sandboxGitBindTargets(repo string) ([]string, *core.RefusalError) {
	if ref := sandboxGitRootRefusal(repo); ref != nil {
		return nil, ref
	}
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, sandboxUnavailable(fmt.Sprintf("could not resolve the reviewed root %s: %v", repo, err))
	}
	paths, ref := sandboxGitChainStrict(repo, realRepo)
	if ref != nil {
		return nil, ref
	}
	pins := make([]string, 0, len(paths))
	for _, path := range paths {
		if pin, ok := sandboxTopLevel(path, repo, realRepo); ok {
			pins = append(pins, pin)
		}
	}
	return sandboxCleanUnique(pins), nil
}

// sandboxTopLevel returns the root's top-level entry that holds path, spelled
// under the literal root, when path sits strictly below the root in either
// of its spellings.
func sandboxTopLevel(path, repo, realRepo string) (string, bool) {
	for _, base := range []string{repo, realRepo} {
		if rel, ok := sandboxRelInside(base, path); ok {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			return filepath.Join(repo, first), true
		}
	}
	return "", false
}

// sandboxGitChainStrict follows the root's `.git` and, when present, the
// commondir its git directory names, and returns every in-root path the
// walks visit. Each pointer must parse and name an existing directory.
func sandboxGitChainStrict(repo, realRepo string) ([]string, *core.RefusalError) {
	dotgit := filepath.Join(repo, sandboxGitName)
	info, err := os.Lstat(dotgit)
	if err != nil {
		return nil, sandboxGitInspectRefusal(dotgit, err)
	}
	paths := []string{dotgit}
	gitdir := dotgit
	switch {
	case info.Mode().IsRegular():
		walked, final, ref := sandboxGitFollowStrict(dotgit, sandboxGitdirPrefix, repo, repo, realRepo)
		if ref != nil {
			return nil, ref
		}
		paths = append(paths, walked...)
		gitdir = final
	case !info.IsDir():
		return nil, sandboxUnavailable(dotgit + " is neither a directory nor a `gitdir:` file")
	}
	walked, ref := sandboxGitCommondirStrict(gitdir, repo, realRepo)
	if ref != nil {
		return nil, ref
	}
	return append(paths, walked...), nil
}

// sandboxGitCommondirStrict follows the commondir a git directory names, if
// it has one.
func sandboxGitCommondirStrict(gitdir, repo, realRepo string) ([]string, *core.RefusalError) {
	commondir := filepath.Join(gitdir, "commondir")
	if _, err := os.Lstat(commondir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, sandboxGitInspectRefusal(commondir, err)
	}
	walked, _, ref := sandboxGitFollowStrict(commondir, "", gitdir, repo, realRepo)
	return walked, ref
}

// sandboxGitFollowStrict walks one pointer's raw target and refuses every
// shape bwrap cannot pin: a target that is not an existing directory, one
// that is the root itself, and any in-root component on the walk that is a
// symlink (it could be replaced) or does not exist yet (it could be created
// as a symlink, which is how `a/../g` escapes once `a` appears). It returns
// the in-root paths visited, the target included, and the target.
func sandboxGitFollowStrict(pointer, prefix, base, repo, realRepo string) ([]string, string, *core.RefusalError) {
	raw, _, ref := sandboxGitPointerRaw(pointer, prefix)
	if ref != nil {
		return nil, "", ref
	}
	steps, final := sandboxGitWalk(base, raw)
	if ref := sandboxGitTargetRefusal(pointer, final, realRepo); ref != nil {
		return nil, "", ref
	}
	walked := make([]string, 0, len(steps)+1)
	for _, step := range steps {
		if _, inside := sandboxTopLevel(step.path, repo, realRepo); !inside {
			continue
		}
		if ref := sandboxGitStepRefusal(pointer, step); ref != nil {
			return nil, "", ref
		}
		walked = append(walked, step.path)
	}
	return append(walked, final), final, nil
}

// sandboxGitTargetRefusal refuses a walk's target that is not an existing
// directory, or that is the root itself.
func sandboxGitTargetRefusal(pointer, final, realRepo string) *core.RefusalError {
	if info, err := os.Stat(final); err != nil || !info.IsDir() {
		return sandboxUnavailable(fmt.Sprintf("%s names the git directory %s, which does not exist as a "+
			"directory, so the command could create it and git would run its hooks later — fix or remove the "+
			"pointer, or verify a fresh clone", pointer, final))
	}
	if resolved, err := filepath.EvalSymlinks(final); err == nil && resolved == realRepo {
		return sandboxUnavailable(fmt.Sprintf(
			"the git location %s is the reviewed root itself, so bwrap cannot make it read-only", final))
	}
	return nil
}

// sandboxGitStepRefusal refuses one in-root component of a pointer walk that
// the command could turn into a link git would follow.
func sandboxGitStepRefusal(pointer string, step sandboxGitStep) *core.RefusalError {
	switch {
	case step.symlink:
		return sandboxUnavailable(fmt.Sprintf("the git pointer %s passes through the symlink %s inside the reviewed "+
			"root, and the link could be replaced — point the pointer at the directory itself", pointer, step.path))
	case step.missing:
		return sandboxUnavailable(fmt.Sprintf("the git pointer %s walks through %s, which does not exist inside the "+
			"reviewed root, so the command could create it as a link that git follows — point the pointer at the "+
			"directory itself", pointer, step.path))
	}
	return nil
}

// sandboxGitStep is one path a git pointer walk visits, before any symlink
// at it is followed.
type sandboxGitStep struct {
	path    string
	missing bool
	symlink bool
}

// sandboxGitWalk resolves a pointer's raw target the way the kernel and git
// do: `.` stays, `..` pops the directory reached so far (after every symlink
// before it was followed), and every other component is Lstat'ed and, when it
// is a symlink, followed. It returns each component path it visits and the
// final path. Cleaning the target first would be wrong: `a/../g` cleans to
// `g` and never looks at `a`, while git resolves `a` and goes wherever it
// points.
func sandboxGitWalk(base, raw string) ([]sandboxGitStep, string) {
	current, rest := base, raw
	if filepath.IsAbs(raw) {
		volume := filepath.VolumeName(raw)
		current, rest = volume+string(filepath.Separator), raw[len(volume):]
	}
	var steps []sandboxGitStep
	for _, part := range strings.FieldsFunc(rest, sandboxIsSeparator) {
		switch part {
		case ".":
		case "..":
			current = filepath.Dir(current)
		default:
			var step sandboxGitStep
			step, current = sandboxGitVisit(filepath.Join(current, part))
			steps = append(steps, step)
		}
	}
	return steps, current
}

// sandboxGitVisit inspects one component and returns where the walk stands
// after it: the component itself, or a symlink's resolved target.
func sandboxGitVisit(path string) (sandboxGitStep, string) {
	info, err := os.Lstat(path)
	if err != nil {
		return sandboxGitStep{path: path, missing: true}, path
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return sandboxGitStep{path: path}, path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return sandboxGitStep{path: path, symlink: true, missing: true}, path
	}
	return sandboxGitStep{path: path, symlink: true}, resolved
}

// sandboxIsSeparator splits a pointer on `/` (git writes it) and on the
// host separator.
func sandboxIsSeparator(r rune) bool {
	return r == '/' || r == filepath.Separator
}

// sandboxGitInspectRefusal names the real I/O error when a git location
// cannot be inspected.
func sandboxGitInspectRefusal(path string, err error) *core.RefusalError {
	return sandboxUnavailable(fmt.Sprintf("could not inspect %s (%v), so it cannot be protected", path, err))
}

// sandboxGitRootRefusal refuses a reviewed root whose `.git` bwrap cannot
// make read-only. bwrap protects a path only by mounting over it, and it
// creates a missing mount point inside the new root — under the writable
// root bind that is a write to the host tree — so a root with no `.git` has
// no clean read-only shape: the command could create <root>/.git/hooks or
// config that git runs later outside any sandbox. A symlinked `.git` is the
// same gap, because only the link's target can be bound read-only and the
// link itself stays replaceable. An error other than "does not exist" is
// named as itself. macOS needs none of this: its profile denies writes to
// every `.git` path below the root, existing or not.
func sandboxGitRootRefusal(repo string) *core.RefusalError {
	dotgit := filepath.Join(repo, sandboxGitName)
	info, err := os.Lstat(dotgit)
	switch {
	case err != nil && !os.IsNotExist(err):
		return sandboxGitInspectRefusal(dotgit, err)
	case err != nil:
		return sandboxUnavailable(fmt.Sprintf("the reviewed root %s has no %s, and bwrap can make only an "+
			"existing path read-only, so the command could create git hooks or config there that run later "+
			"outside the sandbox — verify from the top of a git checkout, or run the whole review in a "+
			"throwaway VM or container", repo, sandboxGitName))
	case info.Mode()&os.ModeSymlink != 0:
		return sandboxUnavailable(fmt.Sprintf("the reviewed root's %s is a symlink, and bwrap can protect only its "+
			"target, so the link could be replaced with a git directory that runs later — replace the link with the "+
			"directory or a `gitdir:` file, or verify a fresh clone", dotgit))
	}
	return nil
}

// sandboxRelInside returns path relative to base when path is strictly below
// base.
func sandboxRelInside(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// sandboxGitDirs lists, for the macOS profile, every in-root path a git
// pointer walk visits — `<root>/.git`, each component of the `gitdir:` and
// commondir walks (missing ones and symlinks included), and the git
// directories they reach — so each is write-denied by path. A component that
// does not exist yet is denied too, so it cannot be created as a link; one
// that is a symlink keeps its own spelling denied, so it cannot be replaced.
// Paths outside the root are left out: every write there is denied already,
// and a subpath rule on an ancestor of the root would deny the root itself.
func sandboxGitDirs(root string) ([]string, *core.RefusalError) {
	dotgit := filepath.Join(root, sandboxGitName)
	info, err := os.Lstat(dotgit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, sandboxGitInspectRefusal(dotgit, err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	dirs := []string{dotgit}
	gitdir := dotgit
	if info.Mode().IsRegular() {
		raw, _, ref := sandboxGitPointerRaw(dotgit, sandboxGitdirPrefix)
		if ref != nil {
			return nil, ref
		}
		var steps []sandboxGitStep
		steps, gitdir = sandboxGitWalk(root, raw)
		dirs = append(dirs, sandboxGitInRoot(steps, gitdir, root, realRoot)...)
	}
	common, ref := sandboxGitCommondirDirs(gitdir, root, realRoot)
	if ref != nil {
		return nil, ref
	}
	return sandboxCleanUnique(append(dirs, common...)), nil
}

// sandboxGitCommondirDirs lists the in-root paths of the commondir walk, if
// the git directory names one.
func sandboxGitCommondirDirs(gitdir, root, realRoot string) ([]string, *core.RefusalError) {
	raw, present, ref := sandboxGitPointerRaw(filepath.Join(gitdir, "commondir"), "")
	if ref != nil || !present {
		return nil, ref
	}
	steps, common := sandboxGitWalk(gitdir, raw)
	return sandboxGitInRoot(steps, common, root, realRoot), nil
}

// sandboxGitInRoot keeps the walk's paths, and its target, that sit strictly
// below the root.
func sandboxGitInRoot(steps []sandboxGitStep, final, root, realRoot string) []string {
	var paths []string
	for _, path := range append(sandboxGitStepPaths(steps), final) {
		if _, inside := sandboxTopLevel(path, root, realRoot); inside {
			paths = append(paths, path)
		}
	}
	return paths
}

func sandboxGitStepPaths(steps []sandboxGitStep) []string {
	paths := make([]string, 0, len(steps))
	for _, step := range steps {
		paths = append(paths, step.path)
	}
	return paths
}

// sandboxGitPointerRaw reads one git pointer file the way git does — the
// whole file, with only trailing CR and LF stripped (spaces are part of the
// path) and, for `.git`, the exact `gitdir: ` prefix — so the target is the
// one git will use. The open never blocks (a FIFO is refused, not waited
// on), and a non-regular file, a read error, or a file past the cap refuses
// instead of yielding a prefix. present is false only when the file does not
// exist.
func sandboxGitPointerRaw(path, prefix string) (target string, present bool, ref *core.RefusalError) {
	raw, err := readcommon.ReadRegularCapped(path, sandboxGitPointerCap)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", true, sandboxUnavailable(fmt.Sprintf("could not read the git pointer %s (%v), so the git "+
			"directory it names cannot be protected", path, err))
	}
	content := strings.TrimRight(string(raw), "\r\n")
	target, ok := strings.CutPrefix(content, prefix)
	if !ok || target == "" {
		return "", true, sandboxUnavailable(fmt.Sprintf("%s has no readable git pointer line, so the git "+
			"directory it should name cannot be protected", path))
	}
	return target, true, nil
}

// sandboxCredentialMounts resolves the locations against the host, so a
// missing entry adds no mount and a file never gets a directory's tmpfs.
func sandboxCredentialMounts(locations []string) []sandboxMount {
	mounts := make([]sandboxMount, 0, len(locations))
	for _, dest := range locations {
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

// sandboxFilesystemRootRefusal refuses a reviewed root that is, or resolves
// to, the filesystem root: a root that is a symlink to `/` would otherwise
// make the whole host writable once the mechanism resolves it.
func sandboxFilesystemRootRefusal(repo, verb string) *core.RefusalError {
	spellings := []string{repo}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		spellings = append(spellings, resolved)
	}
	for _, path := range spellings {
		if filepath.Dir(path) == path {
			return sandboxUnavailable(fmt.Sprintf(
				"the reviewed root %s is the filesystem root %s; refusing to %s the whole host writable", repo, path, verb))
		}
	}
	return nil
}

// sandboxHome reads the home the child will see: the environment snapshot
// when it carries one, else the process home an inherited child environment
// resolves to. An explicit snapshot without HOME cannot be hidden.
func sandboxHome(env []string) (string, bool) {
	// os/exec keeps the last of duplicate entries, so the child's HOME is the
	// last one; hiding credentials under an earlier one would miss them.
	home, found := "", false
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, envSeparator); ok && name == "HOME" {
			home, found = value, true
		}
	}
	if found {
		return home, home != ""
	}
	if len(env) == 0 {
		home, err := os.UserHomeDir()
		return home, err == nil && home != ""
	}
	return "", false
}

// sandboxExecMechanism is the macOS shape: a generated Seatbelt profile that
// denies the network, every Mach service but one lookup-only allow, and every
// write outside the reviewed root and one per-run private directory, and
// denies reads of the credential table and the shared host temp roots.
// Seatbelt has no mount namespace, so this hides paths by policy rather than
// replacing them. The child's TMPDIR/TMP/TEMP, GOTMPDIR, GOCACHE, and
// XDG_CACHE_HOME point into the private directory so common toolchains keep
// a writable scratch and cache.
func sandboxExecMechanism(root string, in sandboxInputs, tmpBase string) (*sandbox, *core.RefusalError) {
	if _, ref := in.homes(); ref != nil {
		return nil, ref
	}
	repo, err := filepath.Abs(root)
	if err != nil {
		return nil, sandboxUnavailable(fmt.Sprintf("could not resolve the reviewed root %s: %v", root, err))
	}
	if ref := sandboxFilesystemRootRefusal(repo, "allow"); ref != nil {
		return nil, ref
	}
	private, profile, ref := sandboxExecPrivate(in, repo, tmpBase)
	if ref != nil {
		return nil, ref
	}
	return &sandbox{
		tool:      sandboxExecTool,
		mechanism: sandboxMechanismSandboxExec,
		prefix:    []string{sandboxExecPath, "-p", profile},
		env:       sandboxPrivateEnv(private),
		cleanup:   func() error { return removePrivateDir(private) },
	}, nil
}

// sandboxExecPrivate creates the per-run private directory and the profile
// around it. The directory is removed on every setup failure, through one
// guarded deferred cleanup at its acquisition (GO-DEFER-01); the caller's
// sandbox owns it on success.
func sandboxExecPrivate(in sandboxInputs, repo, tmpBase string) (private, profile string, ref *core.RefusalError) {
	private, err := os.MkdirTemp(tmpBase, sandboxPrivateDirPrefix)
	if err != nil {
		return "", "", sandboxUnavailable(fmt.Sprintf("could not create the private temporary directory: %v", err))
	}
	defer func() {
		if ref == nil {
			return
		}
		if err := removePrivateDir(private); err != nil {
			ref.Message += fmt.Sprintf(" (the private directory %s could not be removed: %v)", private, err)
		}
	}()
	profile, ref = sandboxExecPrivateProfile(in, repo, private, tmpBase)
	if ref != nil {
		return "", "", ref
	}
	return private, profile, nil
}

// sandboxExecPrivateProfile creates the private cache directory, then builds
// the profile around the private tree.
func sandboxExecPrivateProfile(in sandboxInputs, repo, private, tmpBase string) (string, *core.RefusalError) {
	if err := os.Mkdir(filepath.Join(private, sandboxCacheDir), sandboxPrivateDirMode); err != nil {
		return "", sandboxUnavailable(fmt.Sprintf("could not create the private cache directory: %v", err))
	}
	locations, ref := in.credentialLocations(repo, true)
	if ref != nil {
		return "", ref
	}
	gitDirs, ref := sandboxGitDirs(repo)
	if ref != nil {
		return "", ref
	}
	return sandboxExecProfile(&sandboxProfileInputs{
		credentials: locations, gitDirs: gitDirs, root: repo, private: private, tmpBase: tmpBase,
	})
}

// sandboxPrivateEnv is the child-environment override that keeps scratch and
// caches inside the one writable private tree.
func sandboxPrivateEnv(private string) []string {
	return []string{
		"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private, "GOTMPDIR=" + private,
		"GOCACHE=" + filepath.Join(private, sandboxGoCacheDir),
		"XDG_CACHE_HOME=" + filepath.Join(private, sandboxCacheDir),
	}
}

// removePrivateDir restores owner access to every directory in the private
// tree, then removes it, so a child that ran `chmod 0` on its own scratch
// cannot strand the tree. Symlinks are never followed: the walk reports them
// as non-directories, and only directories are re-permissioned.
func removePrivateDir(private string) error {
	var restoreErrs []error
	walkErr := filepath.WalkDir(private, func(path string, d fs.DirEntry, err error) error {
		restoreErrs = append(restoreErrs, restoreOwnerDir(path, d, err))
		return nil
	})
	if err := os.RemoveAll(private); err != nil {
		return jq.Sealed("remove "+private, errors.Join(append([]error{err, walkErr}, restoreErrs...)...))
	}
	return nil
}

// restoreOwnerDir gives one walked directory back its owner access; a walk
// error or a non-directory (a symlink included) is left as found.
func restoreOwnerDir(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if !d.IsDir() {
		return nil
	}
	return os.Chmod(path, sandboxPrivateDirMode) //nolint:wrapcheck // joined and sealed by removePrivateDir
}

// sandboxExecProfile builds the profile in precedence order — Seatbelt
// applies the last matching rule — so each later rule overrides the earlier
// ones it overlaps:
//
//  1. the header: network, every Mach service but the allowlist, and every
//     write denied;
//  2. reads and writes denied under the shared host temp roots;
//  3. reads and writes re-allowed for the private tree and the reviewed root
//     (each as written and as its realpath), plus metadata reads of their
//     ancestor directories alone, so realpath and git's repository discovery
//     work for a root under a denied temp root while listing it stays denied;
//  4. the device writes a shell needs re-allowed;
//  5. writes denied to every `.git` path component under the reviewed root
//     (the root's own git directory, a nested repository, a `.git` file, in
//     any letter case, existing or not) and to the git directories a `.git`
//     file or a commondir points at — the only paths under the root the PR
//     cannot already control, and the ones git executes from later;
//  6. the credential locations denied last, so a reviewed root under HOME
//     cannot re-expose one.
func sandboxExecProfile(in *sandboxProfileInputs) (string, *core.RefusalError) {
	hostTemp := in.tmpBase
	if hostTemp == "" {
		hostTemp = os.TempDir()
	}
	denyTemp := sandboxExpandRealpaths(append(slices.Clone(sandboxHostTempDirs), hostTemp))
	roots := sandboxExpandRealpaths([]string{in.root})
	allow := sandboxExpandRealpaths([]string{in.private, in.root})
	denyCredential := sandboxExpandRealpaths(in.credentials)
	var b strings.Builder
	b.WriteString(seatbeltHeader)
	if ref := sandboxWriteRules(&b, "deny", denyTemp); ref != nil {
		return "", ref
	}
	if ref := sandboxWriteRules(&b, "allow", allow); ref != nil {
		return "", ref
	}
	if ref := sandboxAncestorMetadata(&b, allow); ref != nil {
		return "", ref
	}
	b.WriteString(seatbeltDeviceWrites)
	if ref := sandboxGitWriteDenials(&b, roots, sandboxExpandRealpaths(in.gitDirs)); ref != nil {
		return "", ref
	}
	if ref := sandboxWriteRules(&b, "deny", denyCredential); ref != nil {
		return "", ref
	}
	return b.String(), nil
}

// sandboxProfileInputs is what one Seatbelt profile is built from.
type sandboxProfileInputs struct {
	root        string
	private     string
	tmpBase     string
	credentials []string
	gitDirs     []string
}

// sandboxGitWriteDenials renders the git control denials: one regex per root
// spelling for any `.git` component below it, then one subpath per resolved
// git directory (which may sit under the root without a `.git` name).
func sandboxGitWriteDenials(b *strings.Builder, roots, gitDirs []string) *core.RefusalError {
	for _, root := range roots {
		literal, ref := seatbeltLiteral("^" + seatbeltRegexQuote(root) + seatbeltGitComponent)
		if ref != nil {
			return ref
		}
		fmt.Fprintf(b, "(deny file-write* (regex %s))", literal)
	}
	for _, dir := range gitDirs {
		literal, ref := seatbeltLiteral(dir)
		if ref != nil {
			return ref
		}
		fmt.Fprintf(b, "(deny file-write* (subpath %s))", literal)
	}
	return nil
}

// seatbeltRegexQuote escapes every regex metacharacter in a path so the root
// matches only itself.
func seatbeltRegexQuote(path string) string {
	var b strings.Builder
	for _, r := range path {
		if strings.ContainsRune(`\.^$|()[]{}*+?`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sandboxExpandRealpaths cleans every path, adds its resolved realpath, and
// removes duplicates while keeping first-seen order. Seatbelt matches the
// resolved vnode path, so the /var -> /private/var spelling of a temp root
// needs both forms. A path that does not exist yet (a git directory a `.git`
// file names, a credential file not created) resolves through its deepest
// existing ancestor, so a rule still matches the spelling the kernel will
// see when the command tries to create it. A path that is itself a symlink
// also gets its own node's spelling (its resolved parent plus its name), not
// only its target's: Seatbelt checks an unlink or rename against the link's
// canonical path, so without it the link could be replaced.
func sandboxExpandRealpaths(paths []string) []string {
	expanded := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		expanded = append(expanded, path)
		if resolved, ok := sandboxRealpath(path); ok {
			expanded = append(expanded, resolved)
		}
		if parent, ok := sandboxRealpath(filepath.Dir(path)); ok {
			expanded = append(expanded, filepath.Join(parent, filepath.Base(path)))
		}
	}
	return sandboxCleanUnique(expanded)
}

// sandboxRealpath resolves path, or the deepest existing ancestor of it with
// the missing tail appended.
func sandboxRealpath(path string) (string, bool) {
	tail := ""
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, tail), true
		}
		if filepath.Dir(current) == current {
			return "", false
		}
		tail = filepath.Join(filepath.Base(current), tail)
	}
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

// sandboxAncestorMetadata renders one rule allowing metadata reads (stat,
// never a listing or a file's contents) of every proper ancestor of the
// allowed paths, matched literally.
func sandboxAncestorMetadata(b *strings.Builder, paths []string) *core.RefusalError {
	ancestors := sandboxAncestors(paths)
	if len(ancestors) == 0 {
		return nil
	}
	b.WriteString("(allow file-read-metadata")
	for _, dir := range ancestors {
		literal, ref := seatbeltLiteral(dir)
		if ref != nil {
			return ref
		}
		b.WriteString(" (literal " + literal + ")")
	}
	b.WriteString(")")
	return nil
}

// sandboxAncestors lists every proper ancestor of the paths, nearest first,
// without duplicates.
func sandboxAncestors(paths []string) []string {
	var ancestors []string
	for _, path := range paths {
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			ancestors = append(ancestors, dir)
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return sandboxCleanUnique(ancestors)
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
		return sandboxUnavailable(fmt.Sprintf("%s could not start (%v) — %s", sb.tool, err, sb.startRemedy()))
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

// startRemedy names the tool the requested boundary needs when its runner
// could not even spawn, per mechanism, instead of one generic sentence.
func (sb *sandbox) startRemedy() string {
	switch sb.tool {
	case bwrapTool:
		return "--sandbox on Linux needs bubblewrap (bwrap) on PATH; install it, " +
			"or pass --sandbox-network-only to accept network isolation alone"
	case unshareTool:
		return "--sandbox-network-only needs util-linux unshare on PATH; install it, " +
			"or use --sandbox with bubblewrap for the full shape"
	case sandboxExecTool:
		return "--sandbox on macOS needs the built-in " + sandboxExecPath +
			"; restore it, or run the bwrap recipe in a Linux VM or container"
	}
	return "install the sandbox runner, or use the manual recipe on another OS"
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
