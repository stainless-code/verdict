package recordcore

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

	msgMalformedVerifyClaims = "verification.json carries malformed claims (want an array of {sig, verdict, verifyCmd, resultHead})"

	msgNonObjectVerifyClaim = "verification.json carries a non-object claim (want {sig, verdict, verifyCmd, resultHead})"

	msgCorruptPrior = "%s is corrupt or not a JSON list (refusing to start fresh — restore it from backup, then re-run)"
)
