// Package index is the index verb's CLI adapter: subcommand and flag
// parsing, one call into the indexcore service, warning routing, envelope
// rendering, and exit mapping. Domain logic lives in internal/core/index.
package index

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires an index run.
type Request = readcommon.Request

// Run executes the index subcommands, returning the exit code.
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
	query    string
	from     string
	at       string
	symbol   string
	path     string
	since    string
	pr       string
	paths    []string
	commits  int
	minCount int
	limit    int
}

// request builds the typed service request.
func (inv *invocation) request(r *cli.Runner, req Request) *indexcore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Pr = inv.pr
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{}
	return &indexcore.Request{
		ReadRequest: read,
		Query:       inv.query,
		From:        inv.from,
		At:          inv.at,
		Symbol:      inv.symbol,
		Path:        inv.path,
		Since:       inv.since,
		Paths:       inv.paths,
		Commits:     inv.commits,
		MinCount:    inv.minCount,
		Limit:       inv.limit,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parse reads the subcommand and its flags.
func parse(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) == 0 {
		return invocation{}, r.ReportUsage(usageError(indexWants)), true
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case indexcore.QueryBuild:
		return parseBuild(r, rest)
	case indexcore.QueryStatus, indexcore.QueryPrune:
		if len(rest) > 0 {
			return invocation{}, r.ReportUsage(usageError(unknownFlag + rest[0] + "' for index " + sub)), true
		}
		return invocation{query: sub}, 0, false
	case indexcore.QueryCallers:
		return parseCallers(r, rest)
	case indexcore.QueryTestsFor:
		return parseTestsFor(r, rest)
	case indexcore.QueryCoChanged:
		return parseCoChanged(r, rest)
	case indexcore.QueryOwners:
		return parseOwners(r, rest)
	case indexcore.QueryBlastRadius:
		return parseBlastRadius(r, rest)
	default:
		return invocation{}, r.ReportUsage(usageError(unknownSubcommand + sub + "' (want " + indexSubcommands + ")")), true
	}
}

// indexWants is the shared "wants" phrase every index usage error carries.
const (
	indexWants        = "index wants build, status, prune, callers, tests-for, co-changed, owners, or blast-radius"
	indexSubcommands  = "build, status, prune, callers, tests-for, co-changed, owners, blast-radius"
	flagAt            = "--at"
	flagLimit         = "--limit"
	flagCommits       = "--commits"
	flagMinCount      = "--min-count"
	flagSince         = "--since"
	flagPrefix        = "-"
	unknownFlag       = "unknown flag '"
	unknownSubcommand = "unknown index subcommand '"
)

// parseTestsFor reads `index tests-for` positionals and flags.
func parseTestsFor(r *cli.Runner, args []string) (invocation, int, bool) {
	inv := invocation{query: indexcore.QueryTestsFor, limit: indexcore.DefaultLimit}
	for i := 0; i < len(args); {
		next, code, done := parseTestsForArg(r, args, i, &inv)
		if done {
			return inv, code, true
		}
		i = next
	}
	if (inv.path == "") == (inv.symbol == "") {
		return inv, r.ReportUsage(usageError(
			"usage: verdict index tests-for <path>|--symbol <pkg.Name> [--at <tree>] [--limit <n>]")), true
	}
	return inv, 0, false
}

// parseTestsForArg reads one tests-for argument and returns the next index.
func parseTestsForArg(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	a := args[i]
	switch {
	case a == "--symbol":
		v, next, u := cli.FlagValue(args, i, "--symbol", "<pkg.Name>")
		if u != nil {
			return 0, r.ReportUsage(u), true
		}
		inv.symbol = v
		return next + 1, 0, false
	case a == flagAt:
		return callerAt(r, args, i, inv)
	case a == flagLimit:
		return queryLimit(r, args, i, inv, indexcore.QueryTestsFor)
	case strings.HasPrefix(a, flagPrefix):
		return 0, r.ReportUsage(usageError(unknownFlag + a +
			"' for index tests-for (want --symbol <pkg.Name>, --at <tree>, --limit <n>)")), true
	default:
		if inv.path != "" {
			return 0, r.ReportUsage(usageError("index tests-for wants one path")), true
		}
		inv.path = a
		return i + 1, 0, false
	}
}

