package democore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/testkit"
)

// errUnexpectedArgv is what the scripted commander returns for an argv no test
// registered: the composed services map it to their own refusal, which is
// enough for every leg here, and it keeps an unscripted call visible in the
// recorded argv list rather than failing the test from a helper.
var errUnexpectedArgv = errors.New("unexpected argv")

// scriptCmd answers registered argv keys (joined with NUL) and reports
// unregistered ones as a plain error.
type scriptCmd struct {
	t       *testing.T
	results map[string]core.Result
	look    func(string) (string, error)
	calls   [][]string
	// pinned is the host each call ran pinned to (core.HostOf).
	pinned []string
}

// Run records the argv and replays the registered result.
func (s *scriptCmd) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	s.t.Helper()
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	argv := slices.Clone(cmd.Argv)
	if argv[0] == "git" {
		root, args, ok := gitcmd.Split(argv)
		if !ok || root != "." || cmd.Env == nil {
			s.t.Errorf("git call %q is not hook-free in the working directory", argv)
		}
		argv = slices.Concat([]string{"git"}, args)
	}
	s.calls = append(s.calls, argv)
	s.pinned = append(s.pinned, core.HostOf(ctx))
	res, ok := s.results[strings.Join(argv, "\x00")]
	if !ok {
		return core.Result{}, errUnexpectedArgv
	}
	return res, nil
}

// LookPath answers the scripted installed set.
func (s *scriptCmd) LookPath(tool string) error {
	if s.look == nil {
		return nil
	}
	if _, err := s.look(tool); err != nil {
		return &core.MissingToolError{Tool: tool}
	}
	return nil
}

// lookTool is the PATH probe a test injects: the listed binaries resolve.
func lookTool(installed ...string) func(string) (string, error) {
	return func(bin string) (string, error) {
		if slices.Contains(installed, bin) {
			return "/usr/local/bin/" + bin, nil
		}
		return "", fmt.Errorf("%s: not found", bin)
	}
}

// failResult is one command that failed, with the stderr a host would print.
func failResult(stderr string) core.Result {
	return core.Result{Stderr: stderr, ExitCode: 1}
}

// demoRequest is one scripted invocation: a target, an agent, and the two
// injected seams.
type demoEnv struct {
	req     *Request
	cmdr    *scriptCmd
	agent   *scriptCmd
	collect *testkit.Collector
	tmp     string
}

// newDemoEnv builds a demo request whose temp parent and clock are test-owned.
func newDemoEnv(t *testing.T, target, agent string, installed ...string) *demoEnv {
	t.Helper()
	tmp := t.TempDir()
	cmdr := &scriptCmd{t: t, results: map[string]core.Result{}, look: lookTool(installed...)}
	c := &testkit.Collector{}
	root := tmp
	read := core.ReadRequest{
		Runtime: &core.Runtime{Env: map[string]string{}, Now: testkit.FixedNow, Progress: c},
		Root:    &root,
	}
	return &demoEnv{
		tmp:     tmp,
		cmdr:    cmdr,
		agent:   &scriptCmd{t: t, results: map[string]core.Result{}, look: lookTool(installed...)},
		collect: c,
		req: &Request{
			ReadRequest:    read,
			Target:         target,
			Agent:          agent,
			TmpRoot:        tmp,
			LookPath:       lookTool(installed...),
			Commander:      cmdr,
			AgentCommander: &scriptCmd{t: t, results: map[string]core.Result{}},
			Environ:        []string{"PATH=/usr/bin"},
		},
	}
}

// tempDirs lists the disposable directories a run left behind.
func (e *demoEnv) tempDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.tmp)
	if err != nil {
		t.Fatalf("read temp root: %v", err)
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "verdict-demo-") {
			out = append(out, entry.Name())
		}
	}
	return out
}

// TestRunRefusesBadTargetBeforeAnyWork proves the URL gate runs first: a
// non-GitHub URL is typed bad-target with no host call, no temp directory, and
// no agent chosen.
func TestRunRefusesBadTargetBeforeAnyWork(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://gitlab.com/acme/app/pull/7", "", "claude")
	_, ref := Run(t.Context(), env.req)
	if ref == nil || ref.Code != codeBadTarget {
		t.Fatalf("ref = %v, want %s", ref, codeBadTarget)
	}
	if len(env.cmdr.calls) != 0 {
		t.Errorf("host calls = %q, want none", env.cmdr.calls)
	}
	if dirs := env.tempDirs(t); len(dirs) != 0 {
		t.Errorf("temp dirs = %q, want none", dirs)
	}
}

