package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hostsandbox"
	"github.com/stainless-code/verdict/internal/jq"
)

// Every review launch also runs in the host's OS sandbox (STA-538). The
// disposable snapshot stops a relative-path write, but most agents' file
// tools take absolute paths, so a prompt-injected diff could still steer one
// into ~/.zshrc, ~/.config, or the operator's checkout, or into reading
// ~/.ssh and leaking it through review text verdict may post. The sandbox
// allows writes only to the launch's snapshot wrapper, a private TMPDIR, the
// agent's own state paths (its login and sessions), and the devices a
// process needs, and denies reads of verify's credential table except where
// a state path re-allows one. The network stays open: the agent has to reach
// its model API. macOS wraps the launch in sandbox-exec with a generated
// profile, Linux in bwrap; any other host has none.

// codeReviewSandboxUnavailable refuses a review the host cannot sandbox for
// a row that confines nothing on its own.
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

// The placeholders a --print render's wrapper carries for the per-launch
// paths, which exist only once a launch makes them.
const (
	placeholderSnapshot = "{snapshot}"
	placeholderTmpdir   = "{tmpdir}"
)

// reviewProbeTimeout and reviewProbeCap bound the no-op the mechanism proves
// itself with before any agent starts, verify's budget.
const (
	reviewProbeTimeout = 15 * time.Second
	reviewProbeCap     = 4 << 10
)

// reviewProbeScript is the macOS probe body: a profile that compiles can
// still deny bash its private TMPDIR.
const reviewProbeScript = `p=$(mktemp "$TMPDIR/verdict-probe.XXXXXX") && printf ok > "$p" && test -s "$p" && rm -f "$p"`

// seatbeltReviewHeader allows everything — the network and Mach services
// included, since agents reach their model API and macOS logins through
// them — then closes the service channels verify closes and denies every
// write; the rules after it re-open only what they name, Seatbelt applying
// the last matching rule.
const seatbeltReviewHeader = `(version 1)(allow default)` + hostsandbox.SeatbeltServiceDenials +
	`(deny file-write* (subpath "/"))`

// bwrapReviewHead is the Linux shape's fixed part: a read-only host, a fresh
// /dev, and a /proc under a private PID namespace, so other processes'
// environments stay hidden. The network is shared, and /run and /tmp stay
// readable like the rest of the host: the resolver behind /etc/resolv.conf
// often lives under /run, and an agent may be installed under /tmp.
var bwrapReviewHead = []string{
	hostsandbox.BwrapTool, hostsandbox.ROBind, "/", "/", "--dev", "/dev", "--proc", "/proc", "--unshare-pid",
}

// bwrapReviewTail starts the agent in its own session, so it cannot push
// input into the operator's terminal, and kills it with verdict.
var bwrapReviewTail = []string{"--new-session", "--die-with-parent", "--"}

// reviewBox is one execution's review-sandbox posture, decided once before
// any agent starts: the mechanism that wraps each launch ("" for none), the
// posture data.sandbox publishes, and what the policy is built from.
type reviewBox struct {
	mechanism   string
	posture     string
	credentials []string
	state       []statePath
}

// statePath is one resolved state path. A prefix entry names every path in
// its directory whose name starts with the path's base name.
type statePath struct {
	path   string
	prefix bool
}

// sandboxed reports whether launches are wrapped.
func (b *reviewBox) sandboxed() bool {
	return b != nil && b.posture == postureSandboxed
}

