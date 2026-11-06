// Package learncore is the learn verb's domain service: the structured local
// knowledge base. Exactly one mode per invocation: append a scoped rule, list
// rules with precision, or retire/restore by human veto with a 30+ char
// reason. The store is .verdict/kb.jsonl (one JSON object per line); every
// rewrite goes through the shared atomic path. internal/learn owns argv
// parsing, rentry, envelope rendering, and exit mapping.
package learncore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved learn invocation. Learn takes no repo/pr
// positionals; the runtime carries env, clock, and progress.
type Request struct {
	core.ReadRequest
	Rule       string
	Scope      string
	ID         string
	Provenance string
	Rigor      string `enum:"said|pointed|walked|ran|reproduced"`
	Reason     string
	Retire     string
	Restore    string
	Status     bool
	JSONMode   bool
}

// Response carries the completed mode: the envelope inputs and the typed data
// profile (a rule list or one rule row).
type Response struct {
	At       string
	Next     string
	Data     *Data
	Warnings []string
}

// Data is the learn envelope's data profile: status carries rules, every
// other mode carries one rule row.
type Data struct { //nolint:govet // the wire shape keeps its field order
	Rules []any
	Rule  *jq.Obj
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	if d.Rule != nil {
		return jq.NewObj("rule", d.Rule)
	}
	return jq.NewObj("rules", d.Rules)
}

type state struct {
	req      *Request
	root     string
	now      string
	next     string
	data     *Data
	warnings []string
}

// storeDir is the tracked shared configuration directory at the Git root.
func (st *state) storeDir() string { return filepath.Join(st.root, learnStoreDir) }

// kbFile is the tracked shared knowledge corpus the verb reads and writes.
func (st *state) kbFile() string { return filepath.Join(st.root, kb.File) }

type appendInput struct {
	rule  string
	scope string
	lid   string
	prov  string
	rigor string
}

// Run executes exactly one learn mode.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, root: req.RepoRootPath(), now: req.TimeUTC()}
	if err := ctx.Err(); err != nil {
		return nil, st.refuse(codeCanceled, msgCanceled)
	}
	if ref := st.runLearn(); ref != nil {
		return nil, ref
	}
	return st.response(), nil
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

func (st *state) humanf(format string, args ...any) {
	if st.req.JSONMode {
		return
	}
	st.req.Human(strings.TrimSuffix(fmt.Sprintf(format, args...), "\n"))
}

func (st *state) response() *Response {
	return &Response{
		At:       st.now,
		Next:     st.next,
		Data:     st.data,
		Warnings: st.warnings,
	}
}

func (st *state) runLearn() *core.RefusalError {
	if ref := st.checkModes(); ref != nil {
		return ref
	}
	switch {
	case st.req.Status:
		return st.runStatus()
	case st.req.Rule != "":
		return st.runAppend()
	case st.req.Retire != "":
		return st.runRetire()
	default:
		return st.runRestore()
	}
}

func (st *state) checkModes() *core.RefusalError {
	if st.countModes() != 1 {
		return st.refuse(codeBadFlags, msgNeedOneMode)
	}
	if st.req.Rule == "" && st.req.Scope != "" {
		return st.refuse(codeBadFlags, msgScopeNeedsRule)
	}
	if st.req.Retire == "" && st.req.Restore == "" && st.req.Reason != "" {
		return st.refuse(codeBadFlags, msgReasonNeedsVeto)
	}
	return nil
}

func (st *state) countModes() int {
	modes := 0
	if st.req.Rule != "" {
		modes++
	}
	if st.req.Status {
		modes++
	}
	if st.req.Retire != "" {
		modes++
	}
	if st.req.Restore != "" {
		modes++
	}
	return modes
}

func (st *state) runStatus() *core.RefusalError {
	rows, ref := st.readStatusRows()
	if ref != nil {
		return ref
	}
	st.next = nextStatus
	st.data = &Data{Rules: rows}
	if st.req.JSONMode {
		return nil
	}
	if len(rows) == 0 {
		st.humanf("learn: no rules yet (pass --rule to append the first)\n")
		return nil
	}
	for _, row := range rows {
		st.printStatusRow(row)
	}
	return nil
}

