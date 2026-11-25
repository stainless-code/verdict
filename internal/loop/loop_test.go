package loop

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	loopcore "github.com/stainless-code/verdict/internal/core/loop"
)

// newRunner builds the runner one call renders through.
func newRunner(jsonMode bool) (*cli.Runner, *bytes.Buffer, *bytes.Buffer) {
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	r := &cli.Runner{
		Out: out, Human: out, Diag: diag, Cmd: verb,
		Effective: cli.Defaults(), JSONMode: jsonMode, Now: time.Now,
	}
	return r, out, diag
}

// request is a loop invocation against a scratch checkout with an empty
// HOME, so no operator prefs leak in.
func request(t *testing.T) Request {
	t.Helper()
	home := t.TempDir()
	return Request{
		Env:  map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")},
		Repo: "acme/app", Root: t.TempDir(), RepoRoot: t.TempDir(),
	}
}

func TestParseFlagsSurface(t *testing.T) {
	t.Parallel()
	r, _, _ := newRunner(false)
	f, code, done := parseFlags(r, []string{
		"--agent=codex", "--max-rounds", "5", "--max-cost-usd=2.5", "--budget", "15m",
		"--output", "out", "--fix-prompt=fix.md", "--print", "--sandbox",
	})
	want := flags{
		agent: "codex", maxRounds: "5", maxCost: "2.5", budget: "15m",
		output: "out", fixPrompt: "fix.md", print: true, sandbox: true,
	}
	if done || code != 0 || f != want {
		t.Fatalf("parse = %+v (code %d, done %v), want %+v", f, code, done, want)
	}
	f, _, _ = parseFlags(r, []string{"--agent", "claude"})
	if f.maxRounds != "3" || f.budget != "600s" {
		t.Errorf("defaults = %+v, want 3 rounds and 600s", f)
	}
}

// TestParseFlagsUsage proves every malformed invocation is a usage error (exit
// 2) naming what was wrong, before anything runs.
func TestParseFlagsUsage(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		want string
		args []string
	}{
		"no agent":     {args: nil, want: "--agent needs a value"},
		"lost value":   {args: []string{"--agent", "--print"}, want: "--agent needs a value"},
		"empty inline": {args: []string{"--agent="}, want: "--agent needs a value"},
		"unknown flag": {args: []string{"--agent", "claude", "--rounds", "2"}, want: "unknown flag '--rounds' for loop"},
		"extra operand": {
			args: []string{"--agent", "claude", "42"},
			want: "loop takes one <owner/repo> positional; unexpected '42'",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, _, diag := newRunner(false)
			_, code, done := parseFlags(r, tc.args)
			if !done || code != cli.ExitUsage || !strings.Contains(diag.String(), tc.want) {
				t.Fatalf("code %d done %v diag %q, want usage naming %q", code, done, diag.String(), tc.want)
			}
		})
	}
}

// TestRunRefusesBadLimits proves a well-formed flag with a value no loop can
// honor is a typed bad-flags refusal (exit 1), with nothing written.
func TestRunRefusesBadLimits(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"rounds":     {"--agent", "claude", "--max-rounds", "0"},
		"rounds pad": {"--agent", "claude", "--max-rounds", "03"},
		"budget":     {"--agent", "claude", "--budget", "soon"},
		"cost":       {"--agent", "claude", "--max-cost-usd", "-2"},
		"prompt":     {"--agent", "claude", "--fix-prompt", "/no/such/file.md"},
		"sandboxes":  {"--agent", "claude", "--sandbox", "--sandbox-network-only"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertBadFlags(t, args)
		})
	}
}

// assertBadFlags proves one invocation refuses bad-flags at exit 1 with an
// error envelope and nothing written under the checkout.
func assertBadFlags(t *testing.T, args []string) {
	t.Helper()
	r, out, _ := newRunner(true)
	req := request(t)
	if code := Run(t.Context(), r, args, req); code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil || env.Error.Code != "bad-flags" {
		t.Fatalf("envelope %q (err %v), want error bad-flags", out.String(), err)
	}
	if _, err := os.Stat(filepath.Join(req.RepoRoot, ".verdict")); err == nil {
		t.Error("a refused loop wrote under the checkout")
	}
}

func TestParseBudget(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]time.Duration{"600": 600 * time.Second, "600s": 600 * time.Second, "10m": 10 * time.Minute} {
		if got, ref := parseBudget(raw); ref != nil || got != want {
			t.Errorf("parseBudget(%q) = %v, %v; want %v", raw, got, ref, want)
		}
	}
	for _, raw := range []string{"0", "-5s", "0s", "ten"} {
		if _, ref := parseBudget(raw); ref == nil {
			t.Errorf("parseBudget(%q) accepted a non-positive or unreadable budget", raw)
		}
	}
}

// TestRunPrintRendersThePlan proves --print exits 0, writes nothing, and lists
// every step under its round with the fix prompt the operator supplied.
func TestRunPrintRendersThePlan(t *testing.T) {
	t.Parallel()
	req := request(t)
	prompt := filepath.Join(t.TempDir(), "fix.md")
	if err := os.WriteFile(prompt, []byte("Only fix the security findings."), 0o600); err != nil {
		t.Fatal(err)
	}
	r, out, _ := newRunner(false)
	args := []string{"--agent", "claude", "--max-rounds", "2", "--fix-prompt", prompt, "--print"}
	if code := Run(t.Context(), r, args, req); code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	got := out.String()
	for _, want := range []string{
		"round 1:\n  fetch   verdict fetch-local acme/app loop-",
		"  record  verdict record acme/app local-loop-",
		"  fix     (when the gate fails) claude -p --output-format json 'Only fix the security findings.\n\nRound 1 of at most 2.",
		"  verify  (when the fix declares patch.json verifyCmd) verdict verify acme/app local-loop-",
		"round 2:\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("print output lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "  fix ") != 1 {
		t.Errorf("print output plans %d fixes, want 1 (none after the last round):\n%s", strings.Count(got, "  fix "), got)
	}
	if _, err := os.Stat(filepath.Join(req.RepoRoot, ".verdict")); err == nil {
		t.Error("--print wrote under the checkout")
	}
}

func TestExitFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		data loopcore.Data
		want int
	}{
		{loopcore.Data{Stop: loopcore.StopPass, Passed: true}, cli.ExitOK},
		{loopcore.Data{Stop: loopcore.StopPlanned}, cli.ExitOK},
		{loopcore.Data{Stop: loopcore.StopMaxRounds}, cli.ExitRefused},
		{loopcore.Data{Stop: loopcore.StopAgentFailed, Refusal: &loopcore.Refusal{Code: "agent-failed"}}, cli.ExitRefused},
		{loopcore.Data{Stop: loopcore.StopRefused, Refusal: &loopcore.Refusal{Code: "canceled"}}, cli.ExitBroke},
		// A deadline that canceled a phase is the wall budget, not a ^C.
		{loopcore.Data{Stop: loopcore.StopWallBudget, Refusal: &loopcore.Refusal{Code: "canceled"}}, cli.ExitRefused},
	} {
		if got := exitFor(&tc.data); got != tc.want {
			t.Errorf("exitFor(%s) = %d, want %d", tc.data.Stop, got, tc.want)
		}
	}
}
