package run

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/repair"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// coordinator runs one two-finder execution. Its mutable fields are written
// only between phases: a parallel phase works from its own state clone and
// outcome, and the fan-in merges those in index order.
type coordinator struct {
	st          *state
	base        *launchArgs
	snapshot    executionSnapshot
	resume      *resumePlan
	fingerprint resumeFingerprint
	record      *jq.Obj
	review      *jq.Obj
	plan        *jq.Obj
	binding     *jq.Obj
	response    *core.RunResponse
	causes      map[string]*core.RefusalError
	dir         string
	source      string
	id          string
	reviews     []*jq.Obj
	failures    []any
	snapMu      sync.RWMutex
	rounds      int
	// resumable records that the aggregate refused an execution `run
	// --resume` continues, so the response can say so.
	resumable bool
}

// check is the snapshot checkpoint. It takes the read lock, so a phase can
// never iterate the digest map while its sibling binds a name into it.
func (c *coordinator) check() *core.RefusalError {
	c.snapMu.RLock()
	defer c.snapMu.RUnlock()
	return c.snapshot.check(c.dir)
}

// bind records the digests of artifacts a phase just wrote.
func (c *coordinator) bind(names ...string) *core.RefusalError {
	c.snapMu.Lock()
	defer c.snapMu.Unlock()
	return c.snapshot.bind(c.dir, names...)
}

// bindPhase records every artifact one phase directory holds.
func (c *coordinator) bindPhase(name string) *core.RefusalError {
	c.snapMu.Lock()
	defer c.snapMu.Unlock()
	return c.snapshot.bindPhase(c.dir, name)
}

// artifacts is the digest map the launcher record carries. It is read after the
// fan-in has joined; the read lock keeps that contract explicit.
func (c *coordinator) artifacts() executionSnapshot {
	c.snapMu.RLock()
	defer c.snapMu.RUnlock()
	return c.snapshot
}

// TwoFinders opts into the coordinator without changing the public CLI.
func TwoFinders(ctx context.Context, req *core.RunRequest) (*core.RunResponse, *core.RefusalError) {
	return execute(ctx, &state{req: req, twoFinders: true, warnings: append([]string{}, req.Warnings...)})
}

func (st *state) coordinate(ctx context.Context, base *launchArgs, rounds int) (*core.RunResponse, *core.RefusalError) {
	c := &coordinator{
		st: st, base: base, dir: base.target.evidence, id: repair.Nonce(), rounds: rounds,
		reviews: make([]*jq.Obj, finderCount), failures: []any{},
	}
	ref := c.run(ctx)
	// Every launch spent whether or not the execution completed, so the
	// receipt settles either way. The settlement must outlive a cancel.
	st.settleExecution(context.WithoutCancel(ctx), c.dir)
	c.executionResponse()
	if ref == nil {
		ref = c.executionRefusal()
	}
	c.settleExit(ref)
	return c.response, ref
}

// settleExit keeps the agent's exit status on the response only when the run's
// own refusal is the agent's failure. The response is whichever phase reported
// last, so a finished sibling or a later phase's failure must not turn an
// evidence refusal into the agent's exit code.
func (c *coordinator) settleExit(ref *core.RefusalError) {
	if c.response != nil && (ref == nil || ref.Code != codeAgentFailed) {
		c.response.Exit = 0
	}
}

func (c *coordinator) run(ctx context.Context) *core.RefusalError {
	if ref := c.initialize(ctx); ref != nil {
		return ref
	}
	if ref := c.runDiscovery(ctx); ref != nil {
		return ref
	}
	if ref := c.freeze(); ref != nil {
		return ref
	}
	if ref := c.runCross(ctx); ref != nil {
		return ref
	}
	return c.finish()
}

// initialize reads the capture, starts the execution, and pins the snapshot
// every checkpoint compares against.
func (c *coordinator) initialize(ctx context.Context) *core.RefusalError {
	captured, ref := readFinderCapture(c.dir)
	if ref != nil {
		return ref
	}
	if captured == nil {
		return c.st.refuse(codeMissingBundle, "two finders require a captured run")
	}
	if ref := c.start(ctx, captured); ref != nil {
		return ref
	}
	c.snapshot, ref = captureSnapshot(c.dir)
	if ref != nil {
		return ref
	}
	return c.seedSealed()
}

