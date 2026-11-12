package pack

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	addHashV2  = "be3ac14b258cbe5fc3a59669a4aa203a262e288373e3a7e4f0b33624adfdaca5"
	addSigV2   = "7183f516f81c92324e31f8035bf3d468996c65f94dfdf468d9e3bcd86c333e6a"
	addGitSha  = "cccccccccccccccccccccccccccccccccccccccc"
	addGitSig  = "daf043b46f622c293594a1d33960e9a388750629ef8c7268b74ad46a2771e42f"
	addExtraH  = "cb4224ee81794294d3cabed7fe50695d4411430fdb1284cdeda734e934c01edc"
	addExtraS  = "9065e725220c100118273d61b9ec1b94a4ac543f44d02dbc5f057446814b06eb"
	addNext    = "commit .verdict/verdict.lock with the vendored pack, then run 'verdict pack list'"
	addLockFmt = `{
  "schema_version": "1",
  "packs": [
    {
      "name": "acme/lenses",
      "source": "%s",
      "version": "1.0.0",
      "hash": "%s",
      "sig": "%s"
    }
  ]
}
`
)

// sourcePack writes one pack directory under root.
func sourcePack(t *testing.T, root, rel, manifest string, files map[string]string) {
	t.Helper()
	body := map[string]string{"pack.json": manifest}
	maps.Copy(body, files)
	writeFixture(t, filepath.Join(root, filepath.FromSlash(rel)), body)
}

// lensesSource writes the shared acme/lenses source pack.
func lensesSource(t *testing.T, root, rel, body string) {
	t.Helper()
	sourcePack(t, root, rel, lockManifest, map[string]string{"lenses/perf.md": body})
}

// TestAddDirVendorsAndLocks proves a directory add validates the pack, copies
// its loaded tree under .verdict/packs/<slug>/, and writes the strict lock.
func TestAddDirVendorsAndLocks(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	resp, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"})
	if ref != nil {
		t.Fatalf("RunAdd: %s: %s", ref.Code, ref.Message)
	}
	if got, want := resp.Human, []string{
		"added acme/lenses@1.0.0 source=dir:src-packs/lenses sha256:" + lockHash,
	}; !slices.Equal(got, want) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	if resp.Next != addNext {
		t.Errorf("next = %q, want %q", resp.Next, addNext)
	}
	if resp.Data.Replaced || resp.Data.PreviousHash != nil {
		t.Errorf("first add reports replaced=%v previous=%v", resp.Data.Replaced, resp.Data.PreviousHash)
	}
	if resp.Data.Pack.Layer != "repo" || resp.Data.Pack.Source != ".verdict/packs/lenses" {
		t.Errorf("data.pack = %+v", resp.Data.Pack)
	}
	assertFile(t, ".verdict/packs/lenses/pack.json", lockManifest)
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body\n")
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, "dir:src-packs/lenses", lockHash, lockSigDir))
}

// TestAddDirReplacesAndReportsOldToNew proves re-adding the same name replaces
// the vendored tree and lock entry, reporting the old -> new hash.
func TestAddDirReplacesAndReportsOldToNew(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("first RunAdd: %s: %s", ref.Code, ref.Message)
	}
	lensesSource(t, repo, "src-packs/lenses", "perf body v2\n")
	resp, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"})
	if ref != nil {
		t.Fatalf("second RunAdd: %s: %s", ref.Code, ref.Message)
	}
	want := "updated acme/lenses@1.0.0 source=dir:src-packs/lenses sha256:" +
		lockHash + " -> sha256:" + addHashV2
	if got := resp.Human; !slices.Equal(got, []string{want}) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	if !resp.Data.Replaced || resp.Data.PreviousHash == nil || *resp.Data.PreviousHash != lockHash {
		t.Fatalf("replaced = %v previous = %v, want true and %s",
			resp.Data.Replaced, resp.Data.PreviousHash, lockHash)
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body v2\n")
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, "dir:src-packs/lenses", addHashV2, addSigV2))
}

