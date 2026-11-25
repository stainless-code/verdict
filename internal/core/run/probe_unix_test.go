//go:build !windows

package run

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// probeIdentityCase is one shipped row whose binary name belongs to another
// tool too. The clash text is that tool's plausible help; the own text is the
// flag quoted in the row's source comment.
type probeIdentityCase struct {
	name      string
	match     string
	clashHelp string
	ownHelp   string
	probeArgs []string
}

var probeIdentityCases = []probeIdentityCase{
	{
		name: "bob", probeArgs: []string{"run", "--help"}, match: "--disable-tool-groups",
		clashHelp: "Usage: bob run <version>\n  list        show installed versions",
		ownHelp:   "Usage: bob run [options]\n  --disable-tool-groups <groups>  Disable specific tool groups",
	},
	{
		name: "cortex", probeArgs: []string{"--help"}, match: "--dangerously-allow-all-tool-calls",
		clashHelp: "Usage: cortex query <version>\n  -query.duration <duration>",
		ownHelp:   "Usage: cortex [options]\n  --bypass, --dangerously-allow-all-tool-calls  Enable bypass mode",
	},
	{
		name: "forge", probeArgs: []string{"--help"}, match: "--conversation-id",
		clashHelp: "Usage: forge render <version>\n  --benchmark  measure render time",
		ownHelp:   "Usage: forge [options]\n  --conversation-id <id>  Resume a named conversation (alias --cid)",
	},
	{
		name: "fx", probeArgs: []string{"ask", "--help"}, match: "--ultrafast",
		clashHelp: "Usage: fx run <version>\n  -r, --raw  print raw JSON",
		ownHelp:   "Usage: fx ask [options]\n  --fast, --ultrafast  request a faster answer",
	},
	{
		name: "grok", probeArgs: []string{"--help"}, match: "--prompt-json",
		clashHelp: "Usage: grok match <version>\n  -e, --expression <regex>",
		ownHelp:   "Usage: grok [options]\n  --prompt-json <JSON>  supply the prompt as JSON",
	},
	{
		name: "kilo", probeArgs: []string{"run", "--help"}, match: "--cloud-fork",
		clashHelp: "Usage: kilo run <version>\n  -v, --verbose  show editor details",
		ownHelp:   "Usage: kilo run [options]\n  --cloud-fork  fetch session from cloud and continue locally (use with --session)",
	},
}

// TestClashingBinaryNeedsDocumentedFlag proves both halves of each identity
// probe: a same-named tool that prints its own help, even with the subcommand
// the probe asks for, refuses before launch, and the documented flag passes.
func TestClashingBinaryNeedsDocumentedFlag(t *testing.T) {
	t.Parallel()
	for i := range probeIdentityCases {
		tc := &probeIdentityCases[i]
		t.Run(tc.name+"/clash", func(t *testing.T) {
			t.Parallel()
			assertClashRefused(t, tc)
		})
		t.Run(tc.name+"/own-help", func(t *testing.T) {
			t.Parallel()
			assertDocumentedFlagPasses(t, tc)
		})
	}
}

func assertClashRefused(t *testing.T, tc *probeIdentityCase) {
	t.Helper()
	root := fixtureRoot(t)
	bin := plantStub(t, tc.name, "#!/bin/sh\nprintf '%s\\n' "+shellLiteral(tc.clashHelp)+"\n")
	_, ref := runWithStub(t, root, tc.name, bin)
	if ref == nil || ref.Code != "agent-mismatch" {
		t.Fatalf("ref = %+v, want agent-mismatch", ref)
	}
	if !strings.Contains(ref.Message, tc.match) {
		t.Errorf("message = %q, want the missing match %q", ref.Message, tc.match)
	}
	if _, err := os.Stat(filepath.Join(bundleDir(root), reviewFile)); !os.IsNotExist(err) {
		t.Errorf("review file exists after refusal (err = %v); want no launch", err)
	}
}

func assertDocumentedFlagPasses(t *testing.T, tc *probeIdentityCase) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	bin := plantStub(t, tc.name, "#!/bin/sh\n"+
		"case \" $* \" in\n"+
		"  *' --help '*) printf '%s\\n' "+shellLiteral(tc.ownHelp)+"; exit 0 ;;\n"+
		"esac\n"+
		"printf '%s' "+shellLiteral(reviewDoc)+" > .verdict/state/42/review.json\n")
	_, ref := runWithStub(t, root, tc.name, bin)
	if ref != nil {
		t.Fatalf("launch refused: %v", ref)
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); err != nil {
		t.Fatalf("review file missing after a passing probe: %v", err)
	}
	rec := readLauncher(t, dir)
	probe, ok := jq.AsMap(rec["probe"])
	if !ok || probe["ok"] != true {
		t.Fatalf("launcher probe = %v, want ok=true", rec["probe"])
	}
	args, ok := probe["args"].([]any)
	if !ok || !slices.Equal(args, stringsToAny(tc.probeArgs)) {
		t.Errorf("launcher probe args = %v, want %q", probe["args"], tc.probeArgs)
	}
}

// plantStub writes an executable shell stand-in under a fresh temporary dir.
// It leaves the process PATH alone, so the tests that plant one run in
// parallel; [stubCommander] is what makes the launcher spawn it.
func plantStub(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	settleForks()
	return path
}

// settleForks makes a just-written script safe to exec while other tests run
// in parallel. A fork that began while the file was still open for writing
// holds a copy of that descriptor until its exec, and executing the file in
// that window fails with ETXTBSY ("text file busy") on Linux. The runtime
// holds [syscall.ForkLock] across a spawn until the child has exec'd, so taking
// it once after the close waits out every such child; a fork that starts later
// never saw the descriptor.
func settleForks() {
	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()
}