// start records the execution and authorizes its spend. A resumed execution
// already carries the record every resume check reads, so its spend — and every
// refusal the resume earned — is decided before the launcher file is touched
// and before a single agent starts. A fresh execution records itself first,
// because the spend projection reads its execution id out of run.json.
func (c *coordinator) start(ctx context.Context, captured *config.RunState) *core.RefusalError {
	if c.st.req.Resume {
		return c.startResume(ctx, captured)
	}
	if ref := c.captureExecution(captured); ref != nil {
		return ref
	}
	return c.st.authorizeSpend(ctx, c.dir, c.rounds)
}

// startResume continues the recorded execution. The spend guard runs only when
// this attempt will launch something: a resume that reuses every phase of a
// finished execution spends nothing, so a tight budget must not stop it.
func (c *coordinator) startResume(ctx context.Context, captured *config.RunState) *core.RefusalError {
	plan, ref := c.planResume(captured)
	if ref != nil {
		return ref
	}
	c.resume = plan
	if plan.launchesPlanned() {
		if ref := c.st.authorizeSpend(ctx, c.dir, c.rounds); ref != nil {
			return ref
		}
	}
	return c.captureResumed()
}

// sessionFlags is the shared session-continuation vocabulary: flag names every
// shipped CLI reading that way continues a previous conversation. A row whose
// CLI spells continuation differently, or reuses one of these names for
// something else, declares it in Launch.ResumeArgs.
var sessionFlags = []string{"--resume", "--continue", "--session", "--session-id", "-r"}

// resumeArguments reports whether the operator's pass-through asks the agent to
// continue an earlier session, which would let a later finder read an earlier
// review's context. A bare subcommand matches too: Codex spells continuation
// `exec resume`.
func resumeArguments(def *agentcatalog.Launch, args []string) bool {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		if slices.Contains(sessionFlags, name) || slices.Contains(def.ResumeArgs, name) {
			return true
		}
	}
	return false
}

// finderPhase names one finder's phase directory, the literal path every
// consumer of the launcher record already knows.
func finderPhase(index int) string { return fmt.Sprintf("x-finder-%d", index+1) }

// openPhase resolves one phase directory, reusing the interrupted execution's
// own directory when this invocation continues it.
func (c *coordinator) openPhase(name string) (string, *core.RefusalError) {
	return phaseDirectory(c.dir, name, c.resume != nil)
}

// prepareDiscovery creates both phase directories and pins their procedures and
// the evidence inventory in index order before either agent starts, so the two
// launches begin from the same bytes and neither can see the other's writes. A
// finder a resumed run verified in place is pinned here instead, and its launch
// is left out.
func (c *coordinator) prepareDiscovery() ([]*launchArgs, *core.RefusalError) {
	args := make([]*launchArgs, finderCount)
	for i := range finderCount {
		arg, ref := c.prepareFinder(i)
		if ref != nil {
			return nil, ref
		}
		args[i] = arg
	}
	if ref := c.check(); ref != nil {
		return nil, ref
	}
	return args, nil
}

// prepareFinder opens one finder phase: a phase a resumed run verified is
// pinned in place, and a phase this run must launch gets its procedure and
// argv.
func (c *coordinator) prepareFinder(index int) (*launchArgs, *core.RefusalError) {
	output, ref := c.openPhase(finderPhase(index))
	if ref != nil {
		return nil, ref
	}
	if c.reusedFinder(index) {
		return nil, c.reuseDiscovery(index)
	}
	if c.resume != nil {
		if ref := c.clearStaleArtifact(finderPhase(index), reviewFile); ref != nil {
			return nil, ref
		}
	}
	return c.discoveryArgs(output, index)
}

// reusedFinder reports whether this invocation continues finder index from the
// interrupted execution instead of launching it again.
func (c *coordinator) reusedFinder(index int) bool {
	return c.resume != nil && c.resume.reusableFinder(index)
}

// reuseDiscovery continues one verified finder phase in place, so the fan-in
// reads it exactly as it reads a phase this invocation launched. The review
// comes from the bytes the plan sealed, and the phase is checked against that
// seal first, so the run never consumes or binds an artifact other than the one
// it verified.
func (c *coordinator) reuseDiscovery(index int) *core.RefusalError {
	if ref := c.sealedBytes(c.resume.finderBytes[index], filepath.Join(finderPhase(index), reviewFile)); ref != nil {
		return ref
	}
	c.reviews[index] = discoveryObject(c.resume.finderBytes[index])
	return c.bindPhase(finderPhase(index))
}

