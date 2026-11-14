// Package risk is the risk verb's CLI adapter: argv parsing, one call into the
// risk service, warning routing, envelope rendering, and exit mapping. Domain
// logic lives in internal/core/risk.
package risk

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	riskcore "github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a risk run.
type Request = readcommon.Request

// usageRisk is the verb's own synopsis, repeated in every usage error.
const usageRisk = "usage: verdict risk <owner/repo> [--status open|all] [--limit <n>] [--json]"

// Run executes `risk <owner/repo> [--status open|all] [--limit <n>] [--json]`,
// returning the process exit code. The queue is advisory: it exits 0 whenever
// it renders.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	opts, code, done := parseFlags(r, req.Repo, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "risk"); done {
		return code
	}
	if req.Repo == "" {
		return r.ReportUsage(&cli.UsageError{Message: usageRisk})
	}
	resp, ref := riskcore.Run(ctx, riskRequest(r, req, opts))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("risk", resp.Repo, "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return cli.ExitOK
}

// riskRequest injects the runner's clock and progress observer into the typed
// service request.
func riskRequest(r *cli.Runner, req Request, opts options) *riskcore.QueueRequest {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	return &riskcore.QueueRequest{
		ReadRequest: read,
		Status:      opts.status,
		Limit:       opts.limit,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// options carries the parsed queue filter and bound.
type options struct {
	status string
	limit  int
}

// parseFlags parses the risk flags: the two value flags and the global --json
// the front door already stripped.
func parseFlags(r *cli.Runner, repo string, args []string) (options, int, bool) {
	opts := options{status: riskcore.QueueStatusOpen, limit: riskcore.DefaultQueueLimit}
	for i := 0; i < len(args); i++ {
		next, code, done := applyFlag(r, repo, args, i, &opts)
		if done {
			return opts, code, true
		}
		i = next
	}
	return opts, 0, false
}

// applyFlag parses the flag at args[i] into opts and returns the index of its
// last consumed token.
func applyFlag(r *cli.Runner, repo string, args []string, i int, opts *options) (int, int, bool) {
	switch args[i] {
	case "--status":
		status, next, code, done := parseStatus(r, args, i)
		opts.status = status
		return next, code, done
	case "--limit":
		limit, next, code, done := parseLimit(r, args, i)
		opts.limit = limit
		return next, code, done
	default:
		return i, r.ReportUsage(operandUsage(repo, args[i])), true
	}
}

// parseStatus reads the --status value and holds it to the published domain.
func parseStatus(r *cli.Runner, args []string, i int) (string, int, int, bool) {
	v, next, u := cli.FlagValue(args, i, "--status", "open|all")
	if u != nil {
		return "", i, r.ReportUsage(u), true
	}
	if v != riskcore.QueueStatusOpen && v != riskcore.QueueStatusAll {
		return "", i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"--status wants open or all, not %q", v)}), true
	}
	return v, next, 0, false
}

// operandUsage explains an argument the queue cannot read: a second positional
// names the gate that does cover one bundle, anything else an unknown flag.
func operandUsage(repo, arg string) *cli.UsageError {
	if !strings.HasPrefix(arg, "-") {
		return &cli.UsageError{Message: fmt.Sprintf(
			"risk takes one <owner/repo>; gating one bundle is verdict report %s %s --max-risk <band>",
			repo, arg)}
	}
	return &cli.UsageError{Message: fmt.Sprintf(
		"unknown flag '%s' for risk (want --status, --limit, --json)", arg)}
}

// parseLimit reads the --limit value and bounds it: the queue's own bound is
// the only one it publishes.
func parseLimit(r *cli.Runner, args []string, i int) (int, int, int, bool) {
	raw, next, u := cli.FlagValue(args, i, "--limit", "<n>")
	if u != nil {
		return 0, i, r.ReportUsage(u), true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > riskcore.MaxQueueLimit {
		return 0, i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"--limit wants an integer from 1 to %d, not %q", riskcore.MaxQueueLimit, raw)}), true
	}
	return n, next, 0, false
}
