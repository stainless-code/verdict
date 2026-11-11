package readgolden_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/testenv"
)

// indexFixture is the six-file Go fixture the caller goldens query: three
// files in app, two in lib, and one command, with exact and lexical call
// sites of app.Target.
var indexFixture = map[string]string{
	"go.mod": "module example.com/m\n\ngo 1.27\n",
	"app/app.go": `package app

func Target() {}

func Caller() {
	Target()
}
`,
	"app/helper.go": `package app

func Helper() {
	Target()
}
`,
	"app/extra.go": `package app

var Extra = Target
`,
	"lib/lib.go": `package lib

import "example.com/m/app"

func UseApp() {
	app.Target()
}
`,
	"lib/other.go": `package lib

func Local() {}
`,
	"cmd/main.go": `package main

import "example.com/m/app"

func main() {
	app.Target()
}
`,
}

// indexRepo writes the fixture as one pinned commit and builds the index
// through the service, so the read-only caller legs start from a built store.
func indexRepo(t *testing.T, dir, _ string) {
	t.Helper()
	writeIndexFixture(t, dir)
	runIndexGit(t, dir, "init", "-q", "-b", "main")
	runIndexGit(t, dir, "add", "-A")
	runIndexGit(t, dir, "commit", "-qm", "index fixture")
	buildIndex(t, dir, indexEnv())
}

// writeIndexFixture writes the six Go files under dir.
func writeIndexFixture(t *testing.T, dir string) {
	t.Helper()
	for name, body := range indexFixture {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// indexEnv returns the hermetic child environment as the runtime snapshot.
func indexEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range testenv.Base(nil) {
		name, value, ok := cutEnv(kv)
		if ok {
			env[name] = value
		}
	}
	return env
}

// buildIndex builds the fixture's index through the service.
func buildIndex(t *testing.T, dir string, env map[string]string) {
	t.Helper()
	req := &indexcore.Request{
		Query: indexcore.QueryBuild,
		ReadRequest: core.ReadRequest{ //nolint:modernize // the embedded field stays keyed for readability
			Runtime:  &core.Runtime{Commander: coreexec.Runner{}, Env: env},
			RepoRoot: dir,
		},
	}
	if _, ref := indexcore.Run(context.Background(), req); ref != nil {
		t.Fatalf("index build: %v", ref)
	}
}

// runIndexGit runs one pinned git command in the fixture.
func runIndexGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_DATE":    "2024-04-01T00:00:00Z",
		"GIT_COMMITTER_DATE": "2024-04-01T00:00:00Z",
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// cutEnv splits one KEY=VALUE child environment entry.
func cutEnv(kv string) (string, string, bool) {
	for i := range len(kv) {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}

// TestIndexCallersGolden pins the caller read: exact rows inside app, lexical
// rows through the import edge, and the machine envelope.
func TestIndexCallersGolden(t *testing.T) {
	t.Parallel()
	args := []string{"index", "callers", "app.Target"}
	goldenRead(t, "index/callers-human", indexRepo, nil, nil, args...)
	args = []string{"index", "callers", "app.Target", "--json"}
	goldenRead(t, "index/callers-json", indexRepo, nil, nil, args...)
}

// TestIndexStatusGolden pins the status read: the stored-tree summary, the
// built state, and the envelope's status kind.
func TestIndexStatusGolden(t *testing.T) {
	t.Parallel()
	goldenRead(t, "index/status-human", indexRepo, nil, nil, "index", "status")
	goldenRead(t, "index/status-json", indexRepo, nil, nil, "index", "status", "--json")
}

// TestIndexPruneGolden pins the prune mutation: the four newest trees and
// their shards survive, and the oldest tree and its unique shard are deleted.
func TestIndexPruneGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"index/prune-human", []string{"index", "prune"}},
		{"index/prune-json", []string{"index", "prune", "--json"}},
	} {
		c := &golden.Case{Name: tc.name, Setup: indexPruneRepo, Args: tc.args}
		c.Run(t, harness)
	}
}

