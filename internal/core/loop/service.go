// Package loopcore is the self-review loop's domain service: bounded rounds of
// fetch-local → run (review only) → record --require-coverage → gate, with an
// agent fix phase between a failed gate and the next round, which re-fetches
// and reviews the fixes from scratch.
//
// Every round composes the shipped services in-process through the shared
// review pipeline (internal/core/reviewflow), the report service's gate, and
// the verify service, so the loop never re-implements a phase and never
// publishes: it edits nothing itself, commits nothing, pushes nothing. The
// agent's fixes land in the working tree only.
//
// internal/loop owns argv parsing, rendering, and exit mapping.
package loopcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
)

const (
	// verbName is the CLI verb the plan's commands start from.
	verbName = "verdict"
	// codeBadFlags is the shared refusal for an option the loop cannot honor.
	codeBadFlags = "bad-flags"
	// codeCanceled is the shared cancel code.
	codeCanceled = "canceled"
	msgCanceled  = "canceled before loop completed"
	// codeAgentFailed and codeAgentTimeout are run's own codes: the first is
	// the agent-failed stop, the second is the wall budget (every spawn's
	// watchdog is the budget that remains).
	codeAgentFailed  = "agent-failed"
	codeAgentTimeout = "agent-timeout"

	// codeBudgetUnknown is run's own code for spend it cannot measure.
	codeBudgetUnknown = "budget-unknown"
	// codeSpendCapped is run's code for a spawn the monthly ceiling refuses.
	codeSpendCapped = "spend-capped"
	// maxRounds bounds --max-rounds: every round is a fresh bundle and up to
	// three agent spawns, and the plan renders each one.
	maxRounds = 20
	// reviewAttempts is the review's launch-to-check rounds: run's default,
	// passed explicitly so the per-attempt watchdog can split the budget.
	reviewAttempts = 2
	// findersOne is the review topology: one finder per round, the same fast
	// pass demo launches.
	findersOne = "1"
	// gateSeverity gates every open finding: the loop's pass means nothing
	// open, at any severity.
	gateSeverity = "informational"
	gateCoverage = "require"
	// gateLive keeps the gate offline: a local bundle has no host threads.
	gateLive = "skip"
	// verifyCapS bounds one verify run inside the wall budget, matching the
	// verify verb's own default watchdog.
	verifyCapS = 300
	// namePrefix starts every round's fetch-local name; the store adds local-.
	namePrefix  = "loop-"
	localPrefix = "local-"
	// idHashLen keeps the run id short enough to read in a bundle listing.
	idHashLen = 6
	// stampLayout is the UTC run stamp the default output and the bundle
	// names carry.
	stampLayout = "20060102T150405Z"
	// patchFile is the verify input the fix agent declares a command in.
	patchFile    = "patch.json"
	fixCostFile  = "fix-cost.json"
	findingsFile = "findings.json"
	summaryFile  = "summary.json"
	// dirMode and fileMode are the receipt permissions: the operator's own
	// review records, readable like the bundle beside them.
	dirMode  = 0o755
	fileMode = 0o644
	// floatBits renders a decimal amount as the receipt's JSON number.
	floatBits = 64
	// statusOpen is the findings-store status a fix phase works on.
	statusOpen = "open"
)

// defaultFixPrompt is the fix phase's built-in instructions. The context block
// that follows it names the round, the findings file, and the bundle.
const defaultFixPrompt = "Self-review loop — fix phase. A review of this working tree is " +
	"recorded in the findings file named below: fix every finding whose status is \"open\". " +
	"Edit the working tree only — do not commit, stage, push, or open a pull request, and " +
	"change nothing under .verdict/ except the patch.json and fix-cost.json named below. " +
	"To prove a fix, write that patch.json as {\"verifyCmd\": \"<one command that exercises " +
	"the fix>\"}: the loop runs `verdict verify` on it after you exit and keeps the receipt. " +
	"Before you exit, report your own spend for this fix as {\"est_usd\": <dollars>} in the " +
	"fix-cost.json named below; without it the loop counts this spend as unknown. " +
	"The next round fetches the working tree again and reviews your fixes from scratch."

