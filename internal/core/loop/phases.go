package loopcore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

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
// <launchPrefix>-<attempt>. Run measures every attempt from the agent CLI's
// own structured output and reports it per attempt. Each attempt is also a
// fresh agent process that rewrites the bundle's cost.json for its own run,
// so the hook snapshots the previous attempt's record before the next one can
// overwrite it, the self-reported fallback.
func (p *flowPhases) review(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchPrefix string,
) ([]string, *core.RefusalError) {
	req := p.req.Flow.RunRequest(b, launch, nil)
	req.MeasureSpend = true
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
		// Wall time, not the injected clock: it is compared with the record's
		// modification time on the filesystem.
		admitted := time.Now()
		w, ref := runcore.AuthorizeSpawn(ctx, req, dir, id)
		warnings = append(warnings, w...)
		if ref == nil {
			attempts = append(attempts, &attemptCost{id: id, admitted: admitted})
		}
		return ref
	}
	ran, ref := runcore.WithAgents(ctx, req, p.req.Flow.Agents)
	captureLast(dir, attempts)
	p.priceReview(ctx, req, dir, attempts, ref)
	if ref != nil {
		return warnings, ref
	}
	return append(warnings, ran.Warnings...), nil
}

// attemptCost is one review attempt's admitted receipt and its own cost
// record, nil when the attempt wrote none.
type attemptCost struct {
	admitted time.Time
	measured *spawnPrice
	id       string
	record   []byte
}

// spawnPrice is one spawn's amount and where it came from.
type spawnPrice struct {
	usd string
	by  string
}

// captureLast snapshots the bundle's cost.json for the latest attempt, when
// that attempt wrote it: a file older than the attempt's admission is an
// earlier attempt's record and leaves this one without a record of its own.
func captureLast(dir string, attempts []*attemptCost) {
	if len(attempts) == 0 {
		return
	}
	last := attempts[len(attempts)-1]
	info, err := os.Lstat(filepath.Join(dir, costFile))
	if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(last.admitted) {
		return
	}
	last.record = spend.ReadBundleFile(dir, costFile)
}

// priceReview prices each admitted attempt, in priority order: the agent
// CLI's own measured cost, else the attempt's own self-reported record, else
// the projected median of past measured spawns; with none of them the attempt
// stays unknown, never free. An attempt whose process never started is zero.
// The record still on disk — the last one written — is zero whenever the
// month-to-date scan already counts it (every record dated in this month),
// whatever else priced the attempt, so a self-report is never counted twice.
func (p *flowPhases) priceReview(
	ctx context.Context, req *core.RunRequest, dir string, attempts []*attemptCost, ref *core.RefusalError,
) {
	if len(attempts) > 0 && ref != nil && ref.Code == codeSpawnFailed {
		p.price(ctx, req, dir, attempts[len(attempts)-1].id, spawnPrice{usd: zeroUSD, by: spend.PricedNotStarted})
		attempts = attempts[:len(attempts)-1]
	}
	onDisk := lastRecorded(attempts)
	now := p.now()
	for i, a := range attempts {
		if price, ok := p.attemptPrice(a, i == onDisk, now); ok {
			p.price(ctx, req, dir, a.id, price)
		}
	}
}

// attemptPrice is one review attempt's price by the priority order.
func (p *flowPhases) attemptPrice(a *attemptCost, onDisk bool, now time.Time) (spawnPrice, bool) {
	if onDisk && spend.CountedInMonth(a.record, now) {
		return spawnPrice{usd: zeroUSD, by: spend.PricedAgent}, true
	}
	if a.measured != nil {
		return *a.measured, true
	}
	if usd, ok := recordAmount(a.record); ok {
		return spawnPrice{usd: usd, by: spend.PricedAgent}, true
	}
	return p.projected()
}

// projected is the median of past measured spawns of this agent, the
// fallback before unknown.
func (p *flowPhases) projected() (spawnPrice, bool) {
	usd, ok := spend.MedianMeasured(readcommon.StateDir(p.req.RepoRoot), p.req.Agent, "")
	return spawnPrice{usd: usd, by: spend.PricedProjected}, ok
}

// lastRecorded is the index of the attempt whose record is still on disk:
// the last one that wrote one, -1 when none did.
func lastRecorded(attempts []*attemptCost) int {
	for i, a := range slices.Backward(attempts) {
		if a.record != nil {
			return i
		}
	}
	return -1
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

// now is the loop's clock, the one its receipts are dated by.
func (p *flowPhases) now() time.Time {
	if p.req.Flow.Now != nil {
		return p.req.Flow.Now()
	}
	return time.Now()
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
		Flags:       reportcore.Flags{Check: gateSeverity, Coverage: gateCoverage, Live: gateLive},
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
	req := p.req.Flow.RunRequest(b, launch, nil)
	req.MeasureSpend = true
	out, ref := runcore.Dispatch(ctx, req, p.req.Flow.Agents, directive)
	if price, ok := p.fixPrice(out, ref, dir); ok {
		p.price(ctx, req, dir, launchID, price)
	}
	return ref
}

// fixPrice is the fix spawn's price by the priority order.
func (p *flowPhases) fixPrice(out runcore.Dispatched, ref *core.RefusalError, dir string) (spawnPrice, bool) {
	switch {
	case !out.Ran && ref != nil && ref.Code == codeSpawnFailed:
		return spawnPrice{usd: zeroUSD, by: spend.PricedNotStarted}, true
	case !out.Ran:
		return spawnPrice{}, false
	case out.USD != "":
		return spawnPrice{usd: out.USD, by: out.PricedBy}, true
	}
	if usd, ok := reportedSpend(dir); ok {
		return spawnPrice{usd: usd, by: spend.PricedAgent}, true
	}
	return p.projected()
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
