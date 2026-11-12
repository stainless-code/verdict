package postcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// eventApprove is the positive binding event the payload and gates name.
const eventApprove = "APPROVE"

// Commander is the exec seam every post spawn goes through: the live-SHA
// read, the authorship read, the live-thread recheck, and the reviews POST
// (stdin payload plus one newline, exact argv). The production
// implementation is [exec.Runner] (separate stdout/stderr capture);
// tests inject a scripted commander and assert the exact argv. No service
// body spawns directly.
type Commander interface {
	Run(ctx context.Context, cmd *core.Command) (core.Result, error)
}

// CodePostFailed is the typed refusal for every post-internal failure.
const CodePostFailed = codePostFailed

const codeCanceled = "canceled"

const msgCanceledPost = "canceled before post completed"

// Run executes `post <owner/repo> <pr>`: bundle gates, drift, the shared
// merge, binding, recheck, payload guards, and submit. It never writes
// stdout/stderr or exits; human lines stream through the runtime progress
// observer, stores go through the shared atomic path, and the envelope
// inputs come back in the response for the adapter to render.
func Run(ctx context.Context, req *core.PostRequest) (*core.PostResponse, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	st.flags = req.Flags
	if err := ctx.Err(); err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: msgCanceledPost})
	}
	if ref := st.checkApproveFlag(); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := st.prepare(ctx); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := st.runGates(ctx); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.publish(ctx)
}

// runGates loads the policy and runs the refusal gates in their fixed order
// before any write.
func (st *state) runGates(ctx context.Context) *core.RefusalError {
	if ref := st.loadRunPolicy(); ref != nil {
		return ref
	}
	st.recordHeadFact()
	for _, gate := range []func(context.Context) *core.RefusalError{
		func(context.Context) *core.RefusalError { return st.complianceGate() },
		func(context.Context) *core.RefusalError { return st.bindRequiredGate() },
		st.bindGate, st.recheckGate,
	} {
		if ref := gate(ctx); ref != nil {
			return ref
		}
	}
	return nil
}

// checkApproveFlag refuses an --approve value outside the documented pair.
func (st *state) checkApproveFlag() *core.RefusalError {
	if st.flags.Approve == approveTrue || st.flags.Approve == approveFalse {
		return nil
	}
	return &core.RefusalError{Code: "bad-approve", Message: fmt.Sprintf(msgBadApprove, st.flags.Approve)}
}

type state struct { //nolint:govet // field grouping documents the post run state
	req          *core.PostRequest
	warnings     []string
	flags        core.PostFlags
	review       *jq.Obj
	merged       recordcore.MergeResult
	event        string
	liveSHA      string
	phash        string
	dir          string
	body         string
	commitID     string
	fetchedSHA   string
	payload      string
	payloads     []string
	extraParts   []string
	live         []liveReview
	findingsFile string
	now          string
	retryAt      string
	controls     *controls.Snapshot
	comments     []any
	diagram      diagram.Status
	bundle       *readcommon.Bundle
	embedded     int
	driftStale   bool
	liveLoaded   bool
	identity     *identity.Identity
	forced       []core.ForcedPost
	// withheld is what the capability table denied this post, in discovery
	// order; post.json carries it so a degraded write stays legible.
	withheld []core.WithheldCapability
	// policy is the typed run bundle when the fetch wrote one; nil on
	// legacy bundles, which degrade to no repository requirements.
	policy *runPolicy
	// secretShapes is what --allow-secret-values let through, in first-seen
	// order; the receipt records it so an overridden post stays legible.
	secretShapes []string
}

func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func (st *state) store() *readcommon.Bundle {
	if st.bundle == nil {
		st.bundle = readcommon.OpenBundle(st.dir)
	}
	return st.bundle
}

func (st *state) provider() string {
	if prov, present := st.store().ProviderMarker(); present {
		return prov
	}
	return providerGitHub
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) human(line string) {
	if st.req != nil && st.req.Runtime != nil {
		st.req.Human(line)
	}
}

func (st *state) commander() (Commander, *core.RefusalError) {
	var rt *core.Runtime
	if st.req != nil {
		rt = st.req.Runtime
	}
	if rt == nil || rt.Commander == nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code: CodePostFailed, Message: "post failed: no commander in runtime",
		})
	}
	cmdr, ok := rt.Commander.(Commander)
	if !ok || cmdr == nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code: CodePostFailed, Message: "post failed: no commander in runtime",
		})
	}
	return cmdr, nil
}

