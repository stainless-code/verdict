package loopcore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	runcore "github.com/stainless-code/verdict/internal/core/run"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
)

// Pricing sources a spawn's ledger row names.
const (
	costFile = "cost.json"
	zeroUSD  = "0"
)

// phases is the loop's seam over the composed services. The production
// implementation calls the shipped services in-process; a test scripts their
// outcomes so every stop condition is reachable without an agent or a host.
type phases interface {
	fetch(ctx context.Context, name string) ([]string, *core.RefusalError)
	authorize(ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchID string) ([]string, *core.RefusalError)
	review(ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchPrefix string) ([]string, *core.RefusalError)
	record(ctx context.Context, b *reviewflow.Bundle) (*core.RecordData, []string, *core.RefusalError)
	gate(ctx context.Context, b *reviewflow.Bundle) (bool, []string, *core.RefusalError)
	fix(ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, directive, dir, launchID string) *core.RefusalError
	declared(dir string) bool
	verify(ctx context.Context, b *reviewflow.Bundle, timeout string) (*Verify, []string)
	cost(dir string) spend.MTD
}

// flowPhases runs every phase through the shipped services.
type flowPhases struct {
	req *Request
	// runVerify is the verify service; nil is verifycore.Run. A test records
	// the posture the service receives through it.
	runVerify func(context.Context, *verifycore.Request) (*verifycore.Response, *core.RefusalError)
	// noUsage records that the agent rejected the structured-output flag, so
	// later spawns in this loop stop asking for it.
	noUsage bool
}

// fetch re-reads the working tree into this round's fresh local bundle.
func (p *flowPhases) fetch(ctx context.Context, name string) ([]string, *core.RefusalError) {
	b := &reviewflow.Bundle{Dir: p.req.RepoRoot, Repo: p.req.Repo, Pr: name}
	resp, ref := p.req.Flow.FetchLocal(ctx, b, &fetchcore.LocalFlags{}, nil)
	if ref != nil {
		return nil, ref
	}
	return resp.Warnings, nil
}

// authorize admits one spawn under the monthly ceiling and writes its
// ledger receipt, so the month-to-date scan counts it.
func (p *flowPhases) authorize(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchID string,
) ([]string, *core.RefusalError) {
	req := p.req.Flow.RunRequest(b, launch, nil)
	// The loop admits its fix spawn here, so the model is read from the fix's
	// own arguments.
	req.PassThrough = p.req.fixArgs()
	return runcore.AuthorizeSpawn(ctx, req, dir, launchID, runcore.SpawnModel(req, p.req.Flow.Agents))
}

// measuring prepares one launch request to measure its spend, unless the
// agent already rejected the structured-output flag in this loop.
func (p *flowPhases) measuring(req *core.RunRequest) {
	req.MeasureSpend = !p.noUsage
	req.OnUsageRejected = func() { p.noUsage = true }
}

// review launches the review-only agent run against the round's bundle. Run
// calls the admission hook before every attempt it spawns — the first launch
// and each repair round — so every attempt is admitted and receipted as
// <launchPrefix>-<attempt>. Run measures every attempt from the agent CLI's
// own structured output and reports it per attempt. Each attempt is also a
// fresh agent process that rewrites the bundle's cost.json for its own run,
// so the hook snapshots the previous attempt's record before the next one can
// overwrite it, the self-reported fallback.
func (p *flowPhases) review(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchPrefix string,
) ([]string, *core.RefusalError) {
	req := p.req.Flow.RunRequest(b, launch, nil)
	req.PassThrough = p.req.AgentArgs
	p.measuring(req)
	model := runcore.SpawnModel(req, p.req.Flow.Agents)
	var attempts []*attemptCost
	var warnings []string
	req.OnSpend = func(attempt int, usd, pricedBy string) {
		if attempt >= 1 && attempt <= len(attempts) {
			attempts[attempt-1].measured = &spawnPrice{usd: usd, by: pricedBy}
		}
	}
	req.Admit = func(ctx context.Context, attempt int) *core.RefusalError {
		captureLast(dir, attempts)
		id := launchPrefix + "-" + strconv.Itoa(attempt)
		prior := snapshotCost(dir)
		w, ref := runcore.AuthorizeSpawn(ctx, req, dir, id, model)
		warnings = append(warnings, w...)
		if ref == nil {
			attempts = append(attempts, &attemptCost{id: id, prior: prior})
		}
		return ref
	}
	ran, ref := runcore.WithAgents(ctx, req, p.req.Flow.Agents)
	captureLast(dir, attempts)
	p.priceReview(ctx, req, dir, attempts, model)
	if ref != nil {
		return warnings, ref
	}
	return append(warnings, ran.Warnings...), nil
}

// attemptCost is one review attempt's admitted receipt and its own cost
// record, nil when the attempt wrote none. prior is the bundle's cost.json as
// it stood at admission.
type attemptCost struct {
	measured *spawnPrice
	prior    *costSnapshot
	id       string
	record   []byte
}

