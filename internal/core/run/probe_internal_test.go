package run

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// probeCdr answers from argv, like the agent it stands in for: the help probe
// prints its identity, and the real directive writes the review. It records
// every command, so a test can prove a refusal launched no agent.
type probeCdr struct {
	answer  func(argv []string) (core.Result, error)
	onWrite func()
	calls   []*core.Command
}

// Run implements core.Commander.
func (p *probeCdr) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	p.calls = append(p.calls, cmd)
	if p.answer != nil {
		return p.answer(cmd.Argv)
	}
	if p.onWrite != nil {
		p.onWrite()
	}
	return core.Result{}, nil
}

// probeDef is the operator row every probe test launches: the fake's help
// subcommand identifies it, and its exec subcommand writes the review.
func probeDef() config.PersonalAgent {
	return config.PersonalAgent{
		Name: "local", Binary: "local-agent", Args: []string{"exec"},
		PromptFlag: "-p", Install: "brew install local-agent",
		ProbeArgs: []string{"ask", "--help"}, ProbeMatch: "ask",
	}
}

// probeRequest resolves the fake binary to a literal path, so a refusal can
// name the path the operator would have to replace in settings.json.
func probeRequest(root string, cdr *probeCdr) *core.RunRequest {
	req := request(root, cdr)
	req.Agent = "local"
	req.LookPath = func(string) (string, error) { return "/opt/agents/local-agent", nil }
	return req
}

// probeCalls counts the real agent launches, never the identity probe.
func (p *probeCdr) launches() int {
	n := 0
	for _, cmd := range p.calls {
		if len(cmd.Argv) > 1 && cmd.Argv[1] == "exec" {
			n++
		}
	}
	return n
}

// TestProbeMatchingLaunches pins the probe's argv, child environment, no-stdin
// contract, 64 KiB capture cap, and launcher record before the real launch.
func TestProbeMatchingLaunches(t *testing.T) {
	root, dir, cdr := probeFixture(t)
	_, ref := WithAgents(t.Context(), probeRequest(root, cdr), []config.PersonalAgent{probeDef()})
	if ref != nil {
		t.Fatalf("launch refused: %v", ref)
	}
	if len(cdr.calls) != 2 || cdr.launches() != 1 {
		t.Fatalf("commands = %d, launches = %d; want a probe and one launch", len(cdr.calls), cdr.launches())
	}
	assertProbeCommand(t, root, cdr.calls[0])
	assertProbeRecord(t, dir)
}

// probeFixture plants a bundle and an agent that answers its own help probe.
func probeFixture(t *testing.T) (string, string, *probeCdr) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cdr := &probeCdr{answer: func(argv []string) (core.Result, error) {
		if slices.Equal(argv[1:], []string{"ask", "--help"}) {
			return core.Result{Stdout: "Usage: local-agent ask --help\n"}, nil
		}
		if slices.Contains(argv, "exec") {
			writeReview(t, dir)
		}
		return core.Result{}, nil
	}}
	return root, dir, cdr
}

// assertProbeCommand pins the process contract the child sees: the resolved
// binary, its own argv, no stdin, the launch's environment, and the byte cap.
func assertProbeCommand(t *testing.T, root string, cmd *core.Command) {
	t.Helper()
	if want := []string{"/opt/agents/local-agent", "ask", "--help"}; !slices.Equal(cmd.Argv, want) {
		t.Errorf("probe argv = %q, want %q", cmd.Argv, want)
	}
	if cmd.OutputCap != 1024*1024 {
		t.Errorf("probe capture cap = %d, want 1048576", cmd.OutputCap)
	}
	if cmd.Dir != root || !slices.Equal(cmd.Env, []string{"ANTHROPIC_API_KEY=sk-ant-0123456789abcdef"}) {
		t.Errorf("probe child = dir %q env %q; want the launch's dir and env", cmd.Dir, cmd.Env)
	}
	if cmd.Stdin == nil {
		t.Fatal("probe inherited stdin; want no stdin")
	}
	buf := make([]byte, 1)
	if n, err := cmd.Stdin.Read(buf); n != 0 || err != io.EOF {
		t.Errorf("probe stdin read = %d, %v; want EOF", n, err)
	}
}

// assertProbeRecord pins the launcher's probe receipt: the original args and
// a passing result, both before the launch is recorded.
func assertProbeRecord(t *testing.T, dir string) {
	t.Helper()
	rec := readLauncher(t, dir)
	got, isMap := jq.AsMap(rec["probe"])
	if !isMap || got["ok"] != true {
		t.Fatalf("launcher probe = %v, want ok=true", rec["probe"])
	}
	if args, ok := got["args"].([]any); !ok || !slices.Equal(args, []any{"ask", "--help"}) {
		t.Errorf("launcher probe args = %v, want [ask --help]", got["args"])
	}
}

// TestProbeRefusalsBeforeLaunch pins every mismatch shape: no agent process
// starts, the typed code is literal, and the message names the agent, the
// resolved path, and why the probe failed.
type probeRefusalCase struct {
	name   string
	answer func(argv []string) (core.Result, error)
	want   string
}