func (st *state) mergeRuntime() core.Runtime {
	var rt core.Runtime
	if st.req.Runtime != nil {
		rt = *st.req.Runtime
	}
	rt.Progress = warnCollector{inner: rt.Progress, warnings: &st.warnings}
	return rt
}

type warnCollector struct {
	inner    core.Progress
	warnings *[]string
}

func (w warnCollector) Human(line string) {
	if w.inner != nil {
		w.inner.Human(line)
	}
}

func (w warnCollector) Warn(msg string) {
	*w.warnings = append(*w.warnings, msg)
	if w.inner != nil {
		w.inner.Warn(msg)
	}
}

func (w warnCollector) Diag(line string) {
	if w.inner != nil {
		w.inner.Diag(line)
	}
}

func refusalOrFailed(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("post failed: %v", err)}
}

func (st *state) prepare(ctx context.Context) *core.RefusalError {
	st.dir = readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	st.event = eventComment
	if ref := st.checkBundle(); ref != nil {
		return ref
	}
	if ref := st.hostAuthGate(); ref != nil {
		return ref
	}
	if ref := st.loadReview(); ref != nil {
		return ref
	}
	if ref := st.secretScanGate(); ref != nil {
		return ref
	}
	st.commitID = st.store().Head()
	if ref := st.runGuards(ctx); ref != nil {
		return ref
	}
	st.checkDrift(ctx)
	if err := recordcore.EnsureHunksSnapshot(st.mergeRuntime(), st.dir); err != nil {
		return refusalOrFailed(err)
	}
	st.applyApprovedFlag()
	st.now = st.req.TimeUTC()
	st.findingsFile = st.dir + "/findings.json"
	if st.flags.DryRun {
		st.findingsFile = st.dir + "/findings.dryrun.json"
	}
	return st.merge()
}

func (st *state) publish(ctx context.Context) (*core.PostResponse, *core.RefusalError) {
	st.preflight()
	st.buildPayloads()
	st.eventLine()
	if st.flags.DryRun {
		return st.dryRunResponse()
	}
	if st.req.JSONMode {
		st.human("dry-run flag missing with --json on a real post is allowed; envelope follows the post")
	}
	if ref := st.duplicateGuard(); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.submit(ctx)
}

func (st *state) checkBundle() *core.RefusalError {
	if strings.HasPrefix(st.req.Pr, localPrefix) {
		return &core.RefusalError{Code: "no-pr-target", Message: msgNoPRTarget}
	}
	if prov := st.provider(); !core.CapabilitiesFor(prov).Post {
		return &core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(msgProviderUnimplemented, prov)}
	}
	if !readcommon.IsDir(st.dir) {
		return &core.RefusalError{Code: codeMissingBundle, Message: fmt.Sprintf(msgMissingBundlePost, st.dir, st.req.Repo, st.req.Pr)}
	}
	if !exists(st.flags.ReviewFile) {
		return &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, st.flags.ReviewFile)}
	}
	if !exists(st.dir + "/full.diff") {
		return &core.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiffPost, st.dir)}
	}
	if err := recordcore.EnsureStore(st.dir); err != nil {
		return refusalOrFailed(err)
	}
	switch prState := st.store().State(); prState {
	case "":
		st.warn("PR state unknown (re-fetch to record it) — proceeding")
	case statusOpen:
	default:
		return &core.RefusalError{Code: "pr-not-open", Message: fmt.Sprintf(msgPRNotOpen, prState)}
	}
	snap, ref := controls.Gate(st.dir, recordcore.BundleBase(st.dir, st.store()))
	st.controls = snap
	if ref != nil {
		return ref
	}
	if ref := packcore.CheckLock(snap); ref != nil {
		return ref
	}
	return nil
}

func (st *state) loadReview() *core.RefusalError {
	review, err := recordcore.LoadReview(st.flags.ReviewFile)
	if err != nil {
		return refusalOrFailed(err)
	}
	bodyRaw, _ := review.Get("body")
	st.review, st.body = review, jq.R(jq.Alt(bodyRaw, ""))
	if err := recordcore.CheckDegenerate(st.body); err != nil {
		return refusalOrFailed(err)
	}
	return nil
}