// costSnapshot is the bundle's cost.json as one moment saw it: the file's
// identity and stamp, and its bytes.
type costSnapshot struct {
	info os.FileInfo
	body []byte
}

// snapshotCost reads the bundle's cost.json, nil when there is no regular
// file.
func snapshotCost(dir string) *costSnapshot {
	info, err := os.Lstat(filepath.Join(dir, costFile))
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return &costSnapshot{info: info, body: spend.ReadBundleFile(dir, costFile)}
}

// same reports whether two snapshots are one unchanged record: the same file,
// stamp, size and bytes.
func (s *costSnapshot) same(o *costSnapshot) bool {
	return s != nil && o != nil && os.SameFile(s.info, o.info) && s.info.ModTime().Equal(o.info.ModTime()) &&
		s.info.Size() == o.info.Size() && bytes.Equal(s.body, o.body)
}

// spawnPrice is one spawn's amount and where it came from.
type spawnPrice struct {
	usd string
	by  string
}

// captureLast snapshots the bundle's cost.json for the latest attempt, when
// that attempt wrote it: the record still standing exactly as it did at the
// attempt's admission is an earlier attempt's and leaves this one without a
// record of its own. The test is "changed since admission", never a clock
// comparison: the filesystem stamps files from a coarser clock than
// [time.Now], so a record written just after admission can carry an earlier
// modification time. A rewrite identical in file, stamp, size and bytes reads
// as unchanged, so the attempt stays unpriced rather than counted twice.
func captureLast(dir string, attempts []*attemptCost) {
	if len(attempts) == 0 {
		return
	}
	last := attempts[len(attempts)-1]
	now := snapshotCost(dir)
	if now == nil || last.prior.same(now) {
		return
	}
	last.record = now.body
}

// priceReview prices each admitted attempt, in priority order: the agent
// CLI's own measured cost (a launch whose process never started measures
// zero), else the attempt's own self-reported record, else the projected
// median of past measured spawns; with none of them the attempt stays
// unknown, never free. A bundle receipted per spawn is counted from these
// receipts alone, so a measured attempt never counts its self-report too.
func (p *flowPhases) priceReview(
	ctx context.Context, req *core.RunRequest, dir string, attempts []*attemptCost, model string,
) {
	for _, a := range attempts {
		if price, ok := p.attemptPrice(a, model); ok {
			p.price(ctx, req, dir, a.id, price)
		}
	}
}

// attemptPrice is one review attempt's price by the priority order.
func (p *flowPhases) attemptPrice(a *attemptCost, model string) (spawnPrice, bool) {
	if a.measured != nil {
		return *a.measured, true
	}
	if usd, ok := recordAmount(a.record); ok {
		return spawnPrice{usd: usd, by: spend.PricedAgent}, true
	}
	return p.projected(model)
}

// projected is the median of past measured spawns of this agent and model,
// the fallback before unknown.
func (p *flowPhases) projected(model string) (spawnPrice, bool) {
	usd, ok := spend.MedianMeasured(readcommon.StateDir(p.req.RepoRoot), p.req.Agent, model)
	return spawnPrice{usd: usd, by: spend.PricedProjected}, ok
}

// recordAmount is one self-reported cost record's root est_usd, when it is
// a known amount.
func recordAmount(record []byte) (string, bool) {
	doc, ok := decodeReport(record)
	if !ok {
		return "", false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return "", false
	}
	return knownAmount(obj)
}

// price records one spawn's amount, warning rather than failing the round
// when the ledger cannot take it: the spawn then stays unknown.
func (p *flowPhases) price(ctx context.Context, req *core.RunRequest, dir, id string, price spawnPrice) {
	if ref := runcore.PriceSpawn(ctx, req, dir, id, price.usd, price.by); ref != nil {
		p.req.Flow.Warn("could not price spawn " + id + ": " + ref.Message)
	}
}

// record records the round with the coverage attestation required.
func (p *flowPhases) record(
	ctx context.Context, b *reviewflow.Bundle,
) (*core.RecordData, []string, *core.RefusalError) {
	recorded, ref := p.req.Flow.Record(ctx, b, reviewflow.RecordOptions{RequireCoverage: true}, nil)
	if ref != nil {
		return nil, nil, ref
	}
	return recorded.Data, recorded.Warnings, nil
}

// gate grades the recorded round: every open finding fails it, the stored
// coverage attestation is required, and live host checks are skipped.
func (p *flowPhases) gate(ctx context.Context, b *reviewflow.Bundle) (bool, []string, *core.RefusalError) {
	resp, ref := reportcore.Run(ctx, &reportcore.Request{
		ReadRequest: p.req.Flow.Read(b),
		Flags:       reportcore.Flags{Check: p.req.MaxSeverity, Coverage: gateCoverage, Live: gateLive},
		Effective:   p.req.Flow.Effective,
	})
	if ref != nil {
		return false, nil, ref
	}
	return !resp.GateFailed, resp.Warnings, nil
}

