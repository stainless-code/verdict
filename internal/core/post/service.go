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
	"github.com/stainless-code/verdict/internal/sig"
)

// Commander is the exec seam every post spawn goes through: the live-SHA
// read, the authorship read, the live-thread recheck, and the reviews POST
// (stdin payload plus one newline, exact argv). The production
// implementation is [exec.SplitCommander] (separate stdout/stderr capture);
// tests inject a scripted commander and assert the exact argv. No service
// body spawns directly.
type Commander interface {
	Run(ctx context.Context, cmd core.Command) (core.Result, error)
}

// CodePostFailed is the typed refusal for every post-internal failure.
const CodePostFailed = codePostFailed

// codeCanceled names the cancel refusal the adapter exits 3 on.
const codeCanceled = "canceled"

// Run executes `post <owner/repo> <pr>`: bundle gates, drift, the shared
// merge, binding, recheck, payload guards, and submit. It never writes
// stdout/stderr or exits; human lines stream through the runtime progress
// observer, stores go through the shared atomic path, and the envelope
// inputs come back in the response for the adapter to render.
func Run(ctx context.Context, req *core.PostRequest) (*core.PostResponse, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	st.flags = req.Flags
	if err := ctx.Err(); err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: "canceled before post completed"})
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

// state carries one post run: the request, the warnings accumulated before
// the response or refusal, and the merge outputs the payload needs.
type state struct {
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
	findingsFile string
	now          string
	controls     *controls.Snapshot
	comments     []any
	diagram      diagram.Status
	embedded     int
	driftStale   bool
}

// withWarnings attaches the accumulated warnings to a refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// human streams one human output line.
func (st *state) human(line string) {
	if st.req != nil && st.req.Runtime != nil {
		st.req.Human(line)
	}
}

// commander resolves the injected commander, refusing when the caller set
// none.
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

// mergeRuntime builds the injected runtime for the shared merge.
func (st *state) mergeRuntime() core.Runtime {
	var rt core.Runtime
	if st.req.Runtime != nil {
		rt = *st.req.Runtime
	}
	rt.Progress = warnCollector{inner: rt.Progress, warnings: &st.warnings}
	return rt
}

// warnCollector forwards progress observations to the adapter while
// accumulating every warning for the envelope.
type warnCollector struct {
	inner    core.Progress
	warnings *[]string
}

// Human forwards one human output line.
func (w warnCollector) Human(line string) {
	if w.inner != nil {
		w.inner.Human(line)
	}
}

// Warn records and forwards one warning.
func (w warnCollector) Warn(msg string) {
	*w.warnings = append(*w.warnings, msg)
	if w.inner != nil {
		w.inner.Warn(msg)
	}
}

// Diag forwards one raw diagnostic line.
func (w warnCollector) Diag(line string) {
	if w.inner != nil {
		w.inner.Diag(line)
	}
}

// refusalOrFailed passes typed core refusals through and wraps anything
// else as a post-failed edge error.
func refusalOrFailed(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("post failed: %v", err)}
}