func (st *state) checkDrift(ctx context.Context) {
	st.fetchedSHA = st.store().Head()
	live, err := st.readLiveSHA(ctx)
	if err != nil {
		live = ""
	}
	st.liveSHA = live
	if live != "" && st.fetchedSHA != "" && live != st.fetchedSHA {
		st.driftStale = true
		fetched, liveShort := readcommon.ShortSHA(st.fetchedSHA), readcommon.ShortSHA(live)
		st.warn(fmt.Sprintf(msgDriftMoved, fetched, liveShort, st.req.Repo, st.req.Pr, fetched))
		st.body = fmt.Sprintf(msgDriftNote, fetched, liveShort,
			st.req.Repo, st.req.Pr, fetched) + "\n\n" + st.body
	} else if live == "" {
		st.warn("live SHA lookup failed — proceeding on fetched hunks")
	}
}

// commanderOrNil returns the injected command runner, or nil when the
// runtime carries none.
func (st *state) commanderOrNil() Commander {
	var rt *core.Runtime
	if st.req != nil {
		rt = st.req.Runtime
	}
	if rt == nil || rt.Commander == nil {
		return nil
	}
	cmdr, ok := rt.Commander.(Commander)
	if !ok {
		return nil
	}
	return cmdr
}

// readLiveSHA reads the host's live head for the bundle's PR, so the drift
// check runs the same read on every host that can post.
func (st *state) readLiveSHA(ctx context.Context) (string, error) {
	_, reader, _ := hostFor(st.provider())
	return reader.liveSHA(ctx, st.commanderOrNil(), st.env(), st.req.Repo, st.req.Pr)
}

// selfLogin reads the authenticated login the authorship gate compares the
// bundle author against.
func (st *state) selfLogin(ctx context.Context) (string, error) {
	_, reader, _ := hostFor(st.provider())
	return reader.selfLogin(ctx, st.commanderOrNil(), st.env())
}

// recheck counts the live unresolved threads the approval guard compares
// against the store.
func (st *state) recheck(ctx context.Context) (int, string, error) {
	_, reader, _ := hostFor(st.provider())
	return reader.recheck(ctx, st.commanderOrNil(), st.env(), st.req.Repo, st.req.Pr, st.dir)
}

// hostAuthGate runs the host's local prerequisite gate, which fires before the
// first call a post makes.
func (st *state) hostAuthGate() *core.RefusalError {
	_, reader, _ := hostFor(st.provider())
	return reader.auth(st.env())
}

// env is the runtime environment the host reads its credentials from.
func (st *state) env() map[string]string {
	if st.req != nil && st.req.Runtime != nil {
		return st.req.Env
	}
	return nil
}

func (st *state) applyApprovedFlag() {
	approvedRaw, _ := st.review.Get("approved")
	if st.flags.Approve == approveFalse && jq.R(jq.Alt(approvedRaw, false)) == approveTrue {
		st.flags.Approve = approveTrue
	}
}

func (st *state) merge() *core.RefusalError {
	caps := core.CapabilitiesFor(st.provider())
	rt := st.mergeRuntime()
	res, err := recordcore.Merge(&rt, &recordcore.MergeParams{
		Dir: st.dir, Prov: st.provider(),
		HeadFull: st.commitID,
		Review:   st.review, ReviewFile: st.flags.ReviewFile,
		Root: st.req.RootPath(), RepoRoot: st.req.RepoRootPath(), Body: st.body,
		DryRun: st.flags.DryRun, FindingsFile: st.findingsFile,
		PreFooter: st.splitPreFooter,
		Effective: st.req.Effective, Forces: st.req.Forces,
		Repo: st.req.Repo, Pr: st.req.Pr, Cmd: "post", Controls: st.controls,
		RequireCoverage: st.flags.RequireCoverage,
		// A host that advertises no inline anchors sends none: the merge keeps
		// every finding in the body instead of an anchor the host would refuse.
		BodyOnlyAnchors: !caps.InlineAnchors,
	})
	if err != nil {
		return refusalOrFailed(err)
	}
	st.merged, st.body, st.diagram = res, res.Body, res.Diagram
	st.recordAnchorGap(caps, res.NMoved)
	return nil
}

