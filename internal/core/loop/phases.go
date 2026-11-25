package loopcore

import (
	"context"
	"encoding/json"
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
	pricedByAgent      = "agent"
	pricedByCostRecord = "cost.json"
	pricedByNoLaunch   = "not-started"
	// codeSpawnFailed is run's code for a process that never started.
	codeSpawnFailed = "agent-spawn-failed"
	costFile        = "cost.json"
	zeroUSD         = "0"
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
	return runcore.AuthorizeSpawn(ctx, p.req.Flow.RunRequest(b, launch, nil), dir, launchID)
}

// review launches the review-only agent run against the round's bundle. Run
// calls the admission hook before every attempt it spawns — the first launch
// and each repair round — so every attempt is admitted and receipted as
// <launchPrefix>-<attempt>. Afterwards the receipts are priced from what the
// review recorded.
func (p *flowPhases) review(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchPrefix string,
) ([]string, *core.RefusalError) {
	req := p.req.Flow.RunRequest(b, launch, nil)
	var admitted []string
	var warnings []string
	req.Admit = func(ctx context.Context, attempt int) *core.RefusalError {
		id := launchPrefix + "-" + strconv.Itoa(attempt)
		w, ref := runcore.AuthorizeSpawn(ctx, req, dir, id)
		warnings = append(warnings, w...)
		if ref == nil {
			admitted = append(admitted, id)
		}
		return ref
	}
	ran, ref := runcore.WithAgents(ctx, req, p.req.Flow.Agents)
	p.priceReview(ctx, req, dir, admitted, ref)
	if ref != nil {
		return warnings, ref
	}
	return append(warnings, ran.Warnings...), nil
}

// priceReview prices the review's admitted attempts. An attempt whose
// process never started is zero. The rest are priced from the review's own
// cost.json: a dated record is already counted by the month-to-date scan, so
// the attempts are zero beside it; an undated schema-2 record (the shape the
// review prompt asks for) is the run's total, so it prices the first attempt
// and the later ones are zero inside it; without a known amount they stay
// unknown, never free.
func (p *flowPhases) priceReview(
	ctx context.Context, req *core.RunRequest, dir string, admitted []string, ref *core.RefusalError,
) {
	if len(admitted) > 0 && ref != nil && ref.Code == codeSpawnFailed {
		p.price(ctx, req, dir, admitted[len(admitted)-1], zeroUSD, pricedByNoLaunch)
		admitted = admitted[:len(admitted)-1]
	}
	if len(admitted) == 0 {
		return
	}
	dated, total, ok := reviewCost(dir)
	switch {
	case dated:
		total = zeroUSD
	case !ok:
		return
	}
	p.price(ctx, req, dir, admitted[0], total, pricedByCostRecord)
	for _, id := range admitted[1:] {
		p.price(ctx, req, dir, id, zeroUSD, pricedByCostRecord)
	}
}

// price records one spawn's amount, warning rather than failing the round
// when the ledger cannot take it: the spawn then stays unknown.
func (p *flowPhases) price(ctx context.Context, req *core.RunRequest, dir, id, usd, source string) {
	if ref := runcore.PriceSpawn(ctx, req, dir, id, usd, source); ref != nil {
		p.req.Flow.Warn("could not price spawn " + id + ": " + ref.Message)
	}
}

// reviewCost reads the review's cost.json: dated reports a record the
// month-to-date scan already counts; otherwise total is a schema-2 record's
// root est_usd when it is a known amount.
func reviewCost(dir string) (dated bool, total string, ok bool) {
	doc, ok := decodeReport(spend.ReadBundleFile(dir, costFile))
	if !ok {
		return false, "", false
	}
	for _, rec := range records(doc) {
		if at, _ := rec.Get("at"); at != nil {
			return true, "", false
		}
	}
	obj, isObj := doc.(*jq.Obj)
	if !isObj {
		return false, "", false
	}
	total, ok = knownAmount(obj)
	return false, total, ok
}

// records lists a cost document's record objects: one object, or an array.
func records(doc any) []*jq.Obj {
	var out []*jq.Obj
	items, isList := doc.([]any)
	if !isList {
		items = []any{doc}
	}
	for _, item := range items {
		if obj, ok := item.(*jq.Obj); ok {
			out = append(out, obj)
		}
	}
	return out
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
		Flags:       reportcore.Flags{Check: gateSeverity, Coverage: gateCoverage, Live: gateLive},
		Effective:   p.req.Flow.Effective,
	})
	if ref != nil {
		return false, nil, ref
	}
	return !resp.GateFailed, resp.Warnings, nil
}

// fix spawns the agent with the fix directive in the working tree, then
// prices the spawn's ledger row: zero when no process started, the agent's
// fix-cost.json report whenever one did — a fix that exits non-zero still
// spent — and unknown without a report.
func (p *flowPhases) fix(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, directive, dir, launchID string,
) *core.RefusalError {
	req := p.req.Flow.RunRequest(b, launch, nil)
	_, ran, ref := runcore.Dispatch(ctx, req, p.req.Flow.Agents, directive)
	switch {
	case !ran && ref != nil && ref.Code == codeSpawnFailed:
		p.price(ctx, req, dir, launchID, zeroUSD, pricedByNoLaunch)
	case ran:
		if usd, ok := reportedSpend(dir); ok {
			p.price(ctx, req, dir, launchID, usd, pricedByAgent)
		}
	}
	return ref
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
	return readcommon.IsFile(filepath.Join(dir, patchFile))
}

// verify runs `verdict verify` on the bundle the fix agent declared a command
// in, under the agent's watchdog posture, so the fix carries the tool's
// receipt rather than the agent's word.
func (p *flowPhases) verify(ctx context.Context, b *reviewflow.Bundle, timeout string) (*Verify, []string) {
	read := p.req.Flow.Read(b)
	read.Runtime = &core.Runtime{
		Env: read.Env, Now: read.Now, Progress: read.Progress, Commander: p.req.Flow.AgentCommander,
	}
	resp, ref := verifycore.Run(ctx, &verifycore.Request{
		ReadRequest:        read,
		TimeoutRaw:         timeout,
		Environ:            p.req.Flow.Environ,
		JSONMode:           p.req.Flow.JSONMode,
		Sandbox:            p.req.VerifySandbox,
		SandboxNetworkOnly: p.req.VerifySandboxNetworkOnly,
	})
	if ref != nil {
		return &Verify{Refusal: asRefusal(ref)}, ref.Warnings
	}
	exit := resp.Exit
	return &Verify{Exit: &exit, TimedOut: resp.TimedOut}, resp.Warnings
}

// cost reads one round bundle's own receipts, whatever their month: the
// round's cost is not cut in two by a UTC month boundary.
func (p *flowPhases) cost(dir string) spend.MTD {
	return spend.BundleTotal(dir)
}
