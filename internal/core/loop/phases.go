package loopcore

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

// maxFixCostBytes bounds the fix agent's spend report: one small object.
const maxFixCostBytes = 4 << 10

// phases is the loop's seam over the composed services. The production
// implementation calls the shipped services in-process; a test scripts their
// outcomes so every stop condition is reachable without an agent or a host.
type phases interface {
	fetch(ctx context.Context, name string) ([]string, *core.RefusalError)
	authorize(ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchID string) ([]string, *core.RefusalError)
	review(ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch) ([]string, *core.RefusalError)
	record(ctx context.Context, b *reviewflow.Bundle) (*core.RecordData, []string, *core.RefusalError)
	gate(ctx context.Context, b *reviewflow.Bundle) (bool, []string, *core.RefusalError)
	fix(ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, directive, dir, launchID string) *core.RefusalError
	declared(dir string) bool
	verify(ctx context.Context, b *reviewflow.Bundle, timeout string) (*Verify, []string)
	cost(dir string, now time.Time) spend.MTD
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

// review launches the review-only agent run against the round's bundle.
func (p *flowPhases) review(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch,
) ([]string, *core.RefusalError) {
	ran, ref := p.req.Flow.Run(ctx, b, launch, nil)
	if ref != nil {
		return nil, ref
	}
	return ran.Warnings, nil
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
// prices the spawn's ledger row from the spend the agent reported in
// fix-cost.json. An absent or unreadable report leaves the spawn unknown.
func (p *flowPhases) fix(
	ctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, directive, dir, launchID string,
) *core.RefusalError {
	req := p.req.Flow.RunRequest(b, launch, nil)
	if _, ref := runcore.Dispatch(ctx, req, p.req.Flow.Agents, directive); ref != nil {
		return ref
	}
	usd, ok := reportedSpend(dir)
	if !ok {
		return nil
	}
	if ref := runcore.PriceSpawn(ctx, req, dir, launchID, usd); ref != nil {
		p.req.Flow.Warn("could not record the fix's reported spend: " + ref.Message)
	}
	return nil
}

// reportedSpend reads the fix agent's {"est_usd": n} report: a finite,
// nonnegative amount, else nothing.
func reportedSpend(dir string) (string, bool) {
	raw, ok := readReport(dir)
	if !ok {
		return "", false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return "", false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return "", false
	}
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

// cost reads one round bundle's own cost receipts.
func (p *flowPhases) cost(dir string, now time.Time) spend.MTD {
	return spend.BundleMTD(dir, now)
}

// readReport reads the agent-written report confined to the bundle: a
// regular file no larger than the cap, never a path that leaves the bundle.
func readReport(dir string) ([]byte, bool) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, false
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(fixCostFile)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFixCostBytes {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxFixCostBytes))
	return raw, err == nil
}