func (st *state) bindGate(ctx context.Context) *core.RefusalError {
	if st.unbound() {
		return nil
	}
	if st.flags.RequestChanges {
		st.event = st.applyVerdictGap(eventRequestChanges)
	} else if st.flags.Approve == approveTrue {
		if ref := st.approveGate(); ref != nil {
			return ref
		}
	}
	if st.event == eventComment {
		return nil
	}
	return st.authorGate(ctx)
}

// verdictLoss is what one requested binding verdict loses on a host that
// cannot carry it: the capability row a receipt records, the operator sentence
// the envelope warns with, and the event to post instead. A host that carries
// the verdict answers with the event alone, so capability and message stay
// empty and nothing is recorded.
type verdictLoss struct {
	capability string
	message    string
	posted     string
}

// verdictGap states what a requested binding verdict loses on this host: a host
// with no approve event cannot claim one, so the review posts COMMENT, while a
// host with no request-changes event still posts the request as its findings
// and summary note — the payload event is verdict's own record of the request,
// not a host write. The capability table decides, so a host that gains either
// event stops degrading. Pure: a test flips one capability and watches both
// answers move.
func verdictGap(caps core.HostCapabilities, event string) verdictLoss {
	switch event {
	case eventRequestChanges:
		if !caps.RequestChanges {
			return verdictLoss{capability: core.CapabilityRequestChanges, message: msgNoChangesEvent, posted: eventRequestChanges}
		}
	case eventApprove:
		if !caps.Approve {
			return verdictLoss{capability: core.CapabilityApprove, message: msgNoApproveEvent, posted: eventComment}
		}
	}
	return verdictLoss{posted: event}
}

// applyVerdictGap warns about a verdict this host cannot carry, records the
// withheld capability for the receipt, and returns the event to post for it.
func (st *state) applyVerdictGap(event string) string {
	caps := core.CapabilitiesFor(st.provider())
	loss := verdictGap(caps, event)
	if loss.capability == "" {
		return loss.posted
	}
	st.warn(fmt.Sprintf(loss.message, hostLabel(st.provider())))
	st.withhold(caps, loss.capability, event)
	return loss.posted
}

// withhold records one capability this host could not carry, with the reason
// code the capability table publishes for it and the binding event the run
// asked for. A capability the host carries is never recorded, so the receipt
// cannot name a loss the run did not take, and a capability is recorded once
// however often a gate revisits it.
func (st *state) withhold(caps core.HostCapabilities, capability, requestedEvent string) {
	reason := caps.ReasonFor(capability)
	if reason == "" {
		return
	}
	for _, recorded := range st.withheld {
		if recorded.Capability == capability {
			return
		}
	}
	st.withheld = append(st.withheld, core.WithheldCapability{
		Capability: capability, Reason: reason, RequestedEvent: requestedEvent,
	})
}

// recordAnchorGap records the inline-anchor loss when the provider takes no
// anchored comment: every finding the body-only merge moved is one the host
// could not carry inline, so the receipt names the capability and the count
// rides the anchors tally and the operator warning.
func (st *state) recordAnchorGap(caps core.HostCapabilities, moved int) {
	if caps.InlineAnchors || moved == 0 {
		return
	}
	st.warn(fmt.Sprintf(msgNoInlineAnchors, hostLabel(st.provider()), moved))
	st.withhold(caps, core.CapabilityInlineAnchors, "")
}

func (st *state) unbound() bool {
	wantBind := st.flags.Approve == approveTrue || st.flags.RequestChanges
	if wantBind && !st.flags.Bind {
		st.warn("binding verdict downgraded to COMMENT (pass --bind to bind)")
	}
	return !wantBind || !st.flags.Bind
}

func (st *state) approveGate() *core.RefusalError {
	if st.applyVerdictGap(eventApprove) == eventComment {
		return nil
	}
	nOpen := recordcore.CountStatus(st.merged.Entries, statusOpen)
	nDeferred := recordcore.CountStatus(st.merged.Entries, "deferred")
	if nOpen+nDeferred > 0 && !st.flags.Force {
		return &core.RefusalError{Code: "approve-blocked", Message: fmt.Sprintf(msgApproveBlocked, nOpen, nDeferred)}
	}
	if ref := st.approvalBlockers(); ref != nil {
		return ref
	}
	if !st.driftStale {
		st.event = eventApprove
		return nil
	}
	st.downgradeStale()
	return nil
}

