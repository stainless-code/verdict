package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// sequenced is a canned commander that changes its side effect per call, so
// one test drives a multi-round loop: write picks the review bytes for the
// call number, result the wait status.
type sequenced struct {
	write  func(call int)
	result func(call int) core.Result
	argv   [][]string
	calls  int
}

// Run implements core.Commander.
func (s *sequenced) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	s.calls++
	s.argv = append(s.argv, cmd.Argv)
	if s.write != nil {
		s.write(s.calls)
	}
	if s.result != nil {
		return s.result(s.calls), nil
	}
	return core.Result{}, nil
}

// collector keeps the human lines the loop streams.
type collector struct {
	human []string
}

func (c *collector) Human(line string) { c.human = append(c.human, line) }
func (c *collector) Warn(string)       {}
func (c *collector) Diag(string)       {}

// repairFixture builds a PR bundle with a diff covering notes.txt, so the
// shape checker's anchor verdicts mean something.
func repairFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", prNumber)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"number":42,"state":"OPEN"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old\n+new\n bottom\n"
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// repairRequest builds a service request with the round cap and a human-line
// collector.
func repairRequest(root string, cmdr *sequenced, rounds string, lines *collector) *core.RunRequest {
	rt := &core.Runtime{Env: map[string]string{}, Now: time.Now, Commander: cmdr, Progress: lines}
	return &core.RunRequest{
		Runtime: rt, Repo: "acme/app", Pr: "42", RepoRoot: root,
		LookPath: func(string) (string, error) {
			return "/opt/agents/claude", nil
		},
		Environ:    []string{"ANTHROPIC_API_KEY=sk-ant-0123456789abcdef"},
		Agent:      "claude",
		TimeoutRaw: "300",
		RoundsRaw:  rounds,
	}
}

func writeDoc(t *testing.T, root, doc string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bundleDir(root), "review.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	// invalidDoc anchors its comment on a file the diff never touched.
	invalidDoc = `{"schema_version":"1","body":"> ✅ first attempt\n\n### Notes\n\nReview text here.",` +
		`"approved":false,"comments":[{"path":"elsewhere.txt","line":1,"side":"RIGHT","body":"note","rigor":"walked"}]}`
	// validDoc is the same review re-anchored on the changed file.
	validDoc = `{"schema_version":"1","body":"> ✅ second attempt\n\n### Notes\n\nReview text here.",` +
		`"approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"note","rigor":"walked"}]}`
	// warningsDoc misses schema_version: warnings only, so leniently valid.
	warningsDoc = `{"body":"> ✅ warnings only\n\n### Notes\n\nReview text here.","approved":false,"comments":[]}`
)

// repairRecord reads the launcher record's repair profile back.
func repairRecord(t *testing.T, dir string) map[string]any {
	t.Helper()
	rec := readLauncher(t, dir)
	repair, ok := rec["repair"].(map[string]any)
	if !ok {
		t.Fatalf("launcher.json carries no repair object: %v", rec)
	}
	return repair
}

// roundLines keeps the human lines announcing a round.
func roundLines(lines *collector) []string {
	var out []string
	for _, line := range lines.human {
		if strings.HasPrefix(line, "» round ") {
			out = append(out, line)
		}
	}
	return out
}

// assertRepairProfile pins the response's repair shape: the cap, the round
// that went valid, and the pass/fail pattern across attempts.
func assertRepairProfile(t *testing.T, resp *core.RunResponse, rounds, toValid int, pass []bool) {
	t.Helper()
	rep := resp.Data.Repair
	if rep == nil {
		t.Fatal("response data carries no repair profile")
	}
	if rep.Rounds != rounds || rep.RoundsToValid != toValid || !rep.FinalValid {
		t.Fatalf("repair profile = %+v, want rounds %d to-valid %d valid", rep, rounds, toValid)
	}
	if len(rep.Attempts) != len(pass) {
		t.Fatalf("attempts = %+v, want %d rounds", rep.Attempts, len(pass))
	}
	for i, want := range pass {
		if rep.Attempts[i].CheckOK != want {
			t.Fatalf("attempts = %+v, want pass %v", rep.Attempts, pass)
		}
	}
}

// assertRoundFiles pins the per-round evidence: every named file exists and
// the named review file holds the want bytes.
func assertRoundFiles(t *testing.T, dir, reviewName, want string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, roundsDir, name)); err != nil {
			t.Errorf("missing per-round file %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, roundsDir, reviewName))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Errorf("%s = %q, want %q", reviewName, raw, want)
	}
}

// assertFileBytes pins one evidence file's exact bytes.
func assertFileBytes(t *testing.T, name string, raw []byte, want string) {
	t.Helper()
	if string(raw) != want {
		t.Errorf("%s = %q, want %q", name, raw, want)
	}
}

// lastPrompt returns the directive the call-th spawn received.
func lastPrompt(cmdr *sequenced, call int) string {
	return cmdr.argv[call-1][len(cmdr.argv[call-1])-1]
}

