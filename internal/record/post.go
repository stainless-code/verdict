package record

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
)

// postFlags carries the parsed post options.
type postFlags struct {
	approve        string
	reviewFile     string
	requestChanges bool
	bind           bool
	force          bool
	dryRun         bool
}

// RunPost executes
// `post <owner/repo> <pr> [--approve[=bool]] [--request-changes] [--bind]
// [--force] [--review-file <path>] [--dry-run] [--json]`, returning the
// process exit code. Human progress goes to the runner's human stream;
// --json mode prints exactly one envelope to stdout.
func RunPost(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	dir := ".review/" + req.Pr
	f, code, done := parsePostFlags(r, args, dir)
	if done {
		return code
	}
	r.Rentry = postRentry(req.Repo, req.Pr, f, r.JSONMode)
	if err := ctx.Err(); err != nil {
		// GO-CLI-05: cancel kills the run with exit 3 and a JSON envelope.
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before post completed"}, cli.ExitBroke)
	}
	code, err := runPost(ctx, r, req, dir, f)
	if err != nil {
		if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("post failed: %v", err)})
	}
	return code
}

// parsePostFlags strips post's flags, reporting unknown ones. A trailing
// value flag reads "" (the shell dies on the shift instead — a documented
// divergence shared with the triage port — and the gate below refuses typed).
func parsePostFlags(r *cli.Runner, args []string, dir string) (postFlags, int, bool) {
	f := postFlags{approve: approveFalse, reviewFile: dir + "/review.json"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--approve", a == "--review-file":
			v := ""
			if i+1 < len(args) {
				i++
				v = args[i]
			}
			setPostValue(&f, a, v)
		case strings.HasPrefix(a, "--approve="):
			f.approve = strings.TrimPrefix(a, "--approve=")
		case a == "--request-changes":
			f.requestChanges = true
		case a == "--bind":
			f.bind = true
		case a == "--force":
			f.force = true
		case a == "--dry-run":
			f.dryRun = true
		case a == "--json":
		default:
			msg := fmt.Sprintf("unknown flag '%s' for post (want --approve[=bool], --request-changes, "+
				"--bind, --force, --review-file <path>, --dry-run, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// setPostValue stores one value-taking post flag's value.
func setPostValue(f *postFlags, name, val string) {
	if name == "--approve" {
		f.approve = val
	} else {
		f.reviewFile = val
	}
}

// postRentry rebuilds the exact re-run command the shell records after flag
// parsing (flags included, in shell order, so a retry reproduces this
// invocation).
func postRentry(repo, pr string, f postFlags, jsonMode bool) string {
	var sb strings.Builder
	sb.WriteString("verdict post " + repo + " " + pr)
	if f.approve != approveFalse {
		sb.WriteString(" --approve=" + f.approve)
	}
	if f.requestChanges {
		sb.WriteString(" --request-changes")
	}
	if f.bind {
		sb.WriteString(" --bind")
	}
	if f.force {
		sb.WriteString(" --force")
	}
	if f.reviewFile != ".review/"+pr+"/review.json" {
		sb.WriteString(" --review-file " + f.reviewFile)
	}
	if f.dryRun {
		sb.WriteString(" --dry-run")
	}
	if jsonMode {
		sb.WriteString(" --json")
	}
	return sb.String()
}

// Frozen store keys and codes the post path reads. The merge owns the
// canonical copies in internal/core/record; these mirrors keep the post
// adapter compiling without widening the core API (every value is pinned by
// the parity goldens).
const (
	fieldPath       = "path"
	fieldLine       = "line"
	fieldStartLine  = "start_line"
	fieldBody       = "body"
	fieldSuggestion = "suggestion"
	statusOpen      = "open"
	localPrefix     = "local-"
	newline         = "\n"
	unknownTime     = "unknown time"
	prSuffix        = "/PR.json"
	codePostFailed  = "post-failed"
	codeMissingDiff = "missing-diff"
	eventComment    = "COMMENT"
	approveTrue     = "true"
	approveFalse    = "false"
)

// msgReviewMissing mirrors the shared loader's missing-file message (the
// checkBundle existence gate must refuse before the full.diff gate, exactly
// like record).
const msgReviewMissing = "missing %s (agent must write {body, approved, comments[]})"

// codeMissingReviewFile mirrors the shared loader's missing-file code.
const codeMissingReviewFile = "missing-review-file"

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// postRun carries the post invocation across its phases.
type postRun struct {
	runner       *cli.Runner
	review       *jq.Obj
	merged       recordcore.MergeResult
	req          Request
	event        string
	liveSHA      string
	phash        string
	dir          string
	body         string
	commitID     string
	fetchedSHA   string
	payload      string
	findingsFile string
	now          string
	flags        postFlags
	comments     []any
	diagram      diagram.Status
	embedded     int
	driftStale   bool
}

// runPost mirrors the post block: bundle gates, drift, the shared merge,
// binding, recheck, payload, guards, and the submit.
func runPost(ctx context.Context, r *cli.Runner, req Request, dir string, f postFlags) (int, error) {
	p := &postRun{runner: r, req: req, dir: dir, flags: f, event: eventComment}
	if f.approve != approveTrue && f.approve != approveFalse {
		return 0, &cli.RefusalError{Code: "bad-approve", Message: fmt.Sprintf(msgBadApprove, f.approve)}
	}
	if ref := p.prepare(ctx); ref != nil {
		return 0, ref
	}
	if ref := p.bindGate(ctx); ref != nil {
		return 0, ref
	}
	if ref := p.recheckGate(ctx); ref != nil {
		return 0, ref
	}
	return p.publish(ctx)
}

// passCore returns a core merge error unchanged: refusal context lives in
// Code/Message (rendered at the edge via [errors.As]), so the wrapcheck
// add-context rule is waived here by design.
func passCore(err error) error { return err }

// prepare runs the bundle gates, review load, drift, hunks, and the shared
// merge.
func (p *postRun) prepare(ctx context.Context) error {
	if ref := p.checkBundle(); ref != nil {
		return ref
	}
	if ref := p.loadReview(); ref != nil {
		return ref
	}
	p.checkDrift(ctx)
	if err := recordcore.EnsureHunksSnapshot(p.mergeRuntime(), p.dir); err != nil {
		return passCore(err)
	}
	p.applyApprovedFlag()
	p.commitID = recordcore.ProvHead(p.dir)
	p.now = cli.NowUTC(p.runner.Now())
	p.findingsFile = p.dir + "/findings.json"
	if p.flags.dryRun {
		p.findingsFile = p.dir + "/findings.dryrun.json"
	}
	return p.merge()
}

// publish prints preflight and payload, then drives the dry-run, guards,
// and submit tail.
func (p *postRun) publish(ctx context.Context) (int, error) {
	r := p.runner
	p.preflight()
	p.buildPayload()
	p.eventLine()
	if p.flags.dryRun {
		return p.dryRunReport()
	}
	// Plain progress line, not a warning: the shell echoes it verbatim with
	// no `warn: ` prefix and no envelope entry.
	if r.JSONMode {
		fmt.Fprintln(r.Human, "dry-run flag missing with --json on a real post is allowed; envelope follows the post")
	}
	if ref := p.duplicateGuard(); ref != nil {
		return 0, ref
	}
	if ref := p.finalizerGuard(); ref != nil {
		return 0, ref
	}
	return p.submit(ctx)
}

// checkBundle ports the post bundle gates: local bundles never post, only
// GitHub posts at all, then the store marker and the required files.
func (p *postRun) checkBundle() error {
	if strings.HasPrefix(p.req.Pr, localPrefix) {
		return &cli.RefusalError{Code: "no-pr-target", Message: msgNoPRTarget}
	}
	if prov := postProvider(p.dir); prov != providerGitHub {
		return &cli.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(msgProviderUnimplemented, prov)}
	}
	if err := recordcore.EnsureStore(p.dir); err != nil {
		return passCore(err)
	}
	if !exists(p.flags.reviewFile) {
		return &cli.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, p.flags.reviewFile)}
	}
	if !exists(p.dir + "/full.diff") {
		return &cli.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiffPost, p.dir)}
	}
	switch prState(p.dir) {
	case "":
		p.runner.Warn("PR state unknown (re-fetch to record it) — proceeding")
	case statusOpen:
	default:
		return &cli.RefusalError{Code: "pr-not-open", Message: fmt.Sprintf(msgPRNotOpen, prState(p.dir))}
	}
	return nil
}

