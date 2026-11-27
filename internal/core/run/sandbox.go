package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hostsandbox"
	"github.com/stainless-code/verdict/internal/jq"
)

// Every review launch also runs in the host's OS sandbox (STA-538). The
// disposable snapshot stops a relative-path write, but most agents' file
// tools take absolute paths, so a prompt-injected diff could still steer one
// into ~/.zshrc, ~/.config, or the operator's checkout, or into reading
// ~/.ssh and leaking it through review text verdict may post. The sandbox
// allows writes only to the launch's snapshot wrapper (its copy and private
// TMPDIR), the agent's own data paths, and the devices a process needs; it
// denies reads of verify's credential table except a path that is the
// agent's own login, the unix sockets a process could reach past the
// filesystem rules (an ssh agent, docker, systemd, tmux) — on Linux an
// abstract-namespace socket in the shared network namespace stays reachable
// — and on macOS every Mach service but a short allowlist (the keychain's
// only for an agent whose login it is). The network stays open: the agent
// has to reach its model API.
// macOS wraps the launch in sandbox-exec with a generated profile, Linux in
// bwrap; any other host has none.

// codeReviewSandboxUnavailable refuses a review the host cannot sandbox for
// a row that confines nothing on its own, and a review whose sandbox policy
// cannot be built.
const codeReviewSandboxUnavailable = "review-sandbox-unavailable"

// flagAllowUnsandboxed is the opt-out the refusal and the warnings name.
const flagAllowUnsandboxed = "--allow-unsandboxed-review"

// The review launches' postures, as data.sandbox.posture publishes them.
const (
	postureSandboxed     = "sandboxed"
	postureSelfSandboxed = "self-sandboxed"
	postureUnsandboxed   = "unsandboxed"
	postureUnavailable   = "unavailable"
)

// mechanismNone is data.sandbox.mechanism when nothing wraps the launch.
const mechanismNone = "none"

// placeholderSnapshot is the per-launch path a --print render's wrapper
// carries, which exists only once a launch makes it.
const placeholderSnapshot = "{snapshot}"

// reviewProbeTimeout and reviewProbeCap bound the no-op the mechanism proves
// itself with before any agent starts, verify's budget.
const (
	reviewProbeTimeout = 15 * time.Second
	reviewProbeCap     = 4 << 10
)

// reviewProbeScript is the macOS probe body: a profile that compiles can
// still deny bash its private TMPDIR.
const reviewProbeScript = `p=$(mktemp "$TMPDIR/verdict-probe.XXXXXX") && printf ok > "$p" && test -s "$p" && rm -f "$p"`

// seatbeltReviewHeader allows everything — the network included, since
// agents reach their model API — then closes the service channels verify
// closes, every write, and the channels that reach outside without a file
// write: every Mach service but seatbeltMachAllow, preference writes, and
// every unix socket but the resolver's and syslog's (an ssh agent,
// gpg-agent, Docker Desktop, tmux, a language server). Default-deny closes
// the whole service class, including services nobody has listed: cfprefsd (a
// `defaults write` into another app's plist runs at its next launch), the
// pasteboard, the keychain brokers, LaunchServices' database (`lsregister`
// of a planted app), login items, the window server (synthetic input under
// the terminal's Accessibility grant), Shortcuts. The rules after it
// re-open only what they name, Seatbelt applying the last matching rule.
const seatbeltReviewHeader = `(version 1)(allow default)` + hostsandbox.SeatbeltServiceDenials +
	`(deny file-write* (subpath "/"))` +
	`(deny user-preference-write)` +
	`(deny mach-lookup)` + seatbeltMachAllow +
	`(deny network-outbound (remote unix-socket))` +
	`(allow network-outbound (remote unix-socket (path-literal "/private/var/run/mDNSResponder")))` +
	`(allow network-outbound (remote unix-socket (path-literal "/private/var/run/syslog")))`

// seatbeltMachAllow is the whole Mach allowlist, found by running the
// catalog's sandboxed CLIs (claude, codex, opencode, pi) and git, curl, node
// and a Go client under a blanket denial against a local stub server and a
// public HTTPS host, and adding only what one needed:
//
//   - opendirectoryd.libinfo answers user and group lookups (verify's
//     allowlist; lookup-only, record writes use another service);
//   - trustd.agent evaluates certificate trust for Security.framework TLS
//     clients (Go's, among them): without it every handshake fails;
//   - SystemConfiguration's configd and DNSConfiguration hand out the system
//     proxy and resolver settings HTTP clients and libresolv read, so a
//     proxied network still reaches the model API.
//
// DNS itself goes through mDNSResponder's socket, allowed below.
const seatbeltMachAllow = `(allow mach-lookup` +
	` (global-name "com.apple.system.opendirectoryd.libinfo")` +
	` (global-name "com.apple.trustd.agent")` +
	` (global-name "com.apple.SystemConfiguration.configd")` +
	` (global-name "com.apple.SystemConfiguration.DNSConfiguration"))`

// seatbeltKeychainAllow opens the keychain brokers to a launch whose own
// login is the keychain (claude's), and to no other: with them, a shell
// could ask for any stored token by name.
const seatbeltKeychainAllow = `(allow mach-lookup (global-name "com.apple.SecurityServer")` +
	` (global-name "com.apple.securityd.xpc"))`

// seatbeltTTYDenial closes every terminal to a review launch, after the
// shared device writes re-open /dev/tty: with a terminal fd an agent can push
// input into the operator's shell (TIOCSTI), read-only or not. The launch
// also starts in a session of its own, with no controlling terminal.
const seatbeltTTYDenial = `(deny file-read* file-write* file-ioctl (literal "/dev/tty") (regex #"^/dev/ttys[0-9]+$"))`

// systemCABundle is macOS's PEM bundle of the system roots. A TLS client that
// loads its roots from the keychain (codex's) fails without the keychain
// brokers; SSL_CERT_FILE points it at the bundle instead.
const systemCABundle = "/etc/ssl/cert.pem"

// seatbeltTempRoots are the shared host temp roots a macOS launch may not
// read: their sockets and stray files belong to the operator's other
// processes. The launch's own TMPDIR lives in its wrapper.
var seatbeltTempRoots = []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp"}

// bwrapHiddenRoots are the Linux directories a launch sees as empty,
// read-only tmpfs mounts: their unix sockets (the systemd user manager and
// its bus, docker and podman, tmux, an ssh agent, gnome-keyring) reach a
// process connect() lets past every filesystem rule.
var bwrapHiddenRoots = []string{"/tmp", "/var/tmp", "/run", "/var/run"}