// stubCommander spawns the stand-in where the launcher asks for the agent by
// bare name. The launcher resolves the agent through its LookPath seam but
// spawns the bare name, which only the process PATH could resolve: and the
// process PATH is shared by every parallel test.
type stubCommander struct {
	inner core.Commander
	name  string
	bin   string
}

// Run swaps the planted agent's bare name for its path, then delegates.
func (s stubCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) > 0 && cmd.Argv[0] == s.name {
		planted := *cmd
		planted.Argv = append([]string{s.bin}, cmd.Argv[1:]...)
		cmd = &planted
	}
	return s.inner.Run(ctx, cmd)
}

// fixtureProbeBudget keeps process fixtures clear of the shipped five-second
// deadline; only TestProbeBudgetIsInjectable exercises a short one.
const fixtureProbeBudget = 30 * time.Second

// runWithStub resolves the shipped row's agent to the stand-in and runs the
// real process commander, so the probe sees the script's own stdout.
func runWithStub(t *testing.T, root, name, bin string) (*core.RunResponse, *core.RefusalError) {
	t.Helper()
	return runWithProbeBudget(t, root, name, bin, fixtureProbeBudget)
}

func runWithProbeBudget(t *testing.T, root, name, bin string, budget time.Duration) (*core.RunResponse, *core.RefusalError) {
	t.Helper()
	req := request(root, stubCommander{inner: coreexec.Watchdog{}, name: name, bin: bin})
	req.Agent = name
	req.LookPath = func(string) (string, error) { return bin, nil }
	req.ProbeTimeout = budget
	return Run(t.Context(), req)
}

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// TestProbeBudgetIsInjectable proves the probe's own deadline is the one that
// produces the timeout classification, and that a test can shorten it.
func TestProbeBudgetIsInjectable(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	bin := plantStub(t, "fx", "#!/bin/sh\n"+
		"case \" $* \" in\n"+
		"  *' --help '*) sleep 1; printf '%s\\n' "+shellLiteral("--ultrafast")+"; exit 0 ;;\n"+
		"esac\n"+
		"printf '%s' "+shellLiteral(reviewDoc)+" > .verdict/state/42/review.json\n")
	_, ref := runWithProbeBudget(t, root, "fx", bin, 50*time.Millisecond)
	if ref == nil || ref.Code != "agent-mismatch" {
		t.Fatalf("ref = %+v, want agent-mismatch", ref)
	}
	if !strings.Contains(ref.Message, "timed out") {
		t.Errorf("message = %q, want the probe timeout classification", ref.Message)
	}
	if _, err := os.Stat(filepath.Join(bundleDir(root), reviewFile)); !os.IsNotExist(err) {
		t.Errorf("review file exists after a timed-out probe (err = %v); want no launch", err)
	}
}

// TestProbeSearchesHeadOfCombinedOutput pins the capture window: help text
// leads, so a match inside the first 64 KiB passes even if the child floods
// afterwards, while a match past that window refuses.
type probeCaptureCase struct {
	name       string
	flood      int
	matchLeads bool
	wantRefuse bool
}

func TestProbeSearchesHeadOfCombinedOutput(t *testing.T) {
	t.Parallel()
	tests := []probeCaptureCase{
		{name: "match leads a flood", flood: probeOutputCap + 4096, matchLeads: true},
		{name: "match fits after 60 KiB", flood: 60 * 1024},
		{name: "match trails a flood", flood: probeOutputCap + 4096, wantRefuse: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertProbeCaptureCase(t, tc)
		})
	}
}

func assertProbeCaptureCase(t *testing.T, tc probeCaptureCase) {
	t.Helper()
	root := fixtureRoot(t)
	bin := plantStub(t, "fx", "#!/bin/sh\n"+
		"case \" $* \" in\n"+
		"  *' --help '*) "+probeFloodHelp(tc)+"exit 0 ;;\n"+
		"esac\n"+
		"printf '%s' "+shellLiteral(reviewDoc)+" > .verdict/state/42/review.json\n")
	_, ref := runWithStub(t, root, "fx", bin)
	_, err := os.Stat(filepath.Join(bundleDir(root), reviewFile))
	assertProbeCaptureOutcome(t, ref, err, tc.wantRefuse)
}

func probeFloodHelp(tc probeCaptureCase) string {
	const flag = "--ultrafast"
	match := "printf '%s\\n' " + shellLiteral(flag) + "\n"
	flood := "printf '%s' " + shellLiteral(strings.Repeat("x", tc.flood)) + "\n"
	if tc.matchLeads {
		return match + flood
	}
	return flood + match
}

func assertProbeCaptureOutcome(t *testing.T, ref *core.RefusalError, err error, wantRefuse bool) {
	t.Helper()
	if wantRefuse {
		assertProbeCaptureRefusal(t, ref, err)
		return
	}
	if ref != nil {
		t.Fatalf("launch refused: %v", ref)
	}
	if err != nil {
		t.Errorf("review file missing after a passing probe: %v", err)
	}
}

func assertProbeCaptureRefusal(t *testing.T, ref *core.RefusalError, err error) {
	t.Helper()
	if ref == nil || ref.Code != "agent-mismatch" {
		t.Fatalf("ref = %+v, want agent-mismatch", ref)
	}
	if !os.IsNotExist(err) {
		t.Errorf("review file exists after refusal (err = %v); want no launch", err)
	}
}
