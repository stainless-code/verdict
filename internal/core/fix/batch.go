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

const (
	outcomeReady    = "ready"
	outcomeCeiling  = stoppedCeiling
	outcomeNoAnchor = "no-anchor"
)

const (
	msgBatchNoVerify = "fix --all hands out one pass per selected sig and needs the " +
		"command that proves them: pass --verify-cmd <cmd> (or name a recipe that " +
		"carries verify_cmd), or write verifyCmd in %s/patch.json, then re-run fix --all"
	msgBatchEmpty = "fix --all: no open inline finding at or above %s — " +
		"lower --min-severity or fix one sig with --sig"
	msgBatchEmptyPaths = "fix --all: no open inline finding at or above %s under %s — " +
		"widen --paths, lower --min-severity, or fix one sig with --sig"
	nextBatchEmpty      = "lower --min-severity or fix one sig with --sig"
	nextBatchEmptyPaths = "widen --paths, lower --min-severity, or fix one sig with --sig"
	nextBatchPasses     = "patch each anchor, run the verify command yourself, write one " +
		"verification.json claim per sig, land with record, then re-run fix --all"
	nextBatchStopped  = "triage the stopped sigs, or re-run fix --all with another floor"
	defaultBatchFloor = "important"
	floorOff          = "off"
)

const batchInstructionsFmt = "instructions: patch each anchor file (%s), run the " +
	"verify command yourself (%s), then write %s/verification.json " +
	"[{sig, verdict, resultHead, verifyCmd}] with one claim per sig, verdict " +
	"VERIFIED and resultHead matching HEAD (this script never runs it — the " +
	"resultHead==HEAD match in the record/post verification gate is the " +
	"freshness proof); land it with record %s %s; re-run fix %s %s %s for the " +
	"next pass\n"

// batchInstructionsPRFmt is the same block for a PR bundle, whose landing path
// is a push and a re-fetch followed by post — record refuses a PR bundle.
const batchInstructionsPRFmt = "instructions: patch each anchor file (%s), run the " +
	"verify command yourself (%s), then write %s/verification.json " +
	"[{sig, verdict, resultHead, verifyCmd}] with one claim per sig, verdict " +
	"VERIFIED and resultHead matching HEAD (this script never runs it — the " +
	"resultHead==HEAD match in the record/post verification gate is the " +
	"freshness proof); push the fix, re-fetch with fetch %s %s, then land it with " +
	"post %s %s; re-run fix %s %s %s for the next pass\n"

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