// TestAddKeepsOtherEntriesSorted proves a second name joins the lock without
// dropping the first, sorted by name.
func TestAddKeepsOtherEntriesSorted(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	sourcePack(t, repo, "src-packs/extra",
		`{"schema_version":"1","name":"acme/extra","version":"0.2.0","lenses":[{"id":"style"}]}`,
		map[string]string{"lenses/style.md": "style body\n"})
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("lenses RunAdd: %s: %s", ref.Code, ref.Message)
	}
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/extra"}); ref != nil {
		t.Fatalf("extra RunAdd: %s: %s", ref.Code, ref.Message)
	}
	assertFile(t, LockFile, `{
  "schema_version": "1",
  "packs": [
    {
      "name": "acme/extra",
      "source": "dir:src-packs/extra",
      "version": "0.2.0",
      "hash": "`+addExtraH+`",
      "sig": "`+addExtraS+`"
    },
    {
      "name": "acme/lenses",
      "source": "dir:src-packs/lenses",
      "version": "1.0.0",
      "hash": "`+lockHash+`",
      "sig": "`+lockSigDir+`"
    }
  ]
}
`)
}

// TestAddRefusesSourceInsideRepoPacks proves a path inside the vendored tree
// refuses instead of copying a pack onto itself.
func TestAddRefusesSourceInsideRepoPacks(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, ".verdict/packs/lenses", "perf body\n")
	_, ref := RunAdd(context.Background(), &AddRequest{Source: ".verdict/packs/lenses"})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if !strings.Contains(ref.Message, "already vendored") {
		t.Fatalf("message = %q, want the already-vendored cause", ref.Message)
	}
	if _, err := os.Stat(LockFile); !os.IsNotExist(err) {
		t.Fatalf("refused add wrote %s", LockFile)
	}
}

// TestAddRefusesInvalidPack proves the existing loader's refusals run before
// anything is vendored or locked.
func TestAddRefusesInvalidPack(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	writeFixture(t, filepath.Join(repo, "broken"), map[string]string{"pack.json": `{"name":`})
	_, ref := RunAdd(context.Background(), &AddRequest{Source: "broken"})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if _, err := os.Stat(".verdict/packs/lenses"); !os.IsNotExist(err) {
		t.Fatal("refused add vendored a tree")
	}
}

// gitCommander scripts the three git calls a hermetic add makes and plants the
// fixture tree in the clone destination.
type gitCommander struct {
	t     *testing.T
	files map[string]string
	calls []gitCall
}

type gitCall struct {
	argv []string
	dir  string
	env  []string
}

func (c *gitCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.calls = append(c.calls, gitCall{argv: slices.Clone(cmd.Argv), dir: cmd.Dir, env: slices.Clone(cmd.Env)})
	if slices.Contains(cmd.Argv, "clone") {
		dest := cmd.Argv[len(cmd.Argv)-1]
		writeFixture(c.t, dest, c.files)
		return core.Result{}, nil
	}
	if len(cmd.Argv) >= 3 && cmd.Argv[1] == "rev-parse" {
		return core.Result{Stdout: addGitSha + "\n"}, nil
	}
	return core.Result{}, nil
}