// discoveryOutcome is one finder's contribution to the fan-in: its review, the
// failure to record, and the refusal that ends the whole run. A worker writes
// only its own outcome, so no two goroutines share a field.
type discoveryOutcome struct {
	ref      *core.RefusalError
	cause    *core.RefusalError
	response *core.RunResponse
	review   *jq.Obj
	worker   *state
	failure  string
	fatal    bool
}

// discoverPhase runs one finder's launch. A fatal refusal — a cancel, a
// snapshot checkpoint, an unwritable phase, a phase that left a link — cancels
// the sibling immediately; an agent's own failure is recorded and lets the
// sibling finish, which is what keeps a partial execution partial.
func (c *coordinator) discoverPhase(ctx context.Context, index int, args *launchArgs, worker *state,
	cancel context.CancelFunc,
) discoveryOutcome {
	out := discoveryOutcome{worker: worker}
	name := finderPhase(index)
	out.response, out.ref = worker.invoke(ctx, args, c.rounds)
	if drift := c.phaseEnded(ctx); drift != nil {
		out.ref, out.fatal = drift, true
		cancel()
		return out
	}
	if out.ref != nil {
		out.failure, out.cause, out.ref = out.ref.Code, out.ref, nil
	}
	out.review = readDiscovery(args.abs)
	if out.review == nil && out.failure == "" {
		out.failure = codeNoArtifact
	}
	if bindRef := c.bindPhase(name); bindRef != nil {
		out.ref, out.fatal = bindRef, true
		cancel()
	}
	return out
}

func readDiscovery(path string) *jq.Obj {
	raw, ok := crashReviewBytes(path)
	if !ok {
		return nil
	}
	return discoveryObject(raw)
}

// discoveryObject normalizes and decodes the review bytes one phase left, which
// a resumed run reads from its own sealed copy rather than from disk again.
func discoveryObject(raw []byte) *jq.Obj {
	normalized, _ := recordcore.NormalizeReview(raw)
	value, err := jq.DecodeOrdered(normalized)
	if err != nil {
		return nil
	}
	obj, ok := value.(*jq.Obj)
	if !ok {
		return nil
	}
	return obj
}

func (c *coordinator) phaseEnded(ctx context.Context) *core.RefusalError {
	if ctx.Err() != nil {
		return c.st.refuse(codeCanceled, msgCanceled)
	}
	return c.check()
}

func (c *coordinator) failed(phase, code string) {
	c.failures = append(c.failures, jq.NewObj(keyPhase, phase, "code", code))
}

// resolveFailure records what this attempt saw of one phase: a phase that
// failed replaces the interruption's row for it, and a phase that succeeded
// clears that row, so the record names each failing phase once and never keeps
// a failure this attempt has already repaired.
func (c *coordinator) resolveFailure(phase, code string, cause *core.RefusalError) {
	delete(c.causes, phase)
	if cause != nil {
		if c.causes == nil {
			c.causes = map[string]*core.RefusalError{}
		}
		c.causes[phase] = cause
	}
	kept := make([]any, 0, len(c.failures)+1)
	for _, row := range c.failures {
		if jq.StrOf(readcommon.FieldOf(row, keyPhase)) != phase {
			kept = append(kept, row)
		}
	}
	c.failures = kept
	if code != "" {
		c.failed(phase, code)
	}
}

// deriveUnion builds this execution's aggregate review, plan, and binding from
// the finder reviews in memory. It writes nothing, so a resume can validate its
// recorded cross checks against exactly the batch the run would ask about
// before the record or the plan is published.
func (c *coordinator) deriveUnion() *core.RefusalError {
	diff, err := noblock.ReadFile(filepath.Join(c.dir, "full.diff"))
	if err != nil {
		return c.st.refuse(codeMissingBundle, "diff missing")
	}
	var unionErr error
	c.review, c.plan, unionErr = discoveryUnion(c.id, diff, c.reviews)
	if unionErr != nil {
		return c.st.refuse(codeBadFlags, unionErr.Error())
	}
	head := readcommon.OpenBundle(c.dir).Head()
	c.plan.Set("schema_version", "2")
	c.plan.Set("execution_id", c.id)
	c.plan.Set("head_sha", head)
	c.plan.Set("diff_sha256", shaHex(diff))
	c.plan.Set("review_sha256", shaHex(coordinatorBytes(c.review)))
	c.binding = jq.NewObj("schema_version", "2", "execution_id", c.id, "head_sha", head,
		"diff_sha256", shaHex(diff), "review_sha256", shaHex(coordinatorBytes(c.review)),
		"plan_sha256", shaHex(coordinatorBytes(c.plan)), "results", []any{})
	return nil
}