// bwrapReviewHead is the Linux shape's fixed part after the runner: a
// read-only host, a fresh /dev, and a /proc under a private PID namespace,
// so other processes' environments stay hidden. The network is shared.
var bwrapReviewHead = []string{
	hostsandbox.ROBind, "/", "/", "--dev", "/dev", "--proc", "/proc", "--unshare-pid",
}

// bwrapReviewTail starts the agent in its own session, so it cannot push
// input into the operator's terminal, and kills it with verdict.
var bwrapReviewTail = []string{"--new-session", "--die-with-parent", "--"}

// socketEnv names the sockets, displays, and terminals a review launch never
// inherits: each points the agent at a channel out of the sandbox (an ssh
// agent, the session bus, gpg-agent, tmux and nvim servers, the runtime
// directory that holds them, the X11 or Wayland display and its cookie).
var socketEnv = []string{
	"SSH_AUTH_SOCK", "DBUS_SESSION_BUS_ADDRESS", "GPG_AGENT_INFO", "TMUX", "NVIM", "XDG_RUNTIME_DIR",
	"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY",
}

// keychainState is the macOS login keychain as a state path.
var keychainState = filepath.Join("Library", "Keychains")

// reviewBox is one execution's review-sandbox posture, decided once before
// any agent starts: the mechanism that wraps each launch ("" for none) and
// the runner it spawns, the posture data.sandbox publishes, and what the
// policy is built from, kept so it is checked again before every launch.
type reviewBox struct {
	projects    *statePath
	project     *agentcatalog.ProjectState
	mechanism   string
	runner      string
	posture     string
	home        string
	credentials []string
	state       []statePath
	readOnly    []statePath
	dirs        []statePath
	operator    bool
}

// statePath is one resolved state path below its base (HOME or the
// relocation root, relocated says which). A prefix entry names every file in
// its directory whose name starts with the path's base name.
type statePath struct {
	path      string
	base      string
	prefix    bool
	relocated bool
}

// sandboxed reports whether launches are wrapped.
func (b *reviewBox) sandboxed() bool {
	return b != nil && b.posture == postureSandboxed
}

// keychain reports whether the agent's own state is the macOS keychain.
func (b *reviewBox) keychain() bool {
	return !b.operator && b.home != "" && slices.ContainsFunc(b.state, func(s statePath) bool {
		return !s.prefix && s.path == filepath.Join(b.home, keychainState)
	})
}

// data is the posture data.sandbox carries; a render adds the wrapper with
// the per-launch path as a placeholder.
func (b *reviewBox) data(render bool) *core.RunSandbox {
	if b == nil {
		return nil
	}
	out := &core.RunSandbox{Mechanism: mechanismNone, Posture: b.posture}
	if b.mechanism == "" {
		return out
	}
	out.Mechanism = b.mechanism
	if render {
		// The profile was built once already when the box was settled; a
		// failure here would only leave the wrapper out.
		if wrapper, err := b.prefix([]string{placeholderSnapshot}, "", false); err == nil {
			out.Wrapper = wrapper
		}
	}
	return out
}

// annotate records the posture in a response.
func (b *reviewBox) annotate(resp *core.RunResponse, render bool) {
	if resp != nil && resp.Data != nil {
		resp.Data.Sandbox = b.data(render)
	}
}

// settleBox decides the execution's review-sandbox posture before any agent
// starts, nil only for a simulated commander. A row whose own OS sandbox
// would nest skips verdict's; the opt-out runs every launch bare; otherwise
// a policy that cannot be built refuses outright, and a host whose mechanism
// is missing or fails its probe runs only a row that confines its own
// writes, warned. A render probes nothing and never refuses for the host: it
// reports the posture a launch would take.
func (st *state) settleBox(ctx context.Context, def *agentcatalog.Launch) *core.RefusalError {
	if !core.StartsHostProcesses(st.commander()) {
		return nil
	}
	owned := operatorSpells(st.req.PassThrough,
		slices.Concat(def.PermissionFlags, def.DiscoveryOverrideFlags, def.ReviewOwnedFlags))
	if st.bareBox(def, owned) {
		return nil
	}
	box, why, err := st.buildBox(ctx, def)
	switch {
	case ctx.Err() != nil:
		return st.refuse(codeCanceled, msgCanceled)
	case err != nil:
		return st.refuse(codeReviewSandboxUnavailable, fmt.Sprintf("review sandbox policy refused: %v — "+
			"verdict runs no review it cannot sandbox as built%s", err, st.optOutRemedy()))
	case why != "":
		return st.noBox(def, owned, why)
	}
	st.box = box
	st.warnStateless(def)
	st.warnNested(def)
	return nil
}

// valueFullAccess is codex's sandbox mode that starts no sandbox of its own.
const valueFullAccess = "danger-full-access"

// warnNested warns when a row with its own OS sandbox runs inside verdict's
// on macOS — the operator's `--` decided its permissions — without turning
// its own off: a nested Seatbelt profile fails, so its commands would.
func (st *state) warnNested(def *agentcatalog.Launch) {
	if def.Confinement != agentcatalog.OwnSandbox || st.box.mechanism != hostsandbox.SandboxExecTool ||
		slices.ContainsFunc(st.req.PassThrough, func(a string) bool { return strings.Contains(a, valueFullAccess) }) {
		return
	}
	st.boxWarn(core.Msgf("%s runs inside verdict's review sandbox because your `--` arguments decide its "+
		"permissions, and its own macOS sandbox fails nested in it: pass `--sandbox %s` after `--` so verdict's "+
		"sandbox alone confines it", def.Name, valueFullAccess))
}

// optOutRemedy names the opt-out, on a verb that has it.
func (st *state) optOutRemedy() string {
	if !st.req.UnsandboxedOptOut {
		return ""
	}
	return "; pass " + flagAllowUnsandboxed + " to review without the sandbox"
}

// bareBox settles the two postures that never build verdict's sandbox: a row
// whose own OS sandbox applies (owned is the operator's `--` deciding its
// permissions instead), and the opt-out.
func (st *state) bareBox(def *agentcatalog.Launch, owned bool) bool {
	switch {
	case def.Confinement == agentcatalog.OwnSandbox && len(def.SnapshotWriteArgs) > 0 && !owned:
		st.box = &reviewBox{posture: postureSelfSandboxed}
		st.boxWarn(core.Msgf("%s runs in its own sandbox (%s), not verdict's review sandbox (a nested macOS sandbox "+
			"fails): it confines its own writes to the review snapshot, but nothing hides your credential files "+
			"(~/.ssh, ~/.aws, gh tokens) from its reads", def.Name, strings.Join(def.SnapshotWriteArgs, " ")))
		return true
	case st.req.AllowUnsandboxedReview:
		st.box = &reviewBox{posture: postureUnsandboxed}
		st.boxWarn(core.Msgf("%s: %s reviews without verdict's OS sandbox — a prompt-injected diff can make it "+
			"write anywhere you can and read your credentials (~/.ssh, ~/.aws, gh tokens) into review text "+
			"verdict may post", flagAllowUnsandboxed, def.Name))
		return true
	}
	return false
}

