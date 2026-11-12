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
	addHashV2  = "760382c944542247a5c7c2937e1e1e209c5c23a8daa2598f8133ccd7e8a1b5e1"
	addSigV2   = "5ebf006e0f2a7773d9381af302d6ca83d7e26b71995c0b7c0d39bb61ed86e69b"
	addGitSha  = "cccccccccccccccccccccccccccccccccccccccc"
	addGitSig  = "7fbcf52aeb6842352ac1df61542361567b1cf9a3c31a8005698382e43edc9c71"
	addExtraH  = "0bc46f5b5eebba69ae13fe8eb2c7774f2c2e72329f906c30a1604011edaf98e1"
	addExtraS  = "d448b96ba0413753159a84e10dbd9c7eb6c984c4d43a078a95b3a9ae0864b8c8"
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

// TestAddFailedReAddKeepsOldTree proves a re-add that fails after the source
// is loaded leaves the previous vendored tree and its lock row intact.
//
//nolint:revive // the stage, fail, and assert sequence reads as one scenario
func TestAddFailedReAddKeepsOldTree(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "old body\n")
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("first RunAdd: %s: %s", ref.Code, ref.Message)
	}
	oldLock, err := os.ReadFile(LockFile)
	if err != nil {
		t.Fatal(err)
	}
	lensesSource(t, repo, "src-packs/lenses", "new body\n")
	p, ref := LoadDir(filepath.Join(repo, "src-packs", "lenses"))
	if ref != nil || p == nil {
		t.Fatalf("LoadDir = (%v, %v)", p, ref)
	}
	if err := os.Remove(filepath.Join(repo, "src-packs", "lenses", "pack.json")); err != nil {
		t.Fatal(err)
	}
	if _, ref := vendorPack(repo, filepath.Join(repo, "src-packs", "lenses"), "lenses", p); ref == nil ||
		ref.Code != CodeBadPack {
		t.Fatalf("vendorPack = %v, want bad-pack", ref)
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "old body\n")
	assertFile(t, LockFile, string(oldLock))
	entries, err := os.ReadDir(filepath.Join(repo, ".verdict", "packs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "lenses" {
		t.Fatalf("pack root = %v, want only lenses", entries)
	}
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

// TestAddVendorsReservedTrees proves pack add copies evals/ and examples/ and
// folds them into the recorded hash.
func TestAddVendorsReservedTrees(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	sourcePack(t, repo, "src-packs/lenses", lockManifest, map[string]string{
		"lenses/perf.md":  "perf body\n",
		"evals/case.sh":   "echo ok\n",
		"examples/use.md": "sample\n",
	})
	resp, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"})
	if ref != nil {
		t.Fatalf("RunAdd: %s: %s", ref.Code, ref.Message)
	}
	const hash = "d91258841b4589e1c2f67cfa1351181c9f0eb1525250bc30341a70e8eaf54b1c"
	if got, want := resp.Human, []string{
		"added acme/lenses@1.0.0 source=dir:src-packs/lenses sha256:" + hash,
	}; !slices.Equal(got, want) {
		t.Fatalf("human = %q, want %q", got, want)
	}
	assertFile(t, ".verdict/packs/lenses/evals/case.sh", "echo ok\n")
	assertFile(t, ".verdict/packs/lenses/examples/use.md", "sample\n")
	assertFile(t, LockFile, fmt.Sprintf(addLockFmt, "dir:src-packs/lenses", hash,
		"e1043904f8319891d6ff9537fd8628c70b455f0a9de1bc59f6897c72c75d0126"))
}

// TestAddSweepsStaleStagingSiblings proves a crashed re-add's temp and backup
// trees do not block the next add and are removed.
func TestAddSweepsStaleStagingSiblings(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	stale := []string{
		".verdict/packs/.lenses.tmp-crash",
		".verdict/packs/.lenses.old-crash",
	}
	writeFixture(t, repo, map[string]string{
		stale[0] + "/junk": "tmp\n",
		stale[1] + "/junk": "old\n",
	})
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("RunAdd: %s: %s", ref.Code, ref.Message)
	}
	for _, dir := range stale {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(dir))); !os.IsNotExist(err) {
			t.Fatalf("stale %s survived the add (stat err %v)", dir, err)
		}
	}
}