// approvalBlockers runs the four gates an approval must clear after the status
// count: privacy, deep validation, lens coverage, and head freshness. Each
// returns a typed refusal or nil, and the first refusal wins.
func (st *state) approvalBlockers() *core.RefusalError {
	for _, gate := range []func() *core.RefusalError{
		st.privacyConflictGate, st.deepValidationGate, st.lensApprovalGate, st.headGate,
	} {
		if ref := gate(); ref != nil {
			return ref
		}
	}
	return nil
}

// downgradeStale turns a stale approval into a comment: the body says which
// commit the review read, so the reader knows the verdict predates the head.
func (st *state) downgradeStale() {
	st.warn(fmt.Sprintf(msgStaleDowngrade, readcommon.ShortSHA(st.fetchedSHA), readcommon.ShortSHA(st.liveSHA),
		st.req.Repo, st.req.Pr, readcommon.ShortSHA(st.fetchedSHA)))
	st.body = fmt.Sprintf(msgStaleNote, st.req.Repo, st.req.Pr, readcommon.ShortSHA(st.fetchedSHA)) + "\n\n" + st.body
	st.event = eventComment
}

func (st *state) authorGate(ctx context.Context) *core.RefusalError {
	author := st.store().Author()
	me, rerr := st.selfLogin(ctx)
	if rerr != nil {
		me = ""
	}
	switch {
	case author != "" && me != "" && author == me:
		st.warn("self-authored PR — binding verdict downgraded to COMMENT")
		st.event = eventComment
	case me == "":
		st.warn(fmt.Sprintf("could not verify PR authorship — proceeding, %s may refuse a self-verdict", hostLabel(st.provider())))
	}
	return nil
}

func (st *state) recheckGate(ctx context.Context) *core.RefusalError {
	if st.requiresCheck(config.CheckNoLiveThreads) {
		return st.liveThreadsGate(ctx)
	}
	return st.legacyRecheckGate(ctx)
}

// liveThreadsGate enforces a selected no-live-threads: zero unresolved live
// threads passes, any unresolved thread or an unreadable read fails, and a
// binding approval refuses on either. --force never applies, a comment run
// keeps the evidence without certifying it, and a dry run takes the same
// live read so a preview never claims what the publish would not prove.
func (st *state) liveThreadsGate(ctx context.Context) *core.RefusalError {
	count, reason, readable := st.liveThreads(ctx)
	st.observeLiveThreads(count, readable, reason)
	if st.event != "APPROVE" {
		return nil
	}
	return st.unmetRefusal(config.CheckNoLiveThreads, msgHintThreads)
}

// liveThreads reads the live unresolved-thread count once, without touching
// the review body or the bind decision.
func (st *state) liveThreads(ctx context.Context) (int, string, bool) {
	count, reason, err := st.recheck(ctx)
	if err != nil {
		if reason == "" {
			reason = err.Error()
		}
		return 0, reason, false
	}
	return count, "", true
}

// legacyRecheckGate is the shipped approval guard for runs that select no
// typed thread check: it blocks on more live threads than stored open
// findings, a --force run keeps its documented bypass, and an unreachable
// recheck downgrades to COMMENT.
func (st *state) legacyRecheckGate(ctx context.Context) *core.RefusalError {
	if st.event != "APPROVE" || st.flags.DryRun {
		return nil
	}
	liveOpen, downgraded := st.probeRecheck(ctx)
	if downgraded {
		return nil
	}
	storedOpen := recordcore.CountStatus(st.merged.Entries, statusOpen)
	blocked := liveOpen > storedOpen
	if blocked && !st.flags.Force {
		nOpen := storedOpen
		return &core.RefusalError{Code: "approve-blocked", Message: fmt.Sprintf(msgApproveBlockedLive, liveOpen, nOpen, liveOpen-nOpen)}
	}
	return nil
}

func (st *state) probeRecheck(ctx context.Context) (int, bool) {
	liveOpen, reason, err := st.recheck(ctx)
	if err == nil {
		return liveOpen, false
	}
	st.warn("live thread recheck unreachable — downgrading to COMMENT (posting on local findings only)")
	st.body = "> ⚠️ Note: live thread recheck unreachable (" + reason +
		"); posting as COMMENT instead of " + eventApprove + ".\n\n" + st.body
	st.event = eventComment
	return 0, true
}

