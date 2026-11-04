// Package calibratecore is the calibrate verb's domain service: the
// live-ground-truth join (thread states plus files changed since the
// review head) and the offline self-grading probe. All gh reads go through
// the injected commander; the probe rewrites kb-grades.jsonl like the
// shell. internal/calibrate owns argv parsing, progress routing, envelope
// rendering, and exit mapping.
package calibratecore

import (
	"context"
	"errors"

	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Refusal codes, outcome and status vocabularies, and store keys shared by
// the live join and the probe, frozen as the wire contract.
const (
	codeNoThreads             = "no-threads"
	codeProviderUnimplemented = "provider-unimplemented"
	codeMissingPost           = "missing-post"
	codeThreadsFailed         = "threads-failed"
	codePRReadFailed          = "pr-read-failed"
	codeBadJudgment           = "bad-judgment"
	codeUnknownJudgment       = "unknown-judgment"
	codeCalibrateFailed       = "calibrate-failed"

	statusAddressed     = "addressed"
	statusFalsePositive = "false-positive"
	statusOpen          = "open"
	statusDeferred      = "deferred"

	outcomeFixed           = "fixed"
	outcomeResolvedUnfixed = "resolved-unfixed"
	outcomeUnknown         = "unknown"
	outcomeUntagged        = "untagged"
	tierResolvedUnfixed    = "resolved_unfixed"

	verdictReal    = "real"
	verdictFalse   = "false"
	verdictUnknown = "unknown"

	helpfulTrue  = "true"
	helpfulFalse = "false"
	helpfulNull  = "null"

	keySig          = "sig"
	keyAgentVerdict = "agent_verdict"
	keyAgentHelpful = "agent_helpful"

	unmarkedValue  = "unmarked"
	postFileSuffix = "/post.json"
)

// Request carries the resolved calibrate invocation: the shared read
// runtime plus the probe flag, the --judge specs, and the --judge-file
// path.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Probe     bool
	Specs     []string
	JudgeFile string
	Warnings  []string
}

// Response carries the completed calibration: the envelope fields and the
// typed data profile (the live join or the probe grades). Human lines
// stream live through the runtime progress observer, so the response
// carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Next     string
	Warnings []string
	Data     *Data
}

// Data is the calibrate verb's --json data profile: exactly one of the
// live join, the offline corpus join, or the probe grades renders.
type Data struct {
	live   *liveResult
	corpus *corpusResult
	probe  *probeResult
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	if d.probe != nil {
		return d.probe.Wire()
	}
	if d.corpus != nil {
		return d.corpus.Wire()
	}
	return d.live.Wire()
}

type state struct { //nolint:govet // calibrate service state is allocated once per invocation
	req      *Request
	warnings []string
	live     *liveResult
	corpus   *corpusResult
	probe    *probeResult
}

// Run executes the live-ground-truth join or the offline self-grading
// probe, selected by the --probe flag.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before calibrate completed"}
	}
	dir := readcommon.ResolveBundleDir(req.Pr)
	if req.Probe {
		if ref := st.runProbe(dir); ref != nil {
			return nil, ref
		}
		return st.probeResponse(), nil
	}
	if ref := st.runJoin(ctx, dir); ref != nil {
		return nil, ref
	}
	if st.corpus != nil {
		return st.corpusResponse(), nil
	}
	return st.liveResponse(), nil
}

func (st *state) runJoin(ctx context.Context, dir string) *core.RefusalError {
	if corpuscore.IsDir(dir) {
		return st.runCorpus(dir)
	}
	return st.runLive(ctx, dir)
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func (st *state) loadRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return st.withWarnings(ref)
	}
	return st.withWarnings(&core.RefusalError{Code: "internal", Message: err.Error()})
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

func (st *state) liveResponse() *Response {
	return &Response{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       st.req.TimeUTC(),
		Next:     nextLive(countPrecision(st.live.outcomes)),
		Warnings: st.warnings,
		Data:     &Data{live: st.live},
	}
}

func (st *state) probeResponse() *Response {
	return &Response{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       st.req.TimeUTC(),
		Next:     nextProbe(&st.probe.counts),
		Warnings: st.warnings,
		Data:     &Data{probe: st.probe},
	}
}