// loadReview reads and gates the review.json through the shared loader (a
// GitHub target is already established, so no local-bundle provider checks).
// The degenerate-body bar matches post exactly.
func (p *postRun) loadReview() error {
	review, err := recordcore.LoadReview(p.flags.reviewFile)
	if err != nil {
		return passCore(err)
	}
	bodyRaw, _ := review.Get("body")
	p.review, p.body = review, jq.R(jq.Alt(bodyRaw, ""))
	return nil
}

// checkDrift ports the stale-SHA guard: anchors validate against the
// fetch-time snapshot, and a moved live HEAD only adds drift context (plus a
// body note) — it never moves the validation target. Lookup failure degrades
// to a warning, never a refusal.
func (p *postRun) checkDrift(ctx context.Context) {
	r := p.runner
	p.fetchedSHA = recordcore.ProvHead(p.dir)
	live, err := ghLiveSHA(ctx, p.req.Repo, p.req.Pr)
	if err != nil {
		live = ""
	}
	p.liveSHA = live
	if live != "" && p.fetchedSHA != "" && live != p.fetchedSHA {
		p.driftStale = true
		fetched, liveShort := readcommon.ShortSHA(p.fetchedSHA), readcommon.ShortSHA(live)
		r.Warn(fmt.Sprintf(msgDriftMoved, fetched, liveShort, p.req.Repo, p.req.Pr, fetched))
		p.body = fmt.Sprintf(msgDriftNote, fetched, liveShort,
			p.req.Repo, p.req.Pr, fetched) + "\n\n" + p.body
	} else if live == "" {
		r.Warn("live SHA lookup failed — proceeding on fetched hunks")
	}
}