// Request is one loop invocation, resolved by the adapter.
type Request struct { //nolint:govet // field grouping documents the request surface
	// Flow carries the runtime, the commanders, the agent table, and the
	// render configuration every composed service shares.
	Flow *reviewflow.Flow
	// RepoRoot is the reviewed checkout: the bundles land in its
	// .verdict/state and the agent works in it.
	RepoRoot string
	// Repo is the owner/repo positional the bundles are recorded under.
	Repo  string
	Agent string
	// MaxRounds is the round ceiling (>= 1).
	MaxRounds int
	// Budget is the wall budget every phase and spawn shares.
	Budget time.Duration
	// MaxCostUSD is the exact decimal ceiling on the summed round costs; ""
	// sets none (the monthly ceiling still binds).
	MaxCostUSD string
	// Output is the new receipts directory; "" writes under
	// .verdict/loop/<stamp> in the checkout.
	Output string
	// FixPrompt replaces the built-in fix instructions when non-empty.
	FixPrompt string
	// Print renders the plan and spawns nothing.
	Print bool
	// VerifySandbox and VerifySandboxNetworkOnly pass `verify --sandbox` and
	// `verify --sandbox-network-only` to the fix phase's verify step: the
	// command is the agent's own proposal from patch.json. Both off matches
	// `verdict verify`; they are mutually exclusive.
	VerifySandbox            bool
	VerifySandboxNetworkOnly bool
}

// Response carries the loop outcome for the adapter.
type Response struct {
	At       string
	Data     *Data
	Next     string
	Warnings []string
}

// loop is one invocation's state.
type loop struct { //nolint:govet // request first: the service reads one invocation
	req      *Request
	ph       phases
	started  time.Time
	deadline time.Time
	runID    string
	output   string
	spent    *big.Rat
	known    bool
	unknown  bool
	data     *Data
	warnings []string
}

// Run executes `loop <owner/repo> --agent <name> [limits]`.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	return run(ctx, req, &flowPhases{req: req})
}

// run is Run over an injected phase seam.
func run(ctx context.Context, req *Request, ph phases) (*Response, *core.RefusalError) {
	if ref := validate(req); ref != nil {
		return nil, ref
	}
	lp := newLoop(req)
	lp.ph = ph
	// The plan resolves the agent before anything is written, so an unknown
	// name refuses here rather than after a receipts directory exists.
	plan, ref := lp.plan(ctx)
	if ref != nil {
		return nil, ref
	}
	lp.data.Plan = plan
	if req.Print {
		lp.data.Stop = StopPlanned
		return lp.response(), nil
	}
	if ref := lp.createOutput(); ref != nil {
		return nil, ref
	}
	lp.rounds(ctx)
	lp.data.ElapsedMS = lp.now().Sub(lp.started).Milliseconds()
	lp.data.CostUSD = lp.costTotal()
	if ref := lp.writeReceipt(summaryFile, lp.data); ref != nil {
		return nil, ref
	}
	return lp.response(), nil
}

// validate refuses the option values no loop could honor before anything is
// written or spawned.
func validate(req *Request) *core.RefusalError {
	switch {
	case req.Flow == nil || req.Flow.Runtime == nil:
		return &core.RefusalError{Code: codeBadFlags, Message: "loop needs a runtime"}
	case req.Agent == "":
		return &core.RefusalError{Code: codeBadFlags, Message: "loop needs --agent <name> (any name from " +
			"`verdict run`'s agent table or your settings.json agents)"}
	case req.MaxRounds < 1 || req.MaxRounds > maxRounds:
		return &core.RefusalError{Code: codeBadFlags, Message: fmt.Sprintf(
			"--max-rounds must be a whole number from 1 to %d", maxRounds)}
	case req.Budget <= 0:
		return &core.RefusalError{Code: codeBadFlags, Message: "--budget must be a positive duration"}
	case req.VerifySandbox && req.VerifySandboxNetworkOnly:
		return &core.RefusalError{Code: codeBadFlags, Message: "--sandbox and --sandbox-network-only are " +
			"mutually exclusive (pick one sandbox for the verify step)"}
	case req.MaxCostUSD != "" && !positiveDecimal(req.MaxCostUSD):
		return &core.RefusalError{Code: codeBadFlags, Message: fmt.Sprintf(
			"--max-cost-usd must be a positive dollar amount, got '%s'", req.MaxCostUSD)}
	}
	return nil
}