// TestRunRefusesWithoutAnyAgent proves the auto-detect refusal: code
// agent-missing, the two-line setup guide, and no directory left behind.
func TestRunRefusesWithoutAnyAgent(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "")
	_, ref := Run(t.Context(), env.req)
	if ref == nil || ref.Code != codeAgentMissing {
		t.Fatalf("ref = %v, want %s", ref, codeAgentMissing)
	}
	if lines := strings.Split(ref.Message, "\n"); len(lines) != 2 {
		t.Errorf("message = %q, want two lines", ref.Message)
	}
	if dirs := env.tempDirs(t); len(dirs) != 0 {
		t.Errorf("temp dirs = %q, want none", dirs)
	}
}

// TestRunUsesGhWhenSignedIn proves the transport probe: an authenticated gh is
// asked first and the fetch then reads with gh, with no anonymous warning.
func TestRunUsesGhWhenSignedIn(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude", "gh")
	env.cmdr.results["gh\x00auth\x00status\x00--hostname\x00github.com\x00--active\x00--no-refresh"] = core.Result{}
	_, ref := Run(t.Context(), env.req)
	if ref == nil {
		t.Fatal("run succeeded against an unscripted fetch; the leg grades the probe order")
	}
	assertFirstCalls(t, env.cmdr.calls,
		[]string{"gh", "auth", "status", "--hostname", "github.com", "--active", "--no-refresh"},
		[]string{"gh", "pr", "view"})
	if slices.Contains(env.collect.Warnings, msgGhAnon) {
		t.Errorf("warnings = %q, want no anonymous-transport warning", env.collect.Warnings)
	}
}

// TestRunPinsHostCallsToGitHub proves a URL demo's gh calls run pinned to
// github.com, so the production runner names it on every one and GH_HOST
// cannot send the read elsewhere; the login ladder already names the host.
func TestRunPinsHostCallsToGitHub(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude", "gh")
	env.cmdr.results["gh\x00auth\x00status\x00--hostname\x00github.com\x00--active\x00--no-refresh"] = core.Result{}
	if _, ref := Run(t.Context(), env.req); ref == nil {
		t.Fatal("run succeeded against an unscripted fetch")
	}
	if len(env.cmdr.pinned) < 2 {
		t.Fatalf("calls = %q, want the login probe and the fetch", env.cmdr.calls)
	}
	for i, host := range env.cmdr.pinned {
		if host != "github.com" {
			t.Errorf("%q ran pinned to %q, want github.com", env.cmdr.calls[i], host)
		}
		if _, ref := core.PinHostArgv(env.cmdr.calls[i], host); ref != nil {
			t.Errorf("the runner would refuse %q: %v", env.cmdr.calls[i], ref)
		}
	}
}

// TestRunFallsBackToPublicAPI proves the unauthenticated path: gh is installed
// but not signed in, so the demo says so once and reads the anonymous API.
func TestRunFallsBackToPublicAPI(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude", "gh", "curl")
	env.cmdr.results["gh\x00auth\x00status\x00--hostname\x00github.com\x00--active\x00--no-refresh"] = failResult("not logged in")
	_, ref := Run(t.Context(), env.req)
	if ref == nil {
		t.Fatal("run succeeded against an unscripted fetch")
	}
	if !slices.Contains(env.collect.Warnings, msgGhAnon) {
		t.Fatalf("warnings = %q, want %q", env.collect.Warnings, msgGhAnon)
	}
	assertFirstCalls(t, env.cmdr.calls,
		[]string{"gh", "auth", "status", "--hostname", "github.com", "--active", "--no-refresh"},
		[]string{"curl", "--disable", "-sSfL", "https://api.github.com/repos/acme/app/pulls/7"})
}

