// Package learn ports the `learn` verb: the structured local knowledge
// base. Exactly one mode per invocation: append a scoped rule, list rules
// with precision, or retire/restore by human veto with a 30+ char reason.
// The store is .review/kb.jsonl (one JSON object per line); every rewrite
// goes through the shared atomic path.
package learn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// Run executes `learn [--rule ... --scope ...] [--status] [--retire ...]
// [--restore ...] [--reason ...] [--id/--provenance/--rigor] [--json]`,
// returning the process exit code. Learn takes no repo/pr positionals.
func Run(ctx context.Context, r *cli.Runner, args []string, _ map[string]string) int {
	r.Repo, r.Pr = "", ""
	r.Rentry = "./scripts/review.sh learn"
	f, code, done := parseFlags(r, args)
	if done {
		return code
	}
	appendRentry(r, &f)
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: msgCanceled}, cli.ExitBroke)
	}
	now := cli.NowUTC(r.Now())
	code, err := runLearn(r, &f, now)
	if err != nil {
		if ref, _ := errors.AsType[*cli.RefusalError](err); ref != nil {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{Code: codeLearnFailed, Message: fmt.Sprintf(msgLearnFailed, err)})
	}
	return code
}

// flags carries the parsed learn options.
type flags struct {
	rule       string
	scope      string
	id         string
	provenance string
	rigor      string
	reason     string
	retire     string
	restore    string
	status     bool
}

// appendInput carries a validated --rule append.
type appendInput struct {
	rule  string
	scope string
	lid   string
	prov  string
	rigor string
}

// parseFlags strips learn's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{provenance: defaultProvenance, rigor: rigorPointed}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--rule", "--scope", "--id", "--provenance", "--rigor", "--retire", "--restore", "--reason":
			if i+1 < len(args) {
				i++
				setFlag(&f, a, args[i])
			} else {
				setFlag(&f, a, "")
			}
		case "--status":
			f.status = true
		case "--json":
		default:
			return f, r.ReportUsage(unknownFlag(a)), true
		}
	}
	return f, 0, false
}

// unknownFlag reports one unknown learn flag.
func unknownFlag(a string) *cli.UsageError {
	return &cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for learn (want %s)", a, usageFlags)}
}

// setFlag stores one value-taking flag.
func setFlag(f *flags, name, val string) {
	switch name {
	case "--rule":
		f.rule = val
	case "--scope":
		f.scope = val
	case "--id":
		f.id = val
	case "--provenance":
		f.provenance = val
	case "--rigor":
		f.rigor = val
	case "--retire":
		f.retire = val
	case "--restore":
		f.restore = val
	case "--reason":
		f.reason = val
	}
}

// appendRentry ports the rentry_cmd build (flag order fixed, values elided).
func appendRentry(r *cli.Runner, f *flags) {
	if f.rule != "" {
		r.Rentry += " --rule ..."
	}
	if f.scope != "" {
		r.Rentry += " --scope ..."
	}
	if f.status {
		r.Rentry += " --status"
	}
	if f.retire != "" {
		r.Rentry += " --retire ..."
	}
	if f.restore != "" {
		r.Rentry += " --restore ..."
	}
	if r.JSONMode {
		r.Rentry += " --json"
	}
}

// runLearn mirrors the learn block: exactly one mode, then the mode body.
func runLearn(r *cli.Runner, f *flags, now string) (int, error) {
	if err := checkModes(f); err != nil {
		return 0, err
	}
	if f.status {
		return runStatus(r, now)
	}
	if f.rule != "" {
		return runAppend(r, f, now)
	}
	if f.retire != "" {
		return runRetire(r, f, now)
	}
	return runRestore(r, f, now)
}

// checkModes enforces exactly-one-mode plus the flag pairing rules.
func checkModes(f *flags) error {
	if countModes(f) != 1 {
		return &cli.RefusalError{Code: codeBadFlags, Message: msgNeedOneMode}
	}
	if misplacedScope(f) {
		return &cli.RefusalError{Code: codeBadFlags, Message: msgScopeNeedsRule}
	}
	if misplacedReason(f) {
		return &cli.RefusalError{Code: codeBadFlags, Message: msgReasonNeedsVeto}
	}
	return nil
}

// countModes counts the selected learn modes.
func countModes(f *flags) int {
	modes := 0
	if f.rule != "" {
		modes++
	}
	if f.status {
		modes++
	}
	if f.retire != "" {
		modes++
	}
	if f.restore != "" {
		modes++
	}
	return modes
}