// positiveDecimal accepts a finite positive amount the spend guards accept.
func positiveDecimal(s string) bool {
	if !spend.ValidLimit(s) {
		return false
	}
	v, err := strconv.ParseFloat(s, floatBits)
	return err == nil && v > 0 && !math.IsInf(v, 0)
}

func newLoop(req *Request) *loop {
	started := time.Now()
	if req.Flow.Now != nil {
		started = req.Flow.Now()
	}
	output := req.Output
	if output == "" {
		output = filepath.Join(req.RepoRoot, ".verdict", "loop", started.UTC().Format(stampLayout))
	}
	data := &Data{
		Agent:      req.Agent,
		MaxRounds:  req.MaxRounds,
		BudgetS:    int(math.Ceil(req.Budget.Seconds())),
		MaxCostUSD: decimalNumber(req.MaxCostUSD),
		Rounds:     []Round{},
		Plan:       []PlanStep{},
	}
	return &loop{
		req:      req,
		started:  started,
		deadline: started.Add(req.Budget),
		runID:    runID(started, output),
		output:   output,
		spent:    new(big.Rat),
		data:     data,
	}
}

// runID names one invocation's bundles: the start stamp plus a short hash of
// the output path, so two loops started in the same second with different
// receipts directories never share a bundle.
func runID(started time.Time, output string) string {
	sum := sha256.Sum256([]byte(output))
	return strings.ToLower(started.UTC().Format(stampLayout)) + "-" + hex.EncodeToString(sum[:])[:idHashLen]
}

func (lp *loop) now() time.Time {
	if lp.req.Flow.Now == nil {
		return time.Now()
	}
	return lp.req.Flow.Now()
}

// createOutput creates the receipts directory, which must be new: receipts
// from two invocations in one directory would read as one run.
func (lp *loop) createOutput() *core.RefusalError {
	if err := os.MkdirAll(filepath.Dir(lp.output), dirMode); err != nil {
		return &core.RefusalError{Code: codeBadFlags, Message: fmt.Sprintf(
			"cannot create the receipts directory's parent: %v", err)}
	}
	if err := os.Mkdir(lp.output, dirMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return &core.RefusalError{Code: codeBadFlags, Message: "--output " + lp.output +
				" already exists — pass a new directory for each loop"}
		}
		return &core.RefusalError{Code: codeBadFlags, Message: fmt.Sprintf("cannot create %s: %v", lp.output, err)}
	}
	lp.data.Output = &lp.output
	return nil
}

// rounds runs the loop until a stop condition holds.
func (lp *loop) rounds(ctx context.Context) {
	for n := 1; ; n++ {
		if stop, ref := lp.budgetStop(ctx); stop != "" {
			lp.stop(stop, ref)
			return
		}
		rd := &Round{
			Round: n, Bundle: localPrefix + lp.name(n), StartedAt: core.NowUTC(lp.now()), OpenSigs: []string{},
		}
		stop, ref := lp.round(ctx, rd)
		rd.EndedAt = core.NowUTC(lp.now())
		rd.CostUSD = lp.roundCost(rd)
		rd.Refusal = ref
		lp.data.Rounds = append(lp.data.Rounds, *rd)
		if wref := lp.writeReceipt(fmt.Sprintf("round-%d.json", n), rd); wref != nil {
			lp.warn(wref.Message)
		}
		if stop != "" {
			lp.stop(stop, ref)
			return
		}
	}
}

