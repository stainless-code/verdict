package run

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/core"
)

// argvSpy records every argv a run spawns before handing the launch to the
// real fixture commander, so a test reads the exact bytes a child received.
type argvSpy struct {
	inner core.Commander
	argvs [][]string
	mu    sync.Mutex
}

// Run implements core.Commander.
func (s *argvSpy) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	s.mu.Lock()
	s.argvs = append(s.argvs, slices.Clone(cmd.Argv))
	s.mu.Unlock()
	return s.inner.Run(ctx, cmd)
}

// seen is a copy of the spawn log, so a reader never walks a slice a phase may
// still append to.
func (s *argvSpy) seen() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.argvs)
}

// discoveryFlag is the restriction the shipped `claude` row declares; the tests
// below pin it as a literal because it is what the child's argv carries.
const discoveryFlag = "--disallowedTools=Bash,PowerShell"

// TestDiscoveryLaunchesCarryTheRowRestriction proves every launch a two-finder
// run makes — both finders and both cross-checks — carries the row's documented
// read-only flags, ahead of the directive and ahead of the operator's own
// pass-through arguments.
func TestDiscoveryLaunchesCarryTheRowRestriction(t *testing.T) {
	t.Parallel()
	argvs := spawnedArgvs(t, "", false)
	if len(argvs) != 4 {
		t.Fatalf("spawned %d processes, want the two finders and two crosses", len(argvs))
	}
	for _, argv := range argvs {
		assertRestrictedArgv(t, argv)
	}
}

// assertRestrictedArgv fails unless one launch carries the claude row's
// restriction exactly once, before the directive, after the json output a
// two-finder execution requests to measure each spawn's spend.
func assertRestrictedArgv(t *testing.T, argv []string) {
	t.Helper()
	if want := []string{"claude", "-p", "--output-format", "json", discoveryFlag}; !slices.Equal(argv[:5], want) {
		t.Errorf("argv = %q, want the prefix %q", argv, want)
	}
	if n := countArg(argv, discoveryFlag); n != 1 {
		t.Errorf("argv = %q carries the restriction %d times, want exactly one", argv, n)
	}
	if strings.Contains(argv[len(argv)-1], discoveryFlag) {
		t.Errorf("the restriction landed inside the directive: %q", argv[len(argv)-1])
	}
}

// spawnedArgvs runs one two-finder execution over the scripted commander and
// returns every argv the launcher spawned.
func spawnedArgvs(t *testing.T, passthrough string, allowShell bool) [][]string {
	t.Helper()
	req, phases := coordinatorFixture(t)
	req.FindersRaw = "2"
	req.AllowShell = allowShell
	if passthrough != "" {
		req.PassThrough = []string{"--allowedTools", passthrough}
	}
	spy := &argvSpy{inner: phases}
	req.Commander = spy
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("run refused: %v", ref)
	}
	return spy.seen()
}

// TestAllowShellOmitsTheRestriction proves --allow-shell restores the row's own
// tool set: no launch carries the flag, and the argv is the one the launcher
// composed before the restriction existed.
func TestAllowShellOmitsTheRestriction(t *testing.T) {
	t.Parallel()
	for _, argv := range spawnedArgvs(t, "", true) {
		if want := []string{"claude", "-p"}; !slices.Equal(argv[:2], want) {
			t.Errorf("argv = %q, want the prefix %q", argv, want)
		}
		if slices.Contains(argv, discoveryFlag) {
			t.Errorf("--allow-shell argv still carries %q: %q", discoveryFlag, argv)
		}
	}
}

// TestPassThroughAllowListKeepsTheRestriction proves an operator's additive
// allow list sits beside the shell denial instead of dropping it: Claude Code
// merges allow and deny lists, so dropping the denial would hand the shell to
// any Bash(...) allow rule the operator's settings carry.
func TestPassThroughAllowListKeepsTheRestriction(t *testing.T) {
	t.Parallel()
	for _, argv := range spawnedArgvs(t, "Read,Write", false) {
		if n := countArg(argv, discoveryFlag); n != 1 {
			t.Errorf("argv = %q carries the restriction %d times, want once", argv, n)
		}
		if n := countArg(argv, "--allowedTools"); n != 1 {
			t.Errorf("argv = %q carries --allowedTools %d times, want once", argv, n)
		}
	}
}

