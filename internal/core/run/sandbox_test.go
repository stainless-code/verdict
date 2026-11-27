package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/hostsandbox"
)

// hostScripted is a scripted commander that says it starts host processes,
// so the run service takes its review-sandbox decision for it; it spawns no
// agent. A call led by a sandbox runner is the mechanism's probe.
type hostScripted struct {
	argvs [][]string
	scripted
	probes    int
	probeExit int
}

// SimulatesProcesses overrides the embedded double's answer: the run takes
// its review-sandbox decision for this commander as for a host one.
func (*hostScripted) SimulatesProcesses() bool { return false }

// Run records every non-git argv, answers the probe, and otherwise behaves
// as the embedded script.
func (h *hostScripted) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if _, _, git := gitcmd.Split(cmd.Argv); !git {
		h.argvs = append(h.argvs, cmd.Argv)
	}
	if isProbe(cmd.Argv) {
		h.probes++
		return core.Result{ExitCode: h.probeExit, Stderr: "bwrap: setting up uid map: Permission denied"}, nil
	}
	return h.scripted.Run(ctx, cmd)
}

// isProbe reports the sandbox mechanism's no-op: the wrapper around `true`
// or the macOS smoke script.
func isProbe(argv []string) bool {
	return len(argv) > 1 && (argv[len(argv)-1] == "true" || argv[len(argv)-1] == reviewProbeScript)
}

// lookNoSandbox resolves every agent but no sandbox runner: a host with no
// OS sandbox, whatever it runs on.
func lookNoSandbox(name string) (string, error) {
	if name == "bwrap" || name == "/usr/bin/sandbox-exec" {
		return "", exec.ErrNotFound
	}
	return "/opt/agents/" + name, nil
}

// sandboxRequest is a one-finder launch of agent through a host commander
// that writes the review the launch owes.
func sandboxRequest(t *testing.T, agent string, look func(string) (string, error)) (*core.RunRequest, *hostScripted) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &hostScripted{write: func() { writeReview(t, dir) }}
	req := request(root, cmdr)
	req.Agent = agent
	req.LookPath = look
	req.UnsandboxedOptOut = true
	req.Environ = append(req.Environ, "HOME="+t.TempDir())
	return req, cmdr
}

// lookEverything resolves every agent and every sandbox runner: a host whose
// OS sandbox is present.
func lookEverything(name string) (string, error) { return "/opt/agents/" + name, nil }

// TestReviewWithoutSandboxRefusesUnconfinedRows proves a row that confines
// neither its writes nor its reads refuses review-sandbox-unavailable on a
// host with no OS sandbox, names the opt-out, and starts nothing — and that
// claude refuses too once --allow-shell gives it a shell.
func TestReviewWithoutSandboxRefusesUnconfinedRows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		agent string
		shell bool
	}{{agent: "pi"}, {agent: "opencode"}, {agent: "claude", shell: true}} {
		t.Run(tc.agent, func(t *testing.T) {
			t.Parallel()
			req, cmdr := sandboxRequest(t, tc.agent, lookNoSandbox)
			req.AllowShell = tc.shell
			_, ref := Run(t.Context(), req)
			assertUnavailable(t, ref, "--allow-unsandboxed-review")
			if cmdr.calls != 0 {
				t.Errorf("the agent ran %d time(s) after the refusal", cmdr.calls)
			}
		})
	}
}

// assertUnavailable fails unless ref is review-sandbox-unavailable and its
// message carries want.
func assertUnavailable(t *testing.T, ref *core.RefusalError, want string) {
	t.Helper()
	if ref == nil || ref.Code != "review-sandbox-unavailable" || !strings.Contains(ref.Message, want) {
		t.Fatalf("refusal = %v, want review-sandbox-unavailable carrying %q", ref, want)
	}
}

// TestReviewOptOutRunsUnwrappedAndWarns proves --allow-unsandboxed-review
// runs the refused row bare, warns naming the risk, and publishes the
// unsandboxed posture.
func TestReviewOptOutRunsUnwrappedAndWarns(t *testing.T) {
	t.Parallel()
	req, cmdr := sandboxRequest(t, "pi", lookNoSandbox)
	req.AllowUnsandboxedReview = true
	resp := runBoxed(t, req)
	assertWarning(t, resp.Warnings, "--allow-unsandboxed-review: pi reviews without verdict's OS sandbox — "+
		"a prompt-injected diff can make it write anywhere you can and read your credentials")
	assertUnwrapped(t, cmdr, "pi")
	assertPosture(t, resp, "none", "unsandboxed")
}

// runBoxed runs req and fails on a refusal.
func runBoxed(t *testing.T, req *core.RunRequest) *core.RunResponse {
	t.Helper()
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	return resp
}

// assertUnwrapped fails unless the launch ran the agent itself, no wrapper
// before it.
func assertUnwrapped(t *testing.T, cmdr *hostScripted, agent string) {
	t.Helper()
	if cmdr.cmd == nil || cmdr.cmd.Argv[0] != agent {
		t.Errorf("launch = %+v, want %s unwrapped", cmdr.cmd, agent)
	}
}