// confinesOwnWrites says why the row's launch does not confine its own
// writes as launched, "" when it does: its own sandbox applies, or its shell
// denial and scoped grant do — neither when the operator's `--` (owned)
// decides its permissions, the shell denial not under --allow-shell, and
// claude's grant not when the operator's own Claude settings allow writes
// wider than it.
func (st *state) confinesOwnWrites(def *agentcatalog.Launch, owned bool) string {
	switch {
	case owned:
		return "your `--` arguments decide its permissions"
	case def.Confinement == agentcatalog.OwnSandbox:
		return ""
	case def.Confinement != agentcatalog.OwnWrites:
		return "it confines neither its writes nor its reads on its own"
	case st.req.AllowShell:
		return "--allow-shell gives it a shell"
	}
	if def.Name == nameClaude {
		return claudeWidenedWrites(st.childEnv())
	}
	return ""
}

// noBox settles a host that cannot sandbox the launch, for the reason why: a
// row that confines its own writes runs warned, a render reports the
// unavailable posture, and every other launch refuses.
func (st *state) noBox(def *agentcatalog.Launch, owned bool, why string) *core.RefusalError {
	not := st.confinesOwnWrites(def, owned)
	if not == "" {
		st.box = &reviewBox{posture: postureUnsandboxed}
		st.boxWarn(core.Msgf("review sandbox unavailable (%s): %s runs unsandboxed — it confines its own writes, "+
			"but its reads are not confined, so a prompt-injected diff could read your credentials", why, def.Name))
		return nil
	}
	if st.req.Print {
		st.box = &reviewBox{posture: postureUnavailable}
		return nil
	}
	remedy := "install bubblewrap on Linux"
	if st.req.UnsandboxedOptOut {
		remedy += ", or pass " + flagAllowUnsandboxed + " to review without the sandbox (a prompt-injected " +
			"diff could then write anywhere you can and read your credentials)"
	}
	return st.refuse(codeReviewSandboxUnavailable, fmt.Sprintf("review sandbox unavailable: %s — %s does not "+
		"review unsandboxed: %s; %s", why, def.Name, not, remedy))
}

// boxWarn records one posture warning for a launch; a render stays quiet
// and reports the posture in data.sandbox instead, as it leaves the row's
// launch warning to the launch.
func (st *state) boxWarn(msg string) {
	if !st.req.Print {
		st.warn(msg)
	}
}

// warnStateless names the one limit a row without state paths runs into in
// the sandbox: it cannot save its own login or sessions.
func (st *state) warnStateless(def *agentcatalog.Launch) {
	if len(def.StateWritable) > 0 || len(def.StateEphemeral) > 0 || def.ProjectState != nil {
		return
	}
	remedy := "it cannot run sandboxed"
	switch {
	case def.Source == agentSourcePrefs:
		remedy = "declare them as agents." + def.Name + ".state_locations in your prefs"
	case st.req.UnsandboxedOptOut:
		remedy = "pass " + flagAllowUnsandboxed + " to review without the sandbox"
	}
	st.boxWarn(core.Msgf("%s declares no state paths: in the review sandbox it can write only its review snapshot "+
		"and a private TMPDIR, so it cannot save its own login or sessions; if it fails for that, %s",
		def.Name, remedy))
}

// buildBox resolves the policy and proves the mechanism with a no-op. It
// says why the host cannot sandbox the launch (no runner, a failed probe),
// or returns the policy error that refuses it outright: a credential or
// state path that cannot be resolved or would re-open more than the agent's
// own data, or a profile that cannot be spelled.
func (st *state) buildBox(ctx context.Context, def *agentcatalog.Launch) (*reviewBox, string, error) {
	mechanism, runner, why := st.reviewMechanism()
	if why != "" {
		return nil, why, nil
	}
	env := absConfigHomes(st.childEnv())
	credentials, err := hostsandbox.Inputs{
		AccountHome: hostsandbox.AccountHome(), ChildEnv: env, HostEnv: st.req.Environ,
	}.CredentialLocations(st.workDir(), mechanism == hostsandbox.SandboxExecTool)
	if err != nil {
		return nil, "", err //nolint:wrapcheck // the shared resolver's message is the refusal's reason
	}
	box, err := newReviewBox(def, env, credentials)
	if err != nil {
		return nil, "", err
	}
	box.mechanism, box.runner, box.posture = mechanism, runner, postureSandboxed
	if _, err := box.prefix([]string{os.TempDir()}, "", false); err != nil {
		return nil, "", err
	}
	if st.req.Print {
		return box, "", nil
	}
	return box, st.probeBox(ctx, box), nil
}

// reviewMechanism names the host's sandbox mechanism and the runner it
// spawns by its resolved path, or why there is none.
func (st *state) reviewMechanism() (mechanism, runner, why string) {
	look := st.req.LookPath
	if look == nil {
		look = exec.LookPath
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := look(hostsandbox.SandboxExecPath); err != nil {
			return "", "", "the built-in " + hostsandbox.SandboxExecPath + " is missing"
		}
		return hostsandbox.SandboxExecTool, hostsandbox.SandboxExecPath, ""
	case "linux":
		return bwrapMechanism(look)
	}
	return "", "", runtime.GOOS + " has no OS sandbox verdict can apply"
}