// parseCoChanged reads `index co-changed` positionals and flags.
func parseCoChanged(r *cli.Runner, args []string) (invocation, int, bool) {
	inv := invocation{
		query: indexcore.QueryCoChanged, limit: indexcore.DefaultCoChangeLimit,
		commits: indexcore.DefaultCoChangeCommits, minCount: indexcore.DefaultMinCount,
	}
	for i := 0; i < len(args); {
		next, code, done := parseCoChangedArg(r, args, i, &inv)
		if done {
			return inv, code, true
		}
		i = next
	}
	if inv.path == "" {
		return inv, r.ReportUsage(usageError(
			"usage: verdict index co-changed <path> [--commits <n>] [--since <dur|ref>] [--min-count <n>] [--limit <n>]")), true
	}
	return inv, 0, false
}

// parseCoChangedArg reads one co-changed argument and returns the next index.
func parseCoChangedArg(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	a := args[i]
	switch a {
	case flagCommits, flagMinCount:
		return coChangeCount(r, args, i, inv, indexcore.QueryCoChanged, a)
	case flagSince:
		v, next, u := cli.FlagValue(args, i, flagSince, "<dur|ref>")
		if u != nil {
			return 0, r.ReportUsage(u), true
		}
		inv.since = v
		return next + 1, 0, false
	case flagLimit:
		return queryLimit(r, args, i, inv, indexcore.QueryCoChanged)
	default:
		if strings.HasPrefix(a, flagPrefix) {
			return 0, r.ReportUsage(usageError(unknownFlag + a + "' for index co-changed" +
				" (want --commits <n>, --since <dur|ref>, --min-count <n>, --limit <n>)")), true
		}
		if inv.path != "" {
			return 0, r.ReportUsage(usageError("index co-changed wants one path")), true
		}
		inv.path = a
		return i + 1, 0, false
	}
}

// coChangeCount reads and validates --commits or --min-count.
func coChangeCount(r *cli.Runner, args []string, i int, inv *invocation, query, flag string) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, flag, "<n>")
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	n, ok := boundedInt(v, indexcore.MaxCommitWindow)
	if !ok {
		return 0, r.ReportUsage(usageError("index " + query + " wants " + flag + " from 1 to " +
			strconv.Itoa(indexcore.MaxCommitWindow))), true
	}
	if flag == flagCommits {
		inv.commits = n
	} else {
		inv.minCount = n
	}
	return next + 1, 0, false
}

// parseOwners reads `index owners` positionals and its --at tree.
func parseOwners(r *cli.Runner, args []string) (invocation, int, bool) {
	inv := invocation{query: indexcore.QueryOwners}
	for i := 0; i < len(args); {
		next, code, done := parseOwnerArg(r, args, i, &inv)
		if done {
			return inv, code, true
		}
		i = next
	}
	if inv.path == "" {
		return inv, r.ReportUsage(usageError("usage: verdict index owners <path> [--at <tree>]")), true
	}
	return inv, 0, false
}

// parseOwnerArg reads one owners argument and returns the next index.
func parseOwnerArg(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	a := args[i]
	switch {
	case a == flagAt:
		return callerAt(r, args, i, inv)
	case strings.HasPrefix(a, flagPrefix):
		return 0, r.ReportUsage(usageError(unknownFlag + a +
			"' for index owners (want --at <tree>)")), true
	default:
		if inv.path != "" {
			return 0, r.ReportUsage(usageError("index owners wants one path")), true
		}
		inv.path = a
		return i + 1, 0, false
	}
}

