package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
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
			"sandbox_workspace_write.exclude_slash_tmp=true), not verdict's review sandbox"},
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
// its own writes only while the operator's own Claude settings grant no
// write wider than its review grant: an allow rule for a write tool with no
// path or a wildcard, or settings that cannot be parsed, refuse it on a
// host with no sandbox; a scoped rule does not.
func TestClaudeWideSettingsVoidItsOwnWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings string
		refuses        bool
	}{
		{name: "bare Write", settings: `{"permissions":{"allow":["Write"]}}`, refuses: true},
		{name: "wildcard Edit", settings: `{"permissions":{"allow":["Edit(//**)"]}}`, refuses: true},
		{name: "unparseable", settings: `{"permissions":`, refuses: true},
		{name: "scoped Read", settings: `{"permissions":{"allow":["Read","Edit(./docs/README.md)"]}}`},
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
	def, _ := lookupAgent("pi")
	home := t.TempDir()
	env := absConfigHomes([]string{"HOME=" + home, "PI_CODING_AGENT_DIR=rel/agent"})
	got, err := stateEntries(&def, home, env, def.StateWritable)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "rel", "agent", "sessions"); got[0].path != want {
		t.Errorf("pi's sessions = %s, want %s", got[0].path, want)
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
		`(global-name "com.apple.pasteboard.1")`,
		`(deny network-outbound (remote unix-socket))`,
		`(deny mach-lookup (global-name "com.apple.SecurityServer")`,
		`(deny file-read* (subpath "/private/tmp"))`,
		`(deny file-read* (regex "^/w/verdict-snapshot-"))`,
		`(allow file-read* file-write* (subpath "/w/verdict-snapshot-1-2"))`,
		`(deny file-read* file-write* (subpath "` + filepath.Join(home, ".ssh") + `"))`,
		`(allow file-read* file-write* (subpath "` + sessions + `"))`,
		`(deny file-write* (literal "` + sessions + `"))`,
		`auth\\.json[^/]*$"))`,
		`(deny file-write* (subpath "` + filepath.Join(home, ".pi", "agent", "extensions") + `"))`,
	}
	assertInOrder(t, profile, ordered)
	if !strings.Contains(profile, `(deny user-preference-write)`) || !strings.Contains(profile, `"com.apple.cfprefsd.agent"`) {
		t.Errorf("profile does not close preference writes:\n%s", profile)
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
	if strings.Contains(profile, `"com.apple.SecurityServer"`) {
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
	modes := map[string]os.FileMode{"/tmp": os.ModeDir, "/run": os.ModeDir, "/var/run": os.ModeSymlink}
	got := hiddenRoots(func(p string) (os.FileMode, bool) { m, ok := modes[p]; return m, ok })
	if !slices.Equal(got, []string{"/tmp", "/run"}) {
		t.Errorf("hidden roots = %v, want [/tmp /run]", got)
	}
}

// TestReviewEnvDropsSocketVariables proves a review launch inherits no
// variable naming a socket out of the sandbox, whatever its case.
func TestReviewEnvDropsSocketVariables(t *testing.T) {
	t.Parallel()
	got := stripSocketEnv([]string{
		"SSH_AUTH_SOCK=/a", "dbus_session_bus_address=unix:x", "GPG_AGENT_INFO=g", "TMUX=t", "NVIM=n",
		"XDG_RUNTIME_DIR=/run/user/1", "HOME=/h",
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