// budgetStop reports what forbids starting another round: a cancel, the wall
// budget, or the cost budget — reached, or unmeasurable.
func (lp *loop) budgetStop(ctx context.Context) (string, *Refusal) {
	switch {
	case ctx.Err() != nil:
		return StopRefused, canceledRefusal()
	case !lp.now().Before(lp.deadline):
		return StopWallBudget, nil
	case lp.overCost():
		return StopCostBudget, nil
	case lp.req.MaxCostUSD != "" && lp.unknown:
		return StopCostBudget, lp.unmeasured(len(lp.data.Rounds))
	}
	return "", nil
}

func (lp *loop) stop(reason string, ref *Refusal) {
	lp.data.Stop = reason
	lp.data.Passed = reason == StopPass
	lp.data.Refusal = ref
}

// round runs one round's phases and reports the stop reason ("" continues).
// Every phase runs under the wall deadline, so a fetch, record, or gate that
// hangs stops the loop on the budget like a spawn does.
func (lp *loop) round(ctx context.Context, rd *Round) (string, *Refusal) {
	dctx, cancel := context.WithTimeout(ctx, lp.deadline.Sub(lp.now()))
	defer cancel()
	b := lp.bundle(rd.Round)
	warnings, ref := lp.ph.fetch(dctx, lp.name(rd.Round))
	lp.warnAll(warnings)
	if ref != nil {
		return lp.refused(ctx, ref)
	}
	dir := filepath.Join(readcommon.StateDir(lp.req.RepoRoot), rd.Bundle)
	if stop, ref := lp.review(ctx, dctx, rd.Round, b, dir); stop != "" {
		return stop, ref
	}
	recorded, warnings, ref := lp.ph.record(dctx, b)
	lp.warnAll(warnings)
	if ref != nil {
		return lp.refused(ctx, ref)
	}
	lp.observe(rd, recorded)
	passed, warnings, ref := lp.ph.gate(dctx, b)
	lp.warnAll(warnings)
	if ref != nil {
		return lp.refused(ctx, ref)
	}
	rd.Gate = &passed
	if passed {
		return lp.passed(ctx)
	}
	if stop, ref := lp.afterFailedGate(rd, dir); stop != "" {
		return stop, ref
	}
	return lp.fix(ctx, dctx, rd, b, dir)
}

// passed is a passing gate's stop. A pass the operator canceled into is
// still a cancel: the run must not exit 0 after a ^C.
func (lp *loop) passed(ctx context.Context) (string, *Refusal) {
	if ctx.Err() != nil {
		return StopRefused, canceledRefusal()
	}
	return StopPass, nil
}

// refused maps a composed verb's refusal onto the loop's stop. A cancel is
// the operator's ^C when the loop's own context is done, and the wall budget
// when only the round's deadline ran out.
func (lp *loop) refused(ctx context.Context, ref *core.RefusalError) (string, *Refusal) {
	switch {
	case ctx.Err() != nil && ref.Code == codeCanceled:
		return StopRefused, asRefusal(ref)
	case ctx.Err() != nil:
		return StopRefused, canceledRefusal()
	case ref.Code == codeCanceled:
		return StopWallBudget, asRefusal(ref)
	}
	return refusalStop(ref), asRefusal(ref)
}

// afterFailedGate checks the stops that forbid a fix phase: no progress since
// the previous round, the last round (nothing would review the fix), and the
// budgets — including spend the round could not measure while a cost ceiling
// is set, which stops the loop rather than spending blind.
func (lp *loop) afterFailedGate(rd *Round, dir string) (string, *Refusal) {
	if lp.noProgress(rd) {
		return StopNoProgress, nil
	}
	if rd.Round >= lp.req.MaxRounds {
		return StopMaxRounds, nil
	}
	if !lp.now().Before(lp.deadline) {
		return StopWallBudget, nil
	}
	pending := lp.ph.cost(dir)
	if lp.overCostWith(pending) {
		return StopCostBudget, nil
	}
	if lp.req.MaxCostUSD != "" && pending.UnknownCount > 0 {
		return StopCostBudget, lp.unmeasured(rd.Round)
	}
	return "", nil
}

