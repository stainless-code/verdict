package report

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// RunNext executes `next <owner/repo> <pr> [--status, --json]`: the
// top-ranked actionable finding plus the command to act on it. It replays
// report internally the way the shell re-invokes the script, so the store
// reads and the failure posture match exactly.
func RunNext(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	statusF, code, done := parseNextFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: "canceled before next completed"}, cli.ExitBroke)
	}
	repOut := replayReport(ctx, r, req, statusF)
	top, ok := firstItem(repOut)
	if !ok {
		return nextStoreError(r, repOut)
	}
	if top == nil {
		return nextEmpty(r, statusF)
	}
	return nextTop(r, top)
}

// parseNextFlags parses the next flags: --status takes any value (report
// validates it), --json flips machine mode.
func parseNextFlags(r *cli.Runner, args []string) (string, int, bool) {
	statusF := statusOpen
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a {
		case "--status":
			statusF = next
			i += 2
		case flagJSON:
			r.JSONMode = true
			routeHuman(r)
			i++
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for next (want --status, --json)", a)}), true
		}
	}
	return statusF, 0, false
}

// replayReport runs report in-process with a discarded diagnostic stream,
// mirroring the shell's `report ... 2>/dev/null` sub-invocation. The
// sub-run re-resolves prefs from the same environment (duplicating prefs
// warnings exactly like the subshell) and ignores global flag forces,
// which the shell never forwards.
func replayReport(_ context.Context, r *cli.Runner, req Request, statusF string) string {
	var outBuf bytes.Buffer
	sub := &cli.Runner{
		Cmd:          "report",
		Repo:         req.Repo,
		Pr:           req.Pr,
		Out:          &outBuf,
		Diag:         io.Discard,
		Human:        io.Discard,
		Now:          r.Now,
		EnvNoColor:   req.Env["NO_COLOR"] != "",
		JSONMode:     true,
		JSONPeeked:   true,
		PrefColorSet: r.PrefColorSet,
		Warnings:     append([]string{}, r.Warnings...),
	}
	prefs, ref := cli.ResolvePrefs(sub, req.Env, os.ReadFile)
	if ref != nil {
		sub.Effective = cli.Defaults()
		return subRefusal(sub, ref)
	}
	sub.Effective = prefs.Effective
	if ref := cli.ApplyGlobalForces(sub, &cli.GlobalForces{}, subNoColor(sub, req.Env)); ref != nil {
		return subRefusal(sub, ref)
	}
	routeHuman(sub)
	flags := Flags{StatusF: statusF, KindF: statusAll, LiveMode: liveAuto}
	st, err := loadState(sub, req, &flags)
	if err != nil {
		return subRefusal(sub, err)
	}
	emitEnvelope(st)
	return outBuf.String()
}

// subNoColor ports the NO_COLOR fallback for the sub-run: an explicitly
// set (even empty) NO_COLOR is respected, otherwise JSON mode defaults on.
func subNoColor(sub *cli.Runner, env map[string]string) bool {
	if sub.EnvNoColor {
		return true
	}
	if _, set := env["NO_COLOR"]; !set && sub.JSONMode {
		return true
	}
	return false
}

// subRefusal renders the sub-report refusal into the replay buffer,
// mirroring the sub-invocation's envelope (usage errors print nothing, so
// the parent reports them as an unreadable store).
func subRefusal(sub *cli.Runner, err error) string {
	if _, ok := errors.AsType[*cli.UsageError](err); ok {
		return ""
	}
	sub.Refuse(err)
	if buf, ok := sub.Out.(*bytes.Buffer); ok {
		return buf.String()
	}
	return ""
}

// firstItem parses the replayed envelope: empty output or a non-null
// error reads as unreadable; otherwise the first item (possibly null when
// the store holds no rows).
func firstItem(repOut string) (any, bool) {
	obj, ok := decodeEnvelope(repOut)
	if !ok {
		return nil, false
	}
	if hasEnvelopeError(obj) {
		return nil, false
	}
	dataObj, ok := envelopeData(obj)
	if !ok {
		return nil, false
	}
	return firstDataItem(dataObj)
}

// decodeEnvelope parses the replayed envelope object.
func decodeEnvelope(repOut string) (*jq.Obj, bool) {
	if repOut == "" {
		return nil, false
	}
	doc, err := jq.DecodeOrdered([]byte(repOut))
	if err != nil {
		return nil, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return nil, false
	}
	return obj, true
}

// hasEnvelopeError reports whether the envelope carries a non-null error.
func hasEnvelopeError(obj *jq.Obj) bool {
	errVal, _ := obj.Get("error")
	return errVal != nil
}

// envelopeData extracts the envelope data object.
func envelopeData(obj *jq.Obj) (*jq.Obj, bool) {
	data, _ := obj.Get("data")
	dataObj, ok := data.(*jq.Obj)
	if !ok || dataObj == nil {
		return nil, false
	}
	return dataObj, true
}

// firstDataItem selects the first envelope item (nil when empty).
func firstDataItem(dataObj *jq.Obj) (any, bool) {
	items, _ := dataObj.Get("items")
	list, ok := items.([]any)
	if !ok || len(list) == 0 {
		return nil, true
	}
	return list[0], true
}

// nextStoreError ports the unreadable-store path: the sub-envelope replays
// verbatim in machine mode, else the fixed human line.
func nextStoreError(r *cli.Runner, repOut string) int {
	if r.JSONMode {
		if repOut != "" {
			fmt.Fprint(r.Out, repOut)
			return 1
		}
		return r.Refuse(&cli.RefusalError{Code: codeReportFailed, Message: "next: report produced no output"})
	}
	fmt.Fprintln(r.Diag, "next: cannot read store (run post or record first)")
	return 1
}

// nextEmpty ports the no-findings path.
func nextEmpty(r *cli.Runner, statusF string) int {
	fmt.Fprintf(r.Human, "next: none %s — nothing to act on\n", statusF)
	if !r.JSONMode {
		return 0
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", keyNext,
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", cli.NowUTC(r.Now()),
		keyNext, "none — nothing to act on",
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj("item", nil),
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 0
}

// nextTop ports the top-finding path.
func nextTop(r *cli.Runner, top any) int {
	locator := fieldOf(top, "locator")
	if locator == nil {
		locator = "?"
	}
	fmt.Fprintf(r.Human, "next: %s [%s] %s — address via address-reviews mode, then post\n",
		sigOutput(top), jq.Str(fieldOf(top, "status")), jq.Str(locator))
	if !r.JSONMode {
		return 0
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", keyNext,
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", cli.NowUTC(r.Now()),
		keyNext, "address via address-reviews mode, then post",
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj("item", top),
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 0
}