// misplacedScope reports --scope without --rule.
func misplacedScope(f *flags) bool {
	if f.rule != "" {
		return false
	}
	return f.scope != ""
}

// misplacedReason reports --reason without --retire/--restore.
func misplacedReason(f *flags) bool {
	if f.retire != "" {
		return false
	}
	if f.restore != "" {
		return false
	}
	return f.reason != ""
}

// runStatus ports `learn --status`: rules with precision, human or envelope.
func runStatus(r *cli.Runner, now string) (int, error) {
	rows, err := readStatusRows()
	if err != nil {
		return 0, err
	}
	if r.JSONMode {
		return printStatusEnvelope(r, rows, now)
	}
	return printStatusHuman(r, rows)
}

// readStatusRows reads the store with precision, refusing corrupt stores.
func readStatusRows() ([]any, error) {
	rows := []any{}
	if !bundle.IsFile(kbFile) {
		return rows, nil
	}
	raw, err := os.ReadFile(kbFile)
	if err != nil {
		return nil, corruptStore()
	}
	decoded, err := jq.DecodeJSONLOrdered(raw)
	if err != nil {
		return nil, corruptStore()
	}
	for _, row := range decoded {
		rows = append(rows, withPrecision(row))
	}
	return rows, nil
}

// printStatusEnvelope prints the learn --status machine envelope.
func printStatusEnvelope(r *cli.Runner, rows []any, now string) (int, error) {
	fmt.Fprintln(r.Out, learnEnvelope(r, now, nextStatus, jq.NewObj(fieldRules, rows)).MarshalPretty())
	return 0, nil
}

// printStatusHuman prints the learn --status human table.
func printStatusHuman(r *cli.Runner, rows []any) (int, error) {
	if len(rows) == 0 {
		fmt.Fprintf(r.Human, "learn: no rules yet (pass --rule to append the first)\n")
		return 0, nil
	}
	for _, row := range rows {
		printStatusRow(r, row)
	}
	return 0, nil
}

// printStatusRow prints one rule row of the human table.
func printStatusRow(r *cli.Runner, row any) {
	m, ok := row.(*jq.Obj)
	if !ok || m == nil {
		return
	}
	fmt.Fprintf(r.Human, "%s [%s] %s hits=%s false_hits=%s judged=%s precision=%s\n",
		jq.Interp(mustGet(m, fieldID)),
		jq.Interp(mustGet(m, fieldStatus)),
		jq.Interp(mustGet(m, fieldRigor)),
		numOrZero(mustGet(m, fieldHits)),
		numOrZero(mustGet(m, fieldFalseHits)),
		numOrZero(mustGet(m, fieldJudged)),
		jq.Interp(mustGet(m, fieldPrecision)),
	)
}

// withPrecision ports the precision render: "n/a" when judged is the
// number 0 (jq's `== 0` is type-strict — a string "0" reads judged), else
// "hits/judged" with `// 0` fallbacks.
func withPrecision(row *jq.Obj) *jq.Obj {
	judgedRaw := mustGet(row, fieldJudged)
	precision := precisionNA
	if !isNumberZero(judgedRaw) {
		precision = numOrZero(mustGet(row, fieldHits)) + "/" + numOrZero(judgedRaw)
	}
	row.Set(fieldPrecision, precision)
	return row
}

// isNumberZero ports `(.judged // 0) == 0`: only the number 0 reads zero
// (null and missing fall back to 0 via `//`; every other value — including
// the string "0" — reads judged).
func isNumberZero(v any) bool {
	if v == nil || v == false {
		return true
	}
	n, ok := jq.IntOf(v)
	return ok && n == 0
}

// numOrZero renders a counter the `// 0` way: null, missing, and false read
// 0; every other value renders as jq would interpolate it.
func numOrZero(v any) string {
	if v == nil || v == false {
		return "0"
	}
	switch t := v.(type) {
	case jq.Number:
		return jq.CanonNumber(t.String())
	case json.Number:
		return jq.CanonNumber(t.String())
	case float64:
		return jq.CanonNumber(fmt.Sprintf("%v", t))
	case int:
		return strconv.Itoa(t)
	case string:
		return t
	case bool:
		return "true"
	default:
		return jq.Compact(v)
	}
}