// TestReviewWithoutSandboxSelfConfinedRowsRunAndWarn proves claude and
// codex — which confine their own review writes — still run on a host with
// no OS sandbox, each warned that verdict confines nothing of its reads.
func TestReviewWithoutSandboxSelfConfinedRowsRunAndWarn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ agent, warning, posture string }{
		{agent: "claude", posture: "unsandboxed", warning: "claude runs unsandboxed — it confines its own writes, " +
			"but its reads are not confined"},
		{agent: "codex", posture: "self-sandboxed", warning: "codex runs in its own sandbox (--sandbox workspace-write -c " +
			"sandbox_workspace_write.exclude_slash_tmp=true -c sandbox_workspace_write.writable_roots=[]), " +
			"not verdict's review sandbox"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			t.Parallel()
			req, cmdr := sandboxRequest(t, tc.agent, lookNoSandbox)
			resp := runBoxed(t, req)
			assertWarning(t, resp.Warnings, tc.warning)
			assertUnwrapped(t, cmdr, tc.agent)
			assertPosture(t, resp, "none", tc.posture)
		})
	}
}

// TestCodexSkipsTheReviewSandboxForItsOwn proves codex is never wrapped in
// verdict's sandbox while its workspace-write sandbox applies, even where the
// host has one (a nested Seatbelt profile fails), and that nothing is
// probed for it.
func TestCodexSkipsTheReviewSandboxForItsOwn(t *testing.T) {
	t.Parallel()
	req, cmdr := sandboxRequest(t, "codex", lookEverything)
	resp := runBoxed(t, req)
	if cmdr.probes != 0 || !slices.Contains(cmdr.cmd.Argv, "workspace-write") {
		t.Errorf("probes = %d, launch argv = %v; want codex under its own sandbox, nothing probed",
			cmdr.probes, cmdr.cmd.Argv)
	}
	assertUnwrapped(t, cmdr, "codex")
	assertPosture(t, resp, "none", "self-sandboxed")
}

// TestReviewSandboxProbeFailureRefuses proves a mechanism that fails its
// no-op refuses an unconfined row before any agent starts, naming why.
func TestReviewSandboxProbeFailureRefuses(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("%s has no review sandbox to probe", runtime.GOOS)
	}
	req, cmdr := sandboxRequest(t, "pi", lookEverything)
	cmdr.probeExit = 1
	_, ref := Run(t.Context(), req)
	assertUnavailable(t, ref, "could not create the sandbox (exit 1: bwrap: setting up uid map")
	if cmdr.probes != 1 || cmdr.calls != 0 {
		t.Errorf("probes = %d, agent runs = %d; want one probe and no agent", cmdr.probes, cmdr.calls)
	}
}

// TestReviewSandboxPrintShowsTheWrapper proves --print reports the posture a
// launch would take and the wrapper it would run under, with the per-launch
// paths as placeholders, and probes nothing.
func TestReviewSandboxPrintShowsTheWrapper(t *testing.T) {
	t.Parallel()
	req, cmdr := sandboxRequest(t, "pi", lookEverything)
	req.Print = true
	resp := runBoxed(t, req)
	if len(cmdr.argvs) != 0 {
		t.Errorf("a render ran %v", cmdr.argvs)
	}
	want, ok := printedWrapper[runtime.GOOS]
	if !ok {
		assertPosture(t, resp, "none", "unavailable")
		return
	}
	assertPosture(t, resp, want.mechanism, "sandboxed")
	joined := strings.Join(resp.Data.Sandbox.Wrapper, " ")
	for _, part := range want.parts {
		if !strings.Contains(joined, part) {
			t.Errorf("wrapper = %q, want it to carry %q", joined, part)
		}
	}
}

// printedWrapper is the literal shape each mechanism's rendered wrapper
// carries: its runner, the placeholders bound writable, and its tail.
var printedWrapper = map[string]struct {
	mechanism string
	parts     []string
}{
	"darwin": {mechanism: "sandbox-exec", parts: []string{
		"/usr/bin/sandbox-exec -p (version 1)(allow default)",
		`(allow file-read* file-write* (subpath "{snapshot}"))`,
		`/.ssh"))`,
	}},
	"linux": {mechanism: "bwrap", parts: []string{
		"bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid ",
		"--bind {snapshot} {snapshot} --new-session",
		"--new-session --die-with-parent --",
	}},
}

// assertPosture checks the published review-sandbox posture.
func assertPosture(t *testing.T, resp *core.RunResponse, mechanism, posture string) {
	t.Helper()
	box := resp.Data.Sandbox
	if box == nil || box.Mechanism != mechanism || box.Posture != posture {
		t.Errorf("sandbox = %+v, want %s/%s", box, mechanism, posture)
	}
}

// assertWarning fails unless one warning carries want.
func assertWarning(t *testing.T, warnings []string, want string) {
	t.Helper()
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, want) }) {
		t.Errorf("warnings = %q, want one carrying %q", warnings, want)
	}
}

// TestReviewSandboxPolicyErrorRefusesOutright proves a sandbox policy that
// cannot be built refuses the launch even for claude, which a missing
// mechanism would let run unsandboxed: a relocation variable that makes
// the agent's state the whole filesystem is a policy error, never a reason
// to drop the sandbox.
func TestReviewSandboxPolicyErrorRefusesOutright(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("%s has no review sandbox policy to build", runtime.GOOS)
	}
	req, cmdr := sandboxRequest(t, "claude", lookEverything)
	req.Environ = append(req.Environ, "CLAUDE_CONFIG_DIR=/")
	_, ref := Run(t.Context(), req)
	assertUnavailable(t, ref, "review sandbox policy refused")
	if cmdr.calls != 0 || cmdr.probes != 0 {
		t.Errorf("agent runs = %d, probes = %d; want nothing started", cmdr.calls, cmdr.probes)
	}
}

