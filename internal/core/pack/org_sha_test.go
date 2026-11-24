package pack

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// shaCommander scripts the pinned fetch clone sequence and plants the fixture
// tree in the clone directory git would create. Neither a 40-hex pin nor a
// semver tag pin is a branch name, so the sequence is init, remote add, the
// shallow fetch of the exact Git ref, sparse checkout when the pin names a
// subtree, checkout of FETCH_HEAD, then the resolved-head pair.
//
//nolint:govet // test helper; field order reads as the scripted call fields
type shaCommander struct {
	t         *testing.T
	files     map[string]string
	calls     []gitCall
	fetchExit int
	// head is what the clone's HEAD resolves to; the zero value answers
	// addGitSha, the commit the add fixtures record.
	head string
}

func (c *shaCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	_, deadline := ctx.Deadline()
	c.calls = append(c.calls, gitCall{
		argv: slices.Clone(cmd.Argv), dir: cmd.Dir, env: slices.Clone(cmd.Env), deadline: deadline,
	})
	switch gitSub(cmd.Argv) {
	case "init":
		writeFixture(c.t, cmd.Argv[len(cmd.Argv)-1], c.files)
		return core.Result{}, nil
	case "fetch":
		return core.Result{ExitCode: c.fetchExit}, nil
	case "rev-parse":
		head := c.head
		if head == "" {
			head = addGitSha
		}
		return core.Result{Stdout: head + "\n"}, nil
	}
	return core.Result{}, nil
}

// gitSub reports the git subcommand one scripted call carries: the first
// argument past the shared `git -c <key>=<value>` prefix, so the
// `sparse-checkout init` step never reads as the clone's own `init`.
func gitSub(argv []string) string {
	for i := 1; i < len(argv); i++ {
		if argv[i] == "-c" {
			i++
			continue
		}
		return argv[i]
	}
	return ""
}

// argvStrings renders every scripted argv for the no-`--branch` assertion.
func (c *shaCommander) argvStrings() [][]string {
	out := make([][]string, 0, len(c.calls))
	for _, call := range c.calls {
		out = append(out, call.argv)
	}
	return out
}

// TestSyncSHAFetchesBySHA proves a 40-hex org pin runs the fetch-by-SHA clone:
// the exact hermetic argv sequence, no `--branch <sha>`, the scrubbed
// environment, and the vendored pack plus its lock row carrying the pin ref.
//
//nolint:cyclop,gocognit,revive // argv, env, and lock assertions read as one contract block
func TestSyncSHAFetchesBySHA(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	const url = "https://example.invalid/acme/packs.git"
	sha := "dddddddddddddddddddddddddddddddddddddddd"
	cmdr := &shaCommander{t: t, head: sha, files: map[string]string{
		"packs/org/pack.json":      lockManifest,
		"packs/org/lenses/perf.md": "perf body\n",
		".git/config":              "planted metadata\n",
	}}
	pin := &OrgPin{Repo: url, Ref: sha, Path: "packs/org"}
	read := core.ReadRequest{Runtime: &core.Runtime{
		Env:       map[string]string{"PATH": "/usr/bin", "HOME": "/home/operator", "GIT_DIR": "/bad"},
		Commander: cmdr,
	}}
	resp, ref := RunSync(context.Background(), &SyncRequest{ReadRequest: read, OrgPin: pin})
	if ref != nil {
		t.Fatalf("RunSync: %s: %s", ref.Code, ref.Message)
	}
	want := "synced acme/lenses@1.0.0 repo=" + url + " ref=" + sha + " path=packs/org sha256:" + lockHash
	if !slices.Equal(resp.Human, []string{want}) {
		t.Fatalf("human = %q, want %q", resp.Human, []string{want})
	}
	if resp.Data.SHA == nil || *resp.Data.SHA != sha {
		t.Fatalf("sha = %v, want the fetched commit %s", resp.Data.SHA, sha)
	}

	clone := cmdr.calls[0].argv[len(cmdr.calls[0].argv)-1]
	tmp := filepath.Dir(clone)
	hooks := "/dev/null"
	if runtime.GOOS == "windows" {
		hooks = filepath.Join(tmp, "hooks")
	}
	prefix := []string{"git", "-c", "core.hooksPath=" + hooks, "-c", "protocol.file.allow=never"}
	wantCalls := [][]string{
		append(slices.Clone(prefix), "init", clone),
		append(slices.Clone(prefix), "remote", "add", "origin", url),
		append(slices.Clone(prefix), "fetch", "--depth=1", "--no-tags", "--no-recurse-submodules", "origin", sha),
		append(slices.Clone(prefix), "sparse-checkout", "init", "--cone"),
		append(slices.Clone(prefix), "sparse-checkout", "set", "packs/org"),
		append(slices.Clone(prefix), "checkout", "--detach", "FETCH_HEAD"),
		append(slices.Clone(prefix), "rev-parse", "HEAD"),
		append(slices.Clone(prefix), "checkout", "--detach", sha),
	}
	if got := cmdr.argvStrings(); !slices.EqualFunc(got, wantCalls, slices.Equal[[]string]) {
		t.Fatalf("git argv = %q\nwant %q", got, wantCalls)
	}
	for i, call := range cmdr.calls {
		if !call.deadline {
			t.Errorf("git call %d carries no deadline", i)
		}
		if slices.Contains(call.argv, "--branch") {
			t.Errorf("git call %d carries --branch: git clone --branch cannot take a commit SHA", i)
		}
	}
	assertGitEnv(t, cmdr.calls[0].env, tmp)
	if cmdr.calls[0].dir != "" {
		t.Errorf("git init ran in %q, want the work dir", cmdr.calls[0].dir)
	}
	if got := cmdr.calls[2].dir; got != clone {
		t.Errorf("git fetch ran in %q, want the clone", got)
	}
	for i := 3; i < len(cmdr.calls); i++ {
		if got := cmdr.calls[i].dir; got != clone {
			t.Errorf("git call %d ran in %q, want the clone", i, got)
		}
	}
	assertSHAVendored(t, repo, url, sha, pin)
}