// countArg counts the entries of argv equal to want.
func countArg(argv []string, want string) int {
	count := 0
	for _, arg := range argv {
		if arg == want {
			count++
		}
	}
	return count
}

// TestDiscoveryRestrictionResolution pins the resolver: the row's declared
// flags by default, nothing for a row that declares none, and nothing after
// --allow-shell.
func TestDiscoveryRestrictionResolution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		row  string
		want []string
	}{
		{name: "claude default", row: "claude", want: []string{"--disallowedTools=Bash,PowerShell"}},
		{name: "bob default", row: "bob", want: []string{"--disable-tool-groups", "execute,mcp"}},
		{name: "pi default", row: "pi", want: []string{"--exclude-tools", "bash,powershell"}},
		{name: "row with no documented flag", row: "codex"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkRestriction(t, launchRow(t, tc.row), nil, false, tc.want)
		})
	}
	checkRestriction(t, launchRow(t, "claude"), nil, true, nil)
}

// TestDiscoveryRestrictionSurvivesEveryOperatorFlag pins the unconditional
// shell denial: no operator flag after `--` drops it — not an allow or deny
// list, not a whole tool set (claude --tools, pi --tools/-t/--no-tools), not
// pi's additive `--tools +codemode` — only --allow-shell does. An operator
// spelling of the restriction's own denylist flag carries the merged denial
// instead, so the restriction is not repeated beside it.
func TestDiscoveryRestrictionSurvivesEveryOperatorFlag(t *testing.T) {
	t.Parallel()
	claude, pi, bob := launchRow(t, "claude"), launchRow(t, "pi"), launchRow(t, "bob")
	keptClaude := []string{"--disallowedTools=Bash,PowerShell"}
	keptPi := []string{"--exclude-tools", "bash,powershell"}
	for _, tc := range []struct {
		def  *agentcatalog.Launch
		pass []string
		want []string
	}{
		{def: claude, pass: []string{"--disallowedTools", "WebFetch"}, want: keptClaude},
		{def: claude, pass: []string{"--allowedTools=mcp__x"}, want: keptClaude},
		{def: claude, pass: []string{"--tools", ""}, want: keptClaude},
		{def: claude, pass: []string{"--model", "opus", "--tools=Bash,Edit"}, want: keptClaude},
		{def: pi, pass: []string{"-t", "read,bash"}, want: keptPi},
		{def: pi, pass: []string{"--no-tools"}, want: keptPi},
		{def: pi, pass: []string{"--no-builtin-tools"}, want: keptPi},
		{def: pi, pass: []string{"--tools", "+codemode"}, want: keptPi},
		{def: pi, pass: []string{"--tools", "-write"}, want: keptPi},
		{def: bob, pass: merged(t, bob, "--disable-tool-groups", "mcp")},
	} {
		checkRestriction(t, tc.def, tc.pass, false, tc.want)
	}
	checkRestriction(t, claude, []string{"--tools", "Bash"}, true, nil)
}

// merged is mergeDenials over pass, failing on a refusal.
func merged(t *testing.T, def *agentcatalog.Launch, pass ...string) []string {
	t.Helper()
	got, bad := mergeDenials(def, pass, false)
	if bad != "" {
		t.Fatalf("mergeDenials(%q) refused %q", pass, bad)
	}
	return got
}