// bwrapMechanism finds bubblewrap on PATH and names it by its absolute path.
func bwrapMechanism(look func(string) (string, error)) (mechanism, runner, why string) {
	path, err := look(hostsandbox.BwrapTool)
	if err != nil {
		return "", "", "bubblewrap (bwrap) is not on PATH"
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return hostsandbox.BwrapTool, path, ""
}

// probeBox runs the wrapper around a no-op in a throwaway private directory
// before any agent starts, so a host that cannot build the sandbox (user
// namespaces restricted, a profile Seatbelt rejects) is found here rather
// than as a failed review.
func (st *state) probeBox(ctx context.Context, box *reviewBox) string {
	private, err := hostsandbox.MakePrivateDir("")
	if err != nil {
		return err.Error()
	}
	defer st.removePrivate(private)
	argv, err := box.prefix([]string{private}, "", true)
	if err != nil {
		return err.Error()
	}
	if box.mechanism == hostsandbox.SandboxExecTool {
		argv = append(argv, "bash", "-c", reviewProbeScript)
	} else {
		argv = append(argv, "true")
	}
	pctx, cancel := context.WithTimeout(ctx, reviewProbeTimeout)
	defer cancel()
	res, err := st.commander().Run(pctx, &core.Command{
		Argv: argv, Env: hostsandbox.OverrideEnv(st.childEnv(), tmpEnv(private)), Dir: private,
		OutputCap: reviewProbeCap,
	})
	switch {
	case ctx.Err() != nil:
		return ctx.Err().Error()
	case err != nil:
		return fmt.Sprintf("%s could not start (%v)", box.mechanism, err)
	case res.TimedOut:
		return fmt.Sprintf("%s did not finish its probe within %s", box.mechanism, reviewProbeTimeout)
	case res.ExitCode != 0:
		detail, _, _ := strings.Cut(strings.TrimSpace(res.Stdout+res.Stderr), "\n")
		return fmt.Sprintf("%s could not create the sandbox (exit %d: %s)", box.mechanism, res.ExitCode, detail)
	}
	return ""
}

// removePrivate removes one private TMPDIR, warning when any of it survives
// so a stranded directory is visible.
func (st *state) removePrivate(private string) {
	if err := hostsandbox.RemovePrivateDir(private); err != nil {
		st.warn(fmt.Sprintf("review sandbox cleanup: %s was not fully removed (%v); remove it by hand", private, err))
	}
}

// trustEnv points a macOS launch's TLS clients that load their roots from the
// keychain at the system bundle, unless the operator chose a bundle or the
// keychain is the agent's own: the profile closes the keychain brokers they
// would read the roots through.
func (b *reviewBox) trustEnv(env []string) []string {
	if b.mechanism != hostsandbox.SandboxExecTool || b.keychain() || hostsandbox.EnvLookup(env, sslCertFile) != "" {
		return nil
	}
	if _, err := os.Stat(systemCABundle); err != nil {
		return nil
	}
	return []string{sslCertFile + inlineValue + systemCABundle}
}

// sslCertFile is the variable OpenSSL-style TLS clients read their root
// bundle from.
const sslCertFile = "SSL_CERT_FILE"

// tmpEnv points a child's scratch at its private directory. Only the temp
// variables move: an agent's caches are its state, not scratch.
func tmpEnv(private string) []string {
	return []string{"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private}
}

// tempEnv points the row's own scratch variables (TempEnv) at the launch's
// private TMPDIR.
func tempEnv(def *agentcatalog.Launch, tmp string) []string {
	out := make([]string, 0, len(def.TempEnv))
	for _, name := range def.TempEnv {
		out = append(out, name+inlineValue+tmp)
	}
	return out
}

// stripSocketEnv drops the socket and terminal variables (socketEnv) from a
// review launch's environment, whatever their case.
func stripSocketEnv(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		name, _, _ := strings.Cut(kv, inlineValue)
		return slices.ContainsFunc(socketEnv, func(s string) bool { return strings.EqualFold(s, name) })
	})
}

// absConfigHomes resolves a relative agent configuration directory
// (configHomes) where verdict runs, as the launch environment does, so the
// sandbox names the directory the agent will use.
func absConfigHomes(env []string) []string {
	out := slices.Clone(env)
	for i, kv := range out {
		name, value, _ := strings.Cut(kv, inlineValue)
		if value == "" || filepath.IsAbs(value) ||
			!slices.ContainsFunc(configHomes, func(h string) bool { return strings.EqualFold(h, name) }) {
			continue
		}
		if abs, err := filepath.Abs(value); err == nil {
			out[i] = name + inlineValue + abs
		}
	}
	return out
}

// launchBox is one launch's sandbox: the execution's box with the launch's
// own project directory added, and the project directory verdict made,
// removed after the launch.
type launchBox struct {
	box      *reviewBox
	projects string
	made     string
}

// launch prepares one sandboxed launch with its working directory cwd: the
// launch's own project directory (launchProject).
func (b *reviewBox) launch(cwd string) (*launchBox, error) {
	out := &launchBox{box: b}
	return out, b.launchProject(out, cwd)
}

// launchProject makes the launch's own project directory — the one the CLI
// names after the working directory's real path — with its read-only
// subdirectories, and adds it to a copy of the box as writable state. A
// name it would shorten, or a directory that already exists, gets none: the
// launch writes no project state.
func (b *reviewBox) launchProject(out *launchBox, cwd string) error {
	if b.projects == nil {
		return nil
	}
	slug, ok := projectSlug(cwd, b.project.MaxSlug)
	if !ok {
		return nil
	}
	if err := makeOwnDir(b.projects.path); err != nil {
		return err
	}
	dir := filepath.Join(b.projects.path, slug)
	if err := os.Mkdir(dir, viewDirMode); err != nil {
		return nil //nolint:nilerr // an existing directory is not this launch's to grant
	}
	out.projects, out.made = b.projects.path, slug
	box := *b
	box.state = append(slices.Clone(b.state), statePath{path: dir, base: b.projects.base, relocated: b.projects.relocated})
	box.readOnly = slices.Clone(b.readOnly)
	for _, rel := range b.project.ReadOnly {
		sub := filepath.Join(dir, filepath.FromSlash(rel))
		if err := makeOwnDir(sub); err != nil {
			return err
		}
		box.readOnly = append(box.readOnly, statePath{path: sub, base: b.projects.base})
	}
	out.box = &box
	return nil
}

// projectSlug is the name the CLI gives a working directory's project
// directory: its real path with every character but an ASCII letter or
// digit a dash; false when the name is longer than max, which the CLI
// shortens with a hash of its own.
func projectSlug(cwd string, limit int) (string, bool) {
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", false
	}
	slug := strings.Map(func(r rune) rune {
		if r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return '-'
	}, resolved)
	return slug, len(slug) <= limit
}

// dropLaunch removes the project directory a launch made, through a root
// handle on its parent so no link inside it is followed, and warns when it
// cannot.
func (st *state) dropLaunch(l *launchBox) {
	if l == nil || l.made == "" {
		return
	}
	root, err := os.OpenRoot(l.projects)
	if err == nil {
		err = root.RemoveAll(l.made)
		_ = root.Close()
	}
	if err != nil {
		st.warn(fmt.Sprintf("review sandbox cleanup: %s was not removed (%v); remove it by hand",
			filepath.Join(l.projects, l.made), err))
	}
}

// wrap builds one launch's sandbox around argv: writes allowed only in the
// launch's wrapper directory — its working copy and the private TMPDIR the
// view puts beside it — and in the row's data paths. The state is checked
// again first: an earlier launch may have changed what a state path names.
func (b *reviewBox) wrap(wrapper, bin string, argv []string) ([]string, error) {
	if err := b.checkState(); err != nil {
		return nil, err
	}
	prefix, err := b.prefix([]string{wrapper}, bin, true)
	if err != nil {
		return nil, jq.Sealed("build the review sandbox", err)
	}
	return slices.Concat(prefix, argv), nil
}

