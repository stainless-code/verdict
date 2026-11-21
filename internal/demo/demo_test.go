package demo

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// newRunner builds the runner a call renders through, with the JSON mode the
// test asked for.
func newRunner(t *testing.T, jsonMode bool) (*cli.Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	r := &cli.Runner{
		Out: out, Human: out, Diag: diag, Cmd: verb,
		Effective: cli.Defaults(), JSONMode: jsonMode, Now: time.Now,
	}
	return r, out, diag
}

// TestParseFlagsSurface proves the flag surface: one optional positional
// anywhere before `--`, --agent in both spellings, --keep, and a separator that
// ends flag parsing.
func TestParseFlagsSurface(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want flags
	}{
		{name: "no arguments", args: nil, want: flags{}},
		{
			name: "url only", args: []string{"https://github.com/acme/app/pull/7"},
			want: flags{target: "https://github.com/acme/app/pull/7"},
		},
		{
			name: "flags before the url", args: []string{"--keep", "--agent", "claude", "https://github.com/acme/app/pull/7"},
			want: flags{target: "https://github.com/acme/app/pull/7", agent: "claude", keep: true},
		},
		{
			name: "flags after the url", args: []string{"https://github.com/acme/app/pull/7", "--agent", "claude", "--keep"},
			want: flags{target: "https://github.com/acme/app/pull/7", agent: "claude", keep: true},
		},
		{
			name: "inline agent", args: []string{"--agent=codex"},
			want: flags{agent: "codex"},
		},
		{
			name: "separator ends flags", args: []string{"--", "--keep"},
			want: flags{target: "--keep"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertParsedFlags(t, tc.args, tc.want)
		})
	}
}

// assertParsedFlags proves one argument list parses to the expected surface.
func assertParsedFlags(t *testing.T, args []string, want flags) {
	t.Helper()
	r, _, _ := newRunner(t, false)
	got, code, done := parseFlags(r, args)
	if done {
		t.Fatalf("parseFlags(%q) reported usage at exit %d", args, code)
	}
	if got != want {
		t.Errorf("parseFlags(%q) = %+v, want %+v", args, got, want)
	}
}

// TestParseFlagsUsage proves the usage refusals: an unknown flag, a flag
// missing its operand, and a second positional all exit 2.
func TestParseFlagsUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range usageRefusals() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertUsageRefusal(t, tc.args, tc.want)
		})
	}
}

// usageRefusal is one argument list that must exit 2, with the text the usage
// error has to carry.
type usageRefusal struct {
	name string
	want string
	args []string
}

// usageRefusals are the three usage refusals the surface owns.
func usageRefusals() []usageRefusal {
	return []usageRefusal{
		{name: "unknown flag", args: []string{"--bogus"}, want: "unknown flag '--bogus' for demo"},
		{name: "missing agent value", args: []string{"--agent"}, want: "--agent"},
		{
			name: "empty inline agent",
			args: []string{"--agent="},
			want: "flag '--agent' needs a value",
		},
		{
			name: "empty separated agent",
			args: []string{"--agent", ""},
			want: "flag '--agent' needs a value",
		},
		{
			name: "two positionals",
			args: []string{"https://github.com/acme/app/pull/7", "https://github.com/acme/app/pull/8"},
			want: "one <public-github-pr-url> positional",
		},
	}
}

// assertUsageRefusal proves one argument list exits 2 with the expected usage
// text on the diagnostic stream.
func assertUsageRefusal(t *testing.T, args []string, want string) {
	t.Helper()
	r, _, diag := newRunner(t, false)
	_, code, done := parseFlags(r, args)
	if !done || code != cli.ExitUsage {
		t.Fatalf("parseFlags(%q) = code %d, done %v; want exit %d", args, code, done, cli.ExitUsage)
	}
	if !strings.Contains(diag.String(), want) {
		t.Errorf("stderr = %q, want it to carry %q", diag.String(), want)
	}
}