// TestClaudeWideSettingsVoidItsOwnWrites proves claude counts as confining
// its own writes only while the operator's own Claude settings allow no
// write tool at all: any allow rule for one — bare, wildcard, or naming one
// host path — or settings that cannot be parsed refuse it on a host with no
// sandbox; a read rule does not.
func TestClaudeWideSettingsVoidItsOwnWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings string
		refuses        bool
	}{
		{name: "bare Write", settings: `{"permissions":{"allow":["Write"]}}`, refuses: true},
		{name: "wildcard Edit", settings: `{"permissions":{"allow":["Edit(//**)"]}}`, refuses: true},
		{name: "unparseable", settings: `{"permissions":`, refuses: true},
		{name: "one home file", settings: `{"permissions":{"allow":["Edit(~/.zshrc)"]}}`, refuses: true},
		{name: "one host file", settings: `{"permissions":{"allow":["Write(/etc/hosts)"]}}`, refuses: true},
		{name: "relative climb", settings: `{"permissions":{"allow":["Edit(../x)"]}}`, refuses: true},
		{name: "empty spec", settings: `{"permissions":{"allow":["Write()"]}}`, refuses: true},
		{name: "spaced", settings: `{"permissions":{"allow":[" MultiEdit (x)"]}}`, refuses: true},
		{name: "reads only", settings: `{"permissions":{"allow":["Read","Bash(git status)"]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertClaudeSettingsVerdict(t, tc.settings, tc.refuses)
		})
	}
}

// assertClaudeSettingsVerdict runs claude with the given user settings on a
// host with no sandbox and checks whether it refused.
func assertClaudeSettingsVerdict(t *testing.T, settings string, refuses bool) {
	t.Helper()
	req, cmdr := sandboxRequest(t, "claude", lookNoSandbox)
	home := t.TempDir()
	writeUnder(t, home, filepath.Join(".claude", "settings.json"), settings)
	req.Environ = append(req.Environ, "HOME="+home)
	_, ref := Run(t.Context(), req)
	if got := ref != nil && ref.Code == codeReviewSandboxUnavailable; got != refuses {
		t.Fatalf("refusal = %v, want refused = %v", ref, refuses)
	}
	if refuses && cmdr.calls != 0 {
		t.Errorf("the agent ran %d time(s) after the refusal", cmdr.calls)
	}
}

// TestCodexOperatorSandboxSpellingsVoidItsOwn proves every spelling of a
// codex flag that decides its sandbox — an attached short operand, a config
// override, a profile — counts as the operator owning its permissions:
// codex is then neither self-sandboxed nor confined on its own, so a host
// with no sandbox refuses it.
func TestCodexOperatorSandboxSpellingsVoidItsOwn(t *testing.T) {
	t.Parallel()
	for _, pass := range [][]string{
		{"-sdanger-full-access"},
		{"-c", "sandbox_mode=danger-full-access"},
		{"--config=sandbox_mode=x"},
		{"-p", "wide"},
		{"--profile", "wide"},
		{"--remote", "ws://127.0.0.1:1"},
		{"--trust"},
		{"-o", "/tmp/last"},
		{"--output-last-message=/tmp/last"},
		{"--enable", "x"},
		{"--disable=x"},
	} {
		t.Run(strings.Join(pass, " "), func(t *testing.T) {
			t.Parallel()
			req, cmdr := sandboxRequest(t, "codex", lookNoSandbox)
			req.PassThrough = pass
			_, ref := Run(t.Context(), req)
			assertUnavailable(t, ref, "your `--` arguments decide its permissions")
			if cmdr.calls != 0 {
				t.Errorf("the agent ran %d time(s) after the refusal", cmdr.calls)
			}
		})
	}
}

// TestReviewSandboxRefusalNamesTheOptOutOnlyWhereItExists proves a verb
// without --allow-unsandboxed-review (demo, drain, backtest) never tells the
// operator to pass it.
func TestReviewSandboxRefusalNamesTheOptOutOnlyWhereItExists(t *testing.T) {
	t.Parallel()
	req, _ := sandboxRequest(t, "pi", lookNoSandbox)
	req.UnsandboxedOptOut = false
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeReviewSandboxUnavailable || strings.Contains(ref.Message, flagAllowUnsandboxed) {
		t.Fatalf("refusal = %v, want review-sandbox-unavailable naming no opt-out", ref)
	}
}

// TestStateEntriesResolveRelativeRelocations proves a relative relocation
// variable names the directory the agent will use — resolved where verdict
// runs, as its launch environment resolves it — not HOME's default.
func TestStateEntriesResolveRelativeRelocations(t *testing.T) {
	t.Parallel()
	def, _ := lookupAgent("claude")
	home := t.TempDir()
	env := absConfigHomes([]string{"HOME=" + home, "CLAUDE_CONFIG_DIR=rel/claude"})
	got, err := stateEntries(&def, home, env, def.StateWritable)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "rel", "claude", "debug"); got[0].path != want || !got[0].relocated {
		t.Errorf("claude's debug = %s (relocated %v), want %s", got[0].path, got[0].relocated, want)
	}
}

// TestSeatbeltReviewProfileShape pins the macOS profile's order and the
// rules a reviewer relies on, on every OS (the profile is plain text): the
// socket, preference, pasteboard, and keychain channels closed; a writable
// prefix matching files of its own directory alone; a data directory's own
// entry pinned; read-only state and the credential table denied after the
// writable allows.
func TestSeatbeltReviewProfileShape(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	box := &reviewBox{
		home:        home,
		credentials: []string{filepath.Join(home, ".ssh")},
		state: []statePath{
			{path: filepath.Join(home, ".pi", "agent", "sessions"), base: home},
			{path: filepath.Join(home, ".pi", "agent", "auth.json"), base: home, prefix: true},
		},
		readOnly: []statePath{{path: filepath.Join(home, ".pi", "agent", "extensions"), base: home}},
	}
	profile, err := seatbeltReviewProfile([]string{"/w/verdict-snapshot-1-2"}, "", box)
	if err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(home, ".pi", "agent", "sessions")
	ordered := []string{
		`(deny user-preference-write)`,
		`(deny mach-lookup)`,
		`(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo")`,
		`(deny network-outbound (remote unix-socket))`,
		`(deny file-read* (subpath "/private/tmp"))`,
		`(deny file-read* (regex "^/w/verdict-snapshot-"))`,
		`(allow file-read* file-write* (subpath "/w/verdict-snapshot-1-2"))`,
		`(deny file-read* file-write* (subpath "` + filepath.Join(home, ".ssh") + `"))`,
		`(allow file-read* file-write* (subpath "` + sessions + `"))`,
		`auth\\.json[^/]*$"))`,
		`(deny file-write* (literal "` + sessions + `"))`,
		`(deny file-write* (subpath "` + filepath.Join(home, ".pi", "agent", "extensions") + `"))`,
	}
	assertInOrder(t, profile, ordered)
	if strings.Count(profile, "(allow mach-lookup") != 1 || strings.Contains(profile, `"com.apple.SecurityServer"`) {
		t.Errorf("a launch that does not own the keychain reaches more than the Mach allowlist:\n%s", profile)
	}
}

// TestSeatbeltMachAllowlistIsMinimal pins the services every macOS review
// launch may look up: a new one is a security decision, not a default.
func TestSeatbeltMachAllowlistIsMinimal(t *testing.T) {
	t.Parallel()
	want := []string{
		"com.apple.system.opendirectoryd.libinfo", "com.apple.trustd.agent",
		"com.apple.SystemConfiguration.configd", "com.apple.SystemConfiguration.DNSConfiguration",
	}
	got := regexp.MustCompile(`global-name "([^"]+)"`).FindAllStringSubmatch(seatbeltMachAllow, -1)
	names := make([]string, 0, len(got))
	for _, m := range got {
		names = append(names, m[1])
	}
	if !slices.Equal(names, want) {
		t.Errorf("Mach allowlist = %q, want %q", names, want)
	}
	if !strings.Contains(seatbeltReviewHeader, "(deny mach-lookup)"+seatbeltMachAllow) {
		t.Error("the review header does not deny every Mach lookup before its allowlist")
	}
}

// TestSeatbeltReviewProfileKeepsClaudesKeychain proves the keychain brokers
// stay reachable only to a launch whose own state is the keychain.
func TestSeatbeltReviewProfileKeepsClaudesKeychain(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	box := &reviewBox{home: home, state: []statePath{{path: filepath.Join(home, "Library", "Keychains"), base: home}}}
	profile, err := seatbeltReviewProfile([]string{"/w"}, "", box)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, seatbeltReviewHeader+seatbeltKeychainAllow) {
		t.Errorf("a keychain-owning launch lost the keychain brokers:\n%s", profile)
	}
}