// prefix is the mechanism's argv before the agent's: writable are the
// launch's own directories, and bin the agent binary, which stays readable
// where a hidden temp root would cover it. A real launch resolves them
// through symlinks (Seatbelt matches the resolved path) and creates the
// writable state directories first; a render's placeholders stay as
// written.
func (b *reviewBox) prefix(writable []string, bin string, resolve bool) ([]string, error) {
	if resolve {
		if err := b.makeStateDirs(); err != nil {
			return nil, err
		}
	}
	if b.mechanism == hostsandbox.BwrapTool {
		return slices.Concat([]string{b.runner}, bwrapReviewArgv(writable, bin, b)), nil
	}
	if resolve {
		writable = hostsandbox.ExpandRealpaths(writable)
	}
	profile, err := seatbeltReviewProfile(writable, bin, b)
	if err != nil {
		return nil, err
	}
	return []string{hostsandbox.SandboxExecPath, "-p", profile}, nil
}

// seatbeltReviewProfile builds the macOS profile in precedence order —
// Seatbelt applies the last matching rule:
//
//  1. the header: everything allowed but the service channels, every
//     write, preference writes, every Mach service past the allowlist, and
//     unix sockets;
//  2. the keychain brokers opened when the keychain is the agent's login;
//  3. reads denied in the shared temp roots and in every other snapshot
//     directory beside the launch's own;
//  4. the launch's own directories re-allowed, reads and writes, the agent
//     binary (or its directory) readable, their ancestors' metadata
//     readable, the device writes a process needs, and every terminal
//     closed;
//  5. the credential table denied, reads and writes;
//  6. the row's data paths re-allowed, reads and writes, so a login the
//     table would hide stays the agent's, each directory's own entry pinned
//     so it cannot be swapped for a link;
//  7. the row's config and code denied writes last.
func seatbeltReviewProfile(writable []string, bin string, box *reviewBox) (string, error) {
	var b strings.Builder
	b.WriteString(seatbeltReviewHeader)
	if box.keychain() {
		b.WriteString(seatbeltKeychainAllow)
	}
	steps := []func() error{
		func() error { return seatbeltReadDenials(&b, writable) },
		func() error { return hostsandbox.WriteRules(&b, "allow", writable) },
		func() error { return seatbeltBinaryRead(&b, bin, writable) },
		func() error { return seatbeltAncestors(&b, writable, bin) },
		func() error { b.WriteString(hostsandbox.SeatbeltDeviceWrites + seatbeltTTYDenial); return nil },
		func() error { return hostsandbox.WriteRules(&b, "deny", hostsandbox.ExpandRealpaths(box.credentials)) },
		func() error { return seatbeltStateRules(&b, box.state, true) },
		func() error { return seatbeltStateRules(&b, box.readOnly, false) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// seatbeltReadDenials denies reads of the shared temp roots and of every
// other snapshot directory beside a writable snapshot wrapper — the
// execution's shared tree and the sibling launches' views, which a launch
// reaches through `../..` — before the wrapper's own allow re-opens it.
func seatbeltReadDenials(b *strings.Builder, writable []string) error {
	if err := hostsandbox.ReadDenials(b, seatbeltTempRoots); err != nil {
		return err //nolint:wrapcheck // the shared builder's message is the refusal's reason
	}
	for _, dir := range hostsandbox.ExpandRealpaths(snapshotBases(writable)) {
		if err := seatbeltRegex(b, "deny file-read*", filepath.Join(dir, contenttree.SnapshotPrefix), ""); err != nil {
			return err
		}
	}
	return nil
}

// seatbeltBinaryRead re-allows reading the agent binary's directory, which
// may sit in a denied temp root — or, when that directory is a temp root
// itself or the directory the snapshots sit in, the binary alone, under
// each spelling, so the allow does not re-open the root.
func seatbeltBinaryRead(b *strings.Builder, bin string, writable []string) error {
	if bin == "" {
		return nil
	}
	if !bareParent(filepath.Dir(bin), slices.Concat(seatbeltTempRoots, snapshotBases(writable))) {
		return hostsandbox.ReadRules(b, hostsandbox.ExpandRealpaths([]string{filepath.Dir(bin)})) //nolint:wrapcheck // as above
	}
	for _, path := range binarySpellings(bin) {
		literal, err := hostsandbox.SeatbeltLiteral(path)
		if err != nil {
			return err //nolint:wrapcheck // as above
		}
		fmt.Fprintf(b, "(allow file-read* (literal %s))", literal)
	}
	return nil
}

// seatbeltAncestors allows metadata reads (stat, never a listing) of every
// ancestor of the launch's own directories and the agent binary: a path
// resolver walks them one by one (Node's realpath does), and a denied temp
// root — the snapshots sit in /tmp when TMPDIR is unset — would fail it.
func seatbeltAncestors(b *strings.Builder, writable []string, bin string) error {
	paths := slices.Clone(writable)
	if bin != "" {
		paths = append(paths, binarySpellings(bin)...)
	}
	return hostsandbox.AncestorMetadata(b, paths) //nolint:wrapcheck // the shared builder's message is the refusal's reason
}

// bareParent reports whether dir, under any spelling, is one of roots: a
// directory whose read allow would re-open what the profile hides.
func bareParent(dir string, roots []string) bool {
	spelled := hostsandbox.ExpandRealpaths(roots)
	return slices.ContainsFunc(hostsandbox.ExpandRealpaths([]string{dir}), func(d string) bool {
		return slices.Contains(spelled, filepath.Clean(d))
	})
}

// binarySpellings are the agent binary's path and the file it resolves to,
// each under every spelling.
func binarySpellings(bin string) []string {
	paths := []string{bin}
	if target, err := filepath.EvalSymlinks(bin); err == nil {
		paths = append(paths, target)
	}
	return hostsandbox.ExpandRealpaths(paths)
}

// snapshotBases are the directories the launch's snapshot wrappers sit in,
// beside the execution's shared tree and the sibling launches' views.
func snapshotBases(writable []string) []string {
	var bases []string
	for _, w := range writable {
		if strings.HasPrefix(filepath.Base(w), contenttree.SnapshotPrefix) {
			bases = append(bases, filepath.Dir(w))
		}
	}
	return bases
}

// seatbeltStateRules renders the row's state under every spelling Seatbelt
// may match: writable entries re-allowed, each directory's own entry pinned
// against unlink and rename, a prefix entry matching files of its
// directory alone; read-only entries denied writes, a prefix one with
// everything below what it names.
func seatbeltStateRules(b *strings.Builder, state []statePath, writable bool) error {
	for _, s := range state {
		if err := seatbeltStateRule(b, s, writable); err != nil {
			return err
		}
	}
	if !writable {
		return nil
	}
	return seatbeltPins(b, state)
}

// seatbeltPins denies writes to each writable directory's own entry, after
// every allow: a prefix allow beside it (`.tmp*` beside `.tmp`) would
// otherwise match the directory itself and re-open it, Seatbelt applying the
// last matching rule.
func seatbeltPins(b *strings.Builder, state []statePath) error {
	var dirs []string
	for _, s := range state {
		if !s.prefix {
			dirs = append(dirs, s.path)
		}
	}
	for _, path := range hostsandbox.ExpandRealpaths(dirs) {
		literal, err := hostsandbox.SeatbeltLiteral(path)
		if err != nil {
			return err //nolint:wrapcheck // as above
		}
		fmt.Fprintf(b, "(deny file-write* (literal %s))", literal)
	}
	return nil
}

// seatbeltStateRule renders one state entry.
func seatbeltStateRule(b *strings.Builder, s statePath, writable bool) error {
	switch {
	case s.prefix && writable:
		return seatbeltPrefix(b, "allow file-read* file-write*", s, "[^/]*$")
	case s.prefix:
		return seatbeltPrefix(b, "deny file-write*", s, "[^/]*(/|$)")
	case !writable:
		return hostsandbox.WriteDenials(b, hostsandbox.ExpandRealpaths([]string{s.path})) //nolint:wrapcheck // as above
	}
	return hostsandbox.WriteRules(b, "allow", hostsandbox.ExpandRealpaths([]string{s.path})) //nolint:wrapcheck // as above
}

// seatbeltPrefix renders one prefix entry's rule on its directory's
// spellings, the stem followed by tail.
func seatbeltPrefix(b *strings.Builder, rule string, s statePath, tail string) error {
	for _, dir := range hostsandbox.ExpandRealpaths([]string{filepath.Dir(s.path)}) {
		if err := seatbeltRegex(b, rule, filepath.Join(dir, filepath.Base(s.path)), tail); err != nil {
			return err
		}
	}
	return nil
}

// seatbeltRegex renders one rule on the regex `^<quoted path><tail>`.
func seatbeltRegex(b *strings.Builder, rule, path, tail string) error {
	literal, err := hostsandbox.SeatbeltLiteral("^" + hostsandbox.SeatbeltRegexQuote(path) + tail)
	if err != nil {
		return err //nolint:wrapcheck // the shared builder's message is the refusal's reason
	}
	fmt.Fprintf(b, "(%s (regex %s))", rule, literal)
	return nil
}

// bwrapReviewArgv builds the Linux shape after the runner: the fixed head;
// /tmp, /run, and a real /var/run as empty tmpfs mounts (the resolver file
// behind /etc/resolv.conf bound back read-only when it lives there); the
// credential table hidden (empty tmpfs over a directory, /dev/null over a
// file); the launch's directories bound writable and the agent binary's
// directory read-only; the row's data paths bound writable and its config
// and code read-only, last, so a login inside a hidden location stays the
// agent's; then the hidden roots remounted read-only. bwrap can bind only
// what exists: a missing writable directory was created first, and a missing
// file stays read-only.
func bwrapReviewArgv(writable []string, bin string, box *reviewBox) []string {
	hidden := hiddenRoots(lstatMode)
	argv := slices.Concat(bwrapReviewHead, pairs(hostsandbox.Tmpfs, hidden))
	if resolver, ok := hiddenResolver(hidden); ok {
		argv = append(argv, hostsandbox.ROBind, resolver, resolver)
	}
	for _, mount := range hostsandbox.CredentialMounts(box.credentials) {
		argv = append(argv, mount.Argv()...)
	}
	argv = append(argv, binds(hostsandbox.Bind, writable)...)
	argv = append(argv, binds(hostsandbox.ROBind, hiddenBinary(bin, hidden, writable))...)
	argv = append(argv, binds(hostsandbox.Bind, existingState(box.state))...)
	argv = append(argv, binds(hostsandbox.ROBind, existingState(box.readOnly))...)
	argv = append(argv, pairs("--remount-ro", hidden)...)
	return append(argv, bwrapReviewTail...)
}

// hiddenBinary is what of the agent binary a hidden root covers, to bind
// back read-only: its directory, or — when that directory is a hidden root
// itself or the directory the snapshots sit in — the binary alone, by its
// path and the file it resolves to, so the bind does not re-open the root.
func hiddenBinary(bin string, hidden, writable []string) []string {
	if bin == "" || !under(bin, hidden) {
		return nil
	}
	if !bareParent(filepath.Dir(bin), slices.Concat(hidden, snapshotBases(writable))) {
		return []string{filepath.Dir(bin)}
	}
	paths := []string{bin}
	if target, err := filepath.EvalSymlinks(bin); err == nil && target != bin && under(target, hidden) {
		paths = append(paths, target)
	}
	return paths
}

// pairs is option followed by each path.
func pairs(option string, paths []string) []string {
	out := make([]string, 0, 2*len(paths))
	for _, path := range paths {
		out = append(out, option, path)
	}
	return out
}

// binds is one bind of each path onto itself.
func binds(option string, paths []string) []string {
	var out []string
	for _, path := range paths {
		out = append(out, option, path, path)
	}
	return out
}

// existingState lists the host paths the state entries name right now.
func existingState(state []statePath) []string {
	var out []string
	for _, s := range state {
		out = append(out, s.existing()...)
	}
	return out
}

// hiddenRoots lists the bwrapHiddenRoots that are real directories: a
// symlink (/var/run to /run on most distros) is covered by its target, and
// bwrap refuses to mount over one.
func hiddenRoots(stat func(string) (os.FileMode, bool)) []string {
	var out []string
	for _, dir := range bwrapHiddenRoots {
		if mode, ok := stat(dir); ok && mode.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// lstatMode is the mode of path itself, without following a final link.
func lstatMode(path string) (os.FileMode, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	return info.Mode(), true
}

// hiddenResolver is the file /etc/resolv.conf resolves to when it lives in a
// hidden root (systemd-resolved's stub under /run), so DNS keeps working.
func hiddenResolver(hidden []string) (string, bool) {
	resolved, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil || !under(resolved, hidden) {
		return "", false
	}
	return resolved, true
}

// under reports whether path, resolved, is inside one of roots.
func under(path string, roots []string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return slices.ContainsFunc(roots, func(root string) bool {
		return hostsandbox.Within(path, root) && path != root
	})
}

// makeStateDirs creates the state directories that do not exist yet: the
// writable ones, so bwrap, which can bind only an existing path, binds them,
// and Seatbelt pins each one's own entry — the agent would create them
// itself on its first run; the read-only ones inside a writable directory,
// so the agent cannot create one as its own; and the directories the CLI
// makes at startup under read-only state (StateDirs). A writable one that
// cannot be made stays read-only; a read-only or startup one that cannot be
// made, or is not a directory of its own afterwards, refuses the launch.
func (b *reviewBox) makeStateDirs() error {
	for _, s := range b.state {
		if !s.prefix {
			_ = os.MkdirAll(s.path, viewDirMode) //nolint:errcheck // best effort: an unmade directory stays read-only
		}
	}
	must := slices.Concat(b.dirs, slices.DeleteFunc(slices.Clone(b.readOnly), func(r statePath) bool {
		return !b.insideData(r)
	}))
	for _, d := range must {
		if err := makeOwnDir(d.path); err != nil {
			return err
		}
	}
	return nil
}

// makeOwnDir makes a directory and confirms it is one, not a link.
func makeOwnDir(path string) error {
	_ = os.MkdirAll(path, viewDirMode) //nolint:errcheck // the check below decides
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		return jq.NewErrf("the state directory %s could not be made as a directory of its own", path)
	}
	return nil
}

// insideData reports whether a read-only directory entry sits strictly
// inside a writable directory entry.
func (b *reviewBox) insideData(r statePath) bool {
	return !r.prefix && slices.ContainsFunc(b.state, func(s statePath) bool {
		return !s.prefix && hostsandbox.Within(r.path, s.path) && r.path != s.path
	})
}

// existing lists the host paths a state entry names right now.
func (s statePath) existing() []string {
	if s.prefix {
		return s.prefixed()
	}
	if _, err := os.Lstat(s.path); err != nil {
		return nil
	}
	return []string{s.path}
}

// prefixed lists the files of a prefix entry's directory whose names start
// with its stem.
func (s statePath) prefixed() []string {
	dir, stem := filepath.Dir(s.path), filepath.Base(s.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stem) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// newReviewBox resolves the row's state under the child's HOME and its
// relocation variables and checks it (checkState).
func newReviewBox(def *agentcatalog.Launch, env, credentials []string) (*reviewBox, error) {
	box := &reviewBox{credentials: credentials, operator: def.Source == agentSourcePrefs}
	if len(def.StateWritable) == 0 && len(def.StateReadOnly) == 0 {
		return box, nil
	}
	home, ok := hostsandbox.Home(env)
	if !ok || !filepath.IsAbs(home) {
		return nil, jq.NewErrf("HOME is unset or relative in the child environment, so %s's state paths "+
			"cannot be named", def.Name)
	}
	box.home = filepath.Clean(home)
	if err := box.resolveState(def, env); err != nil {
		return nil, err
	}
	return box, box.checkState()
}

// resolveState resolves the row's state lists and its project root under
// HOME.
func (b *reviewBox) resolveState(def *agentcatalog.Launch, env []string) error {
	var err error
	if b.state, err = stateEntries(def, b.home, env, def.StateWritable); err != nil {
		return err
	}
	if b.readOnly, err = stateEntries(def, b.home, env, def.StateReadOnly); err != nil {
		return err
	}
	if b.dirs, err = stateEntries(def, b.home, env, def.StateDirs); err != nil {
		return err
	}
	return b.resolveProject(def, env)
}

// resolveProject resolves the row's project root like its state.
func (b *reviewBox) resolveProject(def *agentcatalog.Launch, env []string) error {
	if def.ProjectState == nil {
		return nil
	}
	projects, err := stateEntry(def, b.home, env, def.ProjectState.Root)
	if err != nil {
		return err
	}
	b.projects, b.project = &projects, def.ProjectState
	return nil
}

// stateEntries resolves a list of the row's home-relative entries.
func stateEntries(def *agentcatalog.Launch, home string, env, rels []string) ([]statePath, error) {
	out := make([]statePath, 0, len(rels))
	for _, rel := range rels {
		s, err := stateEntry(def, home, env, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// stateEntry resolves one entry: a clean path strictly below HOME, moved
// under the first relocation variable whose root holds it and whose value is
// absolute; a trailing `*` makes it a prefix.
func stateEntry(def *agentcatalog.Launch, home string, env []string, rel string) (statePath, error) {
	trimmed := strings.TrimSuffix(rel, "*")
	if why := agentcatalog.StatePathShape(trimmed); why != "" {
		return statePath{}, jq.NewErrf("%s's state path %q %s", def.Name, rel, why)
	}
	base, tail, relocated := home, trimmed, false
	for _, v := range def.StateEnv {
		value := hostsandbox.EnvLookup(env, v.Name)
		if below, ok := strings.CutPrefix(trimmed, v.Root); ok && filepath.IsAbs(value) &&
			(below == "" || strings.HasPrefix(below, slash)) {
			base, tail, relocated = filepath.Clean(value), strings.TrimPrefix(below, slash), true
			break
		}
	}
	return statePath{
		path: filepath.Join(base, filepath.FromSlash(tail)), base: base, prefix: trimmed != rel, relocated: relocated,
	}, nil
}

// checkState refuses a writable state path that would re-open more than the
// agent's own data, under every spelling it, HOME, and the credential table
// resolve to: a symlink, or a path with a symlink between it and its base
// (an earlier launch may have swapped one in), the filesystem root, HOME or
// an ancestor of it, or a path holding a credential location. It runs when
// the box is settled and again before every launch.
func (b *reviewBox) checkState() error {
	homes := hostsandbox.ExpandRealpaths([]string{b.home})
	credentials := slices.DeleteFunc(hostsandbox.ExpandRealpaths(b.credentials), b.ownLogin())
	made := slices.Concat(b.state, b.dirs)
	if b.projects != nil {
		made = append(made, *b.projects)
	}
	for _, s := range made {
		if err := b.checkEntry(s, homes, credentials); err != nil {
			return err
		}
	}
	for _, s := range b.readOnly {
		if err := checkBase(s, homes); err != nil {
			return err
		}
	}
	return nil
}

// ownLogin reports whether a credential location is the agent's own login:
// a catalog row names it as a writable entry of its own (claude's keychain,
// codex's auth.json), so a writable directory may hold it. An operator's
// prefs entry never owns one.
func (b *reviewBox) ownLogin() func(string) bool {
	if b.operator {
		return func(string) bool { return false }
	}
	var own []string
	for _, s := range b.state {
		own = append(own, s.path)
	}
	own = hostsandbox.ExpandRealpaths(own)
	return func(credential string) bool { return slices.Contains(own, credential) }
}

// checkEntry checks one writable state entry against the spellings of HOME
// and the credential table less the agent's own logins.
func (b *reviewBox) checkEntry(s statePath, homes, credentials []string) error {
	if err := s.linkFree(); err != nil {
		return err
	}
	if err := checkBase(s, homes); err != nil {
		return err
	}
	for _, path := range hostsandbox.ExpandRealpaths([]string{s.path}) {
		spelled := statePath{path: path, prefix: s.prefix}
		if why := spelled.check(homes, credentials, b.operator); why != "" {
			return jq.NewErrf("the state path %s %s", s.path, why)
		}
	}
	return nil
}

// checkBase refuses a relocation root — the base of an entry a relocation
// variable moved, whatever its value — that is a symlink, the filesystem
// root, or HOME or an ancestor of it under any spelling: every entry under it
// would name the operator's own directories (CLAUDE_CONFIG_DIR=$HOME makes
// ~/debug claude's).
func checkBase(s statePath, homes []string) error {
	if !s.relocated {
		return nil
	}
	base := s.base
	if info, err := os.Lstat(base); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return jq.NewErrf("the state root %s is a symlink; point the variable that relocates it at the directory "+
			"the link names", base)
	}
	for _, path := range hostsandbox.ExpandRealpaths([]string{base}) {
		_, root := hostsandbox.FilesystemRoot(path)
		if root || slices.ContainsFunc(homes, func(home string) bool { return hostsandbox.Within(home, path) }) {
			return jq.NewErrf("the state root %s is the filesystem root, HOME, or an ancestor of HOME; "+
				"point the variable that relocates it below HOME or elsewhere", base)
		}
	}
	return nil
}

// linkFree refuses a state path that is a symlink or reaches its target
// through one below its base, and a prefix entry with a link among its
// files.
func (s statePath) linkFree() error {
	target := s.path
	if s.prefix {
		target = filepath.Dir(s.path)
	}
	rel, err := filepath.Rel(s.base, target)
	if err != nil || rel == up || strings.HasPrefix(rel, up+string(filepath.Separator)) {
		return jq.NewErrf("the state path %s is not below %s", s.path, s.base)
	}
	if at, linked := firstLink(s.base, rel); linked {
		return jq.NewErrf("the state path %s reaches %s through a symlink; replace the link with "+
			"the directory it names", s.path, at)
	}
	return s.prefixLinkFree()
}

// firstLink walks rel's components below base and returns the first that is
// a symlink.
func firstLink(base, rel string) (string, bool) {
	at := base
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		if info, err := os.Lstat(at); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return at, true
		}
	}
	return "", false
}

// prefixLinkFree refuses a prefix entry one of whose files is a symlink.
func (s statePath) prefixLinkFree() error {
	if !s.prefix {
		return nil
	}
	for _, path := range s.existing() {
		if info, err := os.Lstat(path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return jq.NewErrf("the state file %s is a symlink; replace it with the file it names", path)
		}
	}
	return nil
}

// check says why a state path re-opens more than the agent's own data: it
// is the filesystem root, any spelling of HOME or above it, or it holds a
// credential location. A cited catalog path may sit at or inside one — that
// is the point: it re-allows the agent's own login, as claude's macOS
// keychain — but an operator's prefs entry may not touch one. "" passes it.
func (s statePath) check(homes, credentials []string, operator bool) string {
	if _, root := hostsandbox.FilesystemRoot(s.path); root {
		return "is the filesystem root"
	}
	if slices.ContainsFunc(homes, func(home string) bool { return hostsandbox.Within(home, s.path) }) {
		return "is HOME or one of its ancestors"
	}
	for _, c := range credentials {
		if why := s.overlap(c, operator); why != "" {
			return why
		}
	}
	return ""
}

// overlap says how the state path takes in one credential location that is
// not the agent's own login, "" for not at all: holding it, or sitting
// inside it.
func (s statePath) overlap(credential string, operator bool) string {
	switch {
	case operator && (s.encloses(credential) || hostsandbox.Within(s.path, credential)):
		return "overlaps the credential path " + credential
	case s.encloses(credential) && credential != s.path:
		return "holds the credential path " + credential
	case hostsandbox.Within(s.path, credential) && credential != s.path:
		return "sits inside the credential path " + credential
	}
	return ""
}

// encloses reports whether path falls under the state entry.
func (s statePath) encloses(path string) bool {
	if !s.prefix {
		return hostsandbox.Within(path, s.path)
	}
	dir := filepath.Dir(s.path)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == here || rel == up || strings.HasPrefix(rel, up+string(filepath.Separator)) {
		return false
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	return strings.HasPrefix(first, filepath.Base(s.path))
}

// writeTools are claude's file-writing tools, whose allow rule in the
// operator's own settings could widen a review's writes past its grant.
var writeTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}

// claudeWidenedWrites says why the operator's own Claude settings void
// claude's own-writes confinement, "" when they do not: any allow rule for a
// write tool, whatever path it names, would let an unsandboxed review write
// past the bundle. Settings that cannot be read or parsed, or a HOME that
// cannot name them, void it too. The settings directory is
// CLAUDE_CONFIG_DIR, else ~/.claude.
func claudeWidenedWrites(env []string) string {
	dir := hostsandbox.EnvLookup(absConfigHomes(env), "CLAUDE_CONFIG_DIR")
	if !filepath.IsAbs(dir) {
		home, ok := hostsandbox.Home(env)
		if !ok || !filepath.IsAbs(home) {
			return "HOME is unset or relative, so your Claude settings cannot be read"
		}
		dir = filepath.Join(home, ".claude")
	}
	return settingsWidenWrites(filepath.Join(dir, "settings.json"))
}

// settingsWidenWrites reads one Claude settings file read-only and says
// which allow rule widens writes, or why the file cannot be trusted; ""
// for none or no file.
func settingsWidenWrites(path string) string {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err != nil:
		return fmt.Sprintf("your Claude settings %s cannot be read (%v)", path, err)
	}
	var doc struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Sprintf("your Claude settings %s cannot be parsed", path)
	}
	for _, rule := range doc.Permissions.Allow {
		if writeRule(rule) {
			return fmt.Sprintf("your Claude settings %s allow %q, writes past its review grant", path, rule)
		}
	}
	return ""
}

// writeRule reports an allow rule for a write tool, scoped or not.
func writeRule(rule string) bool {
	tool, _, _ := strings.Cut(strings.TrimSpace(rule), "(")
	return slices.Contains(writeTools, strings.TrimSpace(tool))
}

// ReviewSandbox is the posture a review launch of req's agent takes, as a
// --print render reports it — the same decision, nothing probed — for a
// caller that plans a launch without making it (loop's plan). It is nil for
// an unknown agent and when the commander simulates its processes.
func ReviewSandbox(ctx context.Context, req *core.RunRequest, agents []config.PersonalAgent) *core.RunSandbox {
	render := *req
	render.Print = true
	st := &state{req: &render, defs: agents}
	def, ok := st.lookupAgent(render.Agent)
	if !ok || st.settleBox(ctx, &def) != nil {
		return nil
	}
	return st.box.data(true)
}