// runAppend ports the --rule append: validation, duplicate/id gates, then
// the compact row write.
func runAppend(r *cli.Runner, f *flags, now string) (int, error) {
	validated, err := validateAppend(f)
	if err != nil {
		return 0, err
	}
	if err := rejectAppendDup(validated.rule, validated.lid); err != nil {
		return 0, err
	}
	row := appendRowDoc(validated)
	if err := appendRow(row.MarshalCompact() + "\n"); err != nil {
		return 0, &cli.RefusalError{Code: codeLearnFailed, Message: fmt.Sprintf(msgWriteKB, kbFile, err)}
	}
	return printAppendResult(r, validated, now, row)
}

// validateAppend trims and validates the append flags.
func validateAppend(f *flags) (*appendInput, error) {
	ruleT := bundle.TrimSpace(f.rule)
	if ruleT == "" {
		return nil, &cli.RefusalError{Code: codeBadRule, Message: msgEmptyRule}
	}
	scopeT, err := checkScope(f.scope)
	if err != nil {
		return nil, err
	}
	if err := checkRigor(f.rigor); err != nil {
		return nil, err
	}
	return newAppendInput(f, ruleT, scopeT)
}

// checkScope trims and validates the scope glob list.
func checkScope(scope string) (string, error) {
	scopeT := bundle.TrimSpace(scope)
	if scopeT == "" {
		return "", &cli.RefusalError{Code: codeBadScope, Message: msgEmptyScope}
	}
	for elem := range strings.SplitSeq(scopeT, ",") {
		if bundle.TrimSpace(elem) == "" {
			return "", &cli.RefusalError{Code: codeBadScope, Message: msgBlankScopeElem}
		}
	}
	return scopeT, nil
}

// checkRigor validates the rigor knob.
func checkRigor(rigor string) error {
	switch rigor {
	case rigorSaid, rigorPointed, rigorWalked, rigorRan, rigorReproduced:
		return nil
	default:
		return &cli.RefusalError{Code: codeBadRigor, Message: fmt.Sprintf(msgBadRigor, rigor)}
	}
}

// newAppendInput resolves provenance and id for a validated append.
func newAppendInput(f *flags, ruleT, scopeT string) (*appendInput, error) {
	prov := f.provenance
	if prov == "" {
		prov = defaultProvenance
	}
	lid := f.id
	if lid == "" {
		lid = bundle.Slugify(ruleT)
	}
	if !idRe.MatchString(lid) {
		return nil, &cli.RefusalError{Code: codeBadID, Message: fmt.Sprintf(msgBadIDExplicit, lid)}
	}
	return &appendInput{rule: ruleT, scope: scopeT, lid: lid, prov: prov, rigor: f.rigor}, nil
}

// rejectAppendDup refuses duplicate rule text or a taken id, creating the
// store dir first. A missing store reads no rows.
func rejectAppendDup(ruleT, lid string) error {
	if err := os.MkdirAll(reviewDir, reviewDirPerm); err != nil {
		return &cli.RefusalError{Code: codeLearnFailed, Message: fmt.Sprintf(msgMkdirReview, err)}
	}
	if !bundle.IsFile(kbFile) {
		return nil
	}
	rows, ok := readRows()
	if !ok {
		return corruptStore()
	}
	return checkDupRows(rows, ruleT, lid)
}

// checkDupRows refuses when the rule text or id already exists.
func checkDupRows(rows []*jq.Obj, ruleT, lid string) error {
	for _, row := range rows {
		if ruleOf(row) == ruleT {
			return &cli.RefusalError{Code: codeAlreadyLearned, Message: msgAlreadyLearned}
		}
		if idOf(row) == lid {
			return &cli.RefusalError{Code: codeIDTaken, Message: fmt.Sprintf(msgIDTaken, lid)}
		}
	}
	return nil
}

// appendRowDoc builds the KB row for a validated append.
func appendRowDoc(in *appendInput) *jq.Obj {
	return jq.NewObj(
		fieldID, in.lid,
		fieldRule, in.rule,
		fieldScope, in.scope,
		fieldProvenance, in.prov,
		fieldRigor, in.rigor,
		fieldHits, jsonZero,
		fieldFalseHits, jsonZero,
		fieldJudged, jsonZero,
		fieldStatus, statusActive,
	)
}