// TestBwrapReviewArgvShape pins the Linux argv's order on every OS: the
// runner by its resolved path, the hidden temp and runtime roots, the
// credential table, the launch's own directory, the data paths writable and
// the config read-only, and the hidden roots remounted read-only last.
func TestBwrapReviewArgvShape(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions")
	extensions := filepath.Join(home, "extensions")
	for _, dir := range []string{sessions, extensions, filepath.Join(home, ".ssh")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	box := &reviewBox{
		mechanism: "bwrap", runner: "/usr/bin/bwrap", home: home,
		credentials: []string{filepath.Join(home, ".ssh")},
		state:       []statePath{{path: sessions, base: home}},
		readOnly:    []statePath{{path: extensions, base: home}},
	}
	argv, err := box.prefix([]string{"/w"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	ordered := []string{
		"/usr/bin/bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid",
		"--tmpfs " + filepath.Join(home, ".ssh"),
		"--bind /w /w",
		"--bind " + sessions + " " + sessions,
		"--ro-bind " + extensions + " " + extensions,
		"--new-session --die-with-parent --",
	}
	assertInOrder(t, joined, ordered)
	hidden := hiddenRoots(lstatMode)
	for _, dir := range hidden {
		assertInOrder(t, joined, []string{"--tmpfs " + dir + " ", "--bind /w /w", "--remount-ro " + dir})
	}
}

// TestHiddenRootsSkipALinkedVarRun proves /var/run is hidden only when it is
// a real directory: a link to /run is covered by /run, and bwrap refuses to
// mount over a link.
func TestHiddenRootsSkipALinkedVarRun(t *testing.T) {
	t.Parallel()
	modes := map[string]os.FileMode{
		"/tmp": os.ModeDir, "/var/tmp": os.ModeDir, "/run": os.ModeDir, "/var/run": os.ModeSymlink,
	}
	got := hiddenRoots(func(p string) (os.FileMode, bool) { m, ok := modes[p]; return m, ok })
	if want := []string{"/tmp", "/var/tmp", "/run"}; !slices.Equal(got, want) {
		t.Errorf("hidden roots = %v, want %v", got, want)
	}
}

// TestReviewEnvDropsSocketVariables proves a review launch inherits no
// variable naming a socket out of the sandbox, whatever its case.
func TestReviewEnvDropsSocketVariables(t *testing.T) {
	t.Parallel()
	got := stripSocketEnv([]string{
		"SSH_AUTH_SOCK=/a", "dbus_session_bus_address=unix:x", "GPG_AGENT_INFO=g", "TMUX=t", "NVIM=n",
		"XDG_RUNTIME_DIR=/run/user/1", "DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0", "XAUTHORITY=/h/.Xauthority",
		"HOME=/h",
	})
	if !slices.Equal(got, []string{"HOME=/h"}) {
		t.Errorf("env = %v, want HOME alone", got)
	}
}

// assertInOrder fails unless every part appears in s, each after the one
// before it.
func assertInOrder(t *testing.T, s string, parts []string) {
	t.Helper()
	at := 0
	for _, part := range parts {
		i := strings.Index(s[at:], part)
		if i < 0 {
			t.Errorf("%q missing after offset %d in:\n%s", part, at, s)
			return
		}
		at += i + len(part)
	}
}

// TestClaudeScratchFollowsTheLaunchTMPDIR proves claude's own scratch
// variable points at the launch's private TMPDIR, inside its wrapper: claude
// otherwise makes /tmp/claude-<uid>, which the sandbox closes.
func TestClaudeScratchFollowsTheLaunchTMPDIR(t *testing.T) {
	t.Parallel()
	req, cmdr := sandboxRequest(t, "claude", lookNoSandbox)
	runBoxed(t, req)
	tmpdir := hostsandboxEnv(cmdr.cmd.Env, "TMPDIR")
	if got := hostsandboxEnv(cmdr.cmd.Env, "CLAUDE_CODE_TMPDIR"); got == "" || got != tmpdir ||
		filepath.Dir(got) != filepath.Dir(cmdr.cmd.Dir) {
		t.Errorf("CLAUDE_CODE_TMPDIR = %q, TMPDIR = %q, cwd = %q; want the launch's private TMPDIR", got, tmpdir, cmdr.cmd.Dir)
	}
}

// hostsandboxEnv is the last value env gives name.
func hostsandboxEnv(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			value = v
		}
	}
	return value
}

// TestClaudeRelativeHomeVoidsItsOwnWrites proves claude cannot count as
// confining its own writes when HOME cannot name its settings: a relative
// HOME refuses on a host with no sandbox.
func TestClaudeRelativeHomeVoidsItsOwnWrites(t *testing.T) {
	t.Parallel()
	req, cmdr := sandboxRequest(t, "claude", lookNoSandbox)
	req.Environ = append(req.Environ, "HOME=relative/home")
	_, ref := Run(t.Context(), req)
	assertUnavailable(t, ref, "HOME is unset or relative")
	if cmdr.calls != 0 {
		t.Errorf("the agent ran %d time(s) after the refusal", cmdr.calls)
	}
}

// TestCodexWrappedOnMacOSIsToldToDropItsOwnSandbox proves codex, wrapped in
// verdict's macOS sandbox because the operator's `--` decides its
// permissions, is warned that its own sandbox fails nested — unless the
// operator turned it off.
func TestCodexWrappedOnMacOSIsToldToDropItsOwnSandbox(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		pass []string
		warn bool
	}{
		{pass: []string{"-s", "workspace-write"}, warn: true},
		{pass: []string{"--sandbox", "danger-full-access"}},
	} {
		st := &state{req: &core.RunRequest{PassThrough: tc.pass}}
		st.box = &reviewBox{mechanism: hostsandbox.SandboxExecTool, posture: postureSandboxed}
		codex, _ := agentcatalog.Lookup("codex")
		st.warnNested(codex.Launch)
		got := slices.ContainsFunc(st.warnings, func(w string) bool { return strings.Contains(w, "fails nested") })
		if got != tc.warn {
			t.Errorf("--%v: nested warning = %v, want %v (%q)", tc.pass, got, tc.warn, st.warnings)
		}
	}
}