// TestRunRefusesBadTargetAsOneEnvelope proves the adapter wiring end to end: a
// non-GitHub positional reaches the service, which refuses typed, and --json
// prints exactly one envelope carrying that code on stdout with the message on
// stdout too (machine mode keeps the human line out of stderr).
func TestRunRefusesBadTargetAsOneEnvelope(t *testing.T) {
	t.Parallel()
	r, out, _ := newRunner(t, true)
	code := Run(t.Context(), r, []string{"https://gitlab.com/acme/app/pull/7"}, Request{Env: map[string]string{}})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, cli.ExitRefused, r.Diag)
	}
	if n := strings.Count(strings.TrimSpace(out.String()), "\n"); n != 0 && !strings.HasPrefix(out.String(), "{") {
		t.Errorf("stdout is not a single envelope: %q", out.String())
	}
	for _, want := range []string{`"verb": "demo"`, `"code": "bad-target"`, `"data": null`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("envelope is missing %s:\n%s", want, out.String())
		}
	}
	if strings.Count(strings.TrimSpace(out.String()), "schema_version") != 1 {
		t.Errorf("stdout carries more than one envelope:\n%s", out.String())
	}
}

// TestRunRefusesBadTargetHuman proves the human rendering of the same refusal:
// one `error:` line on stderr and nothing on stdout.
func TestRunRefusesBadTargetHuman(t *testing.T) {
	t.Parallel()
	r, out, diag := newRunner(t, false)
	code := Run(t.Context(), r, []string{"not-a-url"}, Request{Env: map[string]string{}})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want nothing before a refusal", out.String())
	}
	if !strings.Contains(diag.String(), "error: demo takes a public GitHub pull request URL") {
		t.Errorf("stderr = %q, want the typed refusal line", diag.String())
	}
}

// TestRefuseMissingToolLine proves the frozen missing-binary line: the message
// is printed alone (no hint, no envelope) and the run exits 1.
func TestRefuseMissingToolLine(t *testing.T) {
	t.Parallel()
	r, out, diag := newRunner(t, false)
	code := refuse(r, &core.RefusalError{Code: codeMissingTool, Message: "missing: curl"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if got := strings.TrimSpace(diag.String()); got != "missing: curl" {
		t.Errorf("stderr = %q, want the frozen line only", got)
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
}

// TestRefuseMissingToolJSONEnvelope proves machine mode renders the same
// failure through the envelope on stdout: a script parsing --json output must
// not lose the one refusal that has no human hint line.
func TestRefuseMissingToolJSONEnvelope(t *testing.T) {
	t.Parallel()
	r, out, diag := newRunner(t, true)
	code := refuse(r, &core.RefusalError{Code: codeMissingTool, Message: "missing: curl"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	if diag.String() != "" {
		t.Errorf("stderr = %q, want the envelope on stdout only", diag.String())
	}
	env, err := jq.Decode(out.Bytes())
	if err != nil {
		t.Fatalf("decode stdout %q: %v", out.String(), err)
	}
	m, ok := jq.AsMap(env)
	if !ok {
		t.Fatalf("stdout = %q, want an envelope object", out.String())
	}
	errObj, ok := jq.AsMap(m["error"])
	if !ok {
		t.Fatalf("envelope error = %v, want an object", m["error"])
	}
	if got := jq.StrOf(errObj["code"]); got != codeMissingTool {
		t.Errorf("error code = %q, want %q", got, codeMissingTool)
	}
	if msg := jq.StrOf(errObj["message"]); !strings.HasPrefix(msg, "missing: curl") {
		t.Errorf("error message = %q, want it to start with %q", msg, "missing: curl")
	}
}

// TestRentry proves the re-entry line names the invocation: the URL when there
// is one, the agent, and a placeholder when the agent was auto-detected.
func TestRentry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		target string
		agent  string
		want   string
	}{
		{
			name: "url and agent", target: "https://github.com/acme/app/pull/7", agent: "claude",
			want: "verdict demo https://github.com/acme/app/pull/7 --agent claude",
		},
		{name: "local and auto", want: "verdict demo --agent <name>"},
	}
	for _, tc := range cases {
		if got := rentry(tc.target, tc.agent); got != tc.want {
			t.Errorf("%s: rentry = %q, want %q", tc.name, got, tc.want)
		}
	}
}
