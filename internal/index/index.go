// Package index is the index verb's CLI adapter: subcommand and flag
// parsing, one call into the indexcore service, warning routing, envelope
// rendering, and exit mapping. Domain logic lives in internal/core/index.
package index

import (
	"context"
	"fmt"
	"strconv"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires an index run.
type Request = readcommon.Request

// Run executes `index build|status|prune|callers`, returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	if code, done := r.Canceled(ctx, "index"); done {
		return code
	}
	parsed, code, done := parse(r, args)
	if done {
		return code
	}
	resp, ref := indexcore.Run(ctx, parsed.request(r, req))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("index", "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return 0
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return 0
}

// invocation is one parsed index command line.
type invocation struct {
	query  string
	from   string
	at     string
	symbol string
	limit  int
}

// request builds the typed service request.
func (inv invocation) request(r *cli.Runner, req Request) *indexcore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{}
	return &indexcore.Request{
		ReadRequest: read,
		Query:       inv.query,
		From:        inv.from,
		At:          inv.at,
		Symbol:      inv.symbol,
		Limit:       inv.limit,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parse reads the subcommand and its flags.
func parse(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) == 0 {
		return invocation{}, r.ReportUsage(usageError("index wants build, status, prune, or callers")), true
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case indexcore.QueryBuild:
		return parseBuild(r, rest)
	case indexcore.QueryStatus, indexcore.QueryPrune:
		if len(rest) > 0 {
			return invocation{}, r.ReportUsage(usageError("unknown flag '" + rest[0] + "' for index " + sub)), true
		}
		return invocation{query: sub}, 0, false
	case indexcore.QueryCallers:
		return parseCallers(r, rest)
	default:
		return invocation{}, r.ReportUsage(usageError("unknown index subcommand '" + sub + "' (want build, status, prune, callers)")), true
	}
}

// parseBuild reads `index build` flags.
//
//nolint:revive // one branch per flag keeps the argv contract readable
func parseBuild(r *cli.Runner, args []string) (invocation, int, bool) {
	inv := invocation{query: indexcore.QueryBuild}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--from":
			v, next, u := cli.FlagValue(args, i, a, "<dir>")
			if u != nil {
				return inv, r.ReportUsage(u), true
			}
			inv.from, i = v, next
		case "--at":
			v, next, u := cli.FlagValue(args, i, a, "<ref>")
			if u != nil {
				return inv, r.ReportUsage(u), true
			}
			inv.at, i = v, next
		default:
			return inv, r.ReportUsage(usageError("unknown flag '" + a + "' for index build (want --from <dir>, --at <ref>)")), true
		}
	}
	return inv, 0, false
}

// parseCallers reads `index callers` positionals and flags.
func parseCallers(r *cli.Runner, args []string) (invocation, int, bool) {
	inv := invocation{query: indexcore.QueryCallers, limit: indexcore.DefaultLimit}
	for i := 0; i < len(args); {
		next, code, done := parseCallerArg(r, args, i, &inv)
		if done {
			return inv, code, true
		}
		i = next
	}
	if inv.symbol == "" {
		return inv, r.ReportUsage(usageError("usage: verdict index callers <symbol> [--at <tree>] [--limit <n>]")), true
	}
	return inv, 0, false
}

// parseCallerArg reads one callers argument and returns the next index.
func parseCallerArg(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	a := args[i]
	switch {
	case a == "--at":
		return callerAt(r, args, i, inv)
	case a == "--limit":
		return callerLimit(r, args, i, inv)
	case len(a) > 0 && a[0] == '-':
		return 0, r.ReportUsage(usageError("unknown flag '" + a + "' for index callers (want --at <tree>, --limit <n>)")), true
	default:
		if inv.symbol != "" {
			return 0, r.ReportUsage(usageError("index callers wants one symbol")), true
		}
		inv.symbol = a
		return i + 1, 0, false
	}
}

// callerAt reads the --at value.
func callerAt(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, "--at", "<tree>")
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	inv.at = v
	return next + 1, 0, false
}

// callerLimit reads and validates the --limit value.
func callerLimit(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, "--limit", "<n>")
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > indexcore.MaxLimit {
		return 0, r.ReportUsage(usageError("--limit wants an integer from 1 to " + strconv.Itoa(indexcore.MaxLimit))), true
	}
	inv.limit = n
	return next + 1, 0, false
}

// usageError builds one plain usage error.
func usageError(msg string) *cli.UsageError { return &cli.UsageError{Message: msg} }