// TestMergeDenialsKeepsTheShellDenial pins the additive denylist: the
// operator's own spelling of the restriction's flag carries the restriction's
// value too, in the spaced, inline, and short forms, every position kept; an
// operand that only looks like the flag, --allow-shell, and a row without a
// mergeable restriction are left as they are.
func TestMergeDenialsKeepsTheShellDenial(t *testing.T) {
	t.Parallel()
	pi, bob, claude := launchRow(t, "pi"), launchRow(t, "bob"), launchRow(t, "claude")
	for _, tc := range []struct {
		def        *agentcatalog.Launch
		pass, want []string
		allowShell bool
	}{
		{def: pi, pass: []string{"--exclude-tools", "markdown"}, want: []string{"--exclude-tools", "bash,powershell,markdown"}},
		{def: pi, pass: []string{"--model", "m", "-xt=grep"}, want: []string{"--model", "m", "-xt=bash,powershell,grep"}},
		{
			def: pi, pass: []string{"-xt", "a", "--exclude-tools", "b"},
			want: []string{"-xt", "bash,powershell,a", "--exclude-tools", "bash,powershell,b"},
		},
		{def: bob, pass: []string{"--disable-tool-groups", "mcp"}, want: []string{"--disable-tool-groups", "execute,mcp,mcp"}},
		{def: pi, pass: []string{"--exclude-tools", "markdown"}, want: []string{"--exclude-tools", "markdown"}, allowShell: true},
		{def: claude, pass: []string{"--disallowedTools", "WebFetch"}, want: []string{"--disallowedTools", "WebFetch"}},
	} {
		if got := merged(t, tc.def, tc.pass...); tc.allowShell {
			if got, _ := mergeDenials(tc.def, tc.pass, true); !slices.Equal(got, tc.want) {
				t.Errorf("%s %q with --allow-shell = %q, want %q", tc.def.Name, tc.pass, got, tc.want)
			}
		} else if !slices.Equal(got, tc.want) {
			t.Errorf("%s %q = %q, want %q", tc.def.Name, tc.pass, got, tc.want)
		}
	}
}

// TestMergeDenialsRefusesAFlagWithNoValue pins the usage error: the
// restriction's own denylist flag takes a value, so a spelling without one —
// dangling at the end, followed by another option, repeated bare, empty
// inline, or in a position another option could claim — refuses rather than
// dropping or misplacing the denial.
func TestMergeDenialsRefusesAFlagWithNoValue(t *testing.T) {
	t.Parallel()
	pi, bob := launchRow(t, "pi"), launchRow(t, "bob")
	for _, tc := range []struct {
		def  *agentcatalog.Launch
		bad  string
		pass []string
	}{
		{def: pi, pass: []string{"--exclude-tools", "--model", "m"}, bad: "--exclude-tools"},
		{def: pi, pass: []string{"--model", "m", "--exclude-tools"}, bad: "--exclude-tools"},
		{def: pi, pass: []string{"--exclude-tools", "--exclude-tools", "x"}, bad: "--exclude-tools"},
		{def: pi, pass: []string{"-xt="}, bad: "-xt="},
		{def: pi, pass: []string{"--append-system-prompt", "--exclude-tools"}, bad: "--exclude-tools"},
		{def: bob, pass: []string{"--disable-tool-groups", "--model", "m"}, bad: "--disable-tool-groups"},
	} {
		if got, bad := mergeDenials(tc.def, tc.pass, false); bad != tc.bad || !slices.Equal(got, tc.pass) {
			t.Errorf("%s %q = %q, bad %q; want unchanged, bad %q", tc.def.Name, tc.pass, got, bad, tc.bad)
		}
	}
}

// TestRunRefusesADanglingDenylistFlag proves the launch end to end: pi with a
// bare --exclude-tools refuses bad-flags before any agent starts.
func TestRunRefusesADanglingDenylistFlag(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cmdr := &scripted{}
	req := request(root, cmdr)
	req.Agent, req.PassThrough = "pi", []string{"--exclude-tools", "--model", "m"}
	if _, ref := Run(t.Context(), req); ref == nil || ref.Code != codeBadFlags {
		t.Fatalf("ref = %v, want bad-flags", ref)
	}
	if cmdr.calls != 0 {
		t.Errorf("spawns = %d, want none", cmdr.calls)
	}
}

// TestToolSetLaunchesKeepTheShellDenial proves the denial end to end on the
// operator spellings that used to drop it: claude's --tools and pi's additive
// --tools forms launch with the row's restriction still in argv.
func TestToolSetLaunchesKeepTheShellDenial(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		agent string
		pass  []string
		deny  []string
	}{
		{agent: "claude", pass: []string{"--tools", "Bash,Edit"}, deny: []string{discoveryFlag}},
		{agent: "pi", pass: []string{"--tools", "+codemode"}, deny: []string{"--exclude-tools", "bash,powershell"}},
		{agent: "pi", pass: []string{"--tools", "-write"}, deny: []string{"--exclude-tools", "bash,powershell"}},
	} {
		root := fixtureRoot(t)
		dir := bundleDir(root)
		cmdr := &scripted{write: func() { writeReview(t, dir) }}
		req := request(root, cmdr)
		req.Agent, req.PassThrough = tc.agent, tc.pass
		if _, ref := Run(t.Context(), req); ref != nil {
			t.Fatalf("%s %q refused: %v", tc.agent, tc.pass, ref)
		}
		if argv := cmdr.cmd.Argv; !containsRun(argv, tc.deny) {
			t.Errorf("%s %q argv = %q, want the denial %q", tc.agent, tc.pass, argv[:len(argv)-1], tc.deny)
		}
	}
}