// prepare runs the bundle gates, review load, the local same-SHA finalizer
// guard, drift, hunks, and the shared merge.
func (st *state) prepare(ctx context.Context) *core.RefusalError {
	st.dir = ".review/" + st.req.Pr
	st.event = eventComment
	if ref := st.checkBundle(); ref != nil {
		return ref
	}
	if ref := st.loadReview(); ref != nil {
		return ref
	}
	// The finalizer guard reads only local stores (PR.json, post.json), so
	// it runs before drift's live lookup: a repeat post on an already-posted
	// SHA refuses without paying for any network round-trip, and the
	// deterministic local refusal wins over the nondeterministic reads that
	// follow. Dry runs stay advisory exactly as before, so they skip it.
	st.commitID = recordcore.ProvHead(st.dir)
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

// publish prints preflight and payload, then drives the dry-run, guards,
// and submit tail.
func (st *state) publish(ctx context.Context) (*core.PostResponse, *core.RefusalError) {
	st.preflight()
	st.buildPayloads()
	st.eventLine()
	if st.flags.DryRun {
		return st.dryRunResponse()
	}
	// Plain progress line, not a warning: the shell echoes it verbatim with
	// no `warn: ` prefix and no envelope entry.
	if st.req.JSONMode {
		st.human("dry-run flag missing with --json on a real post is allowed; envelope follows the post")
	}
	if ref := st.duplicateGuard(); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.submit(ctx)
}

// checkBundle ports the post bundle gates: local bundles never post, only
// GitHub posts at all, then the store marker and the required files.
func (st *state) checkBundle() *core.RefusalError {
	if strings.HasPrefix(st.req.Pr, localPrefix) {
		return &core.RefusalError{Code: "no-pr-target", Message: msgNoPRTarget}
	}
	if prov := postProvider(st.dir); prov != providerGitHub {
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
	switch prState(st.dir) {
	case "":
		st.warn("PR state unknown (re-fetch to record it) — proceeding")
	case statusOpen:
	default:
		return &core.RefusalError{Code: "pr-not-open", Message: fmt.Sprintf(msgPRNotOpen, prState(st.dir))}
	}
	// Last gate: the controls snapshot must match the bundle before any read.
	snap, ref := controls.Gate(st.dir, recordcore.BundleBase(st.dir))
	st.controls = snap
	return ref
}

// loadReview reads and gates the review.json through the shared loader (a
// GitHub target is already established, so no local-bundle provider checks).
// The degenerate-body bar matches post exactly.
func (st *state) loadReview() *core.RefusalError {
	review, err := recordcore.LoadReview(st.flags.ReviewFile)
	if err != nil {
		return refusalOrFailed(err)
	}
	bodyRaw, _ := review.Get("body")
	st.review, st.body = review, jq.R(jq.Alt(bodyRaw, ""))
	return nil
}

// checkDrift ports the stale-SHA guard: anchors validate against the
// fetch-time snapshot, and a moved live HEAD only adds drift context (plus a
// body note) — it never moves the validation target. Lookup failure degrades
// to a warning, never a refusal.
func (st *state) checkDrift(ctx context.Context) {
	st.fetchedSHA = recordcore.ProvHead(st.dir)
	live, err := ghLiveSHA(ctx, st.commanderOrNil(), st.req.Repo, st.req.Pr)
	if err != nil {
		live = ""
	}
	st.liveSHA = live
	if live != "" && st.fetchedSHA != "" && live != st.fetchedSHA {
		st.driftStale = true
		fetched, liveShort := shortSHA(st.fetchedSHA), shortSHA(live)
		st.warn(fmt.Sprintf(msgDriftMoved, fetched, liveShort, st.req.Repo, st.req.Pr, fetched))
		st.body = fmt.Sprintf(msgDriftNote, fetched, liveShort,
			st.req.Repo, st.req.Pr, fetched) + "\n\n" + st.body
	} else if live == "" {
		st.warn("live SHA lookup failed — proceeding on fetched hunks")
	}
}

// commanderOrNil returns the injected commander or nil when unset (drift
// and authorship degrade to warnings, never refusals).
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

// applyApprovedFlag ports the approved-flag merge: the CLI flag wins, and a
// true review.json approval upgrades a false flag.
func (st *state) applyApprovedFlag() {
	approvedRaw, _ := st.review.Get("approved")
	if st.flags.Approve == approveFalse && jq.R(jq.Alt(approvedRaw, false)) == approveTrue {
		st.flags.Approve = approveTrue
	}
}

// merge runs the shared validate_and_merge core over the drift-annotated
// body, persisting the findings file and staging KB credit like the shell.
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

// bindGate ports the binding gate: binding verdicts need --bind (else
// COMMENT); APPROVE needs zero open AND zero deferred findings unless
// --force, plus a review that covers live HEAD (known drift downgrades to
// COMMENT — re-fetch the delta to bind). Self-authored binding verdicts
// downgrade too, since GitHub refuses self-approvals.
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

// unbound ports the non-binding path: without --bind every binding verdict
// lands as COMMENT with a warning, and anything else proceeds untouched.
func (st *state) unbound() bool {
	wantBind := st.flags.Approve == approveTrue || st.flags.RequestChanges
	if wantBind && !st.flags.Bind {
		st.warn("binding verdict downgraded to COMMENT (pass --bind to bind)")
	}
	return !wantBind || !st.flags.Bind
}

// approveGate ports the APPROVE binding checks: no open or deferred findings
// unless --force, and a review that covers live HEAD (known drift downgrades
// to COMMENT — re-fetch the delta to bind, never a silent APPROVE).
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
	st.warn(fmt.Sprintf(msgStaleDowngrade, shortSHA(st.fetchedSHA), shortSHA(st.liveSHA),
		st.req.Repo, st.req.Pr, shortSHA(st.fetchedSHA)))
	// The shell's downgrade note is a real newline plus a literal
	// backslash-n (a two-line source string ending in `\n`), kept
	// byte-identical here.
	st.body = fmt.Sprintf(msgStaleNote, st.req.Repo, st.req.Pr, shortSHA(st.fetchedSHA)) + "\n\\n" + st.body
	st.event = eventComment
	return nil
}

// authorGate ports the self-authored binding check: GitHub refuses
// self-approvals, so a binding verdict on the author's own PR downgrades to
// COMMENT; an unreadable login warns and proceeds (GitHub may still refuse).
func (st *state) authorGate(ctx context.Context) *core.RefusalError {
	author := prAuthor(st.dir)
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

// recheckGate ports the live open-thread recheck on the APPROVE path only:
// more live opens than local opens refuses APPROVE, and an unreachable
// lookup downgrades to COMMENT — never a silent APPROVE, never a failure.
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

// probeRecheck runs the live open-thread probe, downgrading to COMMENT when
// the lookup fails. It reports the open count and whether the run was
// downgraded.
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

// preflight ports the fail-closed preflight block plus the next-step line.
func (st *state) preflight() {
	res := st.merged
	nOpen := recordcore.CountStatus(res.Entries, statusOpen)
	nDeferred := recordcore.CountStatus(res.Entries, "deferred")
	nAddressed := recordcore.CountStatus(res.Entries, "addressed")
	nTerminal := recordcore.CountStatus(res.Entries, "wont-fix") + recordcore.CountStatus(res.Entries, "false-positive")
	commit := shortSHA(st.commitID)
	st.human(fmt.Sprintf("preflight: body=%s (%d chars) | anchors: %d inline, %d moved | "+
		"findings: %d open, %d deferred, %d addressed, %d terminal | hunks indexed: %d | event=%s sha=%s",
		res.Marker, len(st.body), res.NKept, res.NMoved, nOpen, nDeferred, nAddressed, nTerminal, res.HunkN, st.event, commit))
	switch {
	case nOpen > 0 && shortSHA(st.commitID) != "":
		st.human(fmt.Sprintf("next: address open threads, then fetch --base-sha %s for the delta", shortSHA(st.commitID)))
	case nOpen > 0:
		st.human("next: address open threads")
	default:
		st.human("next: none open — nothing to address")
	}
}

// eventLine ports the `» event=` progress line.
func (st *state) eventLine() {
	resolved := 0
	if rv, ok := st.review.Get("resolved"); ok {
		resolved = len(jq.Elements(rv))
	}
	st.human(fmt.Sprintf("» event=%s inline=%d embedded=%d resolved_count=%d",
		st.event, st.merged.NKept, st.embedded, resolved))
}

// duplicateGuard ports the double-post guard: exact payload replays refuse
// unless --force. The marker covers every posted part (a single part joins
// to itself, so the marker stays byte-identical there). The marker is only
// touched after a successful submit, so legit re-reviews hash differently
// and pass.
func (st *state) duplicateGuard() *core.RefusalError {
	st.phash = sig.Sum(st.payloadBytes())
	if exists(st.dir+"/.posted_"+st.phash) && !st.flags.Force {
		return &core.RefusalError{Code: "duplicate-payload", Message: "identical payload already posted — " +
			"refusing duplicate (pass --force to repost)"}
	}
	return nil
}

// finalizerGuard ports the single-review finality: a completed post on the
// same reviewed SHA refuses unless --force; any new SHA proceeds and
// refreshes the record on success. Unreadable or head-less records warn and
// proceed — they are overwritten on success, never wedging the run.
func (st *state) finalizerGuard() *core.RefusalError {
	if st.commitID == "" || st.flags.Force || !exists(st.dir+"/post.json") {
		return nil
	}
	storedHead, readable := postHead(st.dir + "/post.json")
	switch {
	case !readable:
		st.warn("post record unreadable — proceeding, will refresh it on success")
		return nil
	case storedHead == "":
		st.warn("post record carries no head — proceeding, will refresh it on success")
		return nil
	case storedHead == st.commitID:
		storedAt := postTime(st.dir + "/post.json")
		return &core.RefusalError{Code: "already-posted", Message: fmt.Sprintf(msgAlreadyPosted,
			shortSHA(st.commitID), storedAt, st.req.Repo, st.req.Pr, shortSHA(storedHead))}
	default:
		return nil
	}
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