func (st *state) preflight() {
	res := st.merged
	nOpen := recordcore.CountStatus(res.Entries, statusOpen)
	nDeferred := recordcore.CountStatus(res.Entries, "deferred")
	nAddressed := recordcore.CountStatus(res.Entries, "addressed")
	nTerminal := recordcore.CountStatus(res.Entries, "wont-fix") + recordcore.CountStatus(res.Entries, "false-positive")
	commit := readcommon.ShortSHA(st.commitID)
	st.human(fmt.Sprintf("preflight: body=%s (%d chars) | anchors: %d inline, %d moved | "+
		"findings: %d open, %d deferred, %d addressed, %d terminal | hunks indexed: %d | event=%s sha=%s",
		res.Marker, len(st.body), res.NKept, res.NMoved, nOpen, nDeferred, nAddressed, nTerminal, res.HunkN, st.event, commit))
	switch {
	case nOpen > 0 && readcommon.ShortSHA(st.commitID) != "":
		st.human(fmt.Sprintf("next: address open threads, then fetch --base-sha %s for the delta", readcommon.ShortSHA(st.commitID)))
	case nOpen > 0:
		st.human("next: address open threads")
	default:
		st.human("next: none open — nothing to address")
	}
}

func (st *state) eventLine() {
	resolved := 0
	if rv, ok := st.review.Get("resolved"); ok {
		resolved = len(jq.Elements(rv))
	}
	st.human(fmt.Sprintf("» event=%s inline=%d embedded=%d resolved_count=%d",
		st.event, st.merged.NKept, st.embedded, resolved))
}

func (st *state) duplicateGuard() *core.RefusalError {
	if ref := st.duplicateRefusal(); ref != nil {
		if st.flags.Force {
			st.forceBypass(ref.Code, nil, nil, nil)
			return nil
		}
		return ref
	}
	return nil
}

// runGuards runs the pre-merge refusal gates in order: the local same-SHA
// finalizer, then the same-patch sibling scan. A dry run never posts, so it
// skips both.
func (st *state) runGuards(ctx context.Context) *core.RefusalError {
	if st.flags.DryRun {
		return nil
	}
	if ref := st.finalizerGuard(); ref != nil {
		return ref
	}
	return st.patchGuard(ctx)
}

// identityCommander returns the injected command runner for patch identity
// recomputation, or nil when the runtime carries none: without a runner the
// algorithm claim cannot be recomputed, so git-kind identities stay
// unverified instead of trusted.
func (st *state) identityCommander() core.Commander {
	var rt *core.Runtime
	if st.req != nil {
		rt = st.req.Runtime
	}
	if rt == nil {
		return nil
	}
	return rt.Commander
}

// duplicateRefusal fires when the payload already posted: the local marker
// file first, then the live review marker.
func (st *state) duplicateRefusal() *core.RefusalError {
	if exists(st.dir + "/.posted_" + st.phash) {
		return duplicatePayloadRefusal()
	}
	return st.liveDuplicatePayload()
}

func (st *state) finalizerGuard() *core.RefusalError {
	if st.commitID == "" {
		return nil
	}
	if ref, bypass := st.sameSHARefusal(); ref != nil {
		if st.flags.Force {
			st.forced = append(st.forced, bypass)
			return nil
		}
		return ref
	}
	return nil
}

// sameSHARefusal fires when the reviewed SHA already posted: the local
// receipt first, then the live review markers. bypass is the forced[] entry
// a --force run records for the fired guard.
func (st *state) sameSHARefusal() (*core.RefusalError, core.ForcedPost) {
	if exists(st.dir + "/post.json") {
		storedHead, readable := st.store().PostedHead()
		switch {
		case !readable:
			st.warn("post record unreadable — proceeding, will refresh it on success")
		case storedHead == "":
			st.warn("post record carries no head — proceeding, will refresh it on success")
		case storedHead == st.commitID:
			storedAt := st.postedAt()
			return &core.RefusalError{Code: codeAlreadyPosted, Message: fmt.Sprintf(msgAlreadyPosted,
					readcommon.ShortSHA(st.commitID), storedAt, st.req.Repo, st.req.Pr, readcommon.ShortSHA(storedHead))},
				core.ForcedPost{Reason: codeAlreadyPosted, PreviousHead: &storedHead}
		}
	}
	if ref := st.liveAlreadyPosted(); ref != nil {
		head := st.commitID
		return ref, core.ForcedPost{Reason: codeAlreadyPosted, PreviousHead: &head}
	}
	return nil, core.ForcedPost{}
}