func (st *state) runBatch(
	ctx context.Context,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	floor, ref := st.batchFloor()
	if ref != nil {
		return ref
	}
	sel, ref := st.batchSelector(dir, floor)
	if ref != nil {
		return ref
	}
	picks := readcommon.Select(entries, sel)
	if len(picks) == 0 {
		return st.batchNothing(dir, ledgerPath, store, entries, fixOpen, fixOpenInline, now)
	}
	verify, ref := st.batchEnv(ctx, dir)
	if ref != nil {
		return ref
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

func (st *state) validateFloor() *core.RefusalError {
	floor := st.req.MinSeverity
	if floor == "" || floor == floorOff || config.SeverityRank(floor) > 0 {
		return nil
	}
	return st.refuse("bad-min-severity",
		"fix --all: --min-severity takes critical|important|informational|off")
}

func (st *state) batchFloor() (string, *core.RefusalError) {
	if st.req.MinSeverity == "" {
		st.req.MinSeverity = st.defaultBatchFloor()
	}
	if ref := st.validateFloor(); ref != nil {
		return "", ref
	}
	return st.req.MinSeverity, nil
}

// defaultBatchFloor is the implicit floor of an unflagged batch. --all keeps
// the important floor; --selected reads every reacted sig, because a human
// reacting to a finding is the endorsement a floor would otherwise re-judge.
func (st *state) defaultBatchFloor() string {
	if st.req.Selected {
		return floorOff
	}
	return defaultBatchFloor
}

func (st *state) batchEnv(ctx context.Context, dir string) (string, *core.RefusalError) {
	verify := st.batchVerifyCmd(dir)
	if verify == "" {
		return "", st.refuse(codeNoVerifyCmd, st.batchMsg(fmt.Sprintf(msgBatchNoVerify, dir)))
	}
	patch, ref := st.writePatch(ctx, dir)
	if ref != nil {
		return "", ref
	}
	st.patch = patch
	return verify, nil
}

// batchNothing explains a batch that selected no sig: --all over an empty store
// completes, --all over a floor that withheld everything names the scope, and
// --selected refuses, because a selection that names no open finding is a stale
// claim rather than an empty store.
func (st *state) batchNothing(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	if st.req.Selected {
		return st.noSelected()
	}
	if fixOpenInline == 0 {
		return st.completeRun(dir, ledgerPath, store, entries, fixOpen)
	}
	return st.emptyBatch(dir, ledgerPath, store, entries, fixOpen, fixOpenInline, now)
}

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

func batchRecord(entry any) *jq.Obj {
	rec, ok := entry.(*jq.Obj)
	if !ok {
		return nil
	}
	return rec
}

func batchSeverity(entry any) string {
	severity := interpOf(batchRecord(entry), keySeverity)
	if severity == "" {
		return sevUntagged
	}
	return severity
}

func (st *state) printBatch(dir, ledgerPath string, rows []batchRow, verify string) int {
	ready, anchors := batchReady(rows)
	st.printSelection()
	st.humanf("» ledger: %s — batch of %d selected%s, %d pass(es)\n",
		ledgerPath, len(rows), scopeSuffix(st.req.Paths), ready)
	for i := range rows {
		st.printBatchRow(ledgerPath, &rows[i])
	}
	if ready == 0 {
		return ready
	}
	st.printKB(dir)
	st.printBatchInstructions(dir, verify, joinAnchors(anchors))
	st.humanRaw(gateMsg)
	st.humanRaw(budgetMsg)
	st.humanf("next: patch %s with verify %q, write verification.json, "+
		"run %s to land it, then re-run fix %s\n", joinAnchors(anchors), verify,
		st.landVerb(), st.batchMode())
	return ready
}

// printSelection names the reacted scope a --selected batch works from, so the
// output says what narrowed the batch instead of looking like an --all run.
func (st *state) printSelection() {
	if st.selection == nil {
		return
	}
	st.humanf("» selection: %s — %d reacted sig(s)\n", st.selection.path, len(st.selection.sigs))
}

// batchMode renders the flag that re-runs this batch.
func (st *state) batchMode() string {
	if st.req.Selected {
		return "--selected"
	}
	return "--all"
}

// landVerb names the command that lands a pass in this bundle: a local bundle
// records, a PR bundle posts (it has no record path).
func (st *state) landVerb() string {
	if st.providerBundle() {
		return "post"
	}
	return "record"
}

// printBatchInstructions prints the batch's land-and-re-run block: a PR bundle
// lands through a re-fetch and post, a local bundle through record.
func (st *state) printBatchInstructions(dir, verify, anchors string) {
	if st.providerBundle() {
		st.humanf(batchInstructionsPRFmt, anchors, verify, dir,
			st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr, st.batchMode())
		return
	}
	st.humanf(batchInstructionsFmt, anchors, verify, dir,
		st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr, st.batchMode())
}

// providerBundle reports a PR bundle: the only bundle kind that carries no
// record path, and the one --selected can read reactions for.
func (st *state) providerBundle() bool {
	return !readcommon.IsLocalBundleDir(st.dir)
}

func (st *state) selectionPath() *string {
	if st.selection == nil {
		return nil
	}
	return &st.selection.path
}

func batchReady(rows []batchRow) (int, []string) {
	anchors := make([]string, 0, len(rows))
	for i := range rows {
		if rows[i].outcome == outcomeReady {
			anchors = append(anchors, rows[i].anchor)
		}
	}
	return len(anchors), anchors
}

func (st *state) printBatchRow(ledgerPath string, row *batchRow) {
	if row.outcome == outcomeReady {
		st.printPassBlock(readPassBlock(row.rec, row.full(), row.anchor), row.pass, ledgerPath)
		return
	}
	st.humanf("fix: %s [%s] %s — %s: %s\n",
		readcommon.Prefix(row.full()), row.severity, row.locator, row.outcome, row.reason())
}

func (r *batchRow) full() string {
	sig, ok := readcommon.FieldOf(r.pick.Entry, keySig).(string)
	if !ok {
		return ""
	}
	return sig
}

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
	st.next = st.batchNext(nextBatchPasses)
	if ready == 0 {
		st.next = st.batchNext(nextBatchStopped)
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
		Patch:       st.patch,
		Paths:       st.req.Paths,
		Selection:   st.selectionPath(),
	}
}

func (st *state) emptyBatch(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	st.at = now
	st.humanf("%s\n", st.batchMsg(batchEmptyMsg(st.req.MinSeverity, st.req.Paths)))
	st.next = st.batchNext(batchEmptyNext(st.req.Paths))
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
		Paths:       st.req.Paths,
	}
	return nil
}

// batchMsg and batchNext rewrite a shared hint for the mode this run used: the
// --all wording stays byte for byte, while a --selected or PR-bundle run names
// its own flag and the command that lands a pass there.
func (st *state) batchMsg(msg string) string {
	return strings.ReplaceAll(msg, "fix --all", "fix "+st.batchMode())
}

func (st *state) batchNext(next string) string {
	out := st.batchMsg(next)
	return strings.ReplaceAll(out, "land with record", "land with "+st.landVerb())
}

func batchEmptyMsg(floor string, paths []string) string {
	if len(paths) > 0 {
		return fmt.Sprintf(msgBatchEmptyPaths, floor, strings.Join(paths, ", "))
	}
	return fmt.Sprintf(msgBatchEmpty, floor)
}

func batchEmptyNext(paths []string) string {
	if len(paths) > 0 {
		return nextBatchEmptyPaths
	}
	return nextBatchEmpty
}

func scopeSuffix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return " under " + strings.Join(paths, ", ")
}

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