// freeze publishes the derived union: the aggregate review, the plan, and the
// binding this execution reports through.
func (c *coordinator) freeze() *core.RefusalError {
	if ref := c.deriveUnion(); ref != nil {
		return ref
	}
	if _, ref := c.openPhase(verificationDir); ref != nil {
		return ref
	}
	if ref := writeCoordinator(c.dir, reviewFile, c.review); ref != nil {
		return ref
	}
	planPath := filepath.Join(verificationDir, "plan.json")
	if ref := writeCoordinator(c.dir, planPath, c.plan); ref != nil {
		return ref
	}
	return c.bind(reviewFile, planPath)
}

func (c *coordinator) finish() *core.RefusalError {
	if ref := c.dedupe(); ref != nil {
		return ref
	}
	if ref := writeCoordinator(c.dir, filepath.Join(verificationDir, "verdicts.json"), c.binding); ref != nil {
		return ref
	}
	manifest := cloneUnionRow(c.binding)
	manifest.Set(keyFinders, finderCount)
	manifest.Set(keyFinderSource, c.source)
	setManifestBudget(manifest, c.st.budget)
	manifest.Set("results", nil)
	manifest.Set(keyFailures, c.failures)
	manifest.Set("artifacts", c.artifacts())
	manifest.Set("profiles", []any{
		jq.NewObj("finder", "finder-1", "profile", "contract-first"),
		jq.NewObj("finder", "finder-2", "profile", "behavior-first"),
	})
	status := statusComplete
	if len(c.failures) > 0 {
		status = statusPartial
	}
	manifest.Set(keyStatus, status)
	if ref := c.check(); ref != nil {
		return ref
	}
	manifest.Set(resumeRecordKey, c.resumeRecord(true))
	if ref := writeCoordinator(c.dir, launcherFile, manifest); ref != nil {
		return ref
	}
	return c.validateAggregate()
}

// setManifestBudget carries the review-budget decision when one participated.
// A nil budget leaves the key out entirely, so a launch that named no depth
// keeps its manifest byte-identical.
func setManifestBudget(manifest *jq.Obj, recorded *core.ReviewBudget) {
	if recorded == nil {
		return
	}
	manifest.Set("budget", core.Wire(recorded))
}

// causeLines names each failed phase, in phase order, with the agent's own
// error when it left one, so a partial execution's refusal carries the cause
// beside the evidence gap it left behind, and the recovery: `--resume` only
// for an execution it can continue, a fresh run for one whose bound artifacts
// no longer hold.
func (c *coordinator) causeLines() string {
	if len(c.failures) == 0 {
		return ""
	}
	phases := make([]string, 0, len(c.failures))
	for _, row := range c.failures {
		phase := jq.StrOf(readcommon.FieldOf(row, keyPhase))
		if cause := c.causes[phase]; cause != nil {
			phases = append(phases, phase+" "+cause.Code+": "+cause.Message)
		} else {
			phases = append(phases, phase+" "+jq.StrOf(readcommon.FieldOf(row, "code")))
		}
	}
	recovery := "; continue it with --resume: the phases that passed are reused and only the failed ones launch again"
	if !c.resumable {
		recovery = "; it cannot be resumed, so re-run without --resume to start a fresh execution"
	}
	return "; failed: " + strings.Join(phases, "; ") + recovery
}

func (c *coordinator) validateAggregate() *core.RefusalError {
	execution := panelcore.CheckExecution(c.dir, nil)
	if execution.Problem() != "" {
		c.resumable = execution.Incomplete()
		return c.st.refuse("review-insufficient", execution.Problem()+c.causeLines())
	}
	check, ref := repair.Check(c.dir, coordinatorBytes(c.review))
	if ref != nil {
		return ref
	}
	if ref := recordcore.ReviewRefusal(check, false); ref != nil {
		return ref
	}
	if ref := repair.InsufficientRefusal(check, ""); ref != nil {
		return ref
	}
	c.aggregateResponse()
	return nil
}

