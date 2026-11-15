package fixcore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Batch outcome words: one per selected sig, so a batch says what happened to
// every sig it looked at instead of only the one it advanced.
const (
	// outcomeReady marks a sig the batch consumed a pass for.
	outcomeReady = "ready"
	// outcomeCeiling marks a sig that already spent its passes.
	outcomeCeiling = stoppedCeiling
	// outcomeNoAnchor marks a selected finding with no anchor path to scope.
	outcomeNoAnchor = "no-anchor"
)

// The batch's own refusal text and hints.
const (
	// msgBatchNoVerify refuses a batch with no verification path: passes
	// handed out with no command to prove them spend agent runs for nothing.
	msgBatchNoVerify = "fix --all hands out one pass per selected sig and needs the " +
		"command that proves them: pass --verify-cmd <cmd> (or name a recipe that " +
		"carries verify_cmd), or write verifyCmd in %s/patch.json, then re-run fix --all"
	// msgBatchEmpty reports a batch whose floor selected nothing.
	msgBatchEmpty = "fix --all: no open inline finding at or above %s — " +
		"lower --min-severity or fix one sig with --sig"
	// nextBatchEmpty is the envelope follow-up for an empty batch.
	nextBatchEmpty = "lower --min-severity or fix one sig with --sig"
	// nextBatchPasses is the envelope follow-up after a batch consumed passes.
	nextBatchPasses = "patch each anchor, run the verify command yourself, write one " +
		"verification.json claim per sig, land with record, then re-run fix --all"
	// nextBatchStopped is the envelope follow-up when no sig advanced.
	nextBatchStopped = "triage the stopped sigs, or re-run fix --all with another floor"
	// defaultBatchFloor is the batch's severity floor when --min-severity is
	// absent: important, so a batch never spends passes on nits by default.
	defaultBatchFloor = "important"
	// floorOff states the absence of a floor.
	floorOff = "off"
)

// batchInstructionsFmt guides the agent through one batch pass.
const batchInstructionsFmt = "instructions: patch each anchor file (%s), run the " +
	"verify command yourself (%s), then write %s/verification.json " +
	"[{sig, verdict, resultHead, verifyCmd}] with one claim per sig, verdict " +
	"VERIFIED and resultHead matching HEAD (this script never runs it — the " +
	"resultHead==HEAD match in the record/post verification gate is the " +
	"freshness proof); land it with record %s %s; re-run fix %s %s --all for the " +
	"next pass\n"

// batchRow is one selected sig's outcome inside a running batch, plus the
// record and anchor the printed block needs.
type batchRow struct { //nolint:govet // the row reads in ledger order
	pick     readcommon.Ranked
	rec      *jq.Obj
	anchor   string
	escape   string
	pass     int
	outcome  string
	severity string
	locator  string
}

// runBatch executes one batch pass: every open inline finding at or above the
// floor gets the same per-sig ceilings a single pass gets, and each sig's
// outcome is named. A sig at its ceiling or outside its anchor scope is
// reported, not refused, because one stuck finding must not block the rest of
// the batch; the batch refuses only when no verification path exists.
func (st *state) runBatch(
	ctx context.Context,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	if st.req.MinSeverity == "" {
		st.req.MinSeverity = defaultBatchFloor
	}
	if ref := st.validateFloor(); ref != nil {
		return ref
	}
	if fixOpenInline == 0 {
		return st.completeRun(dir, ledgerPath, store, entries, fixOpen)
	}
	picks := readcommon.Select(entries, &readcommon.Selector{
		Status: readcommon.StatusOpen,
		Kind:   readcommon.KindInline,
		Floor:  st.req.MinSeverity,
	})
	if len(picks) == 0 {
		return st.emptyBatch(dir, ledgerPath, store, entries, fixOpen, fixOpenInline, now)
	}
	verify := st.batchVerifyCmd(dir)
	if verify == "" {
		return st.refuse(codeNoVerifyCmd, fmt.Sprintf(msgBatchNoVerify, dir))
	}
	work := st.worktreeFiles(ctx)
	rows := make([]batchRow, 0, len(picks))
	for _, pick := range picks {
		rows = append(rows, st.batchPick(store, pick, work, now))
	}
	if ref := writeLedger(ledgerPath, store); ref != nil {
		return ref
	}
	ready := st.printBatch(dir, ledgerPath, rows, verify)
	st.fillBatchData(dir, ledgerPath, store, entries, rows, verify,
		ready, fixOpen, fixOpenInline)
	return nil
}

