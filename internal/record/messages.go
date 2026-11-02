package record

// Long refusal/warning formats, split for the line-length gate. The rendered
// bytes are byte-identical to the shell's; only the source layout differs.
const (
	msgChunkDrifted = "chunk snapshots drifted from %s/full.diff (commits landed mid-review?) — " +
		"validating anchors against the pinned chunk snapshots; re-fetch with --chunk to re-pin"

	msgChunkUnreadable = "refusing record: chunk manifest %s is unreadable or schema '%s' unknown " +
		"(want schema_version 1) — re-fetch with '%s' to restore the pinned snapshots, then re-run"

	msgChunkNoChunks = "refusing record: chunk manifest %s lists no chunks — " +
		"re-fetch with '%s' to restore the pinned snapshots, then re-run"

	msgChunkSnapMissing = "refusing record: chunk snapshot(s) missing: %s (pinned chunk diff unreadable) — " +
		"re-fetch with '%s' to restore the chunk snapshots, then re-run (partial results never post)"

	msgReviewMissing = "missing %s (agent must write {body, approved, comments[]})"

	msgUnattested = "chunked bundle %s lists %d chunk(s) but the review carries no chunks attestation — " +
		"validating as unattested (exit 0); an unattested partial synthesis posts as complete, so " +
		"re-synthesize across all chunks and stamp every manifest chunk name in top-level \"chunks\""

	msgChunkCoverage = "refusing record: synthesized review covers %d of %d manifest chunk(s) — " +
		"missing %s — re-fetch with '%s', re-synthesize across all chunks, then re-run " +
		"(partial coverage never posts as complete)"

	msgContradictExit = "VERIFIED claim for %s contradicts machine verify exit=%s — " +
		"flip stands, contradiction recorded"

	msgContradictTimeout = "VERIFIED claim for %s contradicts machine verify timedOut=true (exit=%s) — " +
		"flip stands, contradiction recorded"

	msgBadTriage = "refusing post: unknown triage status '%s' " +
		"(want open|addressed|wont-fix|false-positive|deferred)"

	msgRecorded = "recorded: %s/findings.json (%d open) + %s/record.json — " +
		"report/triage read it; post stays refused (no PR target)\n"

	msgBadSchema = "review.json schema_version '%s' unknown (script knows 1)"

	msgNotLocal = "record works on local bundles and provider PRs " +
		"(fetch-local / fetch --provider first); GitHub PRs use post"

	msgNotGH = "GitHub PRs use post; record is for local bundles and provider PRs"

	msgHunksRebuild = "no fetch-time hunk snapshot (%s/hunks.tsv missing) — " +
		"rebuilding from full.diff, validation is best-effort (re-fetch to pin the snapshot)"

	msgMissingDiff = "missing %s/full.diff (run fetch-local / fetch --provider first)"

	msgBadReviewJSON = "review.json is not valid JSON (agent must write {body, approved, comments[]})"

	msgReviewNotObject = "review.json must hold a JSON object (agent must write {body, approved, comments[]})"

	msgMalformedComments = "review.json carries malformed comments (want an array of {path, line, body, ...})"

	msgNonObjectComment = "review.json carries a non-object comments[] entry (want {path, line, body, ...})"

	msgNonStringScalar = "review.json carries a non-string path, body, or suggestion in a comments[] entry (want strings)"

	msgMalformedResolved = "review.json carries malformed resolved (want an array of sig strings)"

	msgNonStringResolved = "review.json carries a non-string resolved[] entry (want sig strings)"

	msgMalformedTriage = "review.json carries malformed triage (want an array of {sig, status, rationale})"

	msgNonObjectTriage = "review.json carries a non-object triage[] entry (want {sig, status, rationale})"

	msgMalformedSections = "review.json carries malformed sections (want an array of {heading, severity})"

	msgNonObjectSection = "review.json carries a non-object sections[] entry (want {heading, severity})"

	msgMalformedVerifyClaims = "verification.json carries malformed claims (want an array of {sig, verdict, verifyCmd, resultHead})"

	msgNonObjectVerifyClaim = "verification.json carries a non-object claim (want {sig, verdict, verifyCmd, resultHead})"

	msgCorruptPrior = "%s is corrupt or not a JSON list (refusing to start fresh — restore it from backup, then re-run)"
)

// Post refusal/warning formats. The rendered bytes are byte-identical to the
// shell's; only the source layout is split for the line-length gate.
const (
	msgNoPRTarget = "post needs a PR bundle (fetch owner/repo N first); local bundles record + report, they never post"

	msgProviderUnimplemented = "post to %s is not implemented yet — fetch, record, report and triage work on it; the manual lists the gaps"

	msgMissingDiffPost = "missing %s/full.diff (run fetch first — anchors validate against it)"

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
