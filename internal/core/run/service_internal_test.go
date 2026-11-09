package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// scripted is a canned commander: it records the argv it was handed, optionally
// performs the child's side effect (writing the review file), and returns the
// scripted wait status. No process is spawned.
type scripted struct {
	err   error
	write func()
	cmd   *core.Command
	res   core.Result
	calls int
}

// Run implements core.Commander.
func (s *scripted) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	s.calls++
	s.cmd = cmd
	if s.write != nil {
		s.write()
	}
	return s.res, s.err
}

// prNumber is the bundle directory and PR number every run fixture uses.
const prNumber = "42"

// fixtureRoot builds the fixture bundle and returns the reviewed repository
// root. PR.json is written so the bundle resolves as a PR bundle, not a local
// one.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", prNumber)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"number":42,"state":"OPEN"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// bundleDir is the fixture bundle path under root.
func bundleDir(root string) string {
	return filepath.Join(root, ".verdict", "state", prNumber)
}

// reviewDoc is a minimal review the shared record path accepts unchanged.
const reviewDoc = `{"schema_version":"1","body":"> ✅ no findings\n\n### Nothing to flag\n\nReviewed the diff.","approved":false,"comments":[]}`

// writeReview plants the review artifact at the contract path.
func writeReview(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "review.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// missingLookPath is a PATH probe that resolves nothing.
func missingLookPath(string) (string, error) {
	return "", errors.New("executable file not found in $PATH")
}

// request builds a service request whose PATH probe resolves the agent.
func request(root string, cmdr *scripted) *core.RunRequest {
	rt := &core.Runtime{Env: map[string]string{}, Now: time.Now, Commander: cmdr}
	return &core.RunRequest{
		Runtime: rt, Repo: "acme/app", Pr: "42", RepoRoot: root,
		LookPath: func(string) (string, error) {
			return "/opt/agents/claude", nil
		},
		Environ:    []string{"ANTHROPIC_API_KEY=sk-ant-0123456789abcdef"},
		Agent:      "claude",
		TimeoutRaw: "300",
	}
}

// TestRunSuccess proves a successful launch asserts the fresh artifact, records
// the provenance, and hands off without recording or posting anything itself.
func TestRunSuccess(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{write: func() { writeReview(t, dir, reviewDoc) }}
	resp, ref := Run(t.Context(), request(root, cmdr))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if resp.Exit != 0 || !resp.Data.Fresh || resp.Data.Agent != "claude" {
		t.Fatalf("response = %+v", resp.Data)
	}
	if cmdr.calls != 1 {
		t.Errorf("spawns = %d, want 1", cmdr.calls)
	}
	assertLauncherRecord(t, dir)
	if !strings.Contains(resp.Next, "verdict post acme/app 42") {
		t.Errorf("next = %q, want the post handoff", resp.Next)
	}
}

// TestRunPassThroughArgv proves the operator's arguments after -- reach the
// agent unchanged and land in the recorded argv between the agent's flags
// and the directive.
func TestRunPassThroughArgv(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{write: func() { writeReview(t, dir, reviewDoc) }}
	req := request(root, cmdr)
	req.PassThrough = []string{"--model", "opus", "--max-turns", "12"}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	shape, ok := readLauncher(t, dir)["argv"].([]any)
	if !ok {
		t.Fatal("launcher.json argv is not an array")
	}
	assertPassThroughArgv(t, cmdr.cmd.Argv, shape)
}

// TestRunPrintSpawnsNothing proves --print renders the exact argv and touches
// neither the store nor the agent.
func TestRunPrintSpawnsNothing(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{}
	req := request(root, cmdr)
	req.Print = true
	req.PassThrough = []string{"--model", "opus"}
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if cmdr.calls != 0 {
		t.Errorf("spawns = %d, want 0 under --print", cmdr.calls)
	}
	if !resp.Data.Printed || resp.Data.Argv[0] != "claude" {
		t.Fatalf("print data = %+v", resp.Data)
	}
	if _, err := os.Stat(filepath.Join(dir, "launcher.json")); err == nil {
		t.Error("--print wrote launcher.json; it must touch nothing")
	}
}

// TestRunRefusals pins the six failure modes plus the two pre-flight refusals:
// code, message fragment, and a non-empty hint for every one.
func TestRunRefusals(t *testing.T) {
	cases := []refusalCase{
		{
			name:   "agent missing from PATH",
			mutate: func(_ *testing.T, _ string, req *core.RunRequest, _ *scripted) { req.LookPath = missingLookPath },
			code:   codeAgentMissing,
			want:   "not installed",
		},
		{
			name: "spawn failure",
			mutate: func(_ *testing.T, _ string, _ *core.RunRequest, cmdr *scripted) {
				cmdr.err = errors.New("fork/exec: permission denied")
			},
			code: codeAgentSpawnFailed,
			want: "could not start agent",
		},
		{
			name: "watchdog timeout",
			mutate: func(_ *testing.T, _ string, _ *core.RunRequest, cmdr *scripted) {
				cmdr.res = core.Result{ExitCode: 137, TimedOut: true}
			},
			code: codeAgentTimeout,
			want: "watchdog",
		},
		{
			name:   "agent non-zero exit",
			mutate: func(_ *testing.T, _ string, _ *core.RunRequest, cmdr *scripted) { cmdr.res = core.Result{ExitCode: 9} },
			code:   codeAgentFailed,
			want:   "exited 9",
		},
		{
			name:   "no artifact",
			mutate: func(*testing.T, string, *core.RunRequest, *scripted) {},
			code:   codeNoArtifact,
			want:   "wrote no",
		},
		{
			name: "artifact older than the run",
			mutate: func(t *testing.T, root string, _ *core.RunRequest, _ *scripted) {
				t.Helper()
				stale := writeReview(t, bundleDir(root), reviewDoc)
				old := time.Now().Add(-time.Hour)
				if err := os.Chtimes(stale, old, old); err != nil {
					t.Fatal(err)
				}
			},
			code: codeStaleArtifact,
			want: "predates this run",
		},
		{
			name:   "unknown agent name",
			mutate: func(_ *testing.T, _ string, req *core.RunRequest, _ *scripted) { req.Agent = "gpt" },
			code:   codeUnknownAgent,
			want:   "known: claude, codex, cursor, opencode",
		},
		{
			name:   "absent bundle",
			mutate: func(*testing.T, string, *core.RunRequest, *scripted) {},
			code:   codeMissingBundle,
			want:   "fetch it first",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRunRefusal(t, tc)
		})
	}
}