// TestBinaryInAHiddenRootStaysAlone proves the agent binary's read allow
// re-opens its directory only when that directory is not a hidden temp
// root or the snapshots' own directory: there the binary alone is allowed
// (macOS) or bound (Linux), by its path and the file it resolves to.
func TestBinaryInAHiddenRootStaysAlone(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := seatbeltBinaryRead(&b, "/tmp/agent", nil); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); strings.Contains(got, "subpath") || !strings.Contains(got, `(literal "/private/tmp/agent")`) {
		t.Errorf("a binary in /tmp re-opened its directory: %s", got)
	}
	b.Reset()
	wrapper := "/w/verdict-snapshot-1-2"
	if err := seatbeltBinaryRead(&b, "/w/agent", []string{wrapper}); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); strings.Contains(got, "subpath") {
		t.Errorf("a binary beside the snapshots re-opened their directory: %s", got)
	}
	b.Reset()
	if err := seatbeltBinaryRead(&b, "/opt/agents/bin/agent", nil); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, `(subpath "/opt/agents/bin")`) {
		t.Errorf("a binary outside the hidden roots lost its directory: %s", got)
	}
}

// TestBwrapBindsABinaryInAHiddenRootAlone is the Linux half: bwrap binds
// the binary alone when its directory is a hidden root, its directory when
// it sits below one.
func TestBwrapBindsABinaryInAHiddenRootAlone(t *testing.T) {
	t.Parallel()
	if got := hiddenBinary("/tmp/agent", []string{"/tmp"}, nil); !slices.Equal(got, []string{"/tmp/agent"}) {
		t.Errorf("bwrap binds %v for a binary in /tmp, want the binary alone", got)
	}
	if got := hiddenBinary("/tmp/x/bin/agent", []string{"/tmp"}, nil); !slices.Equal(got, []string{"/tmp/x/bin"}) {
		t.Errorf("bwrap binds %v for a binary below /tmp, want its directory", got)
	}
}

