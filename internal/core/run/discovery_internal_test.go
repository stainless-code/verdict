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

// TestPassThroughToolFlagSuppressesTheRestriction proves the operator's own
// tool selection wins without duplication: one argv never carries both the
// row's restriction and the flag the operator spelled after `--`.
func TestPassThroughToolFlagSuppressesTheRestriction(t *testing.T) {
	t.Parallel()
	for _, argv := range spawnedArgvs(t, "Read,Write", false) {
		if slices.Contains(argv, discoveryFlag) {
			t.Errorf("pass-through tool flag still produced %q: %q", discoveryFlag, argv)
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

// TestDiscoveryPassThroughSuppressesTheRestriction pins the operator's own tool
// selection: a pass-through spelling of the row's override flags replaces the
// row's restriction instead of duplicating it, in the inline `=` form and the
// short alias alike.
func TestDiscoveryPassThroughSuppressesTheRestriction(t *testing.T) {
	t.Parallel()
	claude, pi, bob := launchRow(t, "claude"), launchRow(t, "pi"), launchRow(t, "bob")
	kept := []string{"--disallowedTools=Bash,PowerShell"}
	t.Run("operator deny list", func(t *testing.T) {
		checkRestriction(t, claude, []string{"--disallowedTools", "WebFetch"}, false, nil)
	})
	t.Run("operator inline deny list", func(t *testing.T) {
		checkRestriction(t, claude, []string{"--disallowedTools=Edit"}, false, nil)
	})
	t.Run("operator allow list", func(t *testing.T) {
		checkRestriction(t, claude, []string{"--allowedTools=Read"}, false, nil)
	})
	t.Run("operator claude tools flag", func(t *testing.T) {
		checkRestriction(t, claude, []string{"--tools", ""}, false, nil)
	})
	t.Run("operator pi short flag", func(t *testing.T) {
		checkRestriction(t, pi, []string{"-t", "read,bash"}, false, nil)
	})
	t.Run("operator bob groups", func(t *testing.T) {
		checkRestriction(t, bob, []string{"--disable-tool-groups", "mcp"}, false, nil)
	})
	t.Run("unrelated pass-through keeps it", func(t *testing.T) {
		checkRestriction(t, claude, []string{"--model", "opus"}, false, kept)
	})
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
		"claude", "-p", "--output-format", "json", discoveryFlag,
		"--model", "opus", redactedValue, redactedValue, promptPlaceholder,
	}
	if shape := resp.Data.Argv; !slices.Equal(shape, want) {
		t.Fatalf("recorded argv = %q, want %q", shape, want)
	}
}