// assertFirstCalls proves the first two argv fired, in order: the probe, then
// the transport's own first read.
func assertFirstCalls(t *testing.T, calls [][]string, want ...[]string) {
	t.Helper()
	if len(calls) < len(want) {
		t.Fatalf("calls = %q, want at least %d", calls, len(want))
	}
	for i, prefix := range want {
		if !slices.Equal(calls[i][:len(prefix)], prefix) {
			t.Errorf("call %d = %q, want prefix %q", i, calls[i], prefix)
		}
	}
}

// TestRunRemovesTempDirOnRefusal proves the cleanup contract on the failure
// path: the disposable directory is created and gone again, --keep keeps it,
// and the kept path is named in a warning.
func TestRunRemovesTempDirOnRefusal(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude", "gh", "curl")
	env.cmdr.results["gh\x00auth\x00status\x00--hostname\x00github.com\x00--active\x00--no-refresh"] = failResult("not logged in")
	if _, ref := Run(t.Context(), env.req); ref == nil {
		t.Fatal("run succeeded against an unscripted fetch")
	}
	if dirs := env.tempDirs(t); len(dirs) != 0 {
		t.Errorf("temp dirs = %q, want none after a refusal", dirs)
	}
	kept := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude", "gh", "curl")
	kept.req.Keep = true
	kept.cmdr.results["gh\x00auth\x00status\x00--hostname\x00github.com\x00--active\x00--no-refresh"] = failResult("not logged in")
	if _, ref := Run(t.Context(), kept.req); ref == nil {
		t.Fatal("kept run succeeded against an unscripted fetch")
	}
	keptDirs := kept.tempDirs(t)
	if len(keptDirs) != 1 {
		t.Fatalf("temp dirs = %q, want exactly one kept", keptDirs)
	}
	// The kept path names the resolved directory: the run resolves its temp
	// root through symlinks before writing anything.
	physical, err := filepath.EvalSymlinks(kept.tmp)
	if err != nil {
		t.Fatalf("resolve the test temp root: %v", err)
	}
	assertWarningNamed(t, kept.req.Progress, keptMessage+filepath.Join(physical, keptDirs[0]))
}

// assertWarningNamed proves one exact warning reached the progress observer.
func assertWarningNamed(t *testing.T, progress core.Progress, want string) {
	t.Helper()
	collector, ok := progress.(*testkit.Collector)
	if !ok {
		t.Fatalf("progress = %T, want the test collector", progress)
	}
	if !slices.Contains(collector.Warnings, want) {
		t.Errorf("warnings = %q, want %q", collector.Warnings, want)
	}
}

// TestRunLocalNeedsACheckout proves the local form's own refusal: outside a
// repository the message names both invocation shapes.
func TestRunLocalNeedsACheckout(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "", "", "claude")
	_, ref := Run(t.Context(), env.req)
	if ref == nil || ref.Code != codeNotARepo {
		t.Fatalf("ref = %v, want %s", ref, codeNotARepo)
	}
	for _, want := range []string{"public pull request URL", "inside a git repository"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("message = %q, want it to name %q", ref.Message, want)
		}
	}
	if dirs := env.tempDirs(t); len(dirs) != 0 {
		t.Errorf("temp dirs = %q, want none", dirs)
	}
}

// TestLocalModeSelection proves the diff a local run reads: the working tree
// when it holds tracked changes, else the last commit, and a typed refusal when
// HEAD has no parent.
func TestLocalModeSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		wantCode string
		results  map[string]core.Result
		want     fetchcore.LocalFlags
	}{
		{
			name:    "dirty tree reads the worktree",
			results: map[string]core.Result{"git\x00-c\x00diff.autoRefreshIndex=false\x00diff\x00--quiet\x00HEAD": {ExitCode: 1}},
			want:    fetchcore.LocalFlags{},
		},
		{
			name: "clean tree reads the last commit",
			results: map[string]core.Result{
				"git\x00-c\x00diff.autoRefreshIndex=false\x00diff\x00--quiet\x00HEAD": {},
				"git\x00rev-parse\x00--verify\x00HEAD~1^{commit}":                     {},
			},
			want: fetchcore.LocalFlags{RangeSpec: parentsRange},
		},
		{
			name: "clean root commit has nothing to review",
			results: map[string]core.Result{
				"git\x00-c\x00diff.autoRefreshIndex=false\x00diff\x00--quiet\x00HEAD": {},
				"git\x00rev-parse\x00--verify\x00HEAD~1^{commit}":                     {ExitCode: 1},
			},
			wantCode: codeBadRange,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertLocalMode(t, tc.results, tc.want.RangeSpec, tc.wantCode)
		})
	}
}