// unmeasured is the refusal a cost-ceilinged loop stops with when a round's
// spend has no known amount.
func (lp *loop) unmeasured(n int) *Refusal {
	return &Refusal{Code: codeBudgetUnknown, Message: fmt.Sprintf("round %d's spend could not be measured "+
		"(an agent spawn reported no cost) — --max-cost-usd %s cannot be enforced, so the loop stopped "+
		"before spending more", n, lp.req.MaxCostUSD)}
}

// noProgress reports a plateau: no finding open after the previous round was
// resolved by this one — the previous open sigs are all still open.
func (lp *loop) noProgress(rd *Round) bool {
	if len(lp.data.Rounds) == 0 || rd.Open == nil {
		return false
	}
	prev := lp.data.Rounds[len(lp.data.Rounds)-1]
	if prev.Open == nil {
		return false
	}
	for _, sig := range prev.OpenSigs {
		if !slices.Contains(rd.OpenSigs, sig) {
			return false
		}
	}
	return true
}

// review launches the review-only agent run against the round's bundle. Run
// admits every attempt it spawns through the phase (the first launch and each
// repair round, receipted separately); its repair loop makes up to
// reviewAttempts launches, so each one's watchdog gets that share of the
// remaining budget, and the round's deadline bounds the whole review either
// way.
func (lp *loop) review(
	ctx, dctx context.Context, n int, b *reviewflow.Bundle, dir string,
) (string, *Refusal) {
	left, ok := lp.secondsLeft()
	if !ok {
		return StopWallBudget, nil
	}
	launch := reviewflow.Launch{
		Agent: lp.req.Agent, FindersRaw: findersOne,
		TimeoutRaw: strconv.Itoa(max(1, int(math.Ceil(left/reviewAttempts)))),
		RoundsRaw:  strconv.Itoa(reviewAttempts),
	}
	warnings, ref := lp.ph.review(dctx, b, launch, dir, lp.name(n)+"-review")
	lp.warnAll(warnings)
	if ref != nil {
		return lp.refused(ctx, ref)
	}
	return "", nil
}

// admit applies the monthly spend ceiling and writes the fix spawn's ledger
// receipt. A spend ceiling's refusal is the cost budget, whoever set it; any
// other admission refusal (a malformed ceiling, an unwritable ledger, a
// cancel) keeps its own outcome.
func (lp *loop) admit(
	ctx, dctx context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, dir, launchID string,
) (string, *Refusal) {
	warnings, ref := lp.ph.authorize(dctx, b, launch, dir, launchID)
	lp.warnAll(warnings)
	if ref == nil {
		return "", nil
	}
	return lp.refused(ctx, ref)
}

// fix runs the fix phase: one agent spawn with the fix directive, then verify
// on the command the agent declared, if any. The verify outcome is recorded,
// never a stop — the next round's review grades the fix — except a cancel,
// which stops the loop at the round where it landed.
func (lp *loop) fix(ctx, dctx context.Context, rd *Round, b *reviewflow.Bundle, dir string) (string, *Refusal) {
	timeout, ok := lp.remaining(0)
	if !ok {
		return StopWallBudget, nil
	}
	launch := reviewflow.Launch{Agent: lp.req.Agent, TimeoutRaw: timeout}
	launchID := lp.name(rd.Round) + "-fix"
	if stop, ref := lp.admit(ctx, dctx, b, launch, dir, launchID); stop != "" {
		return stop, ref
	}
	rd.Fix = &Fix{}
	if ref := lp.ph.fix(dctx, b, launch, lp.fixDirective(rd.Round, dir), dir, launchID); ref != nil {
		return lp.refused(ctx, ref)
	}
	if !lp.ph.declared(dir) {
		return "", nil
	}
	return lp.verifyFix(ctx, dctx, rd, b)
}

