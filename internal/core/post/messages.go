// Package postcore is the post verb's domain service: drift, binding,
// recheck, payload guards, dry-run, and submit behind (ctx, *core.Request)
// → (*core.Response, *core.RefusalError) with the Commander seam. The CLI
// adapter parses, calls, renders, and maps exits; nothing here touches argv,
// stdio, or exit codes.
package postcore

const (
	msgNoPRTarget = "post needs a PR bundle (fetch owner/repo N first); local bundles record + report, they never post"

	msgProviderUnimplemented = "post to %s is not implemented yet — fetch, record, report and triage work on it; the manual lists the gaps"

	msgMissingDiffPost = "missing %s/full.diff (run fetch first — anchors validate against it)"

	msgMissingBundlePost = "missing %s (run fetch %s %s first)"

	msgPRNotOpen = "PR is %s — refusing post (re-fetch if this bundle is stale)"

	msgBadApprove = "--approve takes true|false, got '%s'"

	msgDriftMoved = "HEAD moved since fetch (fetched %s, live %s) — anchors potentially stale; " +
		"run verdict fetch %s %s --base-sha %s and review the delta"

	msgDriftNote = "> ⚠️ Note: commits landed after this review's fetch (fetched `%s`, live `%s`); " +
		"treat line anchors as potentially stale. Run `verdict fetch %s %s --base-sha %s` and review the delta."

	msgApproveBlocked = "refusing APPROVE with %d open + %d deferred findings (triage or pass --force)"

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
	fieldPath         = "path"
	fieldLine         = "line"
	fieldStartLine    = "start_line"
	fieldBody         = "body"
	fieldSuggestion   = "suggestion"
	statusOpen        = "open"
	localPrefix       = "local-"
	newline           = "\n"
	unknownTime       = "unknown time"
	prSuffix          = "/PR.json"
	codePostFailed    = "post-failed"
	codeMissingDiff   = "missing-diff"
	codeMissingBundle = "missing-bundle"
	eventComment      = "COMMENT"
	approveTrue       = "true"
	approveFalse      = "false"
)

const msgReviewMissing = "missing %s (agent must write {body, approved, comments[]})"

const codeAlreadyPosted = "already-posted"

const codeMissingReviewFile = "missing-review-file"