// runDiscovery launches both finders at once and merges what they left in index
// order, so the run reads the same whatever order the agents happened to finish
// in.
func (c *coordinator) runDiscovery(ctx context.Context) *core.RefusalError {
	args, ref := c.prepareDiscovery()
	if ref != nil {
		return ref
	}
	progress := &syncProgress{}
	workers := make([]*state, finderCount)
	skipped := make([]bool, finderCount)
	for i := range finderCount {
		workers[i] = c.st.phaseState(progress)
		skipped[i] = args[i] == nil
	}
	outcomes := make([]discoveryOutcome, finderCount)
	phaseCtx, phaseCancel := context.WithCancel(ctx)
	defer phaseCancel()
	var wg sync.WaitGroup
	for i := range finderCount {
		if skipped[i] {
			continue
		}
		wg.Go(func() {
			outcomes[i] = c.discoverPhase(phaseCtx, i, args[i], workers[i], phaseCancel)
		})
	}
	wg.Wait()
	return c.mergeDiscovery(ctx, outcomes, skipped)
}

// mergeDiscovery folds both finders' outcomes in index order: warnings first,
// then the cancel, the failures, the reviews, and the response the run reports,
// and one final checkpoint over everything both agents wrote. A finder a
// resumed run reused has no outcome, because it launched nothing.
func (c *coordinator) mergeDiscovery(ctx context.Context, outcomes []discoveryOutcome,
	skipped []bool,
) *core.RefusalError {
	for i := range outcomes {
		c.st.replay(outcomes[i].worker)
	}
	if ref := canceledRefusal(ctx, c.st); ref != nil {
		return ref
	}
	fatal := c.recordDiscoveries(outcomes, skipped)
	if fatal != nil {
		return fatal
	}
	return c.check()
}

// recordDiscoveries folds the launched finders' outcomes in index order and
// reports the refusal that ends the run, if any.
func (c *coordinator) recordDiscoveries(outcomes []discoveryOutcome, skipped []bool) *core.RefusalError {
	var fatal *core.RefusalError
	for i := range outcomes {
		if skipped[i] {
			continue
		}
		fatal = mergeFatal(fatal, outcomes[i].ref)
		c.reviews[i] = outcomes[i].review
		c.resolveFailure(finderPhase(i), outcomes[i].failure, outcomes[i].cause)
		if outcomes[i].response != nil {
			c.response = outcomes[i].response
		}
	}
	if ref := c.persistProgress(); ref != nil {
		return ref
	}
	if fatal == nil {
		fatal = c.allFindersFailed(outcomes, skipped)
	}
	return fatal
}

// allFindersFailed reports the agent's own refusal when every launched finder
// failed. A partial execution still has evidence to judge, so it keeps its
// review-insufficient verdict; an execution with nothing to judge is not an
// evidence gap, it is the agent failing, and the operator needs the failure's
// code, message, and exit status to act on it. The decision rests on the
// invoke outcomes, never on what a failed phase left behind: the directive
// tells an agent to write a placeholder review first, so a dead finder's
// artifact can still decode.
func (c *coordinator) allFindersFailed(outcomes []discoveryOutcome, skipped []bool) *core.RefusalError {
	var failed []int
	for i := range outcomes {
		if skipped[i] || !agentFailure(outcomes[i].failure) {
			return nil
		}
		failed = append(failed, i)
	}
	if len(failed) == 0 {
		return nil
	}
	lead := leadFailure(outcomes, failed)
	lines := make([]string, 0, len(failed))
	for _, i := range failed {
		cause := finderCause(&outcomes[i])
		lines = append(lines, finderPhase(i)+" "+cause.Code+": "+cause.Message)
	}
	c.response = outcomes[lead].response
	// Each line is a finder's own refusal, its message already folded where
	// it was built, so the list keeps one finder per line.
	return &core.RefusalError{
		Code:    finderCause(&outcomes[lead]).Code,
		Message: core.Msgf("every finder failed:\n%s", core.Trusted(strings.Join(lines, "\n"))),
	}
}

// agentFailure reports whether a phase's failure code is the agent itself
// failing: it was not the agent asked for or is not installed, never ran, died,
// ran out of time, or wrote nothing. A review that
// exists but fails its check (stale, malformed) is evidence for the union and
// keeps flowing through it.
func agentFailure(code string) bool {
	switch code {
	case codeAgentFailed, codeAgentTimeout, codeAgentSpawnFailed, codeAgentMissing, codeAgentMismatch, codeNoArtifact:
		return true
	}
	return false
}