// data is the posture data.sandbox carries; a render adds the wrapper with
// the per-launch paths as placeholders.
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
		// A placeholder holds no control character, so the profile always
		// builds; a failure would only leave the wrapper out.
		if wrapper, err := b.prefix([]string{placeholderSnapshot, placeholderTmpdir}, false); err == nil {
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
// starts, nil when the commander starts no host process. A row whose own OS
// sandbox would nest skips verdict's; the opt-out runs every launch bare;
// otherwise the host's mechanism must build and pass its probe, and when it
// cannot, only a row that confines its own writes runs, warned. A render
// probes nothing and never refuses: it reports the posture a launch would
// take.
func (st *state) settleBox(ctx context.Context, def *agentcatalog.Launch) *core.RefusalError {
	if !core.StartsHostProcesses(st.commander()) {
		return nil
	}
	owned := operatorSpells(st.req.PassThrough, slices.Concat(def.PermissionFlags, def.DiscoveryOverrideFlags))
	if st.bareBox(def, owned) {
		return nil
	}
	box, why := st.buildBox(ctx, def)
	if ctx.Err() != nil {
		return st.refuse(codeCanceled, msgCanceled)
	}
	if why == "" {
		st.box = box
		st.warnStateless(def)
		return nil
	}
	return st.noBox(def, owned, why)
}

// bareBox settles the two postures that never build verdict's sandbox: a row
// whose own OS sandbox applies (owned is the operator's `--` deciding its
// permissions instead), and the opt-out.
func (st *state) bareBox(def *agentcatalog.Launch, owned bool) bool {
	switch {
	case def.Confinement == agentcatalog.OwnSandbox && len(def.SnapshotWriteArgs) > 0 && !owned:
		st.box = &reviewBox{posture: postureSelfSandboxed}
		st.boxWarn(core.Msgf("%s runs in its own %s sandbox, not verdict's review sandbox (a nested macOS sandbox "+
			"fails): it confines its own writes to the review snapshot, but nothing hides your credential files "+
			"(~/.ssh, ~/.aws, gh tokens) from its reads", def.Name, strings.Join(def.SnapshotWriteArgs[1:], " ")))
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

// confinesOwnWrites reports whether the row's launch confines its own
// writes as launched: its own sandbox applies, or its shell denial and
// scoped grant do — neither when the operator's `--` (owned) decides its
// permissions, and the shell denial not under --allow-shell.
func confinesOwnWrites(def *agentcatalog.Launch, owned, allowShell bool) bool {
	switch {
	case owned:
		return false
	case def.Confinement == agentcatalog.OwnSandbox:
		return true
	}
	return def.Confinement == agentcatalog.OwnWrites && !allowShell
}

// noBox settles a host that cannot sandbox the launch, for the reason why: a
// row that confines its own writes runs warned, a render reports the
// unavailable posture, and every other launch refuses.
func (st *state) noBox(def *agentcatalog.Launch, owned bool, why string) *core.RefusalError {
	if confinesOwnWrites(def, owned, st.req.AllowShell) {
		st.box = &reviewBox{posture: postureUnsandboxed}
		st.boxWarn(core.Msgf("review sandbox unavailable (%s): %s runs unsandboxed — it confines its own writes, "+
			"but its reads are not confined, so a prompt-injected diff could read your credentials", why, def.Name))
		return nil
	}
	if st.req.Print {
		st.box = &reviewBox{posture: postureUnavailable}
		return nil
	}
	return st.refuse(codeReviewSandboxUnavailable, fmt.Sprintf("review sandbox unavailable: %s — %s confines "+
		"neither its writes nor its reads on its own, so it does not review unsandboxed; install bubblewrap "+
		"on Linux, or pass %s to review without the sandbox (a prompt-injected diff could then write anywhere "+
		"you can and read your credentials)", why, def.Name, flagAllowUnsandboxed))
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
	if len(def.StatePaths) > 0 || len(def.StateEnv) > 0 {
		return
	}
	remedy := "pass " + flagAllowUnsandboxed + " to review without the sandbox"
	if def.Source == agentSourcePrefs {
		remedy = "declare them as agents." + def.Name + ".state_locations in your prefs"
	}
	st.boxWarn(core.Msgf("%s declares no state paths: in the review sandbox it can write only its review snapshot "+
		"and a private TMPDIR, so it cannot save its own login or sessions; if it fails for that, %s",
		def.Name, remedy))
}

// buildBox resolves the policy and proves the mechanism with a no-op, or
// says why the host cannot sandbox the launch.
func (st *state) buildBox(ctx context.Context, def *agentcatalog.Launch) (*reviewBox, string) {
	mechanism, why := st.reviewMechanism()
	if why != "" {
		return nil, why
	}
	env := st.childEnv()
	credentials, err := hostsandbox.Inputs{
		AccountHome: hostsandbox.AccountHome(), ChildEnv: env, HostEnv: st.req.Environ,
	}.CredentialLocations(st.workDir(), mechanism == hostsandbox.SandboxExecTool)
	if err != nil {
		return nil, err.Error()
	}
	paths, err := resolveState(def, env, credentials)
	if err != nil {
		return nil, err.Error()
	}
	box := &reviewBox{mechanism: mechanism, posture: postureSandboxed, credentials: credentials, state: paths}
	if st.req.Print {
		return box, ""
	}
	if why := st.probeBox(ctx, box); why != "" {
		return nil, why
	}
	return box, ""
}

// reviewMechanism names the host's sandbox runner, or why there is none.
func (st *state) reviewMechanism() (string, string) {
	look := st.req.LookPath
	if look == nil {
		look = exec.LookPath
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := look(hostsandbox.SandboxExecPath); err != nil {
			return "", "the built-in " + hostsandbox.SandboxExecPath + " is missing"
		}
		return hostsandbox.SandboxExecTool, ""
	case "linux":
		if _, err := look(hostsandbox.BwrapTool); err != nil {
			return "", "bubblewrap (bwrap) is not on PATH"
		}
		return hostsandbox.BwrapTool, ""
	}
	return "", runtime.GOOS + " has no OS sandbox verdict can apply"
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
	argv, err := box.prefix([]string{private}, true)
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

// tmpEnv points a child's scratch at its private directory. Only the temp
// variables move: an agent's caches are its state, not scratch.
func tmpEnv(private string) []string {
	return []string{"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private}
}

// wrapped is one launch's sandboxed command: its argv and environment, and
// the private TMPDIR verdict removes after it.
type wrapped struct {
	private string
	argv    []string
	env     []string
}

// wrap builds one launch's sandbox around argv: a fresh private TMPDIR, and
// writes allowed only there, in the launch's wrapper directory, and in the
// row's state paths.
func (b *reviewBox) wrap(wrapper string, argv, env []string) (*wrapped, error) {
	private, err := hostsandbox.MakePrivateDir("")
	if err != nil {
		return nil, jq.Sealed("make the private TMPDIR", err)
	}
	prefix, err := b.prefix([]string{wrapper, private}, true)
	if err != nil {
		return nil, jq.Sealed("build the review sandbox", errors.Join(err, hostsandbox.RemovePrivateDir(private)))
	}
	return &wrapped{
		argv: slices.Concat(prefix, argv), env: hostsandbox.OverrideEnv(env, tmpEnv(private)), private: private,
	}, nil
}

// prefix is the mechanism's argv before the agent's: writable are the
// launch's own directories, resolved through symlinks when real (Seatbelt
// matches the resolved path) and left as written for a render's
// placeholders.
func (b *reviewBox) prefix(writable []string, resolve bool) ([]string, error) {
	if b.mechanism == hostsandbox.BwrapTool {
		return bwrapReviewArgv(writable, b.state, b.credentials), nil
	}
	if resolve {
		writable = hostsandbox.ExpandRealpaths(writable)
	}
	profile, err := seatbeltReviewProfile(writable, b.state, b.credentials)
	if err != nil {
		return nil, err
	}
	return []string{hostsandbox.SandboxExecPath, "-p", profile}, nil
}

// seatbeltReviewProfile builds the macOS profile in precedence order —
// Seatbelt applies the last matching rule:
//
//  1. the header: everything allowed but the service channels, every write
//     denied;
//  2. reads and writes re-allowed in the launch's own directories, and the
//     device writes a process needs;
//  3. the credential table denied, reads and writes;
//  4. the row's state paths re-allowed last, reads and writes, so a login
//     the table would hide (claude's macOS keychain) stays the agent's.
func seatbeltReviewProfile(writable []string, state []statePath, credentials []string) (string, error) {
	var b strings.Builder
	b.WriteString(seatbeltReviewHeader)
	if err := hostsandbox.WriteRules(&b, "allow", writable); err != nil {
		return "", err //nolint:wrapcheck // the shared builder's message is the refusal's reason
	}
	b.WriteString(hostsandbox.SeatbeltDeviceWrites)
	if err := hostsandbox.WriteRules(&b, "deny", hostsandbox.ExpandRealpaths(credentials)); err != nil {
		return "", err //nolint:wrapcheck // as above
	}
	for _, s := range state {
		if err := seatbeltStateRule(&b, s); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// seatbeltStateRule re-allows one state path under every spelling Seatbelt
// may match; a prefix entry is a regex on its directory's spellings.
func seatbeltStateRule(b *strings.Builder, s statePath) error {
	if !s.prefix {
		return hostsandbox.WriteRules(b, "allow", hostsandbox.ExpandRealpaths([]string{s.path})) //nolint:wrapcheck // as above
	}
	for _, dir := range hostsandbox.ExpandRealpaths([]string{filepath.Dir(s.path)}) {
		literal, err := hostsandbox.SeatbeltLiteral("^" + hostsandbox.SeatbeltRegexQuote(filepath.Join(dir, filepath.Base(s.path))))
		if err != nil {
			return err //nolint:wrapcheck // as above
		}
		fmt.Fprintf(b, "(allow file-read* file-write* (regex %s))", literal)
	}
	return nil
}

// bwrapReviewArgv builds the Linux shape: the fixed head, the credential
// table hidden (empty tmpfs over a directory, /dev/null over a file), then
// the launch's directories and the state paths bound writable last, so a
// state path inside a hidden location stays the agent's. bwrap can bind only
// what exists, so a missing state path stays read-only.
func bwrapReviewArgv(writable []string, state []statePath, credentials []string) []string {
	argv := slices.Clone(bwrapReviewHead)
	for _, mount := range hostsandbox.CredentialMounts(credentials) {
		argv = append(argv, mount.Argv()...)
	}
	for _, dir := range writable {
		argv = append(argv, hostsandbox.Bind, dir, dir)
	}
	for _, s := range state {
		for _, path := range s.existing() {
			argv = append(argv, hostsandbox.Bind, path, path)
		}
	}
	return append(argv, bwrapReviewTail...)
}

// existing lists the host paths a state entry names right now.
func (s statePath) existing() []string {
	if !s.prefix {
		if _, err := os.Lstat(s.path); err != nil {
			return nil
		}
		return []string{s.path}
	}
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

// resolveState resolves the row's state paths under the child's HOME and
// its relocation variables, refusing any that would re-open more than the
// agent's own state: HOME itself or an ancestor of it, the filesystem root,
// or a path enclosing a credential location.
func resolveState(def *agentcatalog.Launch, env, credentials []string) ([]statePath, error) {
	if len(def.StatePaths) == 0 && len(def.StateEnv) == 0 {
		return nil, nil
	}
	home, ok := hostsandbox.Home(env)
	if !ok || !filepath.IsAbs(home) {
		return nil, jq.NewErrf("HOME is unset or relative in the child environment, so %s's state paths "+
			"cannot be named", def.Name)
	}
	return checkedStates(def, filepath.Clean(home), env, credentials)
}

// checkedStates resolves the row's state paths under home and its
// relocation variables, then checks each.
func checkedStates(def *agentcatalog.Launch, home string, env, credentials []string) ([]statePath, error) {
	out, err := homeStates(def, home)
	if err != nil {
		return nil, err
	}
	out = append(out, envStates(def, env)...)
	for _, s := range out {
		if why := s.check(home, credentials, def.Source == agentSourcePrefs); why != "" {
			return nil, jq.NewErrf("%s's state path %s %s", def.Name, s.path, why)
		}
	}
	return out, nil
}

// envStates resolves the row's relocation variables an absolute value sets.
func envStates(def *agentcatalog.Launch, env []string) []statePath {
	var out []statePath
	for _, v := range def.StateEnv {
		if value := hostsandbox.EnvLookup(env, v.Name); filepath.IsAbs(value) {
			out = append(out, statePath{path: filepath.Join(value, v.Suffix)})
		}
	}
	return out
}

// homeStates resolves the row's home-relative entries, each a clean path
// strictly below HOME; a trailing `*` makes it a prefix.
func homeStates(def *agentcatalog.Launch, home string) ([]statePath, error) {
	out := make([]statePath, 0, len(def.StatePaths)+len(def.StateEnv))
	for _, rel := range def.StatePaths {
		trimmed := strings.TrimSuffix(rel, "*")
		if why := agentcatalog.StatePathShape(trimmed); why != "" {
			return nil, jq.NewErrf("%s's state path %q %s", def.Name, rel, why)
		}
		out = append(out, statePath{path: filepath.Join(home, filepath.FromSlash(trimmed)), prefix: trimmed != rel})
	}
	return out, nil
}

// check says why a state path re-opens more than the agent's own state: it
// is the filesystem root, HOME or above it, or it holds a credential
// location. A cited catalog path may sit at or inside one — that is the
// point: it re-allows the agent's own login, as claude's macOS keychain —
// but an operator's prefs entry may not touch one. "" passes it.
func (s statePath) check(home string, credentials []string, operator bool) string {
	if _, root := hostsandbox.FilesystemRoot(s.path); root {
		return "is the filesystem root"
	}
	if hostsandbox.Within(home, s.path) {
		return "is HOME or one of its ancestors"
	}
	for _, c := range credentials {
		if why := s.overlap(c, operator); why != "" {
			return why
		}
	}
	return ""
}

// overlap says how the state path takes in one credential location, "" for
// not at all.
func (s statePath) overlap(credential string, operator bool) string {
	switch {
	case operator && (s.encloses(credential) || hostsandbox.Within(s.path, credential)):
		return "overlaps the credential path " + credential
	case s.encloses(credential) && credential != s.path:
		return "holds the credential path " + credential
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

// ReviewSandbox is the posture a review launch of req's agent takes, as a
// --print render reports it — the same decision, nothing probed — for a
// caller that plans a launch without making it (loop's plan). It is nil for
// an unknown agent and when the commander starts no host process.
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
