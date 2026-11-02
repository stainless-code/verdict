// Package cli owns the process-wide CLI contract shared by every verb: the
// global flag strip, the layered preferences resolution (flags > file >
// env > defaults), warning accumulation, and typed refusals rendered either
// as a machine envelope (--json) or human text with frozen exit codes.
//
// Byte parity with scripts/review.sh is the point: message strings, key
// order, exit codes, and the flags-win-over-file-over-env precedence are all
// ported literally. The one deliberate deviation is logging: GO-LOG-01 wants
// slog, but the frozen CLI contract emits exactly `warn: ...` / `error: ...`
// lines, so this package writes those bytes directly instead of a log
// [record] with timestamps.
package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// Exit codes for the machine contract.
const (
	ExitOK      = 0
	ExitRefused = 1
	ExitUsage   = 2
	ExitBroke   = 3
)

// Runner carries the per-invocation CLI state: routing, prefs, warnings.
type Runner struct {
	Out                io.Writer
	Human              io.Writer
	Diag               io.Writer
	Now                func() time.Time
	Effective          Effective
	Output             string
	EnvReviewColorMode string
	Cmd                string
	Rentry             string
	Pr                 string
	Repo               string
	Warnings           []string
	JSONMode           bool
	JSONPeeked         bool
	EnvNoColor         bool
	PrefColorSet       bool
	NoColor            bool
}

// Effective holds the resolved presentation knobs as "true"/"false"/enum
// strings, mirroring the shell's EFFECTIVE_* globals.
type Effective struct {
	Compact                string
	Color                  string
	PublishFooter          string
	PublishAttribution     string
	PublishAttributionText string
	PublishCopyright       string
	PublishLogo            string
	PublishLogoURL         string
	PublishOverview        string
	DiagramsStructural     string
	DiagramsBehavioral     string
	DiagramsExpanded       string
}

// Defaults mirrors the shell's global initializers.
func Defaults() Effective {
	return Effective{
		Compact:                FlagOff,
		Color:                  colorAuto,
		PublishFooter:          FlagOn,
		PublishAttribution:     AttributionProduct,
		PublishAttributionText: "",
		PublishCopyright:       "",
		PublishLogo:            FlagOn,
		PublishLogoURL:         "https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg",
		PublishOverview:        FlagOn,
		DiagramsStructural:     FlagOn,
		DiagramsBehavioral:     FlagOff,
		DiagramsExpanded:       FlagOff,
	}
}

// DefaultProductURL and MakerURL mirror the shell constants.
const (
	DefaultProductURL = "https://stainless-code.com/verdict"
	DefaultMakerURL   = "https://stainless-code.com"
	DefaultMakerName  = "Stainless Code"
	FooterDivider     = "<!-- verdict-footer -->"
)

// NowUTC formats t like the shell's date -u +%Y-%m-%dT%H:%M:%SZ.
func NowUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// Warn records a warning: `warn: ...` on the diagnostic stream plus the
// WARNINGS_JSON accumulation that lands in the envelope. R5: this is a
// frozen-format writer, not a [slog] call — the bytes are the CLI contract.
// Diagnostic stream writes are un-actionable, so errcheck excludes the
// [fmt.Fprint] family here via .golangci.yml.
func (r *Runner) Warn(msg string) {
	if r.Effective.Color == colorAlways {
		fmt.Fprintf(r.Diag, "\033[33mwarn: %s\033[0m\n", msg)
	} else {
		fmt.Fprintf(r.Diag, "warn: %s\n", msg)
	}
	r.Warnings = append(r.Warnings, msg)
}

// RefusalError is a typed CLI refusal: machine code plus human message. It is
// the only error type the command layer renders; matching uses [errors.As].
type RefusalError struct {
	Code    string
	Message string
}

// Error implements error.
func (e *RefusalError) Error() string { return e.Code + ": " + e.Message }