func (st *state) readStatusRows() ([]any, *core.RefusalError) {
	decoded, err := kb.ReadRows(st.root)
	if errors.Is(err, kb.ErrMissing) {
		return []any{}, nil
	}
	if err != nil {
		return nil, st.corruptStore()
	}
	rows := make([]any, 0, len(decoded))
	for _, row := range decoded {
		rows = append(rows, withPrecision(row))
	}
	return rows, nil
}

func (st *state) printStatusRow(row any) {
	m, ok := row.(*jq.Obj)
	if !ok || m == nil {
		return
	}
	st.humanf("%s [%s] %s hits=%s false_hits=%s judged=%s precision=%s\n",
		jq.Interp(mustGet(m, fieldID)),
		jq.Interp(mustGet(m, fieldStatus)),
		jq.Interp(mustGet(m, fieldRigor)),
		numOrZero(mustGet(m, fieldHits)),
		numOrZero(mustGet(m, fieldFalseHits)),
		numOrZero(mustGet(m, fieldJudged)),
		jq.Interp(mustGet(m, fieldPrecision)),
	)
}

func withPrecision(row *jq.Obj) *jq.Obj {
	judgedRaw := mustGet(row, fieldJudged)
	precision := precisionNA
	if !isNumberZero(judgedRaw) {
		precision = numOrZero(mustGet(row, fieldHits)) + "/" + numOrZero(judgedRaw)
	}
	row.Set(fieldPrecision, precision)
	return row
}

func isNumberZero(v any) bool {
	if v == nil || v == false {
		return true
	}
	n, ok := jq.IntOf(v)
	return ok && n == 0
}

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
		return jq.Float(t)
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

func (st *state) runAppend() *core.RefusalError {
	validated, ref := st.validateAppend()
	if ref != nil {
		return ref
	}
	if ref := st.rejectAppendDup(validated.rule, validated.lid); ref != nil {
		return ref
	}
	row := appendRowDoc(validated)
	if err := kb.AppendRow(st.root, row.MarshalCompact()+"\n"); err != nil {
		return st.refuse(codeLearnFailed, fmt.Sprintf(msgWriteKB, st.kbFile(), err))
	}
	st.next = nextAppend
	st.data = &Data{Rule: row}
	st.humanf("learned %s -> %s (scope %s, rigor %s)\n",
		validated.lid, st.kbFile(), validated.scope, validated.rigor)
	return nil
}

func (st *state) validateAppend() (*appendInput, *core.RefusalError) {
	ruleT := readcommon.TrimSpace(st.req.Rule)
	if ruleT == "" {
		return nil, st.refuse(codeBadRule, msgEmptyRule)
	}
	scopeT, ref := st.checkScope()
	if ref != nil {
		return nil, ref
	}
	if ref := st.checkRigor(); ref != nil {
		return nil, ref
	}
	return st.newAppendInput(ruleT, scopeT)
}

func (st *state) checkScope() (string, *core.RefusalError) {
	scopeT := readcommon.TrimSpace(st.req.Scope)
	if scopeT == "" {
		return "", st.refuse(codeBadScope, msgEmptyScope)
	}
	for elem := range strings.SplitSeq(scopeT, ",") {
		if readcommon.TrimSpace(elem) == "" {
			return "", st.refuse(codeBadScope, msgBlankScopeElem)
		}
	}
	return scopeT, nil
}

func (st *state) checkRigor() *core.RefusalError {
	switch st.req.Rigor {
	case rigorSaid, rigorPointed, rigorWalked, rigorRan, rigorReproduced:
		return nil
	default:
		return st.refuse(codeBadRigor, fmt.Sprintf(msgBadRigor, st.req.Rigor))
	}
}

func (st *state) newAppendInput(ruleT, scopeT string) (*appendInput, *core.RefusalError) {
	prov := st.req.Provenance
	if prov == "" {
		prov = defaultProvenance
	}
	lid := st.req.ID
	if lid == "" {
		lid = readcommon.Slugify(ruleT)
	}
	if !kb.ValidID(lid) {
		return nil, st.refuse(codeBadID, fmt.Sprintf(msgBadIDExplicit, lid))
	}
	return &appendInput{rule: ruleT, scope: scopeT, lid: lid, prov: prov, rigor: st.req.Rigor}, nil
}

