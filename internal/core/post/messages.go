// Package postcore is the post verb's domain service: drift, binding,
// recheck, payload guards, dry-run, and submit behind (ctx, *core.Request)
// → (*core.Response, *core.RefusalError) with the Commander seam. The CLI
// adapter parses, calls, renders, and maps exits; nothing here touches argv,
// stdio, or exit codes.
package postcore

const (
	msgNoPRTarget = "post needs a PR bundle (fetch owner/repo N first); local bundles record + report, they never post"

	msgProviderUnimplemented = "post to %s is not implemented yet — fetch, record, report and triage work on it; the manual lists the gaps"

	msgGitLabPayload = "gitlab post payload unreadable"

	msgGitLabNoDiffRefs = "gitlab merge request read carries no diff refs — a diff discussion needs the " +
		"base, start, and head SHAs to anchor to (re-fetch and retry)"

	msgGitLabChangesEvent = "gitlab has no request-changes review event — posting the discussions and summary note only"

	msgMissingDiffPost = "missing %s/full.diff (run fetch first — anchors validate against it)"

	msgMissingBundlePost = "missing %s (run fetch %s %s first)"

	msgPRNotOpen = "PR is %s — refusing post (re-fetch if this bundle is stale)"

	msgBadApprove = "--approve takes true|false, got '%s'"

	msgDriftMoved = "HEAD moved since fetch (fetched %s, live %s) — anchors potentially stale; " +
		"run verdict fetch %s %s --base-sha %s and review the delta"

	msgDriftNote = "> ⚠️ Note: commits landed after this review's fetch (fetched `%s`, live `%s`); " +
		"treat line anchors as potentially stale. Run `verdict fetch %s %s --base-sha %s` and review the delta."

	msgApproveBlocked = "refusing APPROVE with %d open + %d deferred findings (triage or pass --force)"

	msgRequirementBlocked = "finding %s violates block clause %q from %s in %s — " +
		"address it, narrow the clause, or re-fetch without the policy"

	msgRequirementUnmet = "required check %q is unmet for %s (head %s): %s — %s; --force cannot satisfy a trusted requirement"

	msgHintHead = "fetch the pushed head and review the delta"

	msgHintThreads = "resolve or reply to the open review threads"

	msgApproveBlockedLive = "refusing APPROVE: %d live open thread(s) vs %d local open finding(s) — " +
		"%d thread(s) the store does not know about (fetch + triage them, or re-run with --force)"

	msgStaleDowngrade = "stale fetch — APPROVE downgraded to COMMENT (review covers fetched %s, live HEAD is %s; " +
		"run verdict fetch %s %s --base-sha %s and review the delta to bind)"

	msgStaleNote = "> ⚠️ Note: posting as COMMENT instead of APPROVE — commits landed after this review's fetch, " +
		"so the review does not cover live HEAD; run `verdict fetch %s %s --base-sha %s` and review the delta."

	msgAlreadyPosted = "refusing second post on reviewed SHA %s (already posted at %s — " +
		"new commits re-arm automatically; pass --force to override, " +
		"or run verdict fetch %s %s --base-sha %s and review the delta)"

	msgPostFailed = "post failed: %s — narrow the cause (anchor drift? body size?) — do not drop comments blindly."
)

const (
	fieldPath           = "path"
	fieldLine           = "line"
	fieldStartLine      = "start_line"
	fieldBody           = "body"
	fieldComments       = "comments"
	fieldSuggestion     = "suggestion"
	statusOpen          = "open"
	localPrefix         = "local-"
	newline             = "\n"
	unknownTime         = "unknown time"
	prSuffix            = "/PR.json"
	codePostFailed      = "post-failed"
	codeMissingDiff     = "missing-diff"
	codeMissingBundle   = "missing-bundle"
	eventComment        = "COMMENT"
	eventRequestChanges = "REQUEST_CHANGES"
	approveTrue         = "true"
	approveFalse        = "false"
)

const msgReviewMissing = "missing %s (agent must write {body, approved, comments[]})"

const codeAlreadyPosted = "already-posted"

const codeMissingReviewFile = "missing-review-file"

// msgLensGate names the failing profile evidence, then the ways to publish
// the shortfall without a false approval.
const msgLensGate = "profile lens evidence refuses this approval: %s — %s"

// msgDeepValidation names the deep group whose tool-produced validation is
// missing or stale, then the reason and the step that records it.
const msgDeepValidation = "deep group %s needs a successful validation receipt for this run and head: %s"

// profileDepthDeep is the profile depth that adds the validation pass.
const profileDepthDeep = "deep"

// msgRequireBind is the require_bind refusal: the flag is the only way to
// bind, and no forced path exists.
const msgRequireBind = "require_bind is set by the run policy — pass --bind to bind this approval " +
	"(binding downgrades never satisfy a required bind, and --force cannot bypass it)"

// msgLensFix names the two honest remedies for a lens shortfall.
const msgLensFix = "report the lens shortfall with a comment, or re-run the review so cost.json records every capped group"