// TestRepairValidFirstRoundStopsAtOne proves a valid round 1 ends the loop:
// one spawn, a success, and a repair profile recording one of one.
func TestRepairValidFirstRoundStopsAtOne(t *testing.T) {
	root := repairFixture(t)
	dir := bundleDir(root)
	var lines collector
	cmdr := &sequenced{write: func(int) { writeDoc(t, root, reviewDoc) }}
	resp, ref := Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if cmdr.calls != 1 {
		t.Fatalf("spawns = %d, want 1", cmdr.calls)
	}
	assertRepairProfile(t, resp, 3, 1, []bool{true})
	rep := repairRecord(t, dir)
	if n, _ := jq.IntOf(rep["rounds_to_valid"]); n != 1 {
		t.Errorf("launcher repair.rounds_to_valid = %v, want 1", rep["rounds_to_valid"])
	}
	if got := roundLines(&lines); len(got) != 1 {
		t.Errorf("round lines = %q, want one", got)
	}
}

// TestRepairInvalidThenValid proves the loop re-prompts: two spawns, the
// second round's bytes in place, and rounds_to_valid 2.
func TestRepairInvalidThenValid(t *testing.T) {
	root := repairFixture(t)
	dir := bundleDir(root)
	var lines collector
	cmdr := &sequenced{write: func(call int) {
		if call == 1 {
			writeDoc(t, root, invalidDoc)
		} else {
			writeDoc(t, root, validDoc)
		}
	}}
	resp, ref := Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if cmdr.calls != 2 {
		t.Fatalf("spawns = %d, want 2", cmdr.calls)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, "review.json", raw, validDoc)
	assertRepairProfile(t, resp, 3, 2, []bool{false, true})
	if len(resp.Data.Repair.Attempts[0].Problems) == 0 {
		t.Error("round-1 attempt carries no problems")
	}
	assertRoundFiles(t, dir, "02.review.json", validDoc,
		"01.review.json", "01.check.json", "02.review.json", "02.check.json")
	if got := roundLines(&lines); len(got) != 2 {
		t.Errorf("round lines = %q, want one per round", got)
	}
	assertRoundPrompts(t, cmdr)
}

// assertRoundPrompts pins the two prompts: round 1 sends the one-shot
// directive, round 2 appends the fix-list tail.
func assertRoundPrompts(t *testing.T, cmdr *sequenced) {
	t.Helper()
	if !strings.HasPrefix(lastPrompt(cmdr, 1), "Review acme/app PR 42") {
		t.Errorf("round-1 directive = %q, want the one-shot text first", lastPrompt(cmdr, 1))
	}
	if !strings.Contains(lastPrompt(cmdr, 2), "Fix every error. Reply with one JSON document only.") {
		t.Errorf("round-2 prompt carries no fix list tail")
	}
}

// assertExhausted pins the repair-exhausted refusal: its code, a non-empty
// problem list with a hint, and the spawn count the cap allowed.
func assertExhausted(t *testing.T, ref *core.RefusalError, cmdr *sequenced, spawns int) {
	t.Helper()
	if ref == nil {
		t.Fatal("Run succeeded, want the repair-exhausted refusal")
	}
	if ref.Code != codeRepairExhausted {
		t.Fatalf("code = %q, want %q", ref.Code, codeRepairExhausted)
	}
	if len(ref.Problems) == 0 {
		t.Error("repair-exhausted carries no problems")
	}
	if cmdr.calls != spawns {
		t.Fatalf("spawns = %d, want %d", cmdr.calls, spawns)
	}
	if core.HintFor(ref.Code) == "" {
		t.Errorf("refusal %q has no hint", ref.Code)
	}
}

// TestRepairCrashInRoundTwoKeepsAgentFailed proves a launch failure in a later
// round reports the existing code and spends no further round.
func TestRepairCrashInRoundTwoKeepsAgentFailed(t *testing.T) {
	root := repairFixture(t)
	var lines collector
	cmdr := &sequenced{
		write: func(call int) {
			if call == 1 {
				writeDoc(t, root, invalidDoc)
			}
		},
		result: func(call int) core.Result {
			if call == 1 {
				return core.Result{}
			}
			return core.Result{ExitCode: 9}
		},
	}
	resp, ref := Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	if ref == nil {
		t.Fatalf("Run = %+v, want the agent-failed refusal", resp)
	}
	if ref.Code != codeAgentFailed {
		t.Fatalf("code = %q, want %q", ref.Code, codeAgentFailed)
	}
	assertAgentExit(t, resp, cmdr, 9, 2)
}

// assertAgentExit pins the agent's own failure: its exit code rides the
// response and the loop spent no round past the crash.
func assertAgentExit(t *testing.T, resp *core.RunResponse, cmdr *sequenced, exit, spawns int) {
	t.Helper()
	if cmdr.calls != spawns {
		t.Fatalf("spawns = %d, want %d", cmdr.calls, spawns)
	}
	if resp == nil || resp.Exit != exit {
		t.Fatalf("resp = %+v, want the agent's exit %d", resp, exit)
	}
}

