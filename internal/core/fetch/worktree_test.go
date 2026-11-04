package fetchcore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// worktreeTestHead and worktreeTestStash are the canned commits for the
// isolation tests: HEAD plus the stash-create snapshot of the dirty tree.
const (
	worktreeTestHead  = "cccccccccccccccccccccccccccccccccccccccc"
	worktreeTestStash = "dddddddddddddddddddddddddddddddddddddddd"
)

// worktreeTestDiff is the canned bundle diff for the isolation tests.
const worktreeTestDiff = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"

// worktreeReq builds a fetch-local request with the worktree flag set,
// mirroring the TestRunLocalWorktree harness.
func worktreeReq(cmdr *scriptCommander, human, diag *bytes.Buffer, streamed *[]string) *core.FetchLocalRequest {
	root := ""
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fetchTestTime },
			Progress:  testProgress{warnings: streamed},
			Commander: cmdr,
		},
		Repo: "o/r",
		Pr:   "mine",
		Root: &root,
	}
	return &core.FetchLocalRequest{
		ReadRequest: read,
		Flags:       core.FetchLocalFlags{Worktree: true, HeadControls: true},
		Effective:   core.Defaults(),
		Warnings:    []string{},
		Human:       human,
		Diag:        diag,
	}
}

// TestRunLocalWorktreeDirty proves the default dirty mode isolates through
// the Commander seam: stash-create snapshots the tree, the worktree checks
// out the snapshot, and every bundle read runs git -C against the checkout
// while the untracked advisory stays live. Cleanup removes the checkout.
func TestRunLocalWorktreeDirty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wt := ".review/.worktree-mine"
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":                                           {stdout: "true\n"},
			"git\x00branch\x00--show-current":                                                     {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                                                            {stdout: worktreeTestHead + "\n"},
			"git\x00stash\x00create":                                                              {stdout: worktreeTestStash + "\n"},
			"git\x00worktree\x00add\x00--detach\x00" + wt + "\x00" + worktreeTestStash:            {stdout: ""},
			"git\x00-C\x00" + wt + "\x00diff\x00" + worktreeTestHead + "\x00" + worktreeTestStash: {stdout: worktreeTestDiff},
			"git\x00-C\x00" + wt + "\x00diff\x00--no-renames\x00--name-only\x00" + worktreeTestHead + "\x00" + worktreeTestStash: {stdout: "a.txt\n"},
			"git\x00-C\x00" + wt + "\x00diff\x00--numstat\x00" + worktreeTestHead + "\x00" + worktreeTestStash:                   {stdout: "1\t1\ta.txt\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":                                                                  {stdout: ""},
			"git\x00worktree\x00remove\x00--force\x00" + wt:                                                                      {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	resp, err := RunLocal(context.Background(), worktreeReq(cmdr, &human, &diag, &streamed))
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	if resp.Data == nil || !resp.Data.Local || resp.Data.Kind != "worktree" {
		t.Fatalf("data = %+v", resp.Data)
	}
	// Dirty mode keeps its null-head record: the snapshot commit is a
	// read-side helper, never the bundle's HEAD authority.
	if resp.Data.HeadSHA != "" || resp.Data.BaseSHA != worktreeTestHead {
		t.Errorf("heads = %q %q", resp.Data.HeadSHA, resp.Data.BaseSHA)
	}
	assertLocalFiles(t, worktreeTestDiff)
	assertWorktreeLine(t, &human, wt)
	assertWorktreeLifecycle(t, cmdr, wt, worktreeTestStash)
	assertArgvContains(t, cmdr, []string{
		"git stash create",
		"git -C " + wt + " diff " + worktreeTestHead + " " + worktreeTestStash,
	})
}

// TestRunLocalWorktreeSkipClean proves a clean tree skips isolation: with
// no uncommitted changes stash-create prints nothing, so the run proceeds
// classically with no checkout, no worktree line, and no cleanup.
func TestRunLocalWorktreeSkipClean(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":          {stdout: "true\n"},
			"git\x00branch\x00--show-current":                    {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                           {stdout: worktreeTestHead + "\n"},
			"git\x00stash\x00create":                             {stdout: ""},
			"git\x00diff\x00HEAD":                                {stdout: ""},
			"git\x00diff\x00--no-renames\x00--name-only\x00HEAD": {stdout: ""},
			"git\x00diff\x00--numstat\x00HEAD":                   {stdout: ""},
			"git\x00ls-files\x00--others\x00--exclude-standard":  {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	if _, err := RunLocal(context.Background(), worktreeReq(cmdr, &human, &diag, &streamed)); err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	if got := human.String(); strings.Contains(got, "worktree:") {
		t.Errorf("clean tree prints a worktree line:\n%s", got)
	}
	assertNoWorktreeArgv(t, cmdr)
}

// TestRunLocalWorktreeRange proves a committed range checks out its head
// commit and reads the explicit pair from the checkout.
func TestRunLocalWorktreeRange(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wt := ".review/.worktree-mine"
	base := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	head := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":                                                 {stdout: "true\n"},
			"git\x00branch\x00--show-current":                                                           {stdout: "main\n"},
			"git\x00rev-parse\x00--verify\x00a^{commit}":                                                {stdout: base + "\n"},
			"git\x00rev-parse\x00--verify\x00b^{commit}":                                                {stdout: head + "\n"},
			"git\x00worktree\x00add\x00--detach\x00" + wt + "\x00" + head:                               {stdout: ""},
			"git\x00-C\x00" + wt + "\x00diff\x00" + base + "\x00" + head:                                {stdout: worktreeTestDiff},
			"git\x00-C\x00" + wt + "\x00diff\x00--no-renames\x00--name-only\x00" + base + "\x00" + head: {stdout: "a.txt\n"},
			"git\x00-C\x00" + wt + "\x00diff\x00--numstat\x00" + base + "\x00" + head:                   {stdout: "1\t1\ta.txt\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":                                         {stdout: ""},
			"git\x00worktree\x00remove\x00--force\x00" + wt:                                             {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	root := ""
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fetchTestTime },
			Progress:  testProgress{warnings: &streamed},
			Commander: cmdr,
		},
		Repo: "o/r",
		Pr:   "mine",
		Root: &root,
	}
	req := &core.FetchLocalRequest{
		ReadRequest: read,
		Flags:       core.FetchLocalFlags{Worktree: true, RangeSpec: "a..b", HeadControls: true},
		Effective:   core.Defaults(),
		Warnings:    []string{},
		Human:       &human,
		Diag:        &diag,
	}
	resp, err := RunLocal(context.Background(), req)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	if resp.Data == nil || resp.Data.Kind != "range" {
		t.Fatalf("data = %+v", resp.Data)
	}
	assertArgvContains(t, cmdr, []string{
		"git worktree add --detach " + wt + " " + head,
		"git -C " + wt + " diff " + base + " " + head,
		"git worktree remove --force " + wt,
	})
}

// TestRunLocalWorktreeStaged proves staged mode synthesizes a commit for
// the index tree and reads the checkout against the parent HEAD.
func TestRunLocalWorktreeStaged(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wt := ".review/.worktree-mine"
	tree := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	synth := "ffffffffffffffffffffffffffffffffffffffff"
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree": {stdout: "true\n"},
			"git\x00branch\x00--show-current":           {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                  {stdout: worktreeTestHead + "\n"},
			"git\x00write-tree":                         {stdout: tree + "\n"},
			"git\x00commit-tree\x00" + tree + "\x00-p\x00" + worktreeTestHead + "\x00-m\x00" + msgWorktreeCommit:     {stdout: synth + "\n"},
			"git\x00worktree\x00add\x00--detach\x00" + wt + "\x00" + synth:                                           {stdout: ""},
			"git\x00-C\x00" + wt + "\x00diff\x00" + worktreeTestHead + "\x00" + synth:                                {stdout: worktreeTestDiff},
			"git\x00-C\x00" + wt + "\x00diff\x00--no-renames\x00--name-only\x00" + worktreeTestHead + "\x00" + synth: {stdout: "a.txt\n"},
			"git\x00-C\x00" + wt + "\x00diff\x00--numstat\x00" + worktreeTestHead + "\x00" + synth:                   {stdout: "1\t1\ta.txt\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":                                                      {stdout: ""},
			"git\x00worktree\x00remove\x00--force\x00" + wt:                                                          {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	root := ""
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fetchTestTime },
			Progress:  testProgress{warnings: &streamed},
			Commander: cmdr,
		},
		Repo: "o/r",
		Pr:   "mine",
		Root: &root,
	}
	req := &core.FetchLocalRequest{
		ReadRequest: read,
		Flags:       core.FetchLocalFlags{Worktree: true, Staged: true, HeadControls: true},
		Effective:   core.Defaults(),
		Warnings:    []string{},
		Human:       &human,
		Diag:        &diag,
	}
	if _, err := RunLocal(context.Background(), req); err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	assertArgvContains(t, cmdr, []string{
		"git commit-tree " + tree + " -p " + worktreeTestHead + " -m " + msgWorktreeCommit,
		"git worktree add --detach " + wt + " " + synth,
		"git -C " + wt + " diff " + worktreeTestHead + " " + synth,
		"git worktree remove --force " + wt,
	})
}

// requireRefusal proves err is the typed refusal with the wanted code,
// failing the test otherwise.
func requireRefusal(t *testing.T, err error, code string) *core.RefusalError {
	t.Helper()
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		t.Fatalf("error = %v, want typed refusal %q", err, code)
	}
	if ref.Code != code {
		t.Fatalf("refusal code = %q, want %q (%s)", ref.Code, code, ref.Message)
	}
	if ref.Message == "" {
		t.Fatalf("refusal %q carries no message", code)
	}
	return ref
}

// assertWorktreeLine proves the human stream reports the checkout
// location (the on-demand report this case requires).
func assertWorktreeLine(t *testing.T, human *bytes.Buffer, wt string) {
	t.Helper()
	if got := human.String(); !strings.Contains(got, "worktree: "+wt+" (temporary isolated checkout; removed on exit)") {
		t.Errorf("human lines miss the worktree location:\n%s", got)
	}
}

// assertWorktreeLifecycle proves the checkout lifecycle fired: the
// worktree add for the target plus the remove, with the remove as the
// last argv (cleanup runs even on the refusal paths).
func assertWorktreeLifecycle(t *testing.T, cmdr *scriptCommander, wt, target string) {
	t.Helper()
	assertArgvContains(t, cmdr, []string{
		"git worktree add --detach " + wt + " " + target,
		"git worktree remove --force " + wt,
	})
	if len(cmdr.calls) == 0 || strings.Join(cmdr.calls[len(cmdr.calls)-1], " ") != "git worktree remove --force "+wt {
		t.Errorf("cleanup is not the last argv: %q", cmdr.calls)
	}
}

// assertNoWorktreeArgv proves a run never touches the worktree machinery.
func assertNoWorktreeArgv(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	for _, c := range cmdr.calls {
		for _, tok := range c {
			if tok == "worktree" {
				t.Errorf("run fires worktree argv: %q", cmdr.calls)
			}
		}
	}
}

// TestRunLocalWorktreeAddFails proves a worktree that cannot be created
// refuses typed worktree-failed, scrubs the half-state, and writes no
// bundle.
func TestRunLocalWorktreeAddFails(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wt := ".review/.worktree-mine"
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree": {stdout: "true\n"},
			"git\x00branch\x00--show-current":           {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                  {stdout: worktreeTestHead + "\n"},
			"git\x00stash\x00create":                    {stdout: worktreeTestStash + "\n"},
			"git\x00worktree\x00add\x00--detach\x00" + wt + "\x00" + worktreeTestStash: {
				stdout: "", stderr: "fatal: '" + wt + "' already exists\n", fail: true,
			},
			"git\x00worktree\x00remove\x00--force\x00" + wt: {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(context.Background(), worktreeReq(cmdr, &human, &diag, &streamed))
	ref := requireRefusal(t, err, codeWorktreeFailed)
	if !strings.Contains(ref.Message, readcommon.ShortSHA(worktreeTestStash)) {
		t.Errorf("refusal misses the short target: %q", ref.Message)
	}
	if got := human.String(); strings.Contains(got, "worktree:") {
		t.Errorf("failed setup prints a worktree line:\n%s", got)
	}
	assertArgvContains(t, cmdr, []string{"git worktree remove --force " + wt})
	if _, statErr := os.Stat(".review/local-mine"); !os.IsNotExist(statErr) {
		t.Errorf("failed setup writes a bundle")
	}
}

// TestRunLocalWorktreeDiffFailsCleansUp proves a failure after the checkout
// still removes it: the deferred cleanup fires on the refusal path.
func TestRunLocalWorktreeDiffFailsCleansUp(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wt := ".review/.worktree-mine"
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":                                {stdout: "true\n"},
			"git\x00branch\x00--show-current":                                          {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                                                 {stdout: worktreeTestHead + "\n"},
			"git\x00stash\x00create":                                                   {stdout: worktreeTestStash + "\n"},
			"git\x00worktree\x00add\x00--detach\x00" + wt + "\x00" + worktreeTestStash: {stdout: ""},
			"git\x00-C\x00" + wt + "\x00diff\x00" + worktreeTestHead + "\x00" + worktreeTestStash: {
				stdout: "", stderr: "fatal: bad object\n", fail: true,
			},
			"git\x00worktree\x00remove\x00--force\x00" + wt: {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(context.Background(), worktreeReq(cmdr, &human, &diag, &streamed))
	if ref := requireRefusal(t, err, "diff-failed"); !strings.Contains(ref.Message, "git diff failed") {
		t.Errorf("refusal misses the diff cause: %q", ref.Message)
	}
	assertArgvContains(t, cmdr, []string{"git worktree remove --force " + wt})
	if got := human.String(); !strings.Contains(got, "worktree: "+wt) {
		t.Errorf("refused run misses the reported location:\n%s", got)
	}
}

// TestRunLocalWorktreeCanceledBeforeStart proves a canceled context
// refuses before any spawn, so no checkout can leak.
func TestRunLocalWorktreeCanceledBeforeStart(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var human, diag bytes.Buffer
	var streamed []string
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunLocal(ctx, worktreeReq(cmdr, &human, &diag, &streamed))
	if ref := requireRefusal(t, err, "canceled"); !strings.Contains(ref.Message, "canceled") {
		t.Errorf("refusal misses the cancel cause: %q", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("canceled run spawns: %q", cmdr.calls)
	}
}