// leadFailure picks the finder whose code names the refusal: the first one
// that failed by process, since a process failure outranks a missing artifact
// (the agent never got to write one, and its code is what the operator fixes).
func leadFailure(outcomes []discoveryOutcome, failed []int) int {
	for _, i := range failed {
		if outcomes[i].failure != codeNoArtifact {
			return i
		}
	}
	return failed[0]
}

// finderCause is the refusal one failed finder earned. A finder whose invoke
// succeeded but left an unreadable review has only its failure code, so the
// refusal is built from that.
func finderCause(out *discoveryOutcome) *core.RefusalError {
	if out.cause != nil {
		return out.cause
	}
	return &core.RefusalError{Code: out.failure, Message: "the finder left no readable review"}
}

// canceledRefusal reports the operator's cancel, which outranks every phase's
// own refusal and leaves nothing to merge: the run never finished.
func canceledRefusal(ctx context.Context, st *state) *core.RefusalError {
	if ctx.Err() == nil {
		return nil
	}
	return st.refuse(codeCanceled, msgCanceled)
}

// mergeFatal keeps the refusal the run reports: the first real failure in
// worker order, because the sibling that failure canceled only reports the
// cancel it inherited.
func mergeFatal(current, next *core.RefusalError) *core.RefusalError {
	if next == nil {
		return current
	}
	if current == nil || (current.Code == codeCanceled && next.Code != codeCanceled) {
		return next
	}
	return current
}

// captureExecution records a fresh execution in run.json and pins the
// launcher's resume fingerprint before any phase runs, so an interruption at
// any point leaves a record a later --resume can verify.
func (c *coordinator) captureExecution(captured *config.RunState) *core.RefusalError {
	_, source, ref := c.st.finderPolicy(captured)
	if ref != nil {
		return ref
	}
	c.source = source
	captured.Finders = finderCount
	captured.FinderSource = source
	captured.Execution = &core.ExecutionReference{ID: c.id, Finders: finderCount}
	if err := config.WriteRunState(c.dir, captured); err != nil {
		return c.st.refuse(codeRecordFailed, fmt.Sprintf("capture execution: %v", err))
	}
	fingerprint, ref := c.fingerprintNow(captured, c.source)
	if ref != nil {
		return ref
	}
	c.fingerprint = fingerprint
	manifest := jq.NewObj("schema_version", "2", keyExecutionID, c.id, keyStatus, statusInProgress,
		keyFinders, finderCount, keyFinderSource, c.source)
	setManifestBudget(manifest, c.st.budget)
	manifest.Set(keyFailures, c.failures)
	return c.publish(manifest)
}

// captureResumed rewrites the launcher record for the execution this invocation
// continues. Every bound digest and every unresolved failure the interrupted
// run published is carried over unchanged until finish() replaces them, so a
// kill during this attempt leaves the next --resume the same fail-closed
// bindings: a phase whose bytes nobody sealed is re-run, never reused on trust.
// run.json is left exactly as the record verified it, because its bytes are
// part of the fingerprint.
func (c *coordinator) captureResumed() *core.RefusalError {
	manifest := c.resume.manifest.Clone()
	c.failures = priorFailures(manifest)
	manifest.Set(keyFailures, c.failures)
	manifest.Set(keyExecutionID, c.id)
	manifest.Set(keyStatus, statusInProgress)
	manifest.Set(keyFinders, finderCount)
	manifest.Set(keyFinderSource, c.source)
	return c.publish(manifest)
}

// publish writes the in-progress launcher record and keeps it in hand, so a
// later progress write updates the same object rather than re-reading it.
func (c *coordinator) publish(manifest *jq.Obj) *core.RefusalError {
	manifest.Set(resumeRecordKey, c.resumeRecord(false))
	if ref := writeCoordinator(c.dir, launcherFile, manifest); ref != nil {
		return ref
	}
	c.record = manifest
	return nil
}

// persistProgress publishes the artifacts bound so far and the phases still
// failing. Without it an interrupted attempt leaves only the pre-launch record,
// and the next --resume cannot tell a finished phase from one that never ran.
func (c *coordinator) persistProgress() *core.RefusalError {
	if c.record == nil {
		return nil
	}
	c.record.Set("artifacts", c.artifacts())
	c.record.Set(keyFailures, c.failures)
	return c.publish(c.record)
}

