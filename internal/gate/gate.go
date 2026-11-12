// Package gate is the gate verb's CLI adapter: argv parsing, one composed
// report-core call, and the gate's own exit table (0 pass, 1 gate failed or
// unknown, 2 usage, 3 tool error). Domain logic lives in internal/core/report,
// which already evaluates every dimension; gate only composes them.
package gate

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a gate run.
type Request = readcommon.Request

const (
	liveAuto        = "auto"
	liveStrict      = "strict"
	statusPass      = "pass"
	statusFail      = "fail"
	statusSkip      = "skip"
	statusUnknown   = "unknown"
	idFindings      = "findings"
	idHead          = "head"
	idLive          = "live_threads"
	idCompliance    = "compliance"
	flagMaxSeverity = "--max-severity"
	flagRequireHE   = "--require-head"
	flagLive        = "--live"
	flagCoverage    = "--coverage"
	coverageRequire = "require"
	coverageWarn    = "warn"
	idCoverage      = "coverage"
	idWidth         = 12 // longest dimension id: live_threads
	actionFindings  = "address or triage the open findings"
	actionMissing   = "review + post the fetched HEAD"
	// actionCoverage names both attestation keys: a chunked bundle ignores
	// coverage[], so a recovery naming only that key would loop the operator.
	actionCoverage = "attest the unattested ranges in review.json (coverage[], or every " +
		"chunk in chunks[] when the bundle is chunked), then re-record or re-post"
)

// Run executes `gate <owner/repo> <pr> [flags]`, returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "gate"); done {
		return code
	}
	resp, ref := reportcore.Run(ctx, gateRequest(r, req, &flags))
	if ref != nil {
		return r.RefuseExit(ref, cli.ExitBroke)
	}
	r.Warnings = resp.Warnings
	emit(r, req, resp)
	if resp.GateFailed {
		return cli.ExitRefused
	}
	return cli.ExitOK
}