// applyApprovedFlag ports the approved-flag merge: the CLI flag wins, and a
// true review.json approval upgrades a false flag.
func (p *postRun) applyApprovedFlag() {
	approvedRaw, _ := p.review.Get("approved")
	if p.flags.approve == approveFalse && jq.R(jq.Alt(approvedRaw, false)) == approveTrue {
		p.flags.approve = approveTrue
	}
}

// merge runs the shared validate_and_merge core over the drift-annotated
// body, persisting the findings file and staging KB credit like the shell.
func (p *postRun) merge() error {
	rt := p.mergeRuntime()
	res, err := recordcore.Merge(&rt, &recordcore.MergeParams{
		Dir: p.dir, Prov: providerGitHub,
		HeadFull: p.commitID,
		Review:   p.review, ReviewFile: p.flags.reviewFile,
		Root: p.req.Root, Body: p.body,
		DryRun: p.flags.dryRun, FindingsFile: p.findingsFile,
		PreFooter: p.trimmedBody,
		Effective: p.runner.Effective, Forces: p.req.Forces,
		Repo: p.req.Repo, Pr: p.req.Pr, Cmd: "post",
	})
	if err != nil {
		return passCore(err)
	}
	p.merged, p.body, p.diagram = res, res.Body, res.Diagram
	return nil
}

// mergeRuntime builds the injected runtime for the shared merge.
func (p *postRun) mergeRuntime() core.Runtime {
	return core.Runtime{Env: p.req.Env, Now: p.runner.Now, Progress: runnerProgress{p.runner}}
}

// bindGate ports the binding gate: binding verdicts need --bind (else
// COMMENT); APPROVE needs zero open AND zero deferred findings unless
// --force, plus a review that covers live HEAD (known drift downgrades to
// COMMENT — re-fetch the delta to bind). Self-authored binding verdicts
// downgrade too, since GitHub refuses self-approvals.
func (p *postRun) bindGate(ctx context.Context) error {
	if p.unbound() {
		return nil
	}
	if p.flags.requestChanges {
		p.event = "REQUEST_CHANGES"
	} else if p.flags.approve == approveTrue {
		if ref := p.approveGate(); ref != nil {
			return ref
		}
	}
	if p.event == eventComment {
		return nil
	}
	return p.authorGate(ctx)
}

