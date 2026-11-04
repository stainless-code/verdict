package postcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Commander is the exec seam every post spawn goes through: the live-SHA
// read, the authorship read, the live-thread recheck, and the reviews POST
// (stdin payload plus one newline, exact argv). The production
// implementation is [exec.Runner] (separate stdout/stderr capture);
// tests inject a scripted commander and assert the exact argv. No service
// body spawns directly.
type Commander interface {
	Run(ctx context.Context, cmd core.Command) (core.Result, error)
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
	if st.flags.Approve != approveTrue && st.flags.Approve != approveFalse {
		return nil, st.withWarnings(&core.RefusalError{Code: "bad-approve", Message: fmt.Sprintf(msgBadApprove, st.flags.Approve)})
	}
	if ref := st.prepare(ctx); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := st.bindGate(ctx); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := st.recheckGate(ctx); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.publish(ctx)
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
	st.dir = ".review/" + st.req.Pr
	st.event = eventComment
	if ref := st.checkBundle(); ref != nil {
		return ref
	}
	if ref := st.loadReview(); ref != nil {
		return ref
	}
	st.commitID = st.store().Head()
	if !st.flags.DryRun {
		if ref := st.finalizerGuard(); ref != nil {
			return ref
		}
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
	if err := recordcore.EnsureStore(st.dir); err != nil {
		return refusalOrFailed(err)
	}
	if !exists(st.flags.ReviewFile) {
		return &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, st.flags.ReviewFile)}
	}
	if !exists(st.dir + "/full.diff") {
		return &core.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiffPost, st.dir)}
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
	return ref
}

func (st *state) loadReview() *core.RefusalError {
	review, err := recordcore.LoadReview(st.flags.ReviewFile)
	if err != nil {
		return refusalOrFailed(err)
	}
	bodyRaw, _ := review.Get("body")
	st.review, st.body = review, jq.R(jq.Alt(bodyRaw, ""))
	return nil
}

func (st *state) checkDrift(ctx context.Context) {
	st.fetchedSHA = st.store().Head()
	live, err := ghLiveSHA(ctx, st.commanderOrNil(), st.req.Repo, st.req.Pr)
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

func (st *state) applyApprovedFlag() {
	approvedRaw, _ := st.review.Get("approved")
	if st.flags.Approve == approveFalse && jq.R(jq.Alt(approvedRaw, false)) == approveTrue {
		st.flags.Approve = approveTrue
	}
}

func (st *state) merge() *core.RefusalError {
	rt := st.mergeRuntime()
	res, err := recordcore.Merge(&rt, &recordcore.MergeParams{
		Dir: st.dir, Prov: providerGitHub,
		HeadFull: st.commitID,
		Review:   st.review, ReviewFile: st.flags.ReviewFile,
		Root: st.req.RootPath(), Body: st.body,
		DryRun: st.flags.DryRun, FindingsFile: st.findingsFile,
		PreFooter: st.splitPreFooter,
		Effective: st.req.Effective, Forces: st.req.Forces,
		Repo: st.req.Repo, Pr: st.req.Pr, Cmd: "post", Controls: st.controls,
	})
	if err != nil {
		return refusalOrFailed(err)
	}
	st.merged, st.body, st.diagram = res, res.Body, res.Diagram
	return nil
}

func (st *state) bindGate(ctx context.Context) *core.RefusalError {
	if st.unbound() {
		return nil
	}
	if st.flags.RequestChanges {
		st.event = "REQUEST_CHANGES"
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

func (st *state) unbound() bool {
	wantBind := st.flags.Approve == approveTrue || st.flags.RequestChanges
	if wantBind && !st.flags.Bind {
		st.warn("binding verdict downgraded to COMMENT (pass --bind to bind)")
	}
	return !wantBind || !st.flags.Bind
}

func (st *state) approveGate() *core.RefusalError {
	nOpen := recordcore.CountStatus(st.merged.Entries, statusOpen)
	nDeferred := recordcore.CountStatus(st.merged.Entries, "deferred")
	if nOpen+nDeferred > 0 && !st.flags.Force {
		return &core.RefusalError{Code: "approve-blocked", Message: fmt.Sprintf(msgApproveBlocked, nOpen, nDeferred)}
	}
	if !st.driftStale {
		st.event = "APPROVE"
		return nil
	}
	st.warn(fmt.Sprintf(msgStaleDowngrade, readcommon.ShortSHA(st.fetchedSHA), readcommon.ShortSHA(st.liveSHA),
		st.req.Repo, st.req.Pr, readcommon.ShortSHA(st.fetchedSHA)))
	st.body = fmt.Sprintf(msgStaleNote, st.req.Repo, st.req.Pr, readcommon.ShortSHA(st.fetchedSHA)) + "\n\n" + st.body
	st.event = eventComment
	return nil
}

func (st *state) authorGate(ctx context.Context) *core.RefusalError {
	author := st.store().Author()
	me, rerr := ghSelfLogin(ctx, st.commanderOrNil())
	if rerr != nil {
		me = ""
	}
	switch {
	case author != "" && me != "" && author == me:
		st.warn("self-authored PR — binding verdict downgraded to COMMENT")
		st.event = eventComment
	case me == "":
		st.warn("could not verify PR authorship — proceeding, GitHub may refuse a self-verdict")
	}
	return nil
}

func (st *state) recheckGate(ctx context.Context) *core.RefusalError {
	if st.event != "APPROVE" || st.flags.DryRun {
		return nil
	}
	liveOpen, downgraded := st.probeRecheck(ctx)
	if downgraded {
		return nil
	}
	if liveOpen > recordcore.CountStatus(st.merged.Entries, statusOpen) && !st.flags.Force {
		nOpen := recordcore.CountStatus(st.merged.Entries, statusOpen)
		return &core.RefusalError{Code: "approve-blocked", Message: fmt.Sprintf(msgApproveBlockedLive, liveOpen, nOpen, liveOpen-nOpen)}
	}
	return nil
}

func (st *state) probeRecheck(ctx context.Context) (int, bool) {
	liveOpen, reason, err := postRecheck(ctx, st.commanderOrNil(), st.req.Repo, st.req.Pr, st.dir)
	if err == nil {
		return liveOpen, false
	}
	st.warn("live thread recheck unreachable — downgrading to COMMENT (posting on local findings only)")
	st.body = "> ⚠️ Note: live thread recheck unreachable (" + reason + "); posting as COMMENT instead of APPROVE.\n\n" + st.body
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
	if st.flags.Force {
		return nil
	}
	if exists(st.dir + "/.posted_" + st.phash) {
		return duplicatePayloadRefusal()
	}
	return st.liveDuplicatePayload()
}

func (st *state) finalizerGuard() *core.RefusalError {
	if st.commitID == "" || st.flags.Force {
		return nil
	}
	if exists(st.dir + "/post.json") {
		storedHead, readable := st.store().PostedHead()
		switch {
		case !readable:
			st.warn("post record unreadable — proceeding, will refresh it on success")
		case storedHead == "":
			st.warn("post record carries no head — proceeding, will refresh it on success")
		case storedHead == st.commitID:
			storedAt := st.postedAt()
			return &core.RefusalError{Code: "already-posted", Message: fmt.Sprintf(msgAlreadyPosted,
				readcommon.ShortSHA(st.commitID), storedAt, st.req.Repo, st.req.Pr, readcommon.ShortSHA(storedHead))}
		}
	}
	return st.liveAlreadyPosted()
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