func (c *coordinator) discoveryArgs(output string, index int) (*launchArgs, *core.RefusalError) {
	procedure, ref := finderProcedure(c.dir, output, index)
	if ref != nil {
		return nil, ref
	}
	if err := atomic.WriteFileMode(filepath.Join(output, "procedure.md"), procedure, roundEvidenceMode); err != nil {
		return nil, c.st.refuse(codeRecordFailed, fmt.Sprintf("write finder procedure: %v", err))
	}
	target := launchTarget{
		evidence: c.dir, output: output, procedure: filepath.Join(output, "procedure.md"),
		artifact: reviewFile, checker: recordcore.CheckDiscovery,
	}
	if ref := c.bind(filepath.Join(filepath.Base(output), "procedure.md")); ref != nil {
		return nil, ref
	}
	args, ref := c.st.argsForTarget(&c.base.def, c.base.timeout, target)
	if ref == nil {
		args.guard = c.checkpoint(output)
	}
	return args, ref
}

// aggregateResponse points the response at the aggregate artifacts the
// coordinator wrote and recomputes the handoff from the bundle itself: the
// retained child response named a finder's phase directory, which is not the
// operator's next step.
func (c *coordinator) aggregateResponse() {
	if c.response == nil || c.response.Data == nil {
		return
	}
	c.response.Next = c.st.next(c.dir)
	c.response.Data.Artifact = readcommon.DisplayPath(c.st.req.RepoRootPath(), filepath.Join(c.dir, reviewFile))
	c.response.Data.Record = readcommon.DisplayPath(c.st.req.RepoRootPath(), filepath.Join(c.dir, launcherFile))
}

func (c *coordinator) checkpoint(output string) func() *core.RefusalError {
	return func() *core.RefusalError {
		if ref := c.check(); ref != nil {
			return ref
		}
		return rejectPhaseLinks(output)
	}
}

func (c *coordinator) executionResponse() {
	if c.snapshot == nil {
		return
	}
	if c.response == nil {
		c.response = &core.RunResponse{Repo: c.st.req.Repo, Pr: c.st.req.Pr, At: core.NowUTC(c.st.now())}
	}
	if c.response.Data == nil {
		argv, _ := c.st.displayArgv(c.base)
		c.response.Data = &core.RunData{
			Agent: c.base.def.Name, Binary: c.base.bin, Argv: argv,
			Bundle: readcommon.DisplayPath(c.st.req.RepoRootPath(), c.dir), Timeout: c.base.timeout,
			Budget: c.st.budget,
		}
	}
	counts, status := c.executionCounts()
	failures := make([]core.RunPhaseFailure, 0, len(c.failures))
	for _, row := range c.failures {
		failures = append(failures, core.RunPhaseFailure{
			Phase: jq.StrOf(readcommon.FieldOf(row, keyPhase)), Code: jq.StrOf(readcommon.FieldOf(row, "code")),
		})
	}
	c.response.Data.Execution = &core.RunExecution{
		ID: c.id, Status: status,
		Counts: counts, Failures: failures,
	}
	c.response.Data.Resume = c.resumeData()
	c.response.Resumable = c.resumable
	c.aggregateResponse()
}

// executionCounts publishes the finder summary and status the bundle supports.
// Attach returns its nil summary unchanged when the execution is not Active, so
// a launch that died before writing its marker must not read .Finders off that
// nil. The schema already allows null counts, and "partial" is the honest
// status for a bundle with no execution to call complete.
func (c *coordinator) executionCounts() (*core.FinderSummary, string) {
	summary := panelcore.CheckExecution(c.dir, nil).Attach(nil)
	if summary == nil {
		c.failed("execution", codeRecordFailed)
		return nil, statusPartial
	}
	status := readcommon.JSONStr(filepath.Join(c.dir, launcherFile), keyStatus)
	if status == "" {
		return summary.Finders, statusPartial
	}
	return summary.Finders, status
}

// executionRefusal reports the refusal a response owes when the bundle records
// no execution: every phase may still look valid, but there is no captured
// execution to report, so the launch must not read as success.
func (c *coordinator) executionRefusal() *core.RefusalError {
	if c.response == nil || c.response.Data == nil || c.response.Data.Execution == nil ||
		c.response.Data.Execution.Counts != nil {
		return nil
	}
	return c.st.refuse(codeRecordFailed, "the bundle recorded no execution: launcher.json carries no execution id "+
		"and run.json names no execution — the launch died before it recorded one; re-run the launch")
}