// TestStateChecksRefuseALinkedRelocationRoot proves the state checks refuse
// a relocation root that is a symlink.
func TestStateChecksRefuseALinkedRelocationRoot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := filepath.Join(home, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	relocated := &agentcatalog.Launch{
		Name: "x", StateWritable: []string{".x/data"}, StateEnv: []agentcatalog.StateVar{{Name: "X_HOME", Root: ".x"}},
	}
	if _, err := newReviewBox(relocated, []string{"HOME=" + home, "X_HOME=" + link}, nil); err == nil ||
		!strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("a linked relocation root = %v, want refused", err)
	}
}

// TestStateChecksRefuseCredentialNesting proves the state checks refuse a
// catalog state path strictly inside a credential directory, and a writable
// directory holding a credential the row does not name as its own login,
// while one it names stays allowed.
func TestStateChecksRefuseCredentialNesting(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	inside := &agentcatalog.Launch{Name: "x", StateWritable: []string{".ssh/agent"}}
	if _, err := newReviewBox(inside, []string{"HOME=" + home}, []string{ssh}); err == nil ||
		!strings.Contains(err.Error(), "sits inside the credential path") {
		t.Errorf("state inside ~/.ssh = %v, want refused", err)
	}
	data := filepath.Join(home, ".d")
	own := &agentcatalog.Launch{Name: "x", StateWritable: []string{".d", ".d/auth.json*"}}
	if _, err := newReviewBox(own, []string{"HOME=" + home}, []string{filepath.Join(data, "auth.json")}); err != nil {
		t.Errorf("a data directory holding its own login = %v, want allowed", err)
	}
	other := &agentcatalog.Launch{Name: "x", StateWritable: []string{".d"}}
	if _, err := newReviewBox(other, []string{"HOME=" + home}, []string{filepath.Join(data, "auth.json")}); err == nil {
		t.Error("a data directory holding a login it does not name passed")
	}
}