// printAppendResult prints the append outcome, human or envelope.
func printAppendResult(r *cli.Runner, in *appendInput, now string, row *jq.Obj) (int, error) {
	if r.JSONMode {
		fmt.Fprintln(r.Out, learnEnvelope(r, now, nextAppend, jq.NewObj(fieldRule, row)).MarshalPretty())
		return 0, nil
	}
	fmt.Fprintf(r.Human, "learned %s -> %s (scope %s, rigor %s)\n", in.lid, kbFile, in.scope, in.rigor)
	return 0, nil
}

// runRetire ports `learn --retire`: the tombstone write with a reason.
func runRetire(r *cli.Runner, f *flags, now string) (int, error) {
	if !idRe.MatchString(f.retire) {
		return 0, &cli.RefusalError{Code: codeBadID, Message: fmt.Sprintf(msgBadID, f.retire)}
	}
	rows, cur, err := loadVetoRow(f.retire)
	if err != nil {
		return 0, err
	}
	if jq.Interp(mustGet(cur, fieldStatus)) == statusRetired {
		return 0, &cli.RefusalError{Code: codeAlreadyRetired, Message: fmt.Sprintf(msgAlreadyRetired, f.retire)}
	}
	if utf8.RuneCountInString(f.reason) < reasonMin {
		return 0, &cli.RefusalError{Code: codeBadReason, Message: msgRetireReason}
	}
	tombstoneRetire(cur, now, f.reason)
	if err := writeRows(rows); err != nil {
		return 0, err
	}
	if r.JSONMode {
		fmt.Fprintln(r.Out, learnEnvelope(r, now, nextRetire, jq.NewObj(fieldRule, cur)).MarshalPretty())
		return 0, nil
	}
	fmt.Fprintf(r.Human, "retired %s (tombstone kept, counters untouched)\n", f.retire)
	return 0, nil
}

// runRestore ports `learn --restore`: reactivation with history kept.
func runRestore(r *cli.Runner, f *flags, now string) (int, error) {
	if !idRe.MatchString(f.restore) {
		return 0, &cli.RefusalError{Code: codeBadID, Message: fmt.Sprintf(msgBadID, f.restore)}
	}
	rows, cur, err := loadVetoRow(f.restore)
	if err != nil {
		return 0, err
	}
	if jq.Interp(mustGet(cur, fieldStatus)) == statusActive {
		return 0, &cli.RefusalError{Code: codeNotRetired, Message: fmt.Sprintf(msgNotRetired, f.restore)}
	}
	if utf8.RuneCountInString(f.reason) < reasonMin {
		return 0, &cli.RefusalError{Code: codeBadReason, Message: msgRestoreReason}
	}
	cur.Set(fieldStatus, statusActive)
	appendHistory(cur, now, eventRestored, actorHuman, f.reason)
	if err := writeRows(rows); err != nil {
		return 0, err
	}
	if r.JSONMode {
		fmt.Fprintln(r.Out, learnEnvelope(r, now, nextRestore, jq.NewObj(fieldRule, cur)).MarshalPretty())
		return 0, nil
	}
	fmt.Fprintf(r.Human, "restored %s (status active, tombstone history kept)\n", f.restore)
	return 0, nil
}

// loadVetoRow loads the store strictly and finds the rule row.
func loadVetoRow(id string) ([]*jq.Obj, *jq.Obj, error) {
	rows, err := readRowsStrict(id)
	if err != nil {
		return nil, nil, err
	}
	cur := findRow(rows, id)
	if cur == nil {
		return nil, nil, &cli.RefusalError{Code: codeUnknownRule, Message: fmt.Sprintf(msgUnknownRule, id)}
	}
	return rows, cur, nil
}

// learnEnvelope builds the machine envelope shared by every learn mode.
func learnEnvelope(r *cli.Runner, now, next string, data any) *jq.Obj {
	return jq.NewObj(
		fieldSchemaVersion, schemaVersion1,
		fieldVerb, verbLearn,
		fieldRepo, "",
		fieldPR, nil,
		fieldAt, now,
		fieldNext, next,
		fieldWarnings, cliStrings(r.Warnings),
		fieldData, data,
		fieldError, nil,
	)
}

// tombstoneRetire stamps the retire tombstone, mirroring the shell's jq map.
func tombstoneRetire(cur *jq.Obj, now, reason string) {
	cur.Set(fieldStatus, statusRetired)
	cur.Set(fieldRetiredAt, now)
	cur.Set(fieldRetireReason, reason)
	appendHistory(cur, now, eventRetired, actorHuman, reason)
}