// readLauncher reads the provenance record back as a map.
func readLauncher(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, launcherFile))
	if err != nil {
		t.Fatalf("read launcher.json: %v", err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatalf("decode launcher.json: %v", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatalf("launcher.json is not an object: %s", raw)
	}
	return m
}

// refusalCase is one failure mode and the code, message fragment, and exit the
// run must produce for it.
type refusalCase struct {
	mutate func(t *testing.T, root string, req *core.RunRequest, cmdr *scripted)
	name   string
	code   string
	want   string
}

// assertRunRefusal drives one failure mode through Run and pins the typed
// refusal: its code, a message naming the cause, and a non-empty hint.
func assertRunRefusal(t *testing.T, tc refusalCase) {
	t.Helper()
	root := fixtureRoot(t)
	if tc.code == codeMissingBundle {
		root = t.TempDir()
	}
	cmdr := &scripted{}
	req := request(root, cmdr)
	tc.mutate(t, root, req, cmdr)
	resp, ref := Run(t.Context(), req)
	if ref == nil {
		t.Fatalf("Run = %+v, want the %s refusal", resp, tc.code)
	}
	if ref.Code != tc.code {
		t.Fatalf("code = %q, want %q (%s)", ref.Code, tc.code, ref.Message)
	}
	if !strings.Contains(ref.Message, tc.want) {
		t.Errorf("message = %q, want it to name %q", ref.Message, tc.want)
	}
	if core.HintFor(ref.Code) == "" {
		t.Errorf("refusal %q has no hint", ref.Code)
	}
	assertRefusalExit(t, resp, ref.Code)
}

// assertRefusalExit proves only the agent's own failure propagates its exit.
func assertRefusalExit(t *testing.T, resp *core.RunResponse, code string) {
	t.Helper()
	if resp == nil {
		return
	}
	if code == codeAgentFailed && resp.Exit != 9 {
		t.Errorf("non-zero exit = %d, want the agent's 9", resp.Exit)
	}
	if code != codeAgentFailed && resp.Exit != 0 {
		t.Errorf("exit = %d, want 0: only the agent's own failure propagates", resp.Exit)
	}
}

// assertLauncherRecord pins the provenance record a launch writes: its
// required fields, the agent identity, and the store it must not touch.
func assertLauncherRecord(t *testing.T, dir string) {
	t.Helper()
	rec := readLauncher(t, dir)
	assertLauncherKeys(t, rec)
	if rec["agent"] != "claude" || rec["binary"] != "/opt/agents/claude" {
		t.Errorf("launcher.json agent/binary = %v/%v", rec["agent"], rec["binary"])
	}
	if _, ok := rec["output_tail"]; ok {
		t.Error("launcher.json recorded an empty output tail")
	}
	for _, name := range []string{"findings.json", "record.json", "post.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("run wrote %s; recording and posting stay the operator's next step", name)
		}
	}
}

// assertLauncherKeys pins the provenance fields every launch records.
func assertLauncherKeys(t *testing.T, rec map[string]any) {
	t.Helper()
	for _, key := range []string{"agent", "binary", "argv", "started_at", "ended_at", "exit", "duration_ms", "artifact_fresh"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("launcher.json has no %q", key)
		}
	}
}

// assertPassThroughArgv proves the operator's flags reach the spawn unchanged
// and land in the record verbatim, with the directive reduced to a placeholder.
func assertPassThroughArgv(t *testing.T, got []string, shape []any) {
	t.Helper()
	assertArgvPrefix(t, got, []string{"claude", "-p", "--model", "opus", "--max-turns", "12"})
	if !strings.Contains(got[len(got)-1], ".verdict/state/42") {
		t.Errorf("last argv element %q does not carry the bundle directive", got[len(got)-1])
	}
	if shape[len(shape)-1] != promptPlaceholder {
		t.Errorf("recorded argv tail = %v, want %q", shape[len(shape)-1], promptPlaceholder)
	}
	if shape[2] != "--model" || shape[3] != "opus" {
		t.Errorf("recorded argv = %v, want the pass-through flags verbatim", shape)
	}
}

// assertArgvPrefix proves the spawned argv starts with want and carries exactly
// one more element: the directive.
func assertArgvPrefix(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want)+1 {
		t.Fatalf("argv = %q, want %q plus the directive", got, want)
	}
	for i, part := range want {
		if got[i] != part {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], part)
		}
	}
}
