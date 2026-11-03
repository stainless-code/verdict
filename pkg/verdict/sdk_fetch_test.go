package verdict_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/pkg/verdict"
)

// stubTools puts do-nothing executables on PATH: the fetch pre-check asks the
// host PATH for its tools while every spawn goes to the injected commander.
func stubTools(t *testing.T, tools ...string) {
	t.Helper()
	dir := t.TempDir()
	// The stubs only have to resolve through exec.LookPath (the commander is
	// a fake, so nothing runs them). Windows resolves a bare name only
	// through PATHEXT, so the stub there is a .bat file.
	name, body := "%s", "#!/bin/sh\nexit 1\n"
	if runtime.GOOS == "windows" {
		name, body = "%s.bat", "@exit /b 1\r\n"
	}
	for _, tool := range tools {
		path := filepath.Join(dir, fmt.Sprintf(name, tool))
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// sdkPRJSON is the `gh pr view` body the remote fixture answers with.
const sdkPRJSON = `{"number":7,"title":"Add notes","body":"Body text.","baseRefName":"main",` +
	`"headRefName":"work","headRefOid":"` + sdkHeadA + `","baseRefOid":"` + sdkBaseB + `",` +
	`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/7","commits":[],` +
	`"files":[{"path":"notes.txt","additions":1,"deletions":1}],` +
	`"additions":1,"deletions":1,"isDraft":false,"reviewDecision":"","state":"OPEN"}`

// sdkThreadsJSON is an empty authoritative review-thread read.
const sdkThreadsJSON = `{"data":{"repository":{"pullRequest":{"reviewThreads":` +
	`{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`

// fetchCommander scripts the remote fetch reads: gh for the PR, its diff, the
// file list, reviews/comments, and the GraphQL threads, plus the base-commit
// control snapshot (empty, so the bundle carries no controls).
func fetchCommander(t *testing.T) *fakeCommander {
	t.Helper()
	return &fakeCommander{t: t, rules: []cannedRule{
		{tokens: []string{"gh pr view", "--jq"}, result: verdict.CommandResult{Stdout: "notes.txt (+1/-1)\n"}},
		{tokens: []string{"gh pr view"}, result: verdict.CommandResult{Stdout: sdkPRJSON}},
		{tokens: []string{"gh pr diff"}, result: verdict.CommandResult{Stdout: sdkDiff}},
		{tokens: []string{"pulls/7/files"}, result: verdict.CommandResult{
			Stdout: `[{"filename":"notes.txt","status":"modified","additions":1,"deletions":1,"changes":2}]`,
		}},
		{tokens: []string{"pulls/7/reviews"}, result: verdict.CommandResult{Stdout: `[]`}},
		{tokens: []string{"pulls/7/comments"}, result: verdict.CommandResult{Stdout: `[]`}},
		{tokens: []string{"graphql"}, result: verdict.CommandResult{Stdout: sdkThreadsJSON}},
		{tokens: []string{"git", "ls-tree"}, result: verdict.CommandResult{}},
	}}
}

// localCommander scripts the local fetch git reads: the worktree probe, the
// branch and head, the base control snapshot, the diff, and its stats.
func localCommander(t *testing.T) *fakeCommander {
	t.Helper()
	return &fakeCommander{t: t, rules: []cannedRule{
		{tokens: []string{"rev-parse", "--is-inside-work-tree"}, result: verdict.CommandResult{Stdout: "true\n"}},
		{tokens: []string{"branch", "--show-current"}, result: verdict.CommandResult{Stdout: "work\n"}},
		{tokens: []string{"rev-parse", "HEAD"}, result: verdict.CommandResult{Stdout: sdkHeadA + "\n"}},
		{tokens: []string{"git", "ls-tree"}, result: verdict.CommandResult{}},
		{tokens: []string{"diff", "--numstat"}, result: verdict.CommandResult{Stdout: "1\t1\tnotes.txt\n"}},
		{tokens: []string{"diff", "--no-renames"}, result: verdict.CommandResult{Stdout: "notes.txt\n"}},
		{tokens: []string{"diff", "HEAD"}, result: verdict.CommandResult{Stdout: sdkDiff}},
		{tokens: []string{"ls-files", "--others"}, result: verdict.CommandResult{}},
	}}
}

// TestClientFetch proves the remote fetch through the SDK: the typed
// envelope, the data profile, the bundle files, the streamed reads, and that
// the verb really spawns gh instead of refusing for want of a commander.
func TestClientFetch(t *testing.T) {
	t.Chdir(t.TempDir())
	stubTools(t, "gh", "git", "curl", "glab")
	cmdr := fetchCommander(t)
	progress := &recordedProgress{}
	c := newSDKClient(progress, cmdr)
	defer c.Close()

	out, err := c.Fetch(context.Background(), &verdict.FetchRequest{
		Invocation:    sdkInvocation("7"),
		BudgetConsent: true,
	})
	requireNoError(t, err)
	if out.Repo != "acme/app" || out.PR != "7" || out.At != sdkAt {
		t.Errorf("envelope = repo %q pr %q at %q", out.Repo, out.PR, out.At)
	}
	if !strings.HasPrefix(out.Next, "feed SKILL.md + prompts/system.md") ||
		!strings.Contains(out.Next, ".review/7/PR.json") {
		t.Errorf("next = %q, want the review handoff", out.Next)
	}
	assertFetchedBundle(t, decodeData(t, out.Data))
	assertFetchedFiles(t, ".review/7", []string{
		"PR.json", "full.diff", "toc.txt", "roles.txt", "reviews.json", "inline.json", "threads.json",
	})
	for _, want := range []string{"» gh pr view acme/app#7", "» gh pr diff -> .review/7/full.diff"} {
		if !strings.Contains(progress.joinedHuman(), want) {
			t.Errorf("human = %q, want %q", progress.joinedHuman(), want)
		}
	}
}

// assertFetchedFiles proves every named artifact landed non-empty.
func assertFetchedFiles(t *testing.T, dir string, names []string) {
	t.Helper()
	for _, name := range names {
		if readText(t, dir+"/"+name) == "" {
			t.Errorf("%s/%s is empty, want the fetched artifact", dir, name)
		}
	}
}

// assertFetchedBundle proves the remote fetch data profile.
func assertFetchedBundle(t *testing.T, doc map[string]any) {
	t.Helper()
	assertFields(t, doc, map[string]any{"dir": ".review/7", "head_sha": sdkHeadA, "base_sha": sdkBaseB})
	assertFields(t, asMap(t, doc["capabilities"], "capabilities"), map[string]any{"provider": "github"})
	assertFields(t, asMap(t, doc["controls"], "controls"), map[string]any{"policy": "base", "sha": sdkBaseB})
	assertNumbers(t, doc, map[string]float64{
		"files": 1, "additions": 1, "deletions": 1, "hunks": 1, "diff_bytes": 146,
	})
	artifacts := asMap(t, doc["artifacts"], "artifacts")
	assertFields(t, artifacts, map[string]any{"PR.json": ".review/7/PR.json", "full.diff": ".review/7/full.diff"})
}

// TestClientFetchMissingTool proves the fetch pre-check on the default
// runner: with no gh on PATH the verb fails as the typed missing-tool error
// rather than a fetch refusal.
func TestClientFetchMissingTool(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	c := newSDKClient(nil, nil)
	defer c.Close()

	_, err := c.Fetch(context.Background(), &verdict.FetchRequest{
		Invocation:    sdkInvocation("7"),
		BudgetConsent: true,
	})
	missing, ok := errors.AsType[*verdict.MissingToolError](err)
	if !ok {
		t.Fatalf("err = %v, want *verdict.MissingToolError", err)
	}
	if missing.Tool != "gh" {
		t.Errorf("tool = %q, want gh", missing.Tool)
	}
}

// stubFetchTools installs shell stubs on PATH so the default fetch runner
// spawns them: gh answers the PR reads from fixture files, and git answers the
// base control snapshot with nothing.
func stubFetchTools(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("pr.json", sdkPRJSON, 0o644)
	write("pr.diff", sdkDiff, 0o644)
	write("files.stat", "notes.txt (+1/-1)\n", 0o644)
	write("files.json", `[{"filename":"notes.txt","status":"modified",`+
		`"additions":1,"deletions":1,"changes":2}]`, 0o644)
	write("threads.json", sdkThreadsJSON, 0o644)
	write("gh", `#!/bin/sh
case "$*" in
*"pr view"*"--jq"*) cat `+dir+`/files.stat ;;
*"pr view"*) cat `+dir+`/pr.json ;;
*"pr diff"*) cat `+dir+`/pr.diff ;;
*"pulls/7/files"*) cat `+dir+`/files.json ;;
*"pulls/7/reviews"*|*"pulls/7/comments"*) printf '[]' ;;
*graphql*) cat `+dir+`/threads.json ;;
*) printf '{}' ;;
esac
`, 0o755)
	write("git", "#!/bin/sh\nexit 0\n", 0o755)
	// The stubs shadow the host tools; the host PATH stays behind them so the
	// scripts keep their own plumbing (cat, printf).
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestClientFetchDefaultRunner proves the production fetch posture through the
// SDK: a client with no injected commander must still fetch, which means New
// selected a runner carrying the LookPath seam fetchcore requires.
func TestClientFetchDefaultRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the default fetch runner spawns POSIX shell stubs")
	}
	t.Chdir(t.TempDir())
	stubFetchTools(t)
	progress := &recordedProgress{}
	c := verdict.New(verdict.Options{
		Now:      func() time.Time { return sdkNow },
		Progress: progress,
	})
	defer c.Close()

	out, err := c.Fetch(context.Background(), &verdict.FetchRequest{
		Invocation:    sdkInvocation("7"),
		BudgetConsent: true,
	})
	requireNoError(t, err)
	assertFetchedBundle(t, decodeData(t, out.Data))
	assertFetchedFiles(t, ".review/7", []string{"PR.json", "full.diff", "reviews.json", "threads.json"})
	if !strings.Contains(progress.joinedHuman(), "» gh pr view acme/app#7") {
		t.Errorf("human = %q, want the streamed reads", progress.joinedHuman())
	}
}

// TestClientFetchLocal proves the local fetch through the SDK: the worktree
// bundle, its data profile, the diff-derived stats, and the written source
// stamp.
func TestClientFetchLocal(t *testing.T) {
	t.Chdir(t.TempDir())
	stubTools(t, "git")
	cmdr := localCommander(t)
	progress := &recordedProgress{}
	c := newSDKClient(progress, cmdr)
	defer c.Close()

	out, err := c.FetchLocal(context.Background(), &verdict.FetchLocalRequest{
		Invocation:    sdkInvocation("probe"),
		BudgetConsent: true,
	})
	requireNoError(t, err)
	if out.Repo != "acme/app" || out.At != sdkAt {
		t.Errorf("envelope = repo %q at %q", out.Repo, out.At)
	}
	if !strings.Contains(out.Next, ".review/local-probe/source.json") {
		t.Errorf("next = %q, want the local review handoff", out.Next)
	}
	assertLocalBundle(t, decodeData(t, out.Data))
	assertFetchedFiles(t, ".review/local-probe", []string{"source.json", "full.diff", "toc.txt", "roles.txt"})
	if !strings.Contains(readText(t, ".review/local-probe/source.json"), sdkHeadA) {
		t.Error("source.json does not name the head commit")
	}
	if !strings.Contains(progress.joinedHuman(), "done: .review/local-probe/source.json") {
		t.Errorf("human = %q, want the local fetch summary", progress.joinedHuman())
	}
}

// TestClientFetchLocalDefaultRunner proves the local fetch on the production
// runner: a real git checkout and no injected commander still produces the
// worktree bundle.
func TestClientFetchLocalDefaultRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture drives a POSIX git checkout")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	sdkGitRepo(t, dir, map[string]string{"notes.txt": "top\nold line\nbottom\n"})
	writeFile("notes.txt", "top\nnew line\nbottom\n")
	c := verdict.New(verdict.Options{Now: func() time.Time { return sdkNow }})
	defer c.Close()

	out, err := c.FetchLocal(context.Background(), &verdict.FetchLocalRequest{
		Invocation:    sdkInvocation("probe"),
		BudgetConsent: true,
	})
	requireNoError(t, err)
	doc := decodeData(t, out.Data)
	assertFields(t, doc, map[string]any{"dir": ".review/local-probe", "kind": "worktree"})
	assertNumbers(t, doc, map[string]float64{"files": 1, "additions": 1, "deletions": 1})
	assertFetchedFiles(t, ".review/local-probe", []string{"source.json", "full.diff", "toc.txt"})
	if !strings.Contains(readText(t, ".review/local-probe/full.diff"), "+new line") {
		t.Error("full.diff does not carry the worktree change")
	}
}

// assertLocalBundle proves the local fetch data profile.
func assertLocalBundle(t *testing.T, doc map[string]any) {
	t.Helper()
	assertFields(t, doc, map[string]any{"dir": ".review/local-probe", "kind": "worktree", "base_sha": sdkHeadA})
	assertNumbers(t, doc, map[string]float64{"files": 1, "additions": 1, "deletions": 1, "hunks": 1, "diff_bytes": 146})
	if omitted := asSlice(t, doc["omitted"], "omitted"); len(omitted) != 0 {
		t.Errorf("omitted = %v, want the whole diff in bounds", omitted)
	}
}