// TestMakeStateDirsMakesCodeDirsInsideData proves the code directories a
// writable data directory holds, and the directories the CLI makes at
// startup under read-only state, exist before the launch — so on Linux the
// agent cannot create one as its own and each is bound read-only.
func TestMakeStateDirsMakesCodeDirsInsideData(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	data := filepath.Join(home, "data")
	box := &reviewBox{
		home: home, state: []statePath{{path: data, base: home}},
		readOnly: []statePath{{path: filepath.Join(data, "bin"), base: home}, {path: filepath.Join(home, "cfg"), base: home}},
		dirs:     []statePath{{path: filepath.Join(home, "cache", "bin"), base: home}},
	}
	if err := box.makeStateDirs(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{data, filepath.Join(data, "bin"), filepath.Join(home, "cache", "bin")} {
		if info, err := os.Stat(want); err != nil || !info.IsDir() {
			t.Errorf("%s was not made: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "cfg")); err == nil {
		t.Error("a read-only directory outside the data directory was made")
	}
}

// TestTrustEnvPointsKeychainlessLaunchesAtTheSystemBundle proves a macOS
// launch that cannot reach the keychain brokers gets SSL_CERT_FILE unless the
// operator chose one, and the keychain's own agent does not.
func TestTrustEnvPointsKeychainlessLaunchesAtTheSystemBundle(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(systemCABundle); err != nil {
		t.Skipf("%s is absent", systemCABundle)
	}
	box := &reviewBox{mechanism: hostsandbox.SandboxExecTool}
	if got := box.trustEnv(nil); !slices.Equal(got, []string{"SSL_CERT_FILE=" + systemCABundle}) {
		t.Errorf("trust env = %v", got)
	}
	if got := box.trustEnv([]string{"SSL_CERT_FILE=/mine.pem"}); got != nil {
		t.Errorf("the operator's bundle was overridden: %v", got)
	}
	box.home, box.state = "/h", []statePath{{path: filepath.Join("/h", keychainState)}}
	if got := box.trustEnv(nil); got != nil {
		t.Errorf("the keychain's own agent got %v", got)
	}
}

// TestKeychainIsOnlyTheCatalogLogin proves only a catalog row whose state
// is exactly HOME's login keychain counts as owning the keychain: an
// operator's prefs entry, or a path that merely ends the same way, does not.
func TestKeychainIsOnlyTheCatalogLogin(t *testing.T) {
	t.Parallel()
	own := statePath{path: filepath.Join("/h", keychainState)}
	if !(&reviewBox{home: "/h", state: []statePath{own}}).keychain() {
		t.Error("the catalog login keychain did not count")
	}
	if (&reviewBox{home: "/h", state: []statePath{own}, operator: true}).keychain() {
		t.Error("an operator's prefs entry counted as owning the keychain")
	}
	elsewhere := statePath{path: filepath.Join("/h", "x", keychainState)}
	if (&reviewBox{home: "/h", state: []statePath{elsewhere}}).keychain() {
		t.Error("a path ending in Library/Keychains below HOME's own counted")
	}
}

// TestSeatbeltPinsFollowEveryAllow proves a writable directory's pin comes
// after every allow: a prefix entry beside it (codex's `.tmp*` beside
// `.tmp`) matches the directory itself, and Seatbelt applies the last
// matching rule.
func TestSeatbeltPinsFollowEveryAllow(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), ".tmp")
	var b strings.Builder
	state := []statePath{{path: dir}, {path: dir, prefix: true}}
	if err := seatbeltStateRules(&b, state, true); err != nil {
		t.Fatal(err)
	}
	profile := b.String()
	pin := strings.LastIndex(profile, `(deny file-write* (literal "`+dir+`"))`)
	allow := strings.LastIndex(profile, `[^/]*$"))`)
	if pin < 0 || allow < 0 || pin < allow {
		t.Errorf("the directory's pin precedes the prefix allow that matches it:\n%s", profile)
	}
}

// TestRelocationToHomeRefuses proves a relocation variable naming HOME
// itself refuses, whichever list it moves: claude's data under
// CLAUDE_CONFIG_DIR=$HOME, opencode's read-only data under XDG_DATA_HOME=$HOME.
func TestRelocationToHomeRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ agent, name string }{
		{agent: "claude", name: "CLAUDE_CONFIG_DIR"},
		{agent: "opencode", name: "XDG_DATA_HOME"},
		{agent: "opencode", name: "XDG_CACHE_HOME"},
	} {
		home := t.TempDir()
		def, _ := lookupAgent(tc.agent)
		_, err := newReviewBox(&def, []string{"HOME=" + home, tc.name + "=" + home}, nil)
		if err == nil || !strings.Contains(err.Error(), "HOME, or an ancestor of HOME") {
			t.Errorf("%s=$HOME for %s = %v, want refused", tc.name, tc.agent, err)
		}
	}
}

// TestStartupDirsFailClosed proves a startup or read-only directory that
// cannot be made as a directory of its own refuses the launch: a file or a
// link where it should be.
func TestStartupDirsFailClosed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	writeUnder(t, home, "cache", "a file, not a directory")
	box := &reviewBox{home: home, dirs: []statePath{{path: filepath.Join(cache, "bin"), base: home}}}
	if err := box.makeStateDirs(); err == nil {
		t.Error("a startup directory under a file passed")
	}
	link := filepath.Join(home, "linked")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	box.dirs = []statePath{{path: link, base: home}}
	if err := box.makeStateDirs(); err == nil {
		t.Error("a startup directory that is a link passed")
	}
}

// TestProjectSlugMatchesTheCLI pins the project directory name: the
// working directory's real path with every character but an ASCII letter or
// digit a dash, and none past the length the CLI shortens.
func TestProjectSlugMatchesTheCLI(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "My.Proj_dir x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(resolved, "-")
	if got, ok := projectSlug(dir, 200); !ok || got != want || !strings.HasSuffix(got, "-My-Proj-dir-x") {
		t.Errorf("slug = %q (%v), want %q", got, ok, want)
	}
	if _, ok := projectSlug(dir, 10); ok {
		t.Error("a slug past the CLI's length was granted")
	}
}

// TestCodexReviewOwnedFlagsVoidItsOwnSandbox proves a codex flag verdict
// owns on a review launch — a working directory, a worktree, an approval
// reviewer, trusted hooks — which --allow-shell lets through, counts as the
// operator deciding its permissions: on a host with no sandbox codex then
// refuses instead of running in its own sandbox.
func TestCodexReviewOwnedFlagsVoidItsOwnSandbox(t *testing.T) {
	t.Parallel()
	for _, pass := range [][]string{{"-C", "elsewhere"}, {"--worktree"}, {"--dangerously-bypass-hook-trust"}} {
		req, cmdr := sandboxRequest(t, "codex", lookNoSandbox)
		req.AllowShell, req.PassThrough = true, pass
		_, ref := Run(t.Context(), req)
		assertUnavailable(t, ref, "your `--` arguments decide its permissions")
		if cmdr.calls != 0 {
			t.Errorf("%v: codex ran %d time(s) in its own sandbox", pass, cmdr.calls)
		}
	}
}