func (st *state) rejectAppendDup(ruleT, lid string) *core.RefusalError {
	if err := os.MkdirAll(st.storeDir(), reviewDirPerm); err != nil {
		return st.refuse(codeLearnFailed, fmt.Sprintf(msgMkdirReview, err))
	}
	if !readcommon.IsFile(st.kbFile()) {
		return nil
	}
	rows, err := kb.ReadRows(st.root)
	if err != nil {
		return st.corruptStore()
	}
	return st.checkDupRows(rows, ruleT, lid)
}

func (st *state) checkDupRows(rows []*jq.Obj, ruleT, lid string) *core.RefusalError {
	for _, row := range rows {
		if ruleOf(row) == ruleT {
			return st.refuse(codeAlreadyLearned, msgAlreadyLearned)
		}
		if idOf(row) == lid {
			return st.refuse(codeIDTaken, fmt.Sprintf(msgIDTaken, lid))
		}
	}
	return nil
}

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

func (st *state) runRetire() *core.RefusalError {
	id := st.req.Retire
	if !kb.ValidID(id) {
		return st.refuse(codeBadID, fmt.Sprintf(msgBadID, id))
	}
	rows, cur, ref := st.loadVetoRow(id)
	if ref != nil {
		return ref
	}
	if jq.Interp(mustGet(cur, fieldStatus)) == statusRetired {
		return st.refuse(codeAlreadyRetired, fmt.Sprintf(msgAlreadyRetired, id))
	}
	if utf8.RuneCountInString(st.req.Reason) < reasonMin {
		return st.refuse(codeBadReason, msgRetireReason)
	}
	st.tombstoneRetire(cur)
	if ref := st.writeRows(rows); ref != nil {
		return ref
	}
	st.next = nextRetire
	st.data = &Data{Rule: cur}
	st.humanf("retired %s (tombstone kept, counters untouched)\n", id)
	return nil
}

func (st *state) runRestore() *core.RefusalError {
	id := st.req.Restore
	if !kb.ValidID(id) {
		return st.refuse(codeBadID, fmt.Sprintf(msgBadID, id))
	}
	rows, cur, ref := st.loadVetoRow(id)
	if ref != nil {
		return ref
	}
	if jq.Interp(mustGet(cur, fieldStatus)) == statusActive {
		return st.refuse(codeNotRetired, fmt.Sprintf(msgNotRetired, id))
	}
	if utf8.RuneCountInString(st.req.Reason) < reasonMin {
		return st.refuse(codeBadReason, msgRestoreReason)
	}
	cur.Set(fieldStatus, statusActive)
	st.appendHistory(cur, eventRestored)
	if ref := st.writeRows(rows); ref != nil {
		return ref
	}
	st.next = nextRestore
	st.data = &Data{Rule: cur}
	st.humanf("restored %s (status active, tombstone history kept)\n", id)
	return nil
}

func (st *state) loadVetoRow(id string) ([]*jq.Obj, *jq.Obj, *core.RefusalError) {
	rows, ref := st.readRowsStrict(id)
	if ref != nil {
		return nil, nil, ref
	}
	cur := kb.FindRow(rows, id)
	if cur == nil {
		return nil, nil, st.refuse(codeUnknownRule, fmt.Sprintf(msgUnknownRule, id))
	}
	return rows, cur, nil
}

func (st *state) tombstoneRetire(cur *jq.Obj) {
	cur.Set(fieldStatus, statusRetired)
	cur.Set(fieldRetiredAt, st.now)
	cur.Set(fieldRetireReason, st.req.Reason)
	st.appendHistory(cur, eventRetired)
}

func (st *state) appendHistory(cur *jq.Obj, event string) {
	kb.AppendHistory(cur, jq.NewObj(fieldAt, st.now, fieldEvent, event,
		fieldActor, actorHuman, fieldNote, st.req.Reason))
}