// assertLocalMode proves one scripted git state resolves to the expected diff
// mode, or to the expected refusal code.
func assertLocalMode(t *testing.T, results map[string]core.Result, wantRange, wantCode string) {
	t.Helper()
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.Checkout = t.TempDir()
	env.cmdr.results = results
	flags, ref := localMode(t.Context(), env.req)
	if wantCode != "" {
		if ref == nil || ref.Code != wantCode {
			t.Fatalf("ref = %v, want %s", ref, wantCode)
		}
		return
	}
	if ref != nil {
		t.Fatalf("localMode refused: %v", ref)
	}
	if flags.RangeSpec != wantRange {
		t.Errorf("RangeSpec = %q, want %q", flags.RangeSpec, wantRange)
	}
}

// TestLocalModeWarnsAboutUntrackedFiles proves the one change the commit range
// cannot see is named: a clean tracked tree holding untracked files reads the
// last commit and says the untracked files were left out, while a tree without
// them stays silent.
func TestLocalModeWarnsAboutUntrackedFiles(t *testing.T) {
	t.Parallel()
	clean := map[string]core.Result{
		"git\x00-c\x00diff.autoRefreshIndex=false\x00diff\x00--quiet\x00HEAD": {},
		"git\x00rev-parse\x00--verify\x00HEAD~1^{commit}":                     {},
		"git\x00ls-files\x00--others\x00--exclude-standard":                   {Stdout: "draft.md\n"},
	}
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.Checkout = t.TempDir()
	env.cmdr.results = clean
	if _, ref := localMode(t.Context(), env.req); ref != nil {
		t.Fatalf("localMode refused: %v", ref)
	}
	assertWarningNamed(t, env.req.Progress, msgUntrackedExcluded)

	quiet := newDemoEnv(t, "", "claude", "claude")
	quiet.req.Checkout = t.TempDir()
	quiet.cmdr.results = map[string]core.Result{
		"git\x00-c\x00diff.autoRefreshIndex=false\x00diff\x00--quiet\x00HEAD": {},
		"git\x00rev-parse\x00--verify\x00HEAD~1^{commit}":                     {},
		"git\x00ls-files\x00--others\x00--exclude-standard":                   {},
	}
	if _, ref := localMode(t.Context(), quiet.req); ref != nil {
		t.Fatalf("localMode refused: %v", ref)
	}
	if collector, ok := quiet.req.Progress.(*testkit.Collector); ok && slices.Contains(collector.Warnings, msgUntrackedExcluded) {
		t.Errorf("warnings = %q, want no untracked note for a clean listing", collector.Warnings)
	}
}

// TestStageWarningsKeepsAnEarlierLineOnce proves the composed-service echo is
// folded, not appended: every stage hands its whole warning list back, so the
// naive union would print a fetch warning once per stage.
func TestStageWarningsKeepsAnEarlierLineOnce(t *testing.T) {
	t.Parallel()
	fetch := []string{"read anonymously", "controls absent"}
	ran := stageWarnings(fetch, []string{"read anonymously", "controls absent", "agent wrote late"})
	if got, want := strings.Join(ran, "|"), "read anonymously|controls absent|agent wrote late"; got != want {
		t.Errorf("stageWarnings = %q, want %q", got, want)
	}
	recorded := stageWarnings(ran, []string{"read anonymously", "agent wrote late", "receipt stored"})
	if got, want := strings.Join(recorded, "|"),
		"read anonymously|controls absent|agent wrote late|receipt stored"; got != want {
		t.Errorf("second stageWarnings = %q, want %q", got, want)
	}
}