// TestEphemeralHomesSitInThePrivateTmp proves the throwaway homes are made
// in the launch's private temp directory and seeded through no link, so the
// launch never reports them as its own stray writes.
func TestEphemeralHomesSitInThePrivateTmp(t *testing.T) {
	t.Parallel()
	home, tmp := t.TempDir(), t.TempDir()
	data := filepath.Join(home, ".local", "share")
	writeUnder(t, data, filepath.Join("opencode", "auth.json"), "login")
	if err := os.Symlink(filepath.Join(data, "opencode", "auth.json"),
		filepath.Join(data, "opencode", "mcp-auth.json")); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupAgent("opencode")
	_, err := ephemeralEnv(&def, []string{"HOME=" + home}, tmp)
	if err == nil || !strings.Contains(err.Error(), "mcp-auth.json") {
		t.Fatalf("a linked seed = %v, want the launch refused", err)
	}
	if err := os.Remove(filepath.Join(data, "opencode", "mcp-auth.json")); err != nil {
		t.Fatal(err)
	}
	env, err := ephemeralEnv(&def, []string{"HOME=" + home}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "home-xdg_data_home")
	if !slices.Contains(env, "XDG_DATA_HOME="+dir) {
		t.Errorf("env = %v, want the home in the private tmp", env)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "opencode", "auth.json")); string(got) != "login" {
		t.Errorf("seeded login = %q (%v)", got, err)
	}
}

// TestEphemeralSeedRefusesALinkedSource proves seeding refuses a real
// directory reached through a symlink — opencode's data directory linked to
// another agent's — whether the link is the directory itself or one above
// it.
func TestEphemeralSeedRefusesALinkedSource(t *testing.T) {
	t.Parallel()
	for _, link := range []string{
		filepath.Join(".local", "share", "opencode"), filepath.Join(".local", "share"),
	} {
		home, other := t.TempDir(), t.TempDir()
		writeUnder(t, other, "auth.json", "another agent's login")
		writeUnder(t, other, filepath.Join("opencode", "auth.json"), "another agent's login")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, link)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, filepath.Join(home, link)); err != nil {
			t.Fatal(err)
		}
		def, _ := lookupAgent("opencode")
		if _, err := ephemeralEnv(&def, []string{"HOME=" + home}, t.TempDir()); err == nil {
			t.Errorf("seeding through a linked %s passed", link)
		}
	}
}

// TestEphemeralSeedRefusesALinkedRelocation proves a relocation variable
// that names a symlink refuses, rather than seeding from wherever it points.
func TestEphemeralSeedRefusesALinkedRelocation(t *testing.T) {
	t.Parallel()
	home, target := t.TempDir(), t.TempDir()
	linked := filepath.Join(home, "codex-link")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupAgent("codex")
	if _, err := ephemeralEnv(&def, []string{"HOME=" + home, "CODEX_HOME=" + linked}, t.TempDir()); err == nil {
		t.Error("a linked CODEX_HOME passed")
	}
}

// TestPiSeedKeepsNoExtensionOrCommand proves pi's throwaway agent directory
// gets its login and models as they are, and its settings with only the
// model and network keys: no extension, package, skill, or command setting
// reaches a review.
func TestPiSeedKeepsNoExtensionOrCommand(t *testing.T) {
	t.Parallel()
	home, tmp := t.TempDir(), t.TempDir()
	agent := filepath.Join(".pi", "agent")
	writeUnder(t, home, filepath.Join(agent, "auth.json"), `{"k":"login"}`)
	writeUnder(t, home, filepath.Join(agent, "settings.json"),
		`{"defaultModel":"m","extensions":["/x.ts"],"packages":["npm:x"],"shellPath":"/bin/evil","npmCommand":["x"]}`)
	writeUnder(t, home, filepath.Join(agent, "extensions", "x.ts"), "code")
	def, _ := lookupAgent("pi")
	if _, err := ephemeralEnv(&def, []string{"HOME=" + home}, tmp); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "home-pi_coding_agent_dir")
	if got, err := os.ReadFile(filepath.Join(dir, "settings.json")); string(got) != `{"defaultModel":"m"}` {
		t.Errorf("seeded settings = %s (%v), want the model key alone", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "auth.json")); string(got) != `{"k":"login"}` {
		t.Errorf("seeded login = %s (%v)", got, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "extensions")); !os.IsNotExist(err) {
		t.Errorf("extensions reached the throwaway home (err = %v)", err)
	}
}

// TestEveryReviewLaunchGetsItsThrowawayHome proves a row's throwaway homes
// apply to every review launch, not only to one verdict's sandbox wraps:
// codex in its own sandbox gets its CODEX_HOME in the launch's private
// TMPDIR, as pi does under the opt-out.
func TestEveryReviewLaunchGetsItsThrowawayHome(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		look        func(string) (string, error)
		agent, name string
		optOut      bool
	}{
		{agent: "codex", name: "CODEX_HOME", look: lookEverything},
		{agent: "pi", name: "PI_CODING_AGENT_DIR", look: lookNoSandbox, optOut: true},
	} {
		req, cmdr := sandboxRequest(t, tc.agent, tc.look)
		req.AllowUnsandboxedReview = tc.optOut
		runBoxed(t, req)
		value := hostsandbox.EnvLookup(cmdr.cmd.Env, tc.name)
		tmp := hostsandbox.EnvLookup(cmdr.cmd.Env, "TMPDIR")
		if tmp == "" || value != filepath.Join(tmp, "home-"+strings.ToLower(tc.name)) {
			t.Errorf("%s: %s = %q, want a home in the launch's TMPDIR %q", tc.agent, tc.name, value, tmp)
		}
	}
}
