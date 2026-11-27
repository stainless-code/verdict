package run

import (
	"context"
	"os/exec"
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

// StartsHostProcesses marks the commander as a host one.
func (h *hostScripted) StartsHostProcesses() bool { return true }

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