// verifyFix runs verify on the command the fix declared and keeps its
// receipt. A cancel that lands in verify is the round's stop.
func (lp *loop) verifyFix(ctx, dctx context.Context, rd *Round, b *reviewflow.Bundle) (string, *Refusal) {
	timeout, ok := lp.remaining(verifyCapS)
	if !ok {
		rd.Fix.Verify = &Verify{Refusal: &Refusal{Code: codeAgentTimeout, Message: "no wall budget left to run verify"}}
		return "", nil
	}
	verify, warnings := lp.ph.verify(dctx, b, timeout)
	lp.warnAll(warnings)
	rd.Fix.Verify = verify
	if (verify.Refusal != nil && verify.Refusal.Code == codeCanceled) || dctx.Err() != nil {
		return lp.refused(ctx, &core.RefusalError{Code: codeCanceled, Message: verifyCanceled(verify)})
	}
	return "", nil
}

// verifyCanceled is the cancel message verify raised, or the shared one.
func verifyCanceled(v *Verify) string {
	if v.Refusal != nil && v.Refusal.Message != "" {
		return v.Refusal.Message
	}
	return msgCanceled
}

// canceledRefusal is the shared cancel the loop stops with.
func canceledRefusal() *Refusal {
	return &Refusal{Code: codeCanceled, Message: msgCanceled}
}

// fixDirective is the fix agent's prompt: the instructions (the operator's
// --fix-prompt file, else the built-in text) and the round's context block.
func (lp *loop) fixDirective(n int, dir string) string {
	instructions := strings.TrimSpace(lp.req.FixPrompt)
	if instructions == "" {
		instructions = defaultFixPrompt
	}
	return instructions + "\n\n" + fmt.Sprintf("Round %d of at most %d.\nFindings file: %s\nBundle: %s\n"+
		"fix-cost.json: %s\npatch.json: %s", n, lp.req.MaxRounds, filepath.Join(dir, findingsFile), dir,
		filepath.Join(dir, fixCostFile), filepath.Join(dir, patchFile))
}

// remaining is the watchdog budget for the next spawn: the whole seconds the
// wall budget has left, capped at limit when one is set. ok is false once the
// budget is spent.
func (lp *loop) remaining(limit int) (string, bool) {
	left, ok := lp.secondsLeft()
	if !ok {
		return "", false
	}
	secs := int(math.Ceil(left))
	if limit > 0 && secs > limit {
		secs = limit
	}
	return strconv.Itoa(secs), true
}

// secondsLeft is the wall budget that remains, false once it is spent.
func (lp *loop) secondsLeft() (float64, bool) {
	left := lp.deadline.Sub(lp.now())
	return left.Seconds(), left > 0
}

// observe copies the record's open findings into the round receipt.
func (lp *loop) observe(rd *Round, data *core.RecordData) {
	if data == nil {
		return
	}
	open := data.Findings.Open
	rd.Open = &open
	for i := range data.Items {
		if data.Items[i].Status == statusOpen && data.Items[i].Sig != "" {
			rd.OpenSigs = append(rd.OpenSigs, data.Items[i].Sig)
		}
	}
}

// roundCost reads the round bundle's own cost receipts: the known amount, or
// null when the agent reported none. An unknown receipt leaves the cost
// ceiling enforced against known spend only, and says so.
func (lp *loop) roundCost(rd *Round) *float64 {
	dir := filepath.Join(readcommon.StateDir(lp.req.RepoRoot), rd.Bundle)
	mtd := lp.ph.cost(dir)
	if mtd.UnknownCount > 0 {
		lp.unknown = true
		// With a cost ceiling the next round's start stops on this instead.
		if lp.req.MaxCostUSD == "" {
			lp.warn(fmt.Sprintf("round %d's spend could not be fully measured (an agent spawn reported "+
				"no cost) — it counts as unknown toward the monthly spend ceiling", rd.Round))
		}
	}
	if mtd.KnownUSD == "" {
		return nil
	}
	if rat, ok := new(big.Rat).SetString(mtd.KnownUSD); ok {
		lp.spent.Add(lp.spent, rat)
		lp.known = true
	}
	return decimalNumber(mtd.KnownUSD)
}