// appendHistory appends one history event (missing history reads empty).
func appendHistory(cur *jq.Obj, now, event, actor, note string) {
	hist := []any{}
	if raw, ok := cur.Get(fieldHistory); ok {
		if arr, ok := raw.([]any); ok {
			hist = arr
		}
	}
	hist = append(hist, jq.NewObj(fieldAt, now, fieldEvent, event, fieldActor, actor, fieldNote, note))
	cur.Set(fieldHistory, hist)
}

// readRows reads the KB store when present (missing, unreadable, or corrupt
// reads !ok; the caller refuses corrupt-store).
func readRows() ([]*jq.Obj, bool) {
	raw, err := os.ReadFile(kbFile)
	if err != nil {
		return nil, false
	}
	rows, err := jq.DecodeJSONLOrdered(raw)
	if err != nil {
		return nil, false
	}
	return rows, true
}

// readRowsStrict reads the KB store, refusing when absent or corrupt. An
// absent store (anything but a regular file, matching `[[ -f ]]`) refuses
// unknown-rule naming the missing store; a corrupt store refuses
// corrupt-store.
func readRowsStrict(id string) ([]*jq.Obj, error) {
	if !bundle.IsFile(kbFile) {
		return nil, &cli.RefusalError{Code: codeUnknownRule, Message: fmt.Sprintf(msgUnknownRuleNest, id, kbFile)}
	}
	raw, err := os.ReadFile(kbFile)
	if err != nil {
		return nil, corruptStore()
	}
	rows, err := jq.DecodeJSONLOrdered(raw)
	if err != nil {
		return nil, corruptStore()
	}
	return rows, nil
}

// findRow returns the first row with .id == id.
func findRow(rows []*jq.Obj, id string) *jq.Obj {
	for _, row := range rows {
		if idOf(row) == id {
			return row
		}
	}
	return nil
}

// idOf reads a row id.
func idOf(row *jq.Obj) string {
	v, _ := row.Get(fieldID)
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// ruleOf reads a row rule.
func ruleOf(row *jq.Obj) string {
	v, _ := row.Get(fieldRule)
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// mustGet reads a field (nil when the row is nil).
func mustGet(m *jq.Obj, key string) any {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}

// writeRows rewrites the store compactly (one JSON object per line).
func writeRows(rows []*jq.Obj) error {
	var sb strings.Builder
	for _, row := range rows {
		sb.WriteString(row.MarshalCompact())
		sb.WriteByte('\n')
	}
	if err := atomic.WriteFile(kbFile, []byte(sb.String())); err != nil {
		return &cli.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptMidWrite, kbFile)}
	}
	return nil
}

// appendRow appends one compact row to the store.
func appendRow(line string) error {
	var prev []byte
	if bundle.Exists(kbFile) {
		var err error
		prev, err = os.ReadFile(kbFile)
		if err != nil {
			//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
			return fmt.Errorf("read %s: %v", kbFile, err)
		}
	}
	return atomic.WriteFile(kbFile, append(prev, line...))
}

// corruptStore refuses on an unreadable KB store.
func corruptStore() *cli.RefusalError {
	return &cli.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptStore, kbFile)}
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// Learn constants.
const (
	// kbFile is the KB store path, mirroring $KB_FILE.
	kbFile = reviewDir + "/kb.jsonl"
	// reviewDir is the store directory, mirroring the shell's .review dir.
	reviewDir = ".review"
	// reviewDirPerm mirrors the shell's mkdir mode for the store dir.
	reviewDirPerm = 0o777
	// reasonMin is the veto-reason floor (characters, like ${#}).
	reasonMin = 30
	// defaultProvenance is the --provenance default.
	defaultProvenance = "manual"
	// precisionNA renders when judged is the number 0.
	precisionNA = "n/a"
)

// Refusal codes for the learn verb.
const (
	codeCanceled       = "canceled"
	codeLearnFailed    = "learn-failed"
	codeBadFlags       = "bad-flags"
	codeBadRule        = "bad-rule"
	codeBadScope       = "bad-scope"
	codeBadRigor       = "bad-rigor"
	codeBadID          = "bad-id"
	codeAlreadyLearned = "already-learned"
	codeIDTaken        = "id-taken"
	codeUnknownRule    = "unknown-rule"
	codeAlreadyRetired = "already-retired"
	codeNotRetired     = "not-retired"
	codeBadReason      = "bad-reason"
	codeCorruptStore   = "corrupt-store"
)

