package core

import (
	"slices"
	"strings"
)

// HintRule pairs a refusal-code matcher with its remediation hint. The
// table lives in core because every transport renders it: the CLI prints
// `try:` lines and the MCP adapter carries the hint in its structured
// refusal result, so a transport-local copy would drift.
//
// The matcher is data rather than a closure so [ExactRefusalCodes] can walk
// the same table: the TS client's generated RefusalCode union is rendered
// from it, and a code added here cannot be missed by a source scrape.
type HintRule struct {
	prefix string
	hint   string
	codes  []string
}

// hintRules is the ordered refusal-hint table (specific codes before the
// missing-*/bad-* catchalls). A rule sets either codes (exact matches) or
// prefix (an open family), never both; HintRule orders its fields so the GC
// scans the fewest pointer bytes (govet fieldalignment), which is why the
// literal keys do not follow the declaration order.
var hintRules = []HintRule{
	{codes: []string{"auth-missing"}, hint: "try: export the credential named above, then re-run"},
	{
		codes: []string{"fetch-failed", "pr-read-failed", "threads-failed"},
		hint:  "try: run 'gh auth status', confirm the repo/PR spelling, then re-run",
	},
	{codes: []string{"prompt-dump-failed"}, hint: "try: run from a checkout carrying SKILL.md/prompts, then re-run"},
	{codes: []string{"post-failed"}, hint: "try: narrow anchor drift or body size (see above), then re-run"},
	{
		codes: []string{"diff-too-large"},
		hint:  "try: split the range (--range/--base/--base-sha) or narrow with a paths filter, then re-fetch",
	},
	{codes: []string{"upstream-diff-too-large"}, hint: "try: re-fetch narrowed to a range via --base-sha/--since, " +
		"or narrow with a paths filter, then re-run"},
	{codes: []string{"missing-chunk-coverage", "missing-chunk-snapshot"}, hint: "try: re-fetch the bundle with --chunk, " +
		"then re-synthesize across all chunks and re-run"},
	{prefix: "missing-", hint: "try: run the fetch/record step named above first, then re-run"},
	{
		codes: []string{"not-a-repo", "not-local", "no-pr-target", "no-threads", "no-verify-cmd", "report-failed"},
		hint:  "try: run the fetch/record step named above first, then re-run",
	},
	{
		codes: []string{"corrupt-store"},
		hint:  "try: re-run post/record to rebuild the store, or restore findings.json from backup, then re-run",
	},
	{codes: []string{"bad-timeout"}, hint: "try: pass --timeout <positive-seconds> (default 300)"},
	{codes: []string{"bad-approve"}, hint: "try: pass --approve=true|false"},
	{codes: []string{"bad-review-schema"}, hint: "try: set \"schema_version\": \"1\" in review.json"},
	{codes: []string{"approve-blocked"}, hint: "try: triage the findings or re-run with --force"},
	{codes: []string{"budget-exceeded"}, hint: "try: re-run with --consent-over-budget once the spend " +
		"is approved, or raise REVIEW_BUDGET_USD/.review/budget"},
	{codes: []string{"bad-usage"}, hint: "try: fix the usage record named above to match " +
		"schemas/usage.schema.json, then re-run"},
	{codes: []string{"duplicate-payload"}, hint: "try: re-run with --force to repost the identical payload — " +
		"re-fetch if the earlier review was deleted"},
	{codes: []string{"already-posted"}, hint: "try: re-run with --force to override, or fetch --base-sha " +
		"<posted-head> for the delta and review it instead — re-fetch if the earlier review was deleted"},
	{codes: []string{"degenerate-body"}, hint: "try: open with a verdict marker (see prompts/review.md), then re-run"},
	{codes: []string{"pr-not-open"}, hint: "try: re-fetch the PR (bundle may be stale)"},
	{codes: []string{"unknown-sig", "ambiguous-sig", "unknown-judgment"}, hint: "try: copy the full sig prefix from report output"},
	{
		codes: []string{"bad-sig"},
		hint:  "try: list open inline sigs with report --status open --kind inline, then re-run with the full prefix",
	},
	{codes: []string{"conflicted-tree"}, hint: "try: resolve the conflicts (or abort the merge), then re-run"},
	{codes: []string{"fix-ceiling"}, hint: "try: triage the sig or split the fix — the 3-pass ceiling is hard-coded"},
	{codes: []string{"scope-escape"}, hint: "try: revert or stash files outside the anchor path, then re-run fix"},
	{codes: []string{"rationale-required"}, hint: "try: add --rationale with 30+ chars, or --force"},
	{codes: []string{"already-adopted", "provider-unimplemented"}, hint: "try: use the verb named above for this bundle kind"},
	{codes: []string{"needs-confirm"}, hint: "try: re-run with --yes after reviewing the preview, then re-run"},
	{codes: []string{"bad-settings-credentials"}, hint: "try: remove the credential key from the settings file " +
		"(credentials never live there), then re-run"},
	{codes: []string{"bad-settings"}, hint: "try: keep only output/layout/color/publish keys (see docs/manual.md), then re-run"},
	{codes: []string{"controls-unavailable"}, hint: "try: check out the PR base (actions/checkout fetch-depth: 0, " +
		"or git fetch --depth=1 origin <base>), then re-fetch; fetch --allow-head-controls is the explicit opt-out"},
	{codes: []string{"controls-stale"}, hint: "try: re-fetch the bundle (controls changed under it), then re-run"},
	{codes: []string{"controls-too-large"}, hint: "try: trim the named control file under its cap, commit it at base, then re-fetch"},
	{codes: []string{"bad-review-input"}, hint: "try: fix the malformed agent input named above " +
		"(review.json wants {body, approved, comments[]}; claims want string sigs), then re-run"},
	{prefix: "bad-", hint: "try: re-run with one of the valid values named above"},
	{codes: []string{"already-learned"}, hint: "try: cite the existing rule id instead of learning it twice"},
	{codes: []string{"verify-destructive"}, hint: "try: rewrite verifyCmd to stay in-repo and " +
		"non-destructive (run the test runner, not rm/mkfs/dd), then re-run"},
	{codes: []string{"verify-networked"}, hint: "try: rewrite verifyCmd without network fetches or " +
		"pipe-to-shell (fetch fixtures first, run offline), then re-run"},
	{codes: []string{"verify-credential-read"}, hint: "try: rewrite verifyCmd without reading credential paths " +
		"or piping env into the network, then re-run"},
	{codes: []string{"verify-repo-escape"}, hint: "try: rewrite verifyCmd with repo-relative paths (/tmp for scratch), then re-run"},
	{codes: []string{"id-taken"}, hint: "try: pass a different --id"},
	{codes: []string{"unknown-rule"}, hint: "try: list rules with learn --status, then re-run"},
	{codes: []string{"already-retired", "not-retired"}, hint: "try: check the rule status with learn --status, then re-run"},
	{codes: []string{"mcp-config-unparseable"}, hint: "try: fix the JSON/TOML syntax (or move the file aside), then re-run"},
	{codes: []string{"mcp-config-foreign", "mcp-config-unmergeable"}, hint: "try: remove the existing MCP entry by hand, then re-run"},
	{codes: []string{"mcp-config-no-home"}, hint: "try: set HOME (or CODEX_HOME), then re-run"},
	{prefix: "mcp-config-", hint: "try: check the config path and permissions named above, then re-run"},
}

// matches reports whether the rule answers code.
func (r HintRule) matches(code string) bool {
	if r.prefix != "" {
		return strings.HasPrefix(code, r.prefix)
	}
	return slices.Contains(r.codes, code)
}

// ExactRefusalCodes returns every exact refusal code in the hint table,
// sorted and deduplicated. Open families stay out: they are prefixes, not
// codes. The table defines hints, not the code set — [RefusalCodes] does that —
// and internal/archtest keeps the two in step: a hint keyed on a code the
// binary cannot refuse would never fire.
func ExactRefusalCodes() []string {
	codes := make([]string, 0, len(hintRules))
	for _, rule := range hintRules {
		codes = append(codes, rule.codes...)
	}
	slices.Sort(codes)
	return slices.Compact(codes)
}

// HintFor returns the remediation hint for a refusal code.
func HintFor(code string) string {
	for _, rule := range hintRules {
		if rule.matches(code) {
			return rule.hint
		}
	}
	return "try: address the cause above, then re-run the same command"
}