// TestTempRootRefusesAParentInsideTheCheckout proves the bundle can never land
// in the tree the demo reviews: TMPDIR pointed into the checkout refuses before
// anything is created, and a parent beside it is accepted.
func TestTempRootRefusesAParentInsideTheCheckout(t *testing.T) {
	t.Parallel()
	checkout := t.TempDir()
	inCheckout := filepath.Join(checkout, "tmp")
	if err := os.MkdirAll(inCheckout, 0o700); err != nil {
		t.Fatalf("create the in-checkout temp parent: %v", err)
	}
	// The checkout itself is refused, and so is any directory inside it.
	for _, parent := range []string{inCheckout, checkout} {
		assertTempParentRefused(t, checkout, parent)
	}
	if entries, err := os.ReadDir(inCheckout); err != nil || len(entries) != 0 {
		t.Errorf("temp parent = %q (err %v), want nothing created", entries, err)
	}
	assertTempParentAccepted(t, checkout, t.TempDir())
}

// TestTempRootRefusesInsideTheCallerRoot proves the URL form's guard: a run
// that reviews someone else's pull request from inside a clone still refuses to
// write its bundle in that clone, and names it the same way.
func TestTempRootRefusesInsideTheCallerRoot(t *testing.T) {
	t.Parallel()
	caller := t.TempDir()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude")
	env.req.CallerRoot = caller
	env.req.TmpRoot = filepath.Join(caller, "tmp")
	if err := os.MkdirAll(env.req.TmpRoot, 0o700); err != nil {
		t.Fatalf("create the in-checkout temp parent: %v", err)
	}
	_, ref := tempRoot(env.req)
	if ref == nil || ref.Code != codeFetchFailed {
		t.Fatalf("ref = %v, want %s", ref, codeFetchFailed)
	}
	// The remedy names TMPDIR only: the URL form refuses inside a checkout just
	// as the local form does, so "use the URL form instead" would be a lie.
	if strings.Contains(ref.Message, "public PR URL") {
		t.Errorf("message = %q, want no URL escape hatch", ref.Message)
	}
	if !strings.Contains(ref.Message, "inside the checkout") {
		t.Errorf("message = %q, want it to name the checkout", ref.Message)
	}
	// A caller root beside the parent still creates the directory.
	env.req.CallerRoot = t.TempDir()
	if dir, ref := tempRoot(env.req); ref != nil {
		t.Fatalf("a parent outside the caller root refused: %v", ref)
	} else {
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
}

// TestTempDirRefusalRemovesADirectoryInsideTheCheckout proves the second
// guard: a created directory whose resolution lands inside the checkout is
// removed and the run refuses. The guard is graded directly because MkdirTemp
// cannot be made to produce such a directory deterministically (the parent
// probe resolves the parent first, and a fresh name is never a symlink).
func TestTempDirRefusalRemovesADirectoryInsideTheCheckout(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude")
	checkout := t.TempDir()
	dir := filepath.Join(checkout, "verdict-demo-planted")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create the planted bundle: %v", err)
	}
	ref := tempDirRefusal(env.req, dir, checkout)
	if ref == nil || ref.Code != codeFetchFailed {
		t.Fatalf("ref = %v, want %s", ref, codeFetchFailed)
	}
	if !strings.Contains(ref.Message, "resolved inside the checkout") {
		t.Errorf("message = %q, want the post-resolution refusal", ref.Message)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directory inside the checkout survived: %v", err)
	}
	// A directory outside the checkout is accepted untouched.
	outside := t.TempDir()
	if ref := tempDirRefusal(env.req, outside, checkout); ref != nil {
		t.Fatalf("ref = %v, want nil for a directory outside the checkout", ref)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Errorf("a directory outside the checkout was touched: %v", err)
	}
}

// assertTempParentRefused proves one temp parent inside the checkout refuses
// before anything is created, naming the checkout and the TMPDIR remedy.
func assertTempParentRefused(t *testing.T, checkout, parent string) {
	t.Helper()
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.Checkout = checkout
	env.req.TmpRoot = parent
	dir, ref := tempRoot(env.req)
	if ref == nil || ref.Code != codeFetchFailed {
		t.Fatalf("tempRoot(%s) ref = %v, want %s", parent, ref, codeFetchFailed)
	}
	if dir != "" {
		t.Errorf("dir = %q, want empty alongside the refusal", dir)
	}
	for _, want := range []string{mustResolve(t, checkout), "inside the checkout", "TMPDIR"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("message = %q, want it to name %q", ref.Message, want)
		}
	}
}