func TestProbeRefusalsBeforeLaunch(t *testing.T) {
	tests := []probeRefusalCase{
		{
			name: "non-zero exit",
			answer: func([]string) (core.Result, error) {
				return core.Result{ExitCode: 3, Stderr: "other tool"}, nil
			},
			want: "exited 3",
		},
		{
			name: "timeout",
			answer: func([]string) (core.Result, error) {
				return core.Result{TimedOut: true}, nil
			},
			want: "timed out",
		},
		{
			name: "missing match",
			answer: func([]string) (core.Result, error) {
				return core.Result{Stdout: "a different agent"}, nil
			},
			want: "did not contain",
		},
		{
			name: "spawn error",
			answer: func([]string) (core.Result, error) {
				return core.Result{}, errors.New("permission denied")
			},
			want: "could not run",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertProbeRefusal(t, tc) })
	}
}

// assertProbeRefusal drives one failed probe through the service and checks
// that it refuses before the real launch.
func assertProbeRefusal(t *testing.T, tc probeRefusalCase) {
	t.Helper()
	root := fixtureRoot(t)
	cdr := &probeCdr{answer: tc.answer}
	_, ref := WithAgents(t.Context(), probeRequest(root, cdr), []config.PersonalAgent{probeDef()})
	if ref == nil || ref.Code != "agent-mismatch" {
		t.Fatalf("refusal = %+v, want agent-mismatch", ref)
	}
	if cdr.launches() != 0 {
		t.Errorf("launches = %d, want zero", cdr.launches())
	}
	assertProbeMessage(t, ref.Message, tc.want)
}

// assertProbeMessage pins the facts a refusal shows the operator: the agent,
// the binary path, the probe result, and a concrete next step.
func assertProbeMessage(t *testing.T, message, result string) {
	t.Helper()
	for _, frag := range []string{"local", "/opt/agents/local-agent", result} {
		if !strings.Contains(message, frag) {
			t.Errorf("message = %q, want it to name %q", message, frag)
		}
	}
	hint := core.HintFor("agent-mismatch")
	if !strings.Contains(hint, "settings.json") || !strings.Contains(hint, "--agent") {
		t.Errorf("hint = %q, want the settings.json and --agent remedy", hint)
	}
}

// TestProbeKeepsParentCancellation pins the refusal precedence when the
// caller cancels while the probe is running: the probe's own deadline still
// classifies agent-mismatch, but a canceled parent outranks its result.
func TestProbeKeepsParentCancellation(t *testing.T) {
	root := fixtureRoot(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cdr := &probeCdr{answer: func([]string) (core.Result, error) {
		cancel()
		return core.Result{Stdout: "a different agent"}, nil
	}}
	_, ref := WithAgents(ctx, probeRequest(root, cdr), []config.PersonalAgent{probeDef()})
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("ref = %+v, want canceled", ref)
	}
	if ref.Message != msgCanceled {
		t.Errorf("message = %q, want %q", ref.Message, msgCanceled)
	}
	if cdr.launches() != 0 {
		t.Errorf("launches = %d, want zero", cdr.launches())
	}
}

// TestProbeSkippedUnderPrint proves a render spawns nothing at all: no probe
// process, even when the row declares one.
func TestProbeSkippedUnderPrint(t *testing.T) {
	root := fixtureRoot(t)
	cdr := &probeCdr{answer: func([]string) (core.Result, error) {
		t.Fatal("a --print render spawned a process")
		return core.Result{}, nil
	}}
	req := probeRequest(root, cdr)
	req.Print = true
	resp, ref := WithAgents(t.Context(), req, []config.PersonalAgent{probeDef()})
	if ref != nil {
		t.Fatalf("print refused: %v", ref)
	}
	if len(cdr.calls) != 0 || !resp.Data.Printed {
		t.Errorf("calls = %d, printed = %v; want a render with no process", len(cdr.calls), resp.Data.Printed)
	}
}

// failingProbe fails every identity probe and counts real launches. The two
// finders probe at once, so unlike probeCdr it is safe to call concurrently.
type failingProbe struct{ launched atomic.Int32 }

// Run implements core.Commander.
func (f *failingProbe) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if slices.Contains(cmd.Argv, "exec") {
		f.launched.Add(1)
	}
	return core.Result{ExitCode: 3, Stderr: "other tool"}, nil
}

// TestProbeRefusesBothFindersAsAgentMismatch pins the default two-finder
// launch against a binary that is not the agent: both finders fail their
// identity probe before spawning, and the run says so, not an evidence gap.
func TestProbeRefusesBothFindersAsAgentMismatch(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	writeUnder(t, dir, "run.json", `{"schema_version":"1"}`)
	writeUnder(t, dir, "PR.json", `{"headRefOid":"head"}`)
	writeUnder(t, dir, "procedure.md", "authoritative procedure")
	cdr := &failingProbe{}
	req := request(root, cdr)
	req.Agent = "local"
	req.LookPath = func(string) (string, error) { return "/opt/agents/local-agent", nil }
	req.FindersRaw = "2"
	_, ref := WithAgents(t.Context(), req, []config.PersonalAgent{probeDef()})
	if ref == nil || ref.Code != "agent-mismatch" {
		t.Fatalf("refusal = %+v, want agent-mismatch", ref)
	}
	if n := cdr.launched.Load(); n != 0 {
		t.Errorf("launches = %d, want zero", n)
	}
}
