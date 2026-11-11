package backtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// twoHunkRepo lands one PR that introduces two bad lines far apart, then a fix
// that deletes both in one commit, so the fix parses as two hunks.
func twoHunkRepo(t *testing.T) (r *repo, base, merge, fix string) {
	t.Helper()
	r = newRepo(t)
	r.write("a.txt", "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")
	base = r.commit("feat: base")
	r.switchNew("feature")
	r.write("a.txt", "one\ntwo-bug\nthree\nfour\nfive\nsix\nseven\neight\nnine-bug\nten\n")
	r.commit("feat: pass the values through")
	r.switchMain()
	merge = r.merge("feature", "Merge pull request #21 from acme/feature")
	r.write("a.txt", "one\ntwo-fixed\nthree\nfour\nfive\nsix\nseven\neight\nnine-fixed\nten\n")
	fix = r.commit("fix: repair both values")
	return r, base, merge, fix
}

// cancelAfter cancels the run when stop first matches a command, then runs
// later commands on a context that ignores the cancel: the service's own loop
// checks, not the killed child, decide whether any more git runs. Every
// command spawned after the cancel is recorded.
type cancelAfter struct {
	inner  core.Commander
	stop   func(*core.Command) bool
	cancel context.CancelFunc
	fired  bool
	after  [][]string
}

// Run delegates to the real runner, canceling once.
func (c *cancelAfter) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if c.fired {
		c.after = append(c.after, cmd.Argv)
	}
	detached := c.fired
	if !c.fired && c.stop(cmd) {
		c.fired = true
		c.cancel()
		detached = true
	}
	if detached {
		return c.inner.Run(context.WithoutCancel(ctx), cmd)
	}
	return c.inner.Run(ctx, cmd)
}

// twoMarkerRepo puts two added-only seeds in the window, so neither runs a
// line loop and the seed loop is the only thing between them. The older
// marker is returned so a test can see whether it started.
func twoMarkerRepo(t *testing.T) (r *repo, base, older string) {
	t.Helper()
	r = newRepo(t)
	r.write("main.go", "package main\n")
	base = r.commit("feat: base")
	r.write("main.go", "package main\n\n// one\n")
	older = r.commit("fix: add marker one")
	r.write("main.go", "package main\n\n// one\n// two\n")
	r.commit("fix: add marker two")
	return r, base, older
}

// TestMineCanceledBetweenSeeds proves the seed loop checks the context: the
// cancel lands while the first added-only seed is read, and the second seed
// would otherwise run to completion because nothing else checks between them.
func TestMineCanceledBetweenSeeds(t *testing.T) {
	r, base, older := twoMarkerRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cmdr := &cancelAfter{
		inner: coreexec.Runner{MissingTool: true}, cancel: cancel,
		stop: func(cmd *core.Command) bool { return slices.Contains(cmd.Argv, "--numstat") },
	}
	_, ref := Run(ctx, &Request{
		Runtime: &core.Runtime{Commander: cmdr}, RepoRoot: r.dir,
		Mode: modeMine, Since: base, Until: "HEAD",
	})
	if !cmdr.fired {
		t.Fatal("the cancel never fired")
	}
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("refusal = %+v; want canceled", ref)
	}
	for _, argv := range cmdr.after {
		if slices.Contains(argv, older) {
			t.Errorf("command for the next seed spawned after the cancel: %v", argv)
		}
	}
}

// TestMineCanceledBetweenLines proves the line loop checks the context: the
// cancel lands after the first hunk's anchor and before the second line's
// blame is spawned.
func TestMineCanceledBetweenLines(t *testing.T) {
	r, base, _, _ := twoHunkRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cmdr := &cancelAfter{
		inner: coreexec.Runner{MissingTool: true}, cancel: cancel,
		stop: func(cmd *core.Command) bool { return slices.Contains(cmd.Argv, "grep") },
	}
	_, ref := Run(ctx, &Request{
		Runtime: &core.Runtime{Commander: cmdr}, RepoRoot: r.dir,
		Mode: modeMine, Since: base, Until: "HEAD",
	})
	if !cmdr.fired {
		t.Fatal("the cancel never fired")
	}
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("refusal = %+v; want canceled", ref)
	}
	if len(cmdr.after) != 0 {
		t.Errorf("commands spawned after the cancel = %v; want none", cmdr.after)
	}
}

// truncatingCommander reports the capture cap on every command.
type truncatingCommander struct{}

// Run reports a capped stdout.
func (truncatingCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{StdoutTruncated: true}, nil
}

// TestMineWindowTooLarge proves a capped commit list refuses by name with the
// narrowing hint, not as a generic git failure.
func TestMineWindowTooLarge(t *testing.T) {
	st := &state{
		req:  &Request{Runtime: &core.Runtime{Commander: truncatingCommander{}}},
		root: t.TempDir(),
	}
	_, ref := st.rangeCommits(t.Context(), "aaaaaaa", "bbbbbbb")
	if ref == nil || ref.Code != "window-too-large" {
		t.Fatalf("refusal = %+v; want window-too-large", ref)
	}
	if !strings.Contains(ref.Message, "narrow --since") {
		t.Errorf("message = %q; want it to name --since", ref.Message)
	}
}

// cancelingCommander cancels the run, then reports a failed command.
type cancelingCommander struct {
	cancel context.CancelFunc
	err    error
}

// Run cancels first so the failure sees a done context.
func (c cancelingCommander) Run(context.Context, *core.Command) (core.Result, error) {
	c.cancel()
	return core.Result{ExitCode: 137, Stderr: "killed\n"}, c.err
}

// TestGitFailureReportsCancel proves a git step that fails after a cancel
// reports the cancel code, never a git failure message.
func TestGitFailureReportsCancel(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"exit", nil},
		{"spawn", errors.New("signal: killed")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			st := &state{
				req:  &Request{Runtime: &core.Runtime{Commander: cancelingCommander{cancel: cancel, err: tc.err}}},
				root: t.TempDir(),
			}
			_, ref := st.parents(ctx, "abcdef0")
			if ref == nil || ref.Code != "canceled" {
				t.Fatalf("refusal = %+v; want canceled", ref)
			}
		})
	}
}