// unbound ports the non-binding path: without --bind every binding verdict
// lands as COMMENT with a warning, and anything else proceeds untouched.
func (p *postRun) unbound() bool {
	f := p.flags
	wantBind := f.approve == approveTrue || f.requestChanges
	if wantBind && !f.bind {
		p.runner.Warn("binding verdict downgraded to COMMENT (pass --bind to bind)")
	}
	return !wantBind || !f.bind
}

// approveGate ports the APPROVE binding checks: no open or deferred findings
// unless --force, and a review that covers live HEAD (known drift downgrades
// to COMMENT — re-fetch the delta to bind, never a silent APPROVE).
func (p *postRun) approveGate() error {
	nOpen := recordcore.CountStatus(p.merged.Entries, statusOpen)
	nDeferred := recordcore.CountStatus(p.merged.Entries, "deferred")
	if nOpen+nDeferred > 0 && !p.flags.force {
		return &cli.RefusalError{Code: "approve-blocked", Message: fmt.Sprintf(msgApproveBlocked, nOpen, nDeferred)}
	}
	if !p.driftStale {
		p.event = "APPROVE"
		return nil
	}
	p.runner.Warn(fmt.Sprintf(msgStaleDowngrade, readcommon.ShortSHA(p.fetchedSHA), readcommon.ShortSHA(p.liveSHA),
		p.req.Repo, p.req.Pr, readcommon.ShortSHA(p.fetchedSHA)))
	// The shell's downgrade note is a real newline plus a literal
	// backslash-n (a two-line source string ending in `\n`), kept
	// byte-identical here.
	p.body = fmt.Sprintf(msgStaleNote, p.req.Repo, p.req.Pr, readcommon.ShortSHA(p.fetchedSHA)) + "\n\\n" + p.body
	p.event = eventComment
	return nil
}

// authorGate ports the self-authored binding check: GitHub refuses
// self-approvals, so a binding verdict on the author's own PR downgrades to
// COMMENT; an unreadable login warns and proceeds (GitHub may still refuse).
func (p *postRun) authorGate(ctx context.Context) error {
	author := prAuthor(p.dir)
	me, rerr := ghSelfLogin(ctx)
	if rerr != nil {
		me = ""
	}
	switch {
	case author != "" && me != "" && author == me:
		p.runner.Warn("self-authored PR — binding verdict downgraded to COMMENT")
		p.event = eventComment
	case me == "":
		p.runner.Warn("could not verify PR authorship — proceeding, GitHub may refuse a self-verdict")
	}
	return nil
}

// recheckGate ports the live open-thread recheck on the APPROVE path only:
// more live opens than local opens refuses APPROVE, and an unreachable
// lookup downgrades to COMMENT — never a silent APPROVE, never a failure.
func (p *postRun) recheckGate(ctx context.Context) error {
	if p.event != "APPROVE" || p.flags.dryRun {
		return nil
	}
	liveOpen, downgraded := p.probeRecheck(ctx)
	if downgraded {
		return nil
	}
	if liveOpen > recordcore.CountStatus(p.merged.Entries, statusOpen) && !p.flags.force {
		nOpen := recordcore.CountStatus(p.merged.Entries, statusOpen)
		return &cli.RefusalError{Code: "approve-blocked", Message: fmt.Sprintf(msgApproveBlockedLive, liveOpen, nOpen, liveOpen-nOpen)}
	}
	return nil
}

// probeRecheck runs the live open-thread probe, downgrading to COMMENT when
// the lookup fails. It reports the open count and whether the run was
// downgraded.
func (p *postRun) probeRecheck(ctx context.Context) (int, bool) {
	liveOpen, reason, err := postRecheck(ctx, p.req.Repo, p.req.Pr, p.dir)
	if err == nil {
		return liveOpen, false
	}
	p.runner.Warn("live thread recheck unreachable — downgrading to COMMENT (posting on local findings only)")
	p.body = "> ⚠️ Note: live thread recheck unreachable (" + reason + "); posting as COMMENT instead of APPROVE.\n\n" + p.body
	p.event = eventComment
	return 0, true
}

