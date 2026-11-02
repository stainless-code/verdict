package core

import (
	"slices"
	"strings"
)

// HintRule pairs a refusal-code matcher with its remediation hint. The
// table lives in core because every transport renders it: the CLI prints
// `try:` lines and the MCP adapter carries the hint in its structured
// refusal result, so a transport-local copy would drift.
type HintRule struct {
	match func(code string) bool
	hint  string
}

// hintRules is the ordered refusal-hint table (specific codes before the
// missing-*/bad-* catchalls).
var hintRules = []HintRule{
	{isCode("auth-missing"), "try: export the credential named above, then re-run"},
	{isCode("fetch-failed", "pr-read-failed", "threads-failed"), "try: run 'gh auth status', confirm the repo/PR spelling, then re-run"},
	{isCode("prompt-dump-failed"), "try: run from a checkout carrying SKILL.md/prompts, then re-run"},
	{isCode("post-failed"), "try: narrow anchor drift or body size (see above), then re-run"},
	{isCode("diff-too-large"), "try: split the range (--range/--base/--base-sha) or narrow with a paths filter, then re-fetch"},
	{isCode("upstream-diff-too-large"), "try: re-fetch narrowed to a range via --base-sha/--since, " +
		"or narrow with a paths filter, then re-run"},
	{isCode("missing-chunk-coverage", "missing-chunk-snapshot"), "try: re-fetch the bundle with --chunk, " +
		"then re-synthesize across all chunks and re-run"},
	{hasPrefixCode("missing-"), "try: run the fetch/record step named above first, then re-run"},
	{isCode("not-a-repo", "not-local", "no-pr-target", "no-threads", "no-verify-cmd", "report-failed"), "try: run the fetch/record " +
		"step named above first, then re-run"},
	{isCode("corrupt-store"), "try: re-run post/record to rebuild the store, or restore findings.json from backup, then re-run"},
	{isCode("bad-timeout"), "try: pass --timeout <positive-seconds> (default 300)"},
	{isCode("bad-approve"), "try: pass --approve=true|false"},
	{isCode("bad-review-schema"), "try: set \"schema_version\": \"1\" in review.json"},
	{isCode("approve-blocked"), "try: triage the findings or re-run with --force"},
	{isCode("budget-exceeded"), "try: re-run with --consent-over-budget once the spend " +
		"is approved, or raise REVIEW_BUDGET_USD/.review/budget"},
	{isCode("duplicate-payload"), "try: re-run with --force to repost the identical payload"},
	{isCode("already-posted"), "try: re-run with --force to override, or fetch --base-sha " +
		"<posted-head> for the delta and review it instead"},
	{isCode("degenerate-body"), "try: open with a verdict marker (see prompts/review.md), then re-run"},
	{isCode("pr-not-open"), "try: re-fetch the PR (bundle may be stale)"},
	{isCode("unknown-sig", "ambiguous-sig", "unknown-judgment"), "try: copy the full sig prefix from report output"},
	{isCode("bad-sig"), "try: list open inline sigs with report --status open --kind inline, then re-run with the full prefix"},
	{isCode("conflicted-tree"), "try: resolve the conflicts (or abort the merge), then re-run"},
	{isCode("fix-ceiling"), "try: triage the sig or split the fix — the 3-pass ceiling is hard-coded"},
	{isCode("scope-escape"), "try: revert or stash files outside the anchor path, then re-run fix"},
	{isCode("rationale-required"), "try: add --rationale with 30+ chars, or --force"},
	{isCode("already-adopted", "provider-unimplemented"), "try: use the verb named above for this bundle kind"},
	{isCode("needs-confirm"), "try: re-run with --yes after reviewing the preview, then re-run"},
	{isCode("bad-settings-credentials"), "try: remove the credential key from the settings file " +
		"(credentials never live there), then re-run"},
	{isCode("bad-settings"), "try: keep only output/layout/color/publish keys (see docs/manual.md), then re-run"},
	{isCode("bad-review-input"), "try: fix the malformed agent input named above " +
		"(review.json wants {body, approved, comments[]}; claims want string sigs), then re-run"},
	{hasPrefixCode("bad-"), "try: re-run with one of the valid values named above"},
	{isCode("already-learned"), "try: cite the existing rule id instead of learning it twice"},
	{isCode("verify-destructive"), "try: rewrite verifyCmd to stay in-repo and " +
		"non-destructive (run the test runner, not rm/mkfs/dd), then re-run"},
	{isCode("verify-networked"), "try: rewrite verifyCmd without network fetches or " +
		"pipe-to-shell (fetch fixtures first, run offline), then re-run"},
	{isCode("verify-credential-read"), "try: rewrite verifyCmd without reading credential paths " +
		"or piping env into the network, then re-run"},
	{isCode("verify-repo-escape"), "try: rewrite verifyCmd with repo-relative paths (/tmp for scratch), then re-run"},
	{isCode("id-taken"), "try: pass a different --id"},
	{isCode("unknown-rule"), "try: list rules with learn --status, then re-run"},
	{isCode("already-retired", "not-retired"), "try: check the rule status with learn --status, then re-run"},
	{isCode("mcp-config-unparseable"), "try: fix the JSON/TOML syntax (or move the file aside), then re-run"},
	{isCode("mcp-config-foreign", "mcp-config-unmergeable"), "try: remove the existing MCP entry by hand, then re-run"},
	{isCode("mcp-config-no-home"), "try: set HOME (or CODEX_HOME), then re-run"},
	{hasPrefixCode("mcp-config-"), "try: check the config path and permissions named above, then re-run"},
}

// isCode matches one of the exact codes.
func isCode(codes ...string) func(string) bool {
	return func(code string) bool {
		return slices.Contains(codes, code)
	}
}

// hasPrefixCode matches a code prefix.
func hasPrefixCode(prefix string) func(string) bool {
	return func(code string) bool {
		return strings.HasPrefix(code, prefix)
	}
}

// HintFor returns the remediation hint for a refusal code.
func HintFor(code string) string {
	for _, rule := range hintRules {
		if rule.match(code) {
			return rule.hint
		}
	}
	return "try: address the cause above, then re-run the same command"
}