// fix spawns the agent with the fix directive in the working tree, measuring
// its spend, then prices the spawn's ledger row in priority order: zero when
// no process started; else the CLI's own measured cost; else the agent's
// fix-cost.json report — a fix that exits non-zero still spent; else the
// projected median of past measured spawns; else it stays unknown.
func (p *flowPhases) fix(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, directive, dir, launchID string,
) *core.RefusalError {
	if ref := readcommon.RefuseArtifactLinks(p.req.RepoRoot, dir, patchFile, fixCostFile); ref != nil {
		return ref
	}
	req := p.req.Flow.RunRequest(b, launch, nil)
	req.PassThrough = p.req.fixArgs()
	p.measuring(req)
	out, ref := runcore.Dispatch(ctx, req, p.req.Flow.Agents, directive)
	if price, ok := p.fixPrice(out, dir, runcore.SpawnModel(req, p.req.Flow.Agents)); ok {
		p.price(ctx, req, dir, launchID, price)
	}
	return ref
}

// fixPrice is the fix spawn's price by the priority order.
// A launch whose process provably never started is measured at zero; one
// that started and failed — capped, killed, or exiting non-zero — still spent,
// so it falls through the sources like any other.
func (p *flowPhases) fixPrice(out runcore.Dispatched, dir, model string) (spawnPrice, bool) {
	switch {
	case out.USD != "":
		return spawnPrice{usd: out.USD, by: out.PricedBy}, true
	case !out.Ran:
		return spawnPrice{usd: zeroUSD, by: spend.PricedNotStarted}, true
	}
	if usd, ok := reportedSpend(dir); ok {
		return spawnPrice{usd: usd, by: spend.PricedAgent}, true
	}
	return p.projected(model)
}

// reportedSpend reads the fix agent's {"est_usd": n} report: a finite,
// nonnegative amount, else nothing. The read is the cost scan's own:
// non-blocking, confined to the bundle, a regular file under the cap.
func reportedSpend(dir string) (string, bool) {
	doc, ok := decodeReport(spend.ReadBundleFile(dir, fixCostFile))
	if !ok {
		return "", false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return "", false
	}
	return knownAmount(obj)
}

// decodeReport decodes one agent-written report, nil reading as absent.
func decodeReport(raw []byte) (any, bool) {
	if raw == nil {
		return nil, false
	}
	doc, err := jq.DecodeOrdered(raw)
	return doc, err == nil
}

// knownAmount reads a record's root est_usd as a finite nonnegative literal.
func knownAmount(obj *jq.Obj) (string, bool) {
	est, _ := obj.Get("est_usd")
	var lit string
	switch num := est.(type) {
	case json.Number:
		lit = num.String()
	case jq.Number:
		lit = num.String()
	default:
		return "", false
	}
	if lit == "" || !spend.ValidLimit(lit) {
		return "", false
	}
	return lit, true
}

// declared reports whether the fix agent declared a verify command.
func (p *flowPhases) declared(dir string) bool {
	return readcommon.IsRegularNoFollow(filepath.Join(dir, patchFile))
}

// verify runs `verdict verify` on the bundle the fix agent declared a command
// in, under the agent's watchdog posture, so the fix carries the tool's
// receipt rather than the agent's word.
func (p *flowPhases) verify(ctx context.Context, b *reviewflow.Bundle, timeout string) (*Verify, []string) {
	read := p.req.Flow.Read(b)
	read.Runtime = &core.Runtime{
		Env: read.Env, Now: read.Now, Progress: read.Progress, Commander: p.req.Flow.AgentCommander,
	}
	sandbox, networkOnly := verifyPosture(p.req)
	run := p.runVerify
	if run == nil {
		run = verifycore.Run
	}
	resp, ref := run(ctx, &verifycore.Request{
		ReadRequest:        read,
		TimeoutRaw:         timeout,
		Environ:            p.req.Flow.Environ,
		JSONMode:           p.req.Flow.JSONMode,
		Sandbox:            sandbox,
		SandboxNetworkOnly: networkOnly,
	})
	if ref != nil {
		return &Verify{Refusal: asRefusal(ref)}, ref.Warnings
	}
	exit := resp.Exit
	return &Verify{Exit: &exit, TimedOut: resp.TimedOut}, resp.Warnings
}

// verifyPosture is the verify step's sandbox: the full one by default, the
// network-only one or none only when the operator opted out.
func verifyPosture(req *Request) (sandbox, networkOnly bool) {
	return !req.VerifyNoSandbox && !req.VerifySandboxNetworkOnly, req.VerifySandboxNetworkOnly
}

// cost reads one round bundle's own receipts, whatever their month: the
// round's cost is not cut in two by a UTC month boundary.
func (p *flowPhases) cost(dir string) spend.MTD {
	return spend.BundleTotal(dir)
}