// validateFloor refuses a --min-severity outside the published vocabulary,
// reading `off` as the stated absence of a floor.
func (st *state) validateFloor() *core.RefusalError {
	floor := st.req.MinSeverity
	if floor == "" || floor == floorOff || config.SeverityRank(floor) > 0 {
		return nil
	}
	return st.refuse("bad-min-severity",
		"fix --all: --min-severity takes critical|important|informational|off")
}

// batchPick runs the per-sig ceilings for one selected finding and records the
// pass it consumes, exactly as a single pass would.
func (st *state) batchPick(
	store *ledgerDoc,
	pick readcommon.Ranked,
	work []string,
	now string,
) batchRow {
	row := batchRow{
		pick: pick, rec: batchRecord(pick.Entry),
		severity: batchSeverity(pick.Entry), locator: readcommon.Locator(pick.Entry),
	}
	anchor := interpOf(row.rec, keyPath)
	row.anchor = anchor
	full := row.full()
	passes := ledgerPasses(store, full)
	escapes := escapePaths(work, baselineOf(store, full), anchor)
	switch {
	case passes >= maxPasses:
		tombstone(store, full, stoppedCeiling, passes, now)
		row.outcome = outcomeCeiling
	case passes >= 1 && len(escapes) > 0:
		tombstone(store, full, codeScopeEscape, 1, now)
		row.outcome, row.escape = codeScopeEscape, escapes[0]
	case anchor == "":
		row.outcome = outcomeNoAnchor
	default:
		row.pass, row.outcome = passes+1, outcomeReady
		setPass(store, full, row.pass, now, work)
	}
	return row
}

// batchRecord narrows one ranked entry to its record object.
func batchRecord(entry any) *jq.Obj {
	rec, ok := entry.(*jq.Obj)
	if !ok {
		return nil
	}
	return rec
}

// batchSeverity reads one selected finding's severity, untagged when absent.
func batchSeverity(entry any) string {
	severity := interpOf(batchRecord(entry), keySeverity)
	if severity == "" {
		return sevUntagged
	}
	return severity
}

// printBatch writes one block per ready sig plus one line per skipped sig, in
// rank order, then the bundle-wide kb recall and the batch instructions. It
// returns how many sigs consumed a pass.
func (st *state) printBatch(dir, ledgerPath string, rows []batchRow, verify string) int {
	ready, anchors := batchReady(rows)
	st.humanf("» ledger: %s — batch of %d selected, %d pass(es)\n",
		ledgerPath, len(rows), ready)
	for i := range rows {
		st.printBatchRow(ledgerPath, &rows[i])
	}
	if ready == 0 {
		return ready
	}
	st.printKB(dir)
	st.humanf(batchInstructionsFmt, joinAnchors(anchors), verify, dir,
		st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr)
	st.humanf("%s", gateMsg)
	st.humanf("%s", budgetMsg)
	st.humanf("next: patch %s with verify %q, write verification.json, "+
		"run record to land it, then re-run fix --all\n", joinAnchors(anchors), verify)
	return ready
}

// batchReady counts the sigs that consumed a pass and lists their anchors.
func batchReady(rows []batchRow) (int, []string) {
	anchors := make([]string, 0, len(rows))
	for i := range rows {
		if rows[i].outcome == outcomeReady {
			anchors = append(anchors, rows[i].anchor)
		}
	}
	return len(anchors), anchors
}