// TestAddRefusesSlugCollision proves a second pack that maps to the same
// vendored directory refuses before it overwrites the first row's tree.
func TestAddRefusesSlugCollision(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	sourcePack(t, repo, "src-packs/other",
		`{"schema_version":"1","name":"other/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`,
		map[string]string{"lenses/perf.md": "other body\n"})
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("first RunAdd: %s: %s", ref.Code, ref.Message)
	}
	_, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/other"})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("second RunAdd = %v, want bad-pack", ref)
	}
	if want := "would vendor over acme/lenses at .verdict/packs/lenses"; !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "perf body\n")
	lock, err := os.ReadFile(LockFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(lock), "other/lenses") {
		t.Fatalf("lock = %s, carries the refused row", lock)
	}
}

// TestAddRefusesUnlistedVendoredSlug proves a first add will not overwrite a
// directory that no lock row owns.
func TestAddRefusesUnlistedVendoredSlug(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "perf body\n")
	writeFixture(t, repo, map[string]string{
		".verdict/packs/lenses/pack.json":      `{"schema_version":"1","name":"other/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`,
		".verdict/packs/lenses/lenses/perf.md": "unlisted body\n",
	})
	_, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if want := ".verdict/packs/lenses is not named by .verdict/verdict.lock"; !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "unlisted body\n")
}

// TestAddRefusesUnrecordableDirLabel proves a dir path the lock grammar cannot
// spell refuses before the pack is vendored or any lock is written.
func TestAddRefusesUnrecordableDirLabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows forbids a control character in a directory name")
	}
	repo := t.TempDir()
	t.Chdir(repo)
	const dir = "src-packs/bad\x7flabel"
	lensesSource(t, repo, dir, "perf body\n")
	_, ref := RunAdd(context.Background(), &AddRequest{Source: dir})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if want := "cannot be recorded in .verdict/verdict.lock"; !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
	}
	if _, err := os.Stat(".verdict"); !os.IsNotExist(err) {
		t.Fatalf("refused add wrote .verdict (stat err %v)", err)
	}
}