// forceBypass records one bypassed guard in the post.json forced[] audit:
// the fired code, with the patch identity and prior bundle when a
// same-patch sibling refused.
func (st *state) forceBypass(code string, patchID, prevBundle, prevHead *string) {
	st.forced = append(st.forced, core.ForcedPost{Reason: code, PatchID: patchID, PreviousBundle: prevBundle, PreviousHead: prevHead})
}

// patchGuard refuses a duplicate review of the same patch at a new SHA: it
// binds the current bundle identity to the bundle's present bytes, the
// recomputed algorithm result, and head, then scans sibling bundle
// post.json receipts for a completed publication of the same
// verified patch in the same repository, refusing unless --force (recorded
// in forced[]). It reads post.json only as publication proof, so an
// unposted local bundle never blocks a post. A missing, malformed, stale,
// tampered, or unrecomputable identity warns and proceeds with the SHA-only
// dedupe; a patch id never authorizes a post.
func (st *state) patchGuard(ctx context.Context) *core.RefusalError {
	if st.flags.DryRun {
		return nil
	}
	cur, ok := identity.Verify(ctx, st.identityCommander(), st.dir,
		st.commitID, recordcore.BundleBase(st.dir, st.store()))
	if !ok {
		st.warn("patch identity unavailable — dedupe by SHA only")
		return nil
	}
	if cur.PatchID == nil {
		return nil
	}
	st.identity = cur
	match := scanPatchSiblings(st.dir, cur)
	if match == nil {
		return nil
	}
	if st.flags.Force {
		st.forceBypass("already-reviewed-patch", cur.PatchID, &match.bundle, &match.head)
		return nil
	}
	return &core.RefusalError{Code: "already-reviewed-patch", Message: fmt.Sprintf(
		"already-reviewed-patch: patch %s was posted from %s at head %s (%s) — "+
			"refusing a duplicate review of the same change (pass --force to post again)",
		*cur.PatchID, filepath.ToSlash(filepath.Join(filepath.Dir(st.dir), match.bundle)), readcommon.ShortSHA(match.head), match.postedAt)}
}

// patchSibling is one completed same-patch sibling receipt.
type patchSibling struct {
	bundle   string
	head     string
	postedAt string
}

// scanPatchSiblings returns the first sibling bundle (sorted, deterministic)
// with a completed same-patch publication: a completed post.json receipt
// carrying the same verified patch claim in the same repository. Malformed
// sibling receipts cannot match; a missing .verdict dir matches nothing.
func scanPatchSiblings(dir string, cur *identity.Identity) *patchSibling {
	parent := filepath.Dir(dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	self := filepath.Base(dir)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == self {
			continue
		}
		if sib := siblingMatch(parent, dir, entry.Name(), cur); sib != nil {
			return sib
		}
	}
	return nil
}

// siblingMatch reads one sibling post.json as publication proof: the
// receipt must prove a completed publication carrying an algorithm-valid
// patch claim matching the current verified identity, and both bundles
// must share a known repository identity. An unposted bundle (no post.json,
// no patch fields), an incomplete or pending receipt, a stale foreign
// receipt, or a cross-repository patch never blocks.
func siblingMatch(parent, current, bundle string, cur *identity.Identity) *patchSibling {
	sibDir := parent + "/" + bundle
	store := readcommon.OpenBundle(sibDir)
	patch, kind, ok := identity.CompletedPatch(store.PostMap())
	if !ok {
		return nil
	}
	claim := &identity.Identity{PatchID: &patch, PatchIDKind: kind}
	if !identity.Match(cur, claim) {
		return nil
	}
	if !readcommon.SameRepo(current, sibDir) {
		return nil
	}
	head, _ := store.PostedHead()
	at, present := store.PostedAt()
	if !present {
		at = unknownTime
	}
	return &patchSibling{bundle: bundle, head: head, postedAt: at}
}

func (st *state) postedAt() string {
	at, present := st.store().PostedAt()
	if !present {
		return unknownTime
	}
	return at
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