// overCost reports whether the summed round costs reached --max-cost-usd.
func (lp *loop) overCost() bool {
	return lp.overCostWith(spend.MTD{})
}

// overCostWith adds one in-flight round's known spend to the settled total.
func (lp *loop) overCostWith(pending spend.MTD) bool {
	if lp.req.MaxCostUSD == "" {
		return false
	}
	total := new(big.Rat).Set(lp.spent)
	if rat, ok := new(big.Rat).SetString(pending.KnownUSD); ok && pending.KnownUSD != "" {
		total.Add(total, rat)
	}
	return !spend.Exceeds(lp.req.MaxCostUSD, spend.FormatRat(total))
}

// costTotal is the summary's summed known spend, null when no round reported
// a known amount.
func (lp *loop) costTotal() *float64 {
	if !lp.known {
		return nil
	}
	return decimalNumber(spend.FormatRat(lp.spent))
}

// bundle is one round's target as the shared pipeline names it.
func (lp *loop) bundle(n int) *reviewflow.Bundle {
	return &reviewflow.Bundle{Dir: lp.req.RepoRoot, Repo: lp.req.Repo, Pr: localPrefix + lp.name(n)}
}

// name is one round's fetch-local name.
func (lp *loop) name(n int) string {
	return fmt.Sprintf("%s%s-%d", namePrefix, lp.runID, n)
}

// writeReceipt writes one receipt into the output directory.
func (lp *loop) writeReceipt(name string, v any) *core.RefusalError {
	body := jq.PrettyOrdered(core.Wire(v))
	if err := atomic.WriteFileMode(filepath.Join(lp.output, name), []byte(body), fileMode); err != nil {
		return &core.RefusalError{Code: "record-failed", Message: fmt.Sprintf("cannot write %s: %v", name, err)}
	}
	return nil
}

func (lp *loop) warn(msg string) {
	lp.warnings = append(lp.warnings, msg)
	lp.req.Flow.Warn(msg)
}

// warnAll keeps a composed service's warnings in the loop's list once each.
// The services already streamed them, so they are only recorded here.
func (lp *loop) warnAll(msgs []string) {
	for _, msg := range msgs {
		if !slices.Contains(lp.warnings, msg) {
			lp.warnings = append(lp.warnings, msg)
		}
	}
}

// response builds the adapter's answer.
func (lp *loop) response() *Response {
	return &Response{
		At:       core.NowUTC(lp.now()),
		Data:     lp.data,
		Next:     next(lp.data),
		Warnings: lp.warnings,
	}
}

// next names what the operator does after the loop.
func next(d *Data) string {
	switch d.Stop {
	case StopPlanned:
		return "re-run without --print to start the loop"
	case StopPass:
		return "none — the last round's gate passed; open the pull request"
	case StopMaxRounds, StopNoProgress:
		return "read the last round's open findings, fix or triage them, then run the loop again"
	case StopWallBudget, StopCostBudget:
		return "raise the budget the stop names, or fix the open findings by hand"
	}
	return "resolve the refusal in summary.json, then run the loop again"
}

// refusalStop maps a composed verb's refusal onto the loop's stop reason.
func refusalStop(ref *core.RefusalError) string {
	switch ref.Code {
	case codeAgentFailed:
		return StopAgentFailed
	case codeAgentTimeout:
		return StopWallBudget
	case codeSpendCapped, codeBudgetUnknown:
		return StopCostBudget
	}
	return StopRefused
}

func asRefusal(ref *core.RefusalError) *Refusal {
	if ref == nil {
		return nil
	}
	return &Refusal{Code: ref.Code, Message: ref.Message}
}

// decimalNumber renders an exact decimal literal as the receipt's number,
// null for "".
func decimalNumber(s string) *float64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, floatBits)
	if err != nil {
		return nil
	}
	return &v
}