// printBatchRow writes one selected sig's block or its skip line.
func (st *state) printBatchRow(ledgerPath string, row *batchRow) {
	if row.outcome == outcomeReady {
		st.printPassBlock(readPassBlock(row.rec, row.full(), row.anchor), row.pass, ledgerPath)
		return
	}
	st.humanf("fix: %s [%s] %s — %s: %s\n",
		readcommon.Prefix(row.full()), row.severity, row.locator, row.outcome, row.reason())
}

// full reads the selected sig.
func (r *batchRow) full() string {
	sig, ok := readcommon.FieldOf(r.pick.Entry, keySig).(string)
	if !ok {
		return ""
	}
	return sig
}

// reason states why a skipped row consumed no pass.
func (r *batchRow) reason() string {
	switch r.outcome {
	case outcomeCeiling:
		return fmt.Sprintf("%d/%d passes used — triage it or split the fix", maxPasses, maxPasses)
	case codeScopeEscape:
		return fmt.Sprintf("worktree touches '%s' outside anchor '%s' — "+
			"revert or stash it, then re-run", r.escape, r.anchor)
	case outcomeNoAnchor:
		return "finding carries no anchor path to scope the pass"
	default:
		return ""
	}
}

// batchVerifyCmd reads the batch's verification command: the operator's
// --verify-cmd first, then the bundle's patch.json verifyCmd.
func (st *state) batchVerifyCmd(dir string) string {
	if st.req.VerifyCmd != "" {
		return st.req.VerifyCmd
	}
	raw, err := os.ReadFile(dir + suffixPatchJSON)
	if err != nil {
		return ""
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return ""
	}
	m, ok := doc.(*jq.Obj)
	if !ok {
		return ""
	}
	return interpOf(m, keyVerifyCmd)
}

// fillBatchData fills the batch envelope profile.
func (st *state) fillBatchData(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	rows []batchRow,
	verify string,
	ready, fixOpen, fixOpenInline int,
) {
	out := make([]BatchRow, 0, len(rows))
	for i := range rows {
		out = append(out, BatchRow{
			Sig:      rows[i].full(),
			Locator:  rows[i].locator,
			Severity: rows[i].severity,
			Pass:     rows[i].pass,
			Outcome:  rows[i].outcome,
			Reason:   rows[i].reason(),
		})
	}
	floor := st.req.MinSeverity
	st.next = nextBatchPasses
	if ready == 0 {
		st.next = nextBatchStopped
	}
	st.data = &Data{
		Dir:         dir,
		Ledger:      ledgerPath,
		MaxPasses:   maxPasses,
		OpenInline:  fixOpenInline,
		Fixed:       fixedCount(entries),
		Open:        fixOpen,
		Stopped:     stoppedCount(store),
		VerifyCmd:   &verify,
		Batch:       out,
		MinSeverity: &floor,
	}
}

// emptyBatch reports a batch whose floor selected nothing: no pass is
// consumed, no verify command is demanded, and the operator hears why.
func (st *state) emptyBatch(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	st.at = now
	st.humanf(msgBatchEmpty+"\n", st.req.MinSeverity)
	if ref := writeLedger(ledgerPath, store); ref != nil {
		return ref
	}
	st.next = nextBatchEmpty
	st.data = &Data{
		Dir:         dir,
		Ledger:      ledgerPath,
		MaxPasses:   maxPasses,
		OpenInline:  fixOpenInline,
		Fixed:       fixedCount(entries),
		Open:        fixOpen,
		Stopped:     stoppedCount(store),
		Batch:       []BatchRow{},
		MinSeverity: &st.req.MinSeverity,
	}
	return nil
}

// joinAnchors renders the batch's anchor files for one line, each once and in
// selection order.
func joinAnchors(anchors []string) string {
	seen := make(map[string]bool, len(anchors))
	out := make([]string, 0, len(anchors))
	for _, a := range anchors {
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return strings.Join(out, ", ")
}