// gateRequest composes the report request whose dimensions the gate reads. It
// copies the runtime instead of filling the caller's in, so an injected
// commander survives and concurrent callers never share one.
func gateRequest(r *cli.Runner, req Request, flags *reportcore.Flags) *core.ReportRequest {
	read := req
	rt := &core.Runtime{}
	if read.Runtime != nil {
		*rt = *read.Runtime
	}
	rt.Now = r.Now
	rt.Progress = cli.Progress{R: r}
	if rt.Commander == nil {
		rt.Commander = coreexec.Runner{}
	}
	read.Runtime = rt
	return &core.ReportRequest{
		ReadRequest: read,
		Flags:       *flags,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// emit writes the per-dimension lines, then the envelope or the verdict.
func emit(r *cli.Runner, req Request, resp *core.ReportResponse) {
	gate := resp.Data.Gate
	for _, line := range gateLines(gate) {
		fmt.Fprintln(r.Human, line)
	}
	if r.JSONMode {
		env := core.RenderEnvelope("gate", req.Repo, req.Pr, resp.At, gateNext(gate),
			resp.Warnings, gate.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return
	}
	if hint := gateHint(gate); hint != "" {
		fmt.Fprintln(r.Human, "try: "+hint)
	}
}

// gateLines renders one line per dimension plus the verdict tally.
func gateLines(gate *core.ReportGate) []string {
	lines := make([]string, 0, len(gate.Dimensions)+2)
	for _, dim := range gate.Dimensions {
		lines = append(lines, fmt.Sprintf("gate: %-*s %s  %s", idWidth, dim.ID, dim.Status, dim.Detail))
	}
	verdict := "gate: PASS"
	if !gate.Passed {
		verdict = "gate: FAIL (" + gateTally(gate) + ")"
	}
	lines = append(lines, verdict)
	if skipped := gateSkipped(gate); skipped != "" {
		lines = append(lines, "gate: skipped "+skipped)
	}
	return lines
}

// gateTally counts statuses in a fixed order so the same result always reads
// the same words.
func gateTally(gate *core.ReportGate) string {
	counts := map[string]int{}
	for _, dim := range gate.Dimensions {
		counts[dim.Status]++
	}
	var parts []string
	for _, status := range []string{statusFail, statusUnknown, statusPass, statusSkip} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	return strings.Join(parts, ", ")
}

// gateSkipped names the dimensions the gate could not read, so a neutral
// result is never mistaken for coverage.
func gateSkipped(gate *core.ReportGate) string {
	var ids []string
	for _, dim := range gate.Dimensions {
		if dim.Status == statusSkip {
			ids = append(ids, dim.ID)
		}
	}
	return strings.Join(ids, ", ")
}

// gateHint names the recovery for every dimension that did not pass.
func gateHint(gate *core.ReportGate) string {
	actions := gateActions(gate)
	if len(actions) == 0 {
		return ""
	}
	return strings.Join(actions, ", then ") + ", then re-run gate"
}

// gateNext is the envelope's next action: the same recoveries without the
// re-run clause.
func gateNext(gate *core.ReportGate) string {
	actions := gateActions(gate)
	if len(actions) == 0 {
		return "none — every requested dimension passed"
	}
	return strings.Join(actions, ", then ")
}

func gateActions(gate *core.ReportGate) []string {
	actions := make([]string, 0, len(gate.Dimensions))
	for _, dim := range gate.Dimensions {
		if action := gateAction(dim); action != "" {
			actions = append(actions, action)
		}
	}
	return actions
}

func gateAction(dim core.GateDimension) string {
	action, ok := gateActionsByID[dim.ID]
	if !ok {
		return ""
	}
	return action(dim.Status)
}

// gateActionsByID maps a dimension to the recovery line its verdict calls
// for; a passing dimension contributes nothing.
var gateActionsByID = map[string]func(status string) string{
	idFindings: func(status string) string {
		if status == statusPass {
			return ""
		}
		return actionFindings
	},
	idHead: func(status string) string {
		switch status {
		case statusUnknown:
			return actionMissing
		case statusFail:
			return "post a review for the fetched HEAD"
		}
		return ""
	},
	idLive: func(status string) string {
		switch status {
		case statusFail:
			return "resolve or reply to the live threads"
		case statusUnknown:
			return "restore the network or re-run with --live skip"
		}
		return ""
	},
	idCompliance: func(status string) string {
		switch status {
		case statusFail:
			return "clear the compliance blockers"
		case statusUnknown:
			return "record the compliance evidence the warnings name"
		}
		return ""
	},
	idCoverage: func(status string) string {
		switch status {
		case statusFail:
			return actionCoverage
		case statusUnknown:
			return "attest coverage for the fetched HEAD (post or record)"
		}
		return ""
	},
}

// parseFlags parses the gate flags left to right; a missing operand, a bad
// enum, or no requested dimension is a usage error (exit 2, no envelope).
// --coverage defaults to require: the stored attestation is part of the gate
// unless the operator opts out, the same way --live carries its posture.
func parseFlags(r *cli.Runner, args []string) (reportcore.Flags, int, bool) {
	flags := reportcore.Flags{Live: liveAuto, Coverage: coverageRequire}
	gated := false
	i := 0
	for i < len(args) {
		consumed, code, done := parseFlag(r, &flags, &gated, args, i)
		if done {
			return flags, code, true
		}
		i += consumed
	}
	if !gated {
		return flags, r.ReportUsage(&cli.UsageError{Message: "gate needs at least one dimension " +
			"(want --max-severity critical|important|informational or --require-head; " +
			"--live only sets the offline posture)"}), true
	}
	r.RouteHuman()
	return flags, 0, false
}

func parseFlag(r *cli.Runner, flags *reportcore.Flags, gated *bool, args []string, i int) (int, int, bool) {
	switch args[i] {
	case flagRequireHE:
		flags.Completeness = true
		*gated = true
		return 1, 0, false
	case flagMaxSeverity, flagLive, flagCoverage:
		return parseValueFlag(r, flags, gated, args, i)
	default:
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for gate (want --max-severity, --require-head, --live, --coverage, --json)", args[i])}), true
	}
}

func parseValueFlag(r *cli.Runner, flags *reportcore.Flags, gated *bool, args []string, i int) (int, int, bool) {
	name := args[i]
	want := "auto|strict|skip"
	switch name {
	case flagMaxSeverity:
		want = "critical|important|informational"
	case flagCoverage:
		want = "require|warn"
	}
	v, next, u := cli.FlagValue(args, i, name, want)
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	switch name {
	case flagMaxSeverity:
		return applyTier(r, flags, gated, v, next-i+1)
	case flagCoverage:
		return applyCoverage(r, flags, v, next-i+1)
	}
	return applyLive(r, flags, v, next-i+1)
}

func applyTier(r *cli.Runner, flags *reportcore.Flags, gated *bool, v string, consumed int) (int, int, bool) {
	if !core.ValidTier(v) {
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown --max-severity tier '%s' for gate (want critical|important|informational)", v)}), true
	}
	flags.Check = v
	*gated = true
	return consumed, 0, false
}

func applyLive(r *cli.Runner, flags *reportcore.Flags, v string, consumed int) (int, int, bool) {
	if !validLive(v) {
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown --live posture '%s' for gate (want auto|strict|skip)", v)}), true
	}
	flags.Live = v
	return consumed, 0, false
}

// applyCoverage sets the coverage posture. It is a posture, not a dimension:
// like --live it never satisfies "gate needs at least one dimension", so a
// bare gate stays a usage error instead of silently gating on coverage alone.
func applyCoverage(r *cli.Runner, flags *reportcore.Flags, v string, consumed int) (int, int, bool) {
	if !validCoverage(v) {
		return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown --coverage posture '%s' for gate (want require|warn)", v)}), true
	}
	flags.Coverage = v
	return consumed, 0, false
}

func validLive(s string) bool {
	switch s {
	case liveAuto, liveStrict, "skip":
		return true
	default:
		return false
	}
}

func validCoverage(s string) bool {
	return s == coverageRequire || s == coverageWarn
}