// assertTempParentAccepted proves a parent beside the checkout still creates
// the one disposable directory inside it.
func assertTempParentAccepted(t *testing.T, checkout, parent string) {
	t.Helper()
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.Checkout = checkout
	env.req.TmpRoot = parent
	dir, ref := tempRoot(env.req)
	if ref != nil {
		t.Fatalf("sibling parent refused: %v", ref)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if root := mustResolve(t, parent); !strings.HasPrefix(dir, root) {
		t.Errorf("dir = %q, want it under %q", dir, root)
	}
}

// mustResolve resolves one test directory through symlinks.
func mustResolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

// TestInside proves the containment test works on whole path elements: a
// sibling whose name merely starts with the checkout's name is not inside it.
func TestInside(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir, path string
		want      bool
	}{
		{"/repo", "/repo", true},
		{"/repo/tmp", "/repo", true},
		{"/repo-2", "/repo", false},
		{"/other", "/repo", false},
		{"/repo/tmp", "/repo/", true},
	} {
		// The fixtures are slash-spelled and the check is separator-aware, so
		// each pair is converted before it reaches the code under test.
		dir, path := filepath.FromSlash(tc.dir), filepath.FromSlash(tc.path)
		if got := inside(dir, path); got != tc.want {
			t.Errorf("inside(%q, %q) = %v, want %v", dir, path, got, tc.want)
		}
	}
}

// TestRemoveTempWarnsOnCleanupFailure proves the failure reaches stderr even on
// a refusal, where the response is never returned: the operator still learns
// which directory was left behind. The failure comes from the injected remover,
// not from directory modes a root-run CI would ignore.
func TestRemoveTempWarnsOnCleanupFailure(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.RemoveAll = func(string) error { return errors.New("device busy") }
	dir := "/tmp/verdict-demo-stubborn"
	resp := &Response{}
	removeTemp(env.req, dir, resp)
	want := "could not remove " + dir + ": device busy"
	if len(resp.Warnings) != 1 || resp.Warnings[0] != want {
		t.Fatalf("warnings = %q, want exactly %q", resp.Warnings, want)
	}
	// The adapter returns no response on a refusal, so the progress stream is
	// the only channel that reaches the operator.
	assertWarningNamed(t, env.req.Progress, want)
}

// TestRemoveTempSuccessIsSilent proves the other side: a successful removal and
// a --keep run add no failure warning.
func TestRemoveTempSuccessIsSilent(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "", "claude", "claude")
	removed := ""
	env.req.RemoveAll = func(dir string) error {
		removed = dir
		return nil
	}
	resp := &Response{}
	removeTemp(env.req, "/tmp/verdict-demo-gone", resp)
	if removed != "/tmp/verdict-demo-gone" {
		t.Errorf("removed = %q, want the bundle directory", removed)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q, want none", resp.Warnings)
	}
}

// TestLocalModeCanceledProbes proves an interrupted local probe is the cancel,
// never a bad range or an unreadable tree. The commander keeps answering after
// the cancel, so each probe's own context check is what has to fire.
func TestLocalModeCanceledProbes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		probe func(context.Context, *Request) *core.RefusalError
		name  string
	}{
		{name: "dirty probe", probe: func(ctx context.Context, req *Request) *core.RefusalError {
			_, ref := gitDirty(ctx, req)
			return ref
		}},
		{name: "parent probe", probe: func(ctx context.Context, req *Request) *core.RefusalError {
			_, ref := hasParentCommit(ctx, req)
			return ref
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newDemoEnv(t, "", "claude", "claude")
			env.req.Checkout = t.TempDir()
			env.req.Commander = &spawnedCmd{}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			ref := tc.probe(ctx, env.req)
			if ref == nil || ref.Code != "canceled" {
				t.Fatalf("ref = %v, want canceled", ref)
			}
		})
	}
}