// TestCommitAddRestoresTreeWhenLockFails proves a failed lock write rolls the
// vendored tree back to the previous one and leaves the old lock intact.
//
//nolint:revive // the swap, fail, and rollback sequence reads as one scenario
func TestCommitAddRestoresTreeWhenLockFails(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	lensesSource(t, repo, "src-packs/lenses", "old body\n")
	if _, ref := RunAdd(context.Background(), &AddRequest{Source: "src-packs/lenses"}); ref != nil {
		t.Fatalf("first RunAdd: %s: %s", ref.Code, ref.Message)
	}
	oldLock, err := os.ReadFile(LockFile)
	if err != nil {
		t.Fatal(err)
	}
	lensesSource(t, repo, "src-packs/lenses", "new body\n")
	p, ref := LoadDir(filepath.Join(repo, "src-packs", "lenses"))
	if ref != nil || p == nil {
		t.Fatalf("LoadDir = (%v, %v)", p, ref)
	}
	backup, ref := vendorPack(repo, filepath.Join(repo, "src-packs", "lenses"), "lenses", p)
	if ref != nil {
		t.Fatalf("vendorPack: %s: %s", ref.Code, ref.Message)
	}
	if backup == "" {
		t.Fatal("vendorPack returned no backup for a re-add")
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "new body\n")
	big := &Lock{SchemaVersion: LockSchemaVersion, Packs: []LockEntry{{
		Name: "acme/lenses", Source: "dir:" + strings.Repeat("x", MaxFileBytes),
		Version: "1.0.0", Hash: p.Hash,
	}}}
	big.Packs[0].Sig = LockSig(&big.Packs[0])
	if ref := commitAdd(repo, "lenses", backup, big); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("commitAdd = %v, want bad-pack", ref)
	}
	assertFile(t, ".verdict/packs/lenses/lenses/perf.md", "old body\n")
	assertFile(t, LockFile, string(oldLock))
	entries, err := os.ReadDir(filepath.Join(repo, ".verdict", "packs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "lenses" {
		t.Fatalf("pack root = %v, want only lenses", entries)
	}
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
//
//nolint:govet // test helper; field order reads as the scripted call fields
type gitCommander struct {
	t           *testing.T
	files       map[string]string
	calls       []gitCall
	cloneExit   int
	cloneStderr string
}

type gitCall struct {
	argv     []string
	dir      string
	env      []string
	deadline bool
}

func (c *gitCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	_, deadline := ctx.Deadline()
	c.calls = append(c.calls, gitCall{
		argv: slices.Clone(cmd.Argv), dir: cmd.Dir, env: slices.Clone(cmd.Env), deadline: deadline,
	})
	if slices.Contains(cmd.Argv, "clone") {
		dest := cmd.Argv[len(cmd.Argv)-1]
		writeFixture(c.t, dest, c.files)
		return core.Result{Stderr: c.cloneStderr, ExitCode: c.cloneExit}, nil
	}
	if slices.Contains(cmd.Argv, "rev-parse") {
		return core.Result{Stdout: addGitSha + "\n"}, nil
	}
	return core.Result{}, nil
}

// TestAddGitPinsHermeticArgvEnvAndRecordsSha proves the production clone argv
// carries every hermetic flag, the environment is scrubbed, and the lock
// records the resolved HEAD commit.
//
//nolint:cyclop,revive // the argv, env, and lock assertions read as one contract block
func TestAddGitPinsHermeticArgvEnvAndRecordsSha(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	const url = "https://example.invalid/acme/packs.git"
	cmdr := &gitCommander{t: t, files: map[string]string{
		"pack.json":                lockManifest,
		"lenses/perf.md":           "perf body\n",
		".git/config":              "planted metadata\n",
		".git/hooks/post-checkout": "#!/bin/sh\nexit 0\n",
	}}
	resp, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{
			Env: map[string]string{
				"PATH": "/usr/bin", "HOME": "/home/operator", "GIT_DIR": "/bad",
				"GIT_TEMPLATE_DIR": "/bad/templates", "GIT_SSH_COMMAND": "/bad/ssh",
				"GIT_EXEC_PATH": "/bad/exec", "GIT_PROXY_COMMAND": "/bad/proxy",
			},
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
	for i, call := range cmdr.calls {
		if !call.deadline {
			t.Errorf("git call %d carries no deadline", i)
		}
	}
	assertGitEnv(t, clone.env, tmp)
	prefix := []string{"git", "-c", "core.hooksPath=" + hooks, "-c", "protocol.file.allow=never"}
	wantRevParse := append(slices.Clone(prefix), "rev-parse", "HEAD")
	if got := cmdr.calls[1].argv; !slices.Equal(got, wantRevParse) {
		t.Errorf("rev-parse argv = %q, want %q", got, wantRevParse)
	}
	wantCheckout := append(slices.Clone(prefix), "checkout", "--detach", addGitSha)
	if got := cmdr.calls[2].argv; !slices.Equal(got, wantCheckout) {
		t.Errorf("checkout argv = %q, want %q", got, wantCheckout)
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

// TestAddRefusesOversizedClone proves the clone's file count and total bytes
// are checked against the pack caps before the loader sees the tree.
//
//nolint:revive // the two cap fixtures read as one block
func TestAddRefusesOversizedClone(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "file count",
			files: func() map[string]string {
				files := map[string]string{"pack.json": lockManifest, "lenses/perf.md": "perf body\n"}
				for i := range MaxFiles {
					files[fmt.Sprintf("junk/%03d.txt", i)] = "x"
				}
				return files
			}(),
			want: "the git clone carries more than 256 files",
		},
		{
			name: "total bytes",
			files: func() map[string]string {
				files := map[string]string{"pack.json": lockManifest, "lenses/perf.md": "perf body\n"}
				for i := range 17 {
					files[fmt.Sprintf("junk/%02d.bin", i)] = strings.Repeat("x", MaxFileBytes)
				}
				return files
			}(),
			want: "the git clone is over the 1048576-byte total cap",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmdr := &gitCommander{t: t, files: tc.files}
			_, ref := RunAdd(context.Background(), &AddRequest{
				Runtime: &core.Runtime{Commander: cmdr},
				Source:  "https://example.invalid/acme/packs.git",
			})
			if ref == nil || ref.Code != CodePackTooLarge {
				t.Fatalf("RunAdd = %v, want pack-too-large", ref)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", ref.Message, tc.want)
			}
		})
	}
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
		{"https://user@github.com/a/b.git", "", "", false},
		{"https://ghp_SUPERSECRET@host/a/b.git", "", "", false},
		{"https://us@er:S3CRET@host/x.git", "", "", false},
		{"https://user:p@ss@host/x.git", "", "", false},
		{"https://user%3AS3CRET@host/x.git", "", "", false},
		{"https://us%40er:S3CRET@host/x.git", "", "", false},
		{"http://user@host/a", "", "", false},
		{"git@github.com:a/b.git", "git@github.com:a/b.git", "", true},
		{"git@github.com:a/b.git@release/1", "git@github.com:a/b.git", "release/1", true},
		{"ssh://git@host/a/b@v1", "ssh://git@host/a/b", "v1", true},
		{"https://user:tok@host/repo.git", "", "", false},
		{"ssh://user:tok@host/repo.git", "", "", false},
		{"user:tok@host:repo.git", "", "", false},
		{"--upload-pack=x@y:z", "", "", false},
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

// TestAddRefusesCredentialedSource proves every URL userinfo shape refuses
// before the clone and before any write, and every rendered message masks the
// whole userinfo.
//
//nolint:gocognit,revive // the credential matrix reads as one block
func TestAddRefusesCredentialedSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		secret string
	}{
		{"https password", "https://oauth2:ghp_TOKEN@host/acme/packs.git", "ghp_TOKEN"},
		{"https token only", "https://ghp_SUPERSECRET@host/acme/packs.git", "ghp_SUPERSECRET"},
		{"second at", "https://us@er:S3CRET@host/x.git", "S3CRET"},
		{"at in password", "https://user:p@ss@host/x.git", "p@ss"},
		{"bare https user", "https://ghp_BAREUSER@host/x.git", "ghp_BAREUSER"},
		{"ssh password", "ssh://user:S3CRET@host/x", "S3CRET"},
		{"percent colon", "https://user%3AS3CRET@host/x.git", "S3CRET"},
		{"percent at", "https://us%40er:S3CRET@host/x.git", "S3CRET"},
		{"http userinfo", "http://ghp_SUPERSECRET@host/x.git", "ghp_SUPERSECRET"},
		{"scp password", "oauth2:S3CRET@host:acme/packs.git", "S3CRET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			t.Chdir(repo)
			cmdr := &gitCommander{t: t}
			_, ref := RunAdd(context.Background(), &AddRequest{
				Runtime: &core.Runtime{Commander: cmdr},
				Source:  tc.source,
			})
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("RunAdd(%q) = %v, want bad-pack", tc.source, ref)
			}
			if strings.Contains(ref.Message, tc.secret) {
				t.Fatalf("message = %q, carries %q", ref.Message, tc.secret)
			}
			if !strings.Contains(ref.Message, "***") {
				t.Fatalf("message = %q, want a fully masked userinfo", ref.Message)
			}
			if len(cmdr.calls) != 0 {
				t.Fatalf("git calls = %d, want none before the refusal", len(cmdr.calls))
			}
			if _, err := os.Stat(".verdict"); !os.IsNotExist(err) {
				t.Fatalf("refused add wrote .verdict (stat err %v)", err)
			}
		})
	}
}