// indexPruneRepo builds five trees and stamps their files oldest to newest, so
// prune's modification-time order is fixture data rather than the clock.
func indexPruneRepo(t *testing.T, dir, _ string) {
	t.Helper()
	writeIndexFixture(t, dir)
	runIndexGit(t, dir, "init", "-q", "-b", "main")
	var trees []string
	for i := range 5 {
		body := "package app\n\nfunc Target" + strconv.Itoa(i) + "() {}\n"
		if err := os.WriteFile(filepath.Join(dir, "app", "target.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		runIndexGit(t, dir, "add", "-A")
		runIndexGit(t, dir, "commit", "-qm", "tree "+strconv.Itoa(i))
		trees = append(trees, runIndexGitOut(t, dir, "rev-parse", "HEAD^{tree}"))
		buildIndex(t, dir, indexEnv())
	}
	for i, tree := range trees {
		stamp := time.Unix(int64(1000+i), 0)
		path := filepath.Join(dir, ".verdict", "index", "trees", tree+".json")
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

// runIndexGitOut runs one pinned git command and returns its trimmed stdout.
func runIndexGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_DATE":    "2024-04-01T00:00:00Z",
		"GIT_COMMITTER_DATE": "2024-04-01T00:00:00Z",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// graphFixture is the candidate-test and co-change fixture: a target package
// with a same-package test, a package that references the symbol through an
// import edge, a package that only imports the target's package, and a
// CODEOWNERS chain whose last matching rule owns a/a.go.
var graphFixture = map[string]string{
	"go.mod": "module example.com/m\n\ngo 1.27\n",
	"a/a.go": `package a

func Target() {}

func Caller() {
	Target()
}
`,
	"a/other.go": `package a

func Other() {}
`,
	"a/a_test.go": `package a

import "testing"

func TestTarget(t *testing.T) {
	Target()
}
`,
	"b/b_test.go": `package b

import (
	"testing"

	"example.com/m/a"
)

func TestUses(t *testing.T) {
	a.Target()
}
`,
	"c/c.go": `package c

func Local() {}
`,
	"c/c_test.go": `package c

import (
	"testing"

	"example.com/m/a"
)

func TestLocal(t *testing.T) {
	a.Other()
}
`,
	".github/CODEOWNERS": "# global owner\n*       @everyone\n/a/*    @a-owner\na/*.go  @go-owner\n",
}

// graphRepo writes the fixture as four commits: a/a.go moves with b's test in
// two of them and with c's test in one, so the co-change counts are fixture
// data. The index is built for the last tree.
func graphRepo(t *testing.T, dir, _ string) {
	t.Helper()
	writeFiles(t, dir, graphFixture)
	runIndexGit(t, dir, "init", "-q", "-b", "main")
	runIndexGit(t, dir, "add", "-A")
	runIndexGit(t, dir, "commit", "-qm", "graph fixture")
	steps := []struct{ path, body string }{
		{"a/a.go", "package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {}\n"},
		{"b/b_test.go", graphFixture["b/b_test.go"] + "\nfunc TestMore(t *testing.T) {\n\ta.Target()\n}\n"},
		{"a/a.go", "package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {}\n\nfunc Third() {}\n"},
		{"c/c_test.go", graphFixture["c/c_test.go"] + "\nfunc TestMore(t *testing.T) {\n\ta.Other()\n}\n"},
		{"a/a.go", "package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n\nfunc Second() {}\n\nfunc Third() {}\n\nfunc Fourth() {}\n"},
		{"b/b_test.go", graphFixture["b/b_test.go"] + "\nfunc TestMore(t *testing.T) {\n\ta.Target()\n}\n\nfunc TestThird(t *testing.T) {\n\ta.Target()\n}\n"},
	}
	for i := 0; i < len(steps); i += 2 {
		writeFiles(t, dir, map[string]string{steps[i].path: steps[i].body, steps[i+1].path: steps[i+1].body})
		runIndexGit(t, dir, "add", "-A")
		runIndexGit(t, dir, "commit", "-qm", "graph step")
	}
	buildIndex(t, dir, indexEnv())
}

// TestIndexGraphGolden pins the candidate-test, co-change, and owner reads:
// all three via kinds, literal partner counts with the window, and the last
// matching CODEOWNERS rule.
func TestIndexGraphGolden(t *testing.T) {
	t.Parallel()
	goldenRead(t, "index/tests-for-human", graphRepo, nil, nil, "index", "tests-for", "a/a.go")
	goldenRead(t, "index/tests-for-json", graphRepo, nil, nil, "index", "tests-for", "a/a.go", "--json")
	goldenRead(t, "index/owners-human", graphRepo, nil, nil, "index", "owners", "a/a.go")
	goldenRead(t, "index/owners-json", graphRepo, nil, nil, "index", "owners", "a/a.go", "--json")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"index/co-changed-human", []string{"index", "co-changed", "a/a.go"}},
		{"index/co-changed-json", []string{"index", "co-changed", "a/a.go", "--json"}},
	} {
		c := &golden.Case{Name: tc.name, Setup: graphRepo, Args: tc.args}
		c.Run(t, harness)
	}
}
