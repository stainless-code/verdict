package contenttree

import (
	"context"
	"io"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/jq"
)

// outputCap bounds a git read that carries one id or an error line.
const outputCap = 64 << 10

// listCap bounds a path listing or a batch of ids, and one blob read whole
// (a tracked file under verdict's own state, restored into a snapshot).
const listCap = 256 << 20

// The git subcommands and flags used more than once.
const (
	cmdLsFiles    = "ls-files"
	cmdHashObject = "hash-object"
	cmdWorktree   = "worktree"
	flagZ         = "-z"
	nul           = "\x00"
)

// off is the value that turns a boolean setting off.
const off = "false"

// byteExact is the configuration every git call here adds to the shared
// hook-free settings ([gitcmd.Env]): no line-ending conversion, so a hash
// names the bytes on disk. It rides GIT_CONFIG_COUNT, which outranks every
// config file. No call here runs a filter at all: hashing is --no-filters
// and no call writes a working tree.
var byteExact = []string{
	"core.autocrlf", off,
	"core.eol", "lf",
	"core.safecrlf", off,
}

// hashStdin writes the bytes r yields as one blob, unfiltered.
func (g Git) hashStdin(ctx context.Context, r io.Reader) (string, error) {
	cmd := g.command("", cmdHashObject, "-w", "--no-filters", "--stdin")
	cmd.Stdin = r
	res, err := g.check(ctx, cmd)
	if err != nil {
		return "", err
	}
	return objectID(cmdHashObject, res.Stdout)
}

// run runs git against the throwaway index (none when index is empty) and
// turns a nonzero exit into an error naming git's reason.
func (g Git) run(ctx context.Context, index string, args ...string) (core.Result, error) {
	return g.check(ctx, g.command(index, args...))
}

// runEnv runs git with extra added to the environment and turns a nonzero
// exit into an error naming git's reason.
func (g Git) runEnv(ctx context.Context, extra []string, args ...string) (core.Result, error) {
	cmd := g.command("", args...)
	cmd.Env = append(cmd.Env, extra...)
	return g.check(ctx, cmd)
}

func (g Git) check(ctx context.Context, cmd *core.Command) (core.Result, error) {
	sub := gitcmd.Subcommand(cmd.Argv)
	res, err := g.Cmdr.Run(ctx, cmd)
	switch {
	case err != nil:
		return res, jq.Sealed("git "+sub, err)
	case res.ExitCode != 0:
		reason, _, _ := strings.Cut(strings.TrimSpace(res.Stderr), "\n")
		return res, jq.NewErrf("git %s exited %d: %s", sub, res.ExitCode, reason)
	case res.StdoutTruncated:
		return res, jq.NewErrf("git %s printed more than %d bytes", sub, cmd.OutputCap)
	}
	return res, nil
}

func (g Git) exec(ctx context.Context, args ...string) (core.Result, error) {
	res, err := g.Cmdr.Run(ctx, g.command("", args...))
	if err != nil {
		return res, jq.Sealed("git "+args[0], err)
	}
	return res, nil
}

// command builds the hook-free `git -C <root> args...` ([gitcmd.Argv]) with
// the byte-exact configuration and, when index is set, the throwaway index.
// The inherited location variables are gone ([gitcmd.Env]), so the index
// set here is the only one any call reads or writes.
func (g Git) command(index string, args ...string) *core.Command {
	env := gitcmd.WithConfig(gitcmd.Env(g.Env), byteExact...)
	if index != "" {
		env = append(env, "GIT_INDEX_FILE="+index)
	}
	return &core.Command{Argv: gitcmd.Argv(g.Root, args...), Env: env, OutputCap: outputCap}
}