func (st *state) readRowsStrict(id string) ([]*jq.Obj, *core.RefusalError) {
	rows, err := kb.ReadRows(st.root)
	if errors.Is(err, kb.ErrMissing) {
		return nil, st.refuse(codeUnknownRule, fmt.Sprintf(msgUnknownRuleNest, id, st.kbFile()))
	}
	if err != nil {
		return nil, st.corruptStore()
	}
	return rows, nil
}

func idOf(row *jq.Obj) string {
	v, _ := row.Get(fieldID)
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func ruleOf(row *jq.Obj) string {
	v, _ := row.Get(fieldRule)
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func mustGet(m *jq.Obj, key string) any {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}

func (st *state) writeRows(rows []*jq.Obj) *core.RefusalError {
	if err := kb.WriteRows(st.root, rows); err != nil {
		return st.refuse(codeCorruptStore, fmt.Sprintf(msgCorruptMidWrite, st.kbFile()))
	}
	return nil
}

func (st *state) corruptStore() *core.RefusalError {
	return st.refuse(codeCorruptStore, fmt.Sprintf(msgCorruptStore, st.kbFile()))
}

const (
	// learnStoreDir is the tracked shared configuration directory.
	learnStoreDir = ".verdict"
	// reviewDirPerm is the mode the store dir is created with.
	reviewDirPerm = 0o777
	// reasonMin is the veto-reason floor (characters, like ${#}).
	reasonMin = 30
	// defaultProvenance is the --provenance default.
	defaultProvenance = "manual"
	// precisionNA renders when judged is the number 0.
	precisionNA = "n/a"
)

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

const (
	fieldID           = "id"
	fieldRule         = "rule"
	fieldScope        = "scope"
	fieldProvenance   = "provenance"
	fieldRigor        = "rigor"
	fieldHits         = "hits"
	fieldFalseHits    = "false_hits"
	fieldJudged       = "judged"
	fieldStatus       = "status"
	fieldPrecision    = "precision"
	fieldHistory      = "history"
	fieldRetiredAt    = "retiredAt"
	fieldRetireReason = "retireReason"
	fieldAt           = "at"
	fieldEvent        = "event"
	fieldActor        = "actor"
	fieldNote         = "note"
)

const (
	statusActive  = "active"
	statusRetired = "retired"
	eventRetired  = "retired"
	eventRestored = "restored"
	actorHuman    = "human"
)

const (
	rigorSaid       = "said"
	rigorPointed    = "pointed"
	rigorWalked     = "walked"
	rigorRan        = "ran"
	rigorReproduced = "reproduced"
)

const (
	msgCanceled        = "canceled before learn completed"
	msgNeedOneMode     = "learn needs exactly one of --rule <text>, --status, --retire <id>, --restore <id>"
	msgScopeNeedsRule  = "learn: --scope belongs to --rule (append mode)"
	msgReasonNeedsVeto = "learn: --reason belongs to --retire/--restore"
	msgEmptyRule       = "refusing learn: --rule is empty (pass the one-line rule text)"
	msgEmptyScope      = "refusing learn: --scope is empty (pass a comma-separated glob list, e.g. 'src/*.ts')"
	msgBlankScopeElem  = "refusing learn: --scope holds an empty element (comma-separated globs, no blanks)"
	msgBadRigor        = "refusing learn: unknown rigor '%s' (want said|pointed|walked|ran|reproduced)"
	msgBadIDExplicit   = "refusing learn: id '%s' is not ^[a-z0-9][a-z0-9-]{0,64}$ (pass --id explicitly)"
	msgBadID           = "refusing learn: id '%s' is not ^[a-z0-9][a-z0-9-]{0,64}$"
	msgMkdirReview     = "learn could not create .verdict (storage writable?): %v"
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

const (
	nextStatus  = "learn --rule ... to append; learn --retire/--restore to veto"
	nextAppend  = "cite the rule id in review.json kb lists"
	nextRetire  = "retired rules never recall; restore with learn --restore"
	nextRestore = "active rules recall again on scope match"
)

var jsonZero = jq.Number("0")