// TestAddRedactsFailedCloneStderr proves a clone failure never renders the
// URL git echoed back with its userinfo intact.
func TestAddRedactsFailedCloneStderr(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	cmdr := &gitCommander{
		t:           t,
		cloneStderr: "fatal: unable to access 'https://er:S3CRET@example.invalid/acme/packs.git/': refused\n",
		cloneExit:   128,
	}
	_, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Source:  "https://example.invalid/acme/packs.git",
	})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if strings.Contains(ref.Message, "S3CRET") {
		t.Fatalf("message = %q, carries the clone credential", ref.Message)
	}
	if want := "https://***@example.invalid/acme/packs.git"; !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
	}
}

// TestRedactURLTextMasksEveryURLUserinfo proves a whole userinfo, including a
// second @, is masked in a free-form message.
func TestRedactURLTextMasksEveryURLUserinfo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ text, want string }{
		{"access 'https://user:tok@host/x'", "access 'https://***@host/x'"},
		{"access 'https://us@er:tok@host/x'", "access 'https://***@host/x'"},
		{"access 'https://user:p@ss@host/x'", "access 'https://***@host/x'"},
		{"no URL here", "no URL here"},
	} {
		if got := redactURLText(tc.text); got != tc.want {
			t.Errorf("redactURLText(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// TestAddRefusesLeadingDashSource proves a source git would parse as an option
// refuses as bad-pack before any clone argument exists.
func TestAddRefusesLeadingDashSource(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)
	cmdr := &gitCommander{t: t}
	_, ref := RunAdd(context.Background(), &AddRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Source:  "--upload-pack=x@y:z",
	})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("RunAdd = %v, want bad-pack", ref)
	}
	if !strings.Contains(ref.Message, "starts with '-'") {
		t.Fatalf("message = %q, want the leading-dash cause", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Fatalf("git calls = %d, want none", len(cmdr.calls))
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