// parseBlastRadius reads `index blast-radius` flags: a PR or a path list.
func parseBlastRadius(r *cli.Runner, args []string) (invocation, int, bool) {
	inv := invocation{
		query: indexcore.QueryBlastRadius, limit: indexcore.DefaultLimit,
		commits: indexcore.DefaultCoChangeCommits, minCount: indexcore.DefaultMinCount,
	}
	for i := 0; i < len(args); {
		next, code, done := parseBlastArg(r, args, i, &inv)
		if done {
			return inv, code, true
		}
		i = next
	}
	if (inv.pr == "") == (len(inv.paths) == 0) {
		return inv, r.ReportUsage(usageError(blastUsage)), true
	}
	return inv, 0, false
}

// blastUsage is the one usage line both missing and doubled inputs print.
const blastUsage = "usage: verdict index blast-radius --pr <n>|--paths <path> [--paths <path>...] " +
	"[--at <tree>] [--limit <n>] [--commits <n>] [--since <dur|ref>] [--min-count <n>]"

// parseBlastArg reads one blast-radius argument and returns the next index.
//
//nolint:revive // one branch per flag keeps the argv contract readable
func parseBlastArg(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	a := args[i]
	switch a {
	case "--pr":
		v, next, u := cli.FlagValue(args, i, "--pr", "<pr>")
		if u != nil {
			return 0, r.ReportUsage(u), true
		}
		inv.pr = v
		return next + 1, 0, false
	case "--paths":
		v, next, u := cli.FlagValue(args, i, "--paths", "<path>")
		if u != nil {
			return 0, r.ReportUsage(u), true
		}
		inv.paths = append(inv.paths, v)
		return next + 1, 0, false
	case flagCommits, flagMinCount:
		return coChangeCount(r, args, i, inv, indexcore.QueryBlastRadius, a)
	case flagSince:
		v, next, u := cli.FlagValue(args, i, flagSince, "<dur|ref>")
		if u != nil {
			return 0, r.ReportUsage(u), true
		}
		inv.since = v
		return next + 1, 0, false
	case flagAt:
		return callerAt(r, args, i, inv)
	case flagLimit:
		return queryLimit(r, args, i, inv, indexcore.QueryBlastRadius)
	default:
		if strings.HasPrefix(a, flagPrefix) {
			return 0, r.ReportUsage(usageError(unknownFlag + a + "' for index blast-radius" +
				" (want --pr <n>, --paths <path>, --at <tree>, --limit <n>, --commits <n>, --since <dur|ref>, --min-count <n>)")), true
		}
		return 0, r.ReportUsage(usageError("index blast-radius takes --pr <n> or --paths <path>, not the positional '" + a + "'")), true
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
			return inv, r.ReportUsage(usageError(unknownFlag + a +
				"' for index build (want --from <dir>, --at <ref>)")), true
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
	case a == flagAt:
		return callerAt(r, args, i, inv)
	case a == flagLimit:
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
	v, next, u := cli.FlagValue(args, i, flagAt, "<tree>")
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	inv.at = v
	return next + 1, 0, false
}

// callerLimit reads and validates the --limit value.
func callerLimit(r *cli.Runner, args []string, i int, inv *invocation) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, flagLimit, "<n>")
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

// queryLimit reads and validates one subcommand's --limit value.
func queryLimit(r *cli.Runner, args []string, i int, inv *invocation, query string) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, flagLimit, "<n>")
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	n, ok := boundedInt(v, indexcore.MaxLimit)
	if !ok {
		return 0, r.ReportUsage(usageError("index " + query + " wants --limit from 1 to " +
			strconv.Itoa(indexcore.MaxLimit))), true
	}
	inv.limit = n
	return next + 1, 0, false
}

// boundedInt parses one positive integer no larger than the bound.
func boundedInt(v string, bound int) (int, bool) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > bound {
		return 0, false
	}
	return n, true
}

// usageError builds one plain usage error.
func usageError(msg string) *cli.UsageError { return &cli.UsageError{Message: msg} }