// TestGitDirtyDoesNotRefresh proves the local probe never refreshes the
// checkout's index: the one-shot config override leads the argv.
func TestGitDirtyDoesNotRefresh(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.Checkout = t.TempDir()
	env.cmdr.results["git\x00-c\x00diff.autoRefreshIndex=false\x00diff\x00--quiet\x00HEAD"] = core.Result{ExitCode: 1}
	dirty, ref := gitDirty(t.Context(), env.req)
	if ref != nil {
		t.Fatalf("gitDirty refused: %v", ref)
	}
	if !dirty {
		t.Error("dirty = false, want true for exit 1")
	}
	if len(env.cmdr.calls) != 1 {
		t.Fatalf("calls = %q, want one", env.cmdr.calls)
	}
	want := "git -c diff.autoRefreshIndex=false diff --quiet HEAD"
	if got := strings.Join(env.cmdr.calls[0], " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// TestGhSignedInWalksTheFlagLadder proves the login probe asks the active
// account first and walks back one flag at a time: a gh that knows neither
// flag is asked four times, a gh that knows one is asked two or three, and its
// answer decides. A failure that is not the unknown-flag refusal is taken at
// face value after exactly one probe.
func TestGhSignedInWalksTheFlagLadder(t *testing.T) {
	t.Parallel()
	for _, tc := range ghLadderCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertGhLadder(t, tc)
		})
	}
	// A definite answer costs exactly one probe, whichever way it goes.
	assertGhProbe(t, core.Result{}, true)
	assertGhProbe(t, failResult("not logged in"), false)
}

// ghLadderCase is one older-gh shape: which rungs reject a flag they do not
// know, and how many probes it therefore costs.
type ghLadderCase struct {
	unknown map[string]bool
	name    string
	want    bool
	calls   int
}

// ghLadderCases are the four gh generations the ladder has to serve.
func ghLadderCases() []ghLadderCase {
	return []ghLadderCase{
		{name: "modern gh answers at once", want: true, calls: 1},
		{name: "gh without --no-refresh", unknown: map[string]bool{"--no-refresh": true}, want: true, calls: 2},
		// The ladder is static, so a gh that rejects --active still costs a
		// rung carrying only --active before the next spelling runs.
		{name: "gh without --active", unknown: map[string]bool{"--active": true}, want: true, calls: 3},
		{
			name:    "gh without either flag",
			unknown: map[string]bool{"--no-refresh": true, "--active": true},
			want:    true, calls: 4,
		},
	}
}

// assertGhLadder scripts one gh generation and grades the probe count.
func assertGhLadder(t *testing.T, tc ghLadderCase) {
	t.Helper()
	env := newDemoEnv(t, "", "claude", "gh")
	for _, rung := range ghAuthLadder {
		key := "gh\x00" + strings.Join(rung, "\x00")
		env.cmdr.results[key] = rungAnswer(rung, tc.unknown)
	}
	if got := ghSignedIn(t.Context(), env.req); got != tc.want {
		t.Errorf("ghSignedIn = %v, want %v", got, tc.want)
	}
	if len(env.cmdr.calls) != tc.calls {
		t.Fatalf("calls = %q, want %d", env.cmdr.calls, tc.calls)
	}
}

// rungAnswer is what one rung answers: an unknown-flag failure when the gh
// rejects a flag it carries, a clean exit otherwise.
func rungAnswer(rung []string, unknown map[string]bool) core.Result {
	for _, arg := range rung {
		if unknown[arg] {
			return failResult("unknown flag: " + arg)
		}
	}
	return core.Result{}
}

// assertGhProbe proves one definite answer costs exactly one probe, led by the
// active-account spelling.
func assertGhProbe(t *testing.T, first core.Result, want bool) {
	t.Helper()
	env := newDemoEnv(t, "", "claude", "gh")
	env.cmdr.results["gh\x00auth\x00status\x00--hostname\x00github.com\x00--active\x00--no-refresh"] = first
	if got := ghSignedIn(t.Context(), env.req); got != want {
		t.Errorf("ghSignedIn = %v, want %v", got, want)
	}
	if len(env.cmdr.calls) != 1 {
		t.Errorf("calls = %q, want exactly one probe", env.cmdr.calls)
	}
	if got := strings.Join(env.cmdr.calls[0], " "); got != "gh auth status --hostname github.com --active --no-refresh" {
		t.Errorf("argv = %q, want the active-account probe first", got)
	}
}