// containsRun reports whether want appears in argv as consecutive elements.
func containsRun(argv, want []string) bool {
	for i := 0; i+len(want) <= len(argv); i++ {
		if slices.Equal(argv[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// TestPiDenylistLaunchKeepsTheShellDenial proves the launch end to end: pi
// with the operator's own --exclude-tools still excludes bash and powershell,
// once, at the operator's position.
func TestPiDenylistLaunchKeepsTheShellDenial(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, cmdr)
	req.Agent = "pi"
	req.PassThrough = []string{"--exclude-tools", "markdown"}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("run refused: %v", ref)
	}
	argv := cmdr.cmd.Argv
	if want := []string{"pi", "-p", "--exclude-tools", "bash,powershell,markdown"}; !slices.Equal(argv[:4], want) {
		t.Errorf("argv = %q, want the prefix %q", argv[:len(argv)-1], want)
	}
	if n := countArg(argv, "--exclude-tools"); n != 1 {
		t.Errorf("argv carries --exclude-tools %d times, want once", n)
	}
}

// launchRow resolves one shipped launch definition, failing on a name the
// catalog lost.
func launchRow(t *testing.T, name string) *agentcatalog.Launch {
	t.Helper()
	row, ok := agentcatalog.Lookup(name)
	if !ok || row.Launch == nil {
		t.Fatalf("catalog lost %s", name)
	}
	return row.Launch
}

// checkRestriction compares one resolver call with its literal expectation.
func checkRestriction(t *testing.T, def *agentcatalog.Launch, passthrough []string, allowShell bool, want []string) {
	t.Helper()
	if got := discoveryRestriction(def, passthrough, allowShell); !slices.Equal(got, want) {
		t.Fatalf("restriction = %q, want %q", got, want)
	}
}

// TestDrainLaunchCarriesTheRestriction proves the drain drafting launch takes
// the same read-only argv: it writes drain.replies.json, which the restriction
// leaves alone, and the artifact name still points at that file.
func TestDrainLaunchCarriesTheRestriction(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := drainState(root, "7", "abc123")
	st.req.Now = time.Now
	args, ref := st.argsFor(launchRow(t, "claude"), filepath.Join(root, ".verdict", "state", "7"), 30)
	if ref != nil {
		t.Fatalf("drain args refused: %v", ref)
	}
	if want := []string{"claude", "-p", discoveryFlag}; !slices.Equal(args.argv[:3], want) {
		t.Errorf("drain argv = %q, want the prefix %q", args.argv, want)
	}
	if filepath.Base(args.abs) != drainFile {
		t.Errorf("drain artifact = %q, want %s", args.abs, drainFile)
	}
}

// TestRecordedArgvRedactsAfterTheRestriction proves the provenance record's
// pass-through numbering counts the discovery restriction: the redacted
// operands stay where the child saw them, and the restriction stays visible.
func TestRecordedArgvRedactsAfterTheRestriction(t *testing.T) {
	t.Parallel()
	req, phases := coordinatorFixture(t)
	req.FindersRaw = "2"
	req.PassThrough = []string{"--model", "opus", "--api-key", "sk-ant-0123456789abcdef"}
	req.Environ = []string{"ANTHROPIC_API_KEY=sk-ant-0123456789abcdef"}
	spy := &argvSpy{inner: phases}
	req.Commander = spy
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("run refused: %v", ref)
	}
	want := []string{
		"claude", "-p", "--output-format", "json", discoveryFlag, "--allowedTools=Edit(./.verdict/state/42/x-cross-2/**)",
		"--model", "opus", redactedValue, redactedValue, promptPlaceholder,
	}
	if shape := resp.Data.Argv; !slices.Equal(shape, want) {
		t.Fatalf("recorded argv = %q, want %q", shape, want)
	}
}