// assertSHAVendored proves the SHA clone's output: the vendored pack, the lock
// row carrying the pin's path and ref, and the offline verifier accepting it.
func assertSHAVendored(t *testing.T, repo, url, sha string, pin *OrgPin) {
	t.Helper()
	assertFile(t, ".verdict/packs/lenses/pack.json", lockManifest)
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body\n")
	lock, ref := loadLock(repo)
	if ref != nil {
		t.Fatalf("loadLock: %s: %s", ref.Code, ref.Message)
	}
	if lock == nil || len(lock.Packs) != 1 {
		t.Fatalf("lock = %+v, want one entry", lock)
	}
	entry := lock.Packs[0]
	if entry.Source != "git:"+url+"@"+sha || entry.OrgPath != "packs/org" || entry.OrgRef != sha {
		t.Errorf("lock entry = %+v, want source git:%s@%s org_path packs/org org_ref %s",
			entry, url, sha, sha)
	}
	if entry.Sig != LockSig(&entry) {
		t.Errorf("lock sig = %s, want %s", entry.Sig, LockSig(&entry))
	}
	if ref := VerifyOrgPin(repo, pin); ref != nil {
		t.Fatalf("VerifyOrgPin after the SHA sync: %s: %s", ref.Code, ref.Message)
	}
}

// TestGitCloneFetchesTagPinByFullRef proves a tag pin is fetched as
// refs/tags/<tag>, never by `git clone --branch`: git resolves
// refs/heads/v1.2.3 before refs/tags/v1.2.3, so a branch of the pin's name
// would otherwise answer for the pin.
func TestGitCloneFetchesTagPinByFullRef(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cmdr := &shaCommander{t: t}
	if ref := gitClone(context.Background(), cmdr, nil, tmp, "https://host/x.git", "v1.2.3",
		rootSelf, refByGitRef); ref != nil {
		t.Fatalf("gitClone: %s: %s", ref.Code, ref.Message)
	}
	assertPinnedTagClone(t, cmdr, filepath.Join(tmp, "repo"))
}

// assertPinnedTagClone proves one root tag pin's git sequence end to end: the
// fetch step order, no `--branch` and no sparse checkout, refs/tags/<tag> from
// origin, a FETCH_HEAD checkout, and every step after init inside the clone.
func assertPinnedTagClone(t *testing.T, cmdr *shaCommander, clone string) {
	t.Helper()
	subs := make([]string, 0, len(cmdr.calls))
	for _, call := range cmdr.calls {
		subs = append(subs, gitSub(call.argv))
	}
	if want := []string{"init", "remote", "fetch", "checkout"}; !slices.Equal(subs, want) {
		t.Fatalf("git steps = %q, want %q", subs, want)
	}
	assertNoBranchStep(t, cmdr)
	assertNoSparseStep(t, cmdr)
	assertFetchArgv(t, cmdr.calls[2].argv, "refs/tags/v1.2.3")
	assertLastArg(t, cmdr.calls[3].argv, "FETCH_HEAD")
	assertCallsInDir(t, cmdr.calls[2:], clone)
}