// TestRepairIdenticalRetryStopsEarly proves a round that repeats the prior
// bytes ends the run with the no-progress cause instead of spending the cap.
func TestRepairIdenticalRetryStopsEarly(t *testing.T) {
	root := repairFixture(t)
	var lines collector
	cmdr := &sequenced{write: func(int) { writeDoc(t, root, invalidDoc) }}
	_, ref := Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	if ref == nil {
		t.Fatal("Run succeeded, want the repair-exhausted refusal")
	}
	if ref.Code != codeRepairExhausted {
		t.Fatalf("code = %q, want %q", ref.Code, codeRepairExhausted)
	}
	if !strings.Contains(ref.Message, "no progress") {
		t.Errorf("message = %q, want the no-progress cause", ref.Message)
	}
	if cmdr.calls != 2 {
		t.Fatalf("spawns = %d, want 2: the identical round must not spend round 3", cmdr.calls)
	}
}

// TestRepairExhaustedCarriesProblems proves the capped run refuses typed with
// the final round's problems for the operator to fix.
func TestRepairExhaustedCarriesProblems(t *testing.T) {
	root := repairFixture(t)
	var lines collector
	cmdr := &sequenced{write: func(call int) {
		writeDoc(t, root, "not json attempt "+strings.Repeat("x", call))
	}}
	_, ref := Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	assertExhausted(t, ref, cmdr, 3)
}

// TestRepairWarningsOnlyNeedsNoExtraRound proves leniency: a warnings-only
// review is valid and never triggers another round.
func TestRepairWarningsOnlyNeedsNoExtraRound(t *testing.T) {
	root := repairFixture(t)
	var lines collector
	cmdr := &sequenced{write: func(int) { writeDoc(t, root, warningsDoc) }}
	resp, ref := Run(t.Context(), repairRequest(root, cmdr, "3", &lines))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if cmdr.calls != 1 {
		t.Fatalf("spawns = %d, want 1: warnings must not re-prompt", cmdr.calls)
	}
	assertRepairProfile(t, resp, 3, 1, []bool{true})
}

// TestRoundsValidation pins the --rounds range: 1-5 parses, and an absent
// value keeps the one-shot default.
func TestRoundsValidation(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"1", 1}, {"2", 2}, {"5", 5}} {
		st := &state{req: &core.RunRequest{RoundsRaw: tc.raw}}
		n, ref := st.rounds()
		if ref != nil {
			t.Errorf("rounds(%q) refused: %v", tc.raw, ref)
		} else if n != tc.want {
			t.Errorf("rounds(%q) = %d, want %d", tc.raw, n, tc.want)
		}
	}
	st := &state{req: &core.RunRequest{}}
	if n, ref := st.rounds(); ref != nil || n != 1 {
		t.Errorf("rounds(\"\") = %d, %v; want the one-shot default", n, ref)
	}
}

// TestRoundsRejects pins the --rounds refusals: anything outside 1-5, or not
// a number, is bad-flags.
func TestRoundsRejects(t *testing.T) {
	for _, raw := range []string{"0", "6", "x", "-1", " 2", "+2", "10"} {
		st := &state{req: &core.RunRequest{RoundsRaw: raw}}
		if _, ref := st.rounds(); ref == nil || ref.Code != codeBadFlags {
			t.Errorf("rounds(%q) = no bad-flags refusal", raw)
		}
	}
}

// TestRepairPrintWithRoundsRefuses proves --print stays the one-command
// escape hatch: pairing it with --rounds refuses bad-flags and spawns
// nothing.
func TestRepairPrintWithRoundsRefuses(t *testing.T) {
	root := fixtureRoot(t)
	var lines collector
	cmdr := &sequenced{}
	req := repairRequest(root, cmdr, "2", &lines)
	req.Print = true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeBadFlags {
		t.Fatalf("ref = %v, want the bad-flags refusal", ref)
	}
	if cmdr.calls != 0 {
		t.Errorf("spawns = %d, want 0", cmdr.calls)
	}
}

// TestRepairOneShotLeavesNoRepairProfile proves the default path is
// untouched: no repair object in the response or the record.
func TestRepairOneShotLeavesNoRepairProfile(t *testing.T) {
	root := repairFixture(t)
	dir := bundleDir(root)
	var lines collector
	cmdr := &sequenced{write: func(int) { writeDoc(t, root, reviewDoc) }}
	resp, ref := Run(t.Context(), repairRequest(root, cmdr, "1", &lines))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if resp.Data.Repair != nil {
		t.Errorf("one-shot data carries %+v, want no repair profile", resp.Data.Repair)
	}
	raw, err := os.ReadFile(filepath.Join(dir, launcherFile))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := jq.AsMap(doc)
	if _, ok := m["repair"]; ok {
		t.Error("one-shot launcher.json carries repair")
	}
	if _, ok := m["attempts"]; ok {
		t.Error("one-shot launcher.json carries attempts")
	}
}