// spawnedCmd is a commander that answers every call successfully even after the
// context is done: the probes' own context checks are what the cancel tests
// grade, not the seam short-circuiting them.
type spawnedCmd struct{}

// Run answers a clean exit for any argv.
func (spawnedCmd) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, nil
}

// LookPath reports every tool present.
func (spawnedCmd) LookPath(string) error { return nil }

// TestResolveTempDirRemovesAnUnresolvableDir proves the refusal is not a leak:
// a directory that cannot be resolved through symlinks exists but is unusable,
// so the refusal is returned only after it has been removed.
func TestResolveTempDirRemovesAnUnresolvableDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	dir, ref := resolveTempDir(loop)
	if ref == nil || ref.Code != codeFetchFailed {
		t.Fatalf("ref = %v, want %s", ref, codeFetchFailed)
	}
	if dir != "" {
		t.Errorf("dir = %q, want empty alongside the refusal", dir)
	}
	if _, err := os.Lstat(loop); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the unresolvable directory survived: %v", err)
	}
}

// TestOriginSlug proves the local bundle's display name: the origin remote's
// owner/repo in its three spellings, and a local/ fallback otherwise.
func TestOriginSlug(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{"https", "https://github.com/acme/app.git\n", "acme/app"},
		{"https without suffix", "https://github.com/acme/app", "acme/app"},
		{"ssh scp form", "git@github.com:acme/app.git\n", "acme/app"},
		{"ssh url", "ssh://git@github.com/acme/app.git\n", "acme/app"},
		{"deeper path refuses", "https://github.com/acme/app/tree/main", "local/repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newDemoEnv(t, "", "claude", "claude")
			env.req.Checkout = filepath.Join(t.TempDir(), "repo")
			env.cmdr.results["git\x00remote\x00get-url\x00origin"] = core.Result{Stdout: tc.url}
			if got := originSlug(t.Context(), env.req); got != tc.want {
				t.Errorf("originSlug = %q, want %q", got, tc.want)
			}
		})
	}
	env := newDemoEnv(t, "", "claude", "claude")
	env.req.Checkout = filepath.Join(t.TempDir(), "repo")
	if got := originSlug(t.Context(), env.req); got != "local/repo" {
		t.Errorf("originSlug without origin = %q, want local/repo", got)
	}
}

// TestRunCanceled proves the cancel contract: a canceled context is the
// canceled refusal (the adapter exits 3 on it) and touches nothing.
func TestRunCanceled(t *testing.T) {
	t.Parallel()
	env := newDemoEnv(t, "https://github.com/acme/app/pull/7", "claude", "claude")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, ref := Run(ctx, env.req)
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("ref = %v, want canceled", ref)
	}
	if len(env.cmdr.calls) != 0 {
		t.Errorf("host calls = %q, want none", env.cmdr.calls)
	}
}

// TestDataWireShape pins the envelope data's key order: a consumer reading
// `data` gets the documented shape, and an added field is a visible diff here.
func TestDataWireShape(t *testing.T) {
	t.Parallel()
	wire, ok := core.Wire(&Data{}).(*jq.Obj)
	if !ok {
		t.Fatalf("core.Wire(Data) = %T, want a wire object", core.Wire(&Data{}))
	}
	want := []string{
		"mode", "fetched_via", "head_sha", "base_sha", "agent", "finders",
		"review_body", "topology", "findings", "approval", "bundle_dir", "kept", "duration_ms",
	}
	if got := strings.Join(wire.Keys, " "); got != strings.Join(want, " ") {
		t.Errorf("data keys = %v, want %v", wire.Keys, want)
	}
}

// TestWatchdogBudget proves the launcher budget: an absent --timeout keeps the
// run verb's default, and the raw operator value rides through untouched so the
// run service refuses anything that is not a positive number.
func TestWatchdogBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, want string }{
		{raw: "", want: runcore.DefaultTimeoutS},
		{raw: "900", want: "900"},
		{raw: "abc", want: "abc"},
	} {
		if got := watchdogBudget(tc.raw); got != tc.want {
			t.Fatalf("watchdogBudget(%q) = %q; want %q", tc.raw, got, tc.want)
		}
	}
}