// Refuse renders a refusal like the shell's fail() and returns exit 1
// (check_gate stays false on the record path, so every refusal exits 1).
// The edge accepts error and recovers the typed refusal with [errors.As]
// (R4); non-refusal errors render as internal failures, never silently.
func (r *Runner) Refuse(err error) int {
	return r.RefuseExit(err, ExitRefused)
}

// RefuseExit renders a refusal with an explicit exit code. Only the
// cancellation path (GO-CLI-05, exit 3) uses a non-1 code.
func (r *Runner) RefuseExit(err error, code int) int {
	ref, ok := refusalOf(err)
	if !ok {
		ref = &RefusalError{Code: "internal", Message: err.Error()}
	}
	msg := ref.Message + "\n" + HintFor(ref.Code)
	if r.JSONMode {
		env := jq.NewObj(
			"schema_version", "1",
			"verb", r.Cmd,
			"repo", r.Repo,
			"pr", PrJSON(r.Pr),
			"at", NowUTC(r.Now()),
			"next", "",
			"warnings", cliStrings(r.Warnings),
			"data", nil,
			"error", jq.NewObj("code", ref.Code, "message", msg),
		)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	} else {
		if r.Effective.Color == colorAlways {
			fmt.Fprintf(r.Diag, "\033[31merror: %s\033[0m\n", msg)
		} else {
			fmt.Fprintf(r.Diag, "error: %s\n", msg)
		}
	}
	if r.Rentry != "" {
		fmt.Fprintf(r.Diag, "re-enter: %s\n", r.Rentry)
	}
	return code
}

// refusalOf recovers the typed refusal carried by err via [errors.As].
func refusalOf(err error) (*RefusalError, bool) {
	if ref, ok := errors.AsType[*RefusalError](err); ok {
		return ref, true
	}
	return nil, false
}

// UsageError is a plain flag error: message on stderr, exit 2, no envelope.
type UsageError struct {
	Message string
}

// Error implements error.
func (e *UsageError) Error() string { return e.Message }

// ReportUsage prints the message and returns exit 2.
func (r *Runner) ReportUsage(err *UsageError) int {
	fmt.Fprintln(r.Diag, err.Message)
	return ExitUsage
}

// OrderedEnvelope holds envelope fields in shell construction order. The
// shell's `jq -n '{...}'` preserves insertion order, so envelopes render
// through *jq.Obj (see Refuse), never through sorted maps.
type OrderedEnvelope struct {
	Pr            any
	Data          any
	Error         any
	SchemaVersion string
	Verb          string
	Repo          string
	At            string
	Next          string
	Warnings      []string
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// PrJSON ports `($p|tonumber? // null)`: a valid JSON number literal reads
// as a number, everything else reads null.
func PrJSON(pr string) any {
	if isJSONNumber(pr) {
		return jq.Number(pr)
	}
	return nil
}

// isJSONNumber reports whether s is a valid JSON number literal.
func isJSONNumber(s string) bool {
	return numberRe.MatchString(s)
}

var numberRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// hintRule pairs a matcher with its remediation hint. Rules evaluate in
// order, mirroring the shell's case table (specific codes before the
// missing-*/bad-* catchalls).
type hintRule struct {
	match func(code string) bool
	hint  string
}

// hintRules is the ordered refusal-hint table.
var hintRules = []hintRule{
	{isCode("auth-missing"), "try: export the credential named above, then re-run"},
	{isCode("fetch-failed", "pr-read-failed", "threads-failed"), "try: run 'gh auth status', confirm the repo/PR spelling, then re-run"},
	{isCode("prompt-dump-failed"), "try: run from a checkout carrying SKILL.md/prompts, then re-run"},
	{isCode("post-failed"), "try: narrow anchor drift or body size (see above), then re-run"},
	{isCode("diff-too-large"), "try: split the range (--range/--base/--base-sha) or narrow with a paths filter, then re-fetch"},
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
	{isCode(codeBadSettings), "try: keep only output/layout/color/publish keys (see docs/manual.md), then re-run"},
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

// SortedKeys returns the sorted keys of m (jq `keys[]` order).
func SortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