// assertNoBranchStep proves no scripted call cloned with `--branch`.
func assertNoBranchStep(t *testing.T, cmdr *shaCommander) {
	t.Helper()
	for _, call := range cmdr.calls {
		if slices.Contains(call.argv, "--branch") {
			t.Fatalf("a tag pin ran --branch: %q", call.argv)
		}
	}
}

// assertLastArg proves one argv ends with want.
func assertLastArg(t *testing.T, argv []string, want string) {
	t.Helper()
	if got := argv[len(argv)-1]; got != want {
		t.Fatalf("argv = %q, want it to end with %q", argv, want)
	}
}

// assertCallsInDir proves every given call ran inside dir.
func assertCallsInDir(t *testing.T, calls []gitCall, dir string) {
	t.Helper()
	for i, call := range calls {
		if call.dir != dir {
			t.Errorf("git call %d ran in %q, want %q", i, call.dir, dir)
		}
	}
}

// assertNoSparseStep proves no scripted call ran a sparse checkout.
func assertNoSparseStep(t *testing.T, cmdr *shaCommander) {
	t.Helper()
	for _, call := range cmdr.calls {
		if slices.Contains(call.argv, "sparse-checkout") {
			t.Fatalf("a root pin ran a sparse checkout: %q", call.argv)
		}
	}
}

// assertFetchArgv proves one fetch call carries origin and the wanted refspec.
func assertFetchArgv(t *testing.T, argv []string, wantRef string) {
	t.Helper()
	if got := argv[len(argv)-1]; got != wantRef {
		t.Fatalf("fetch refspec = %q, want %q", got, wantRef)
	}
	if got := argv[len(argv)-2]; got != "origin" {
		t.Fatalf("fetch remote = %q, want origin", got)
	}
}

// TestPinRefspecPinsTheTagNamespace proves the refspec rendering: a semver tag
// resolves under refs/tags/, a 40-hex commit by its object id, and neither is
// ever a bare name git could resolve to a branch.
func TestPinRefspecPinsTheTagNamespace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ref  string
		want string
	}{
		{"v1.2.3", "refs/tags/v1.2.3"},
		{"2.0.0", "refs/tags/2.0.0"},
		{"v1.2.3-rc.1", "refs/tags/v1.2.3-rc.1"},
		{"dddddddddddddddddddddddddddddddddddddddd", "dddddddddddddddddddddddddddddddddddddddd"},
	} {
		if got := pinRefspec(tc.ref); got != tc.want {
			t.Errorf("pinRefspec(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// TestGitClonePinnedFailsCleanly proves a fetch git cannot complete refuses
// pack-git-failed with fixed wording, before any checkout exists — and that a
// ref git cannot fetch is the same typed refusal, never a silent empty clone.
func TestGitClonePinnedFailsCleanly(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
	}{
		{"failed fetch", "dddddddddddddddddddddddddddddddddddddddd"},
		{"non-hex ref", "not-a-commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFailedFetch(t, tc.ref)
		})
	}
}

// assertFailedFetch proves one unfetchable ref refuses before any checkout.
func assertFailedFetch(t *testing.T, ref string) {
	t.Helper()
	tmp := t.TempDir()
	cmdr := &shaCommander{t: t, fetchExit: gitFatalExit}
	got := gitClonePinned(context.Background(), cmdr, nil, tmp, "https://host/x.git", ref,
		rootSelf, tmp+"/repo")
	if got == nil || got.Code != CodePackGitFailed {
		t.Fatalf("gitClonePinned(%q) = %v, want %s", ref, got, CodePackGitFailed)
	}
	if want := "git fetch failed"; !strings.Contains(got.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", got.Message, want)
	}
	if len(cmdr.calls) != 3 {
		t.Fatalf("git calls = %d, want init, remote, fetch", len(cmdr.calls))
	}
	for _, call := range cmdr.calls {
		if slices.Contains(call.argv, "checkout") {
			t.Fatalf("a failed fetch still ran a checkout: %q", call.argv)
		}
	}
}