// TestAddGitPinsHermeticArgvEnvAndRecordsSha proves the production clone argv
// carries every hermetic flag, the environment is scrubbed, and the lock
// records the resolved HEAD commit.
//
//nolint:revive // the argv, env, and lock assertions read as one contract block
func TestAddGitPinsHermeticArgvEnvAndRecordsSha(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	const url = "https://example.invalid/acme/packs.git"
	cmdr := &gitCommander{t: t, files: map[string]string{
		"pack.json":      lockManifest,
		"lenses/perf.md": "perf body\n",
	}}
	resp, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{"PATH": "/usr/bin", "HOME": "/home/operator", "GIT_DIR": "/bad"},
			Commander: cmdr,
		},
		Source: url + "@v1.2.3",
	})
	if ref != nil {
		t.Fatalf("RunAdd: %s: %s", ref.Code, ref.Message)
	}
	if len(cmdr.calls) != 3 {
		t.Fatalf("git calls = %d, want clone, rev-parse, checkout", len(cmdr.calls))
	}
	clone := cmdr.calls[0]
	tmp := filepath.Dir(clone.argv[len(clone.argv)-1])
	hooks := "/dev/null"
	if runtime.GOOS == "windows" {
		hooks = filepath.Join(tmp, "hooks")
	}
	wantClone := []string{
		"git", "-c", "core.hooksPath=" + hooks, "-c", "protocol.file.allow=never",
		"clone", "--depth=1", "--no-tags", "--no-recurse-submodules",
		"--branch", "v1.2.3", url, filepath.Join(tmp, "repo"),
	}
	if !slices.Equal(clone.argv, wantClone) {
		t.Fatalf("clone argv = %q, want %q", clone.argv, wantClone)
	}
	if clone.dir != "" || clone.env == nil {
		t.Fatalf("clone dir=%q env=%v", clone.dir, clone.env)
	}
	assertGitEnv(t, clone.env, tmp)
	if got, want := cmdr.calls[1].argv, []string{"git", "rev-parse", "HEAD"}; !slices.Equal(got, want) {
		t.Errorf("rev-parse argv = %q, want %q", got, want)
	}
	if got, want := cmdr.calls[2].argv, []string{"git", "checkout", "--detach", addGitSha}; !slices.Equal(got, want) {
		t.Errorf("checkout argv = %q, want %q", got, want)
	}
	if cmdr.calls[1].dir != filepath.Join(tmp, "repo") || cmdr.calls[2].dir != filepath.Join(tmp, "repo") {
		t.Errorf("rev-parse/checkout dirs = %q/%q, want the clone", cmdr.calls[1].dir, cmdr.calls[2].dir)
	}
	source := "git:" + url + "@" + addGitSha
	if got, want := resp.Human, []string{
		"added acme/lenses@1.0.0 source=" + source + " sha256:" + lockHash,
	}; !slices.Equal(got, want) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, source, lockHash, addGitSig))
}

// assertGitEnv proves the clone environment is exactly the scrubbed set.
func assertGitEnv(t *testing.T, env []string, tmp string) {
	t.Helper()
	want := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"HOME=" + filepath.Join(tmp, "home"),
		"PATH=/usr/bin",
		"XDG_CACHE_HOME=" + filepath.Join(tmp, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(tmp, "config"),
		"XDG_DATA_HOME=" + filepath.Join(tmp, "data"),
		"XDG_STATE_HOME=" + filepath.Join(tmp, "state"),
	}
	if !slices.Equal(env, want) {
		t.Fatalf("env = %q, want %q", env, want)
	}
}

// TestSplitGitSource pins the accepted URL and @ref forms.
func TestSplitGitSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		source string
		url    string
		ref    string
		ok     bool
	}{
		{"https://github.com/a/b.git", "https://github.com/a/b.git", "", true},
		{"https://github.com/a/b.git@v1", "https://github.com/a/b.git", "v1", true},
		{"https://user@github.com/a/b.git", "https://user@github.com/a/b.git", "", true},
		{"git@github.com:a/b.git", "git@github.com:a/b.git", "", true},
		{"git@github.com:a/b.git@release/1", "git@github.com:a/b.git", "release/1", true},
		{"ssh://git@host/a/b@v1", "ssh://git@host/a/b", "v1", true},
		{"http://host/a", "", "", false},
		{"https://host/a@-bad", "", "", false},
		{"https://host/a@", "", "", false},
		{"relative/dir", "", "", false},
	} {
		url, ref, ok := splitGitSource(tc.source)
		if url != tc.url || ref != tc.ref || ok != tc.ok {
			t.Errorf("splitGitSource(%q) = %q, %q, %v; want %q, %q, %v",
				tc.source, url, ref, ok, tc.url, tc.ref, tc.ok)
		}
	}
}

// assertFile compares one fixture file with its literal bytes.
func assertFile(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(raw) != want {
		t.Fatalf("%s =\n%d bytes\n%q\nwant\n%q", path, len(raw), string(raw), want)
	}
}