// Envelope and row field names (jq object keys for the store and envelope).
const (
	fieldSchemaVersion = "schema_version"
	fieldVerb          = "verb"
	fieldRepo          = "repo"
	fieldPR            = "pr"
	fieldAt            = "at"
	fieldNext          = "next"
	fieldWarnings      = "warnings"
	fieldData          = "data"
	fieldError         = "error"
	fieldRules         = "rules"
	fieldRule          = "rule"
	fieldID            = "id"
	fieldScope         = "scope"
	fieldProvenance    = "provenance"
	fieldRigor         = "rigor"
	fieldHits          = "hits"
	fieldFalseHits     = "false_hits"
	fieldJudged        = "judged"
	fieldStatus        = "status"
	fieldPrecision     = "precision"
	fieldHistory       = "history"
	fieldRetiredAt     = "retiredAt"
	fieldRetireReason  = "retireReason"
	fieldEvent         = "event"
	fieldActor         = "actor"
	fieldNote          = "note"
)

// Envelope values and history actors shared by every learn mode.
const (
	schemaVersion1 = "1"
	verbLearn      = "learn"
	statusActive   = "active"
	statusRetired  = "retired"
	eventRetired   = "retired"
	eventRestored  = "restored"
	actorHuman     = "human"
)

// Rigor knob values for the --rigor gate.
const (
	rigorSaid       = "said"
	rigorPointed    = "pointed"
	rigorWalked     = "walked"
	rigorRan        = "ran"
	rigorReproduced = "reproduced"
)

// Refusal and usage message formats. Split for the line-length gate; the
// rendered bytes are byte-identical to the shell's.
const (
	msgCanceled        = "canceled before learn completed"
	msgLearnFailed     = "learn failed: %v"
	usageFlags         = "--rule, --scope, --id, --provenance, --rigor, --status, --retire, --restore, --reason, --json"
	msgNeedOneMode     = "learn needs exactly one of --rule <text>, --status, --retire <id>, --restore <id>"
	msgScopeNeedsRule  = "learn: --scope belongs to --rule (append mode)"
	msgReasonNeedsVeto = "learn: --reason belongs to --retire/--restore"
	msgEmptyRule       = "refusing learn: --rule is empty (pass the one-line rule text)"
	msgEmptyScope      = "refusing learn: --scope is empty (pass a comma-separated glob list, e.g. 'src/*.ts')"
	msgBlankScopeElem  = "refusing learn: --scope holds an empty element (comma-separated globs, no blanks)"
	msgBadRigor        = "refusing learn: unknown rigor '%s' (want said|pointed|walked|ran|reproduced)"
	msgBadIDExplicit   = "refusing learn: id '%s' is not ^[a-z0-9][a-z0-9-]{0,64}$ (pass --id explicitly)"
	msgBadID           = "refusing learn: id '%s' is not ^[a-z0-9][a-z0-9-]{0,64}$"
	msgMkdirReview     = "learn could not create .review (storage writable?): %v"
	msgAlreadyLearned  = "rule text already learned (cite the existing rule id instead of learning it twice)"
	msgIDTaken         = "id '%s' is taken (pass a different --id)"
	msgWriteKB         = "learn could not write %s: %v"
	msgAlreadyRetired  = "learn: rule '%s' is already retired"
	msgRetireReason    = "learn --retire needs --reason with 30+ chars (human veto, on the record)"
	msgNotRetired      = "learn: rule '%s' is already active"
	msgRestoreReason   = "learn --restore needs --reason with 30+ chars (human veto, on the record)"
	msgUnknownRule     = "learn: no rule '%s' (see learn --status)"
	msgUnknownRuleNest = "learn: no rule '%s' (no %s yet — see learn --status)"
	msgCorruptMidWrite = "kb store %s unreadable mid-write (restore from backup)"
	msgCorruptStore    = "kb store %s is unreadable (restore from backup or fix the bad line)"
)

// Machine-envelope next hints for every learn mode.
const (
	nextStatus  = "learn --rule ... to append; learn --retire/--restore to veto"
	nextAppend  = "cite the rule id in review.json kb lists"
	nextRetire  = "retired rules never recall; restore with learn --restore"
	nextRestore = "active rules recall again on scope match"
)

// jsonZero renders the zero counters as the number literal 0.
var jsonZero = jq.Number("0")

// idRe matches the learn id shape ^[a-z0-9][a-z0-9-]{0,64}$ (shared with
// the kb citation gate).
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,64}$`)