// preflight ports the fail-closed preflight block plus the next-step line.
func (p *postRun) preflight() {
	r := p.runner
	res := p.merged
	nOpen := recordcore.CountStatus(res.Entries, statusOpen)
	nDeferred := recordcore.CountStatus(res.Entries, "deferred")
	nAddressed := recordcore.CountStatus(res.Entries, "addressed")
	nTerminal := recordcore.CountStatus(res.Entries, "wont-fix") + recordcore.CountStatus(res.Entries, "false-positive")
	commit := readcommon.ShortSHA(p.commitID)
	fmt.Fprintf(r.Human, "preflight: body=%s (%d chars) | anchors: %d inline, %d moved | "+
		"findings: %d open, %d deferred, %d addressed, %d terminal | hunks indexed: %d | event=%s sha=%s\n",
		res.Marker, len(p.body), res.NKept, res.NMoved, nOpen, nDeferred, nAddressed, nTerminal, res.HunkN, p.event, commit)
	switch {
	case nOpen > 0 && readcommon.ShortSHA(p.commitID) != "":
		fmt.Fprintf(r.Human, "next: address open threads, then fetch --base-sha %s for the delta\n", readcommon.ShortSHA(p.commitID))
	case nOpen > 0:
		fmt.Fprintf(r.Human, "next: address open threads\n")
	default:
		fmt.Fprintf(r.Human, "next: none open — nothing to address\n")
	}
}

// buildPayload ports the payload construction: only GitHub-known comment
// keys ship (triage fields stay in findings.json), and suggestions render as
// fenced blocks inside body.
func (p *postRun) buildPayload() {
	p.comments = buildPostComments(p.merged.Kept)
	p.embedded = countEmbedded(p.merged.Kept)
	payload := jq.NewObj(
		"body", p.body,
		"event", p.event,
		"commit_id", commitIDValue(p.commitID),
		"comments", p.comments,
	)
	// Pretty-printed like the shell's `jq -n` (insertion order, 2-space
	// indent, no trailing newline — the command substitution strips it).
	// The stub-log comparison covers these bytes exactly.
	p.payload = payload.MarshalPretty()
}

// commitIDValue ports `commit_id:(if $commit=="" then null else $commit end)`.
func commitIDValue(commitID string) any {
	if commitID == "" {
		return nil
	}
	return commitID
}

// eventLine ports the `» event=` progress line.
func (p *postRun) eventLine() {
	resolved := 0
	if rv, ok := p.review.Get("resolved"); ok {
		resolved = len(jq.Elements(rv))
	}
	fmt.Fprintf(p.runner.Human, "» event=%s inline=%d embedded=%d resolved_count=%d\n",
		p.event, p.merged.NKept, p.embedded, resolved)
}

// duplicateGuard ports the double-post guard: exact payload replays refuse
// unless --force. The marker is only touched after a successful submit, so
// legit re-reviews hash differently and pass.
func (p *postRun) duplicateGuard() error {
	p.phash = sig.Sum([]byte(p.payload))
	if exists(p.dir+"/.posted_"+p.phash) && !p.flags.force {
		return &cli.RefusalError{Code: "duplicate-payload", Message: "identical payload already posted — " +
			"refusing duplicate (pass --force to repost)"}
	}
	return nil
}

// finalizerGuard ports the single-review finality: a completed post on the
// same reviewed SHA refuses unless --force; any new SHA proceeds and
// refreshes the record on success. Unreadable or head-less records warn and
// proceed — they are overwritten on success, never wedging the run.
func (p *postRun) finalizerGuard() error {
	if p.commitID == "" || p.flags.force || !exists(p.dir+"/post.json") {
		return nil
	}
	storedHead, readable := postHead(p.dir + "/post.json")
	switch {
	case !readable:
		p.runner.Warn("post record unreadable — proceeding, will refresh it on success")
		return nil
	case storedHead == "":
		p.runner.Warn("post record carries no head — proceeding, will refresh it on success")
		return nil
	case storedHead == p.commitID:
		storedAt := postTime(p.dir + "/post.json")
		return &cli.RefusalError{Code: "already-posted", Message: fmt.Sprintf(msgAlreadyPosted,
			readcommon.ShortSHA(p.commitID), storedAt, p.req.Repo, p.req.Pr, readcommon.ShortSHA(storedHead))}
	default:
		return nil
	}
}
