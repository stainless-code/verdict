package fixcore

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
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
	msgBatchRecipeEmpty = "fix --recipe %s: no open inline finding at or above %s under " +
		"lenses %s — findings need a matching lens value, or lower --min-severity, " +
		"widen --paths, or pick another recipe"
	msgBatchRecipeEmptyPaths = "fix --recipe %s: no open inline finding at or above %s under " +
		"lenses %s under %s — findings need a matching lens value, or lower --min-severity, " +
		"widen --paths, or pick another recipe"
	msgBatchRecipeNoLenses = "fix --recipe %s: the recipe declares no lenses, so it owns " +
		"no findings — pick a recipe that declares lenses or a different batch scope"
	nextBatchEmpty          = "lower --min-severity or fix one sig with --sig"
	nextBatchEmptyPaths     = "widen --paths, lower --min-severity, or fix one sig with --sig"
	nextBatchRecipe         = "lower --min-severity, widen --paths, pick another recipe, or fix one sig with --sig"
	nextBatchRecipeNoLenses = "pick a recipe that declares lenses or a different batch scope"
	nextBatchPasses         = "patch each anchor, run the verify command yourself, write one " +
		"verification.json claim per sig, land with record, then re-run fix --all"
	nextBatchStopped  = "triage the stopped sigs, or re-run fix --all with another floor"
	defaultBatchFloor = "important"
	floorOff          = "off"
)

// commaSpace joins the sigs or lens ids a message names.
const commaSpace = ", "

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
	if st.recipeEmpty {
		return st.recipeNothing(dir, ledgerPath, store, entries, fixOpen, fixOpenInline, now)
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
// claim rather than an empty store. A --recipe scope is checked first so a
// composite --selected --recipe empty intersection names both scopes and
// preserves data.recipe instead of pinning a bare no-selected refusal.
func (st *state) batchNothing(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	if st.req.Recipe != "" {
		return st.recipeNothing(dir, ledgerPath, store, entries, fixOpen, fixOpenInline, now)
	}
	if st.req.Selected {
		return st.noSelected()
	}
	if fixOpenInline == 0 {
		return st.completeRun(dir, ledgerPath, store, entries, fixOpen)
	}
	return st.emptyBatch(dir, ledgerPath, store, entries, fixOpen, fixOpenInline, now)
}

// recipeNothing explains a recipe-scoped batch that selected no sig. A recipe
// that resolves to no lenses owns nothing, so the run reports the empty scope
// instead of widening to every open inline finding. Otherwise the message
// names the recipe and its lens set and says findings need that provenance.
func (st *state) recipeNothing(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	st.at = now
	st.humanf("%s\n", st.recipeEmptyMsg())
	st.next = st.batchNext(st.recipeEmptyNext())
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
		Selection:   st.selectionPath(),
		Recipe:      recipePtr(st.req.Recipe),
	}
	return nil
}

func (st *state) recipeEmptyNext() string {
	if len(st.recipeLenses) == 0 {
		return nextBatchRecipeNoLenses
	}
	return nextBatchRecipe
}

// recipePrefix names the scope the re-run text carries: a composite
// --selected --recipe run reads "fix --selected --recipe <name>", so the
// empty-scope message never widens the next pass by dropping either flag.
func (st *state) recipePrefix() string {
	if st.req.Selected {
		return "fix --selected --recipe " + st.req.Recipe
	}
	return "fix --recipe " + st.req.Recipe
}

func (st *state) recipeEmptyMsg() string {
	if len(st.recipeLenses) == 0 {
		return st.recipeZeroLensMsg()
	}
	return st.recipeLensMismatchMsg()
}

func (st *state) recipeZeroLensMsg() string {
	msg := st.recipePrefix() + ": the recipe declares no lenses, so it owns " +
		"no findings — pick a recipe that declares lenses or a different batch scope"
	if st.req.Selected && st.selection != nil {
		msg = st.recipePrefix() + ": the recipe declares no lenses, so it owns " +
			"no findings (selection " + st.selection.path + " names " +
			strconv.Itoa(len(st.selection.sigs)) + " reacted sig(s)) — " +
			"pick a recipe that declares lenses or a different batch scope"
	}
	return msg
}

func (st *state) recipeLensMismatchMsg() string {
	lenses := strings.Join(st.recipeLenses, commaSpace)
	if st.req.Selected && st.selection != nil {
		selected := fmt.Sprintf("%s names %d reacted sig(s) and none is open inline under lenses %s",
			st.selection.path, len(st.selection.sigs), lenses)
		if len(st.req.Paths) > 0 {
			return st.recipePrefix() + ": " + selected + " under " +
				strings.Join(st.req.Paths, commaSpace) + " in the store — " +
				"findings need a matching lens value, or lower --min-severity, " +
				"widen --paths, or pick another recipe"
		}
		return st.recipePrefix() + ": " + selected + " in the store — " +
			"findings need a matching lens value, or lower --min-severity, " +
			"widen --paths, or pick another recipe"
	}
	if len(st.req.Paths) > 0 {
		return fmt.Sprintf(msgBatchRecipeEmptyPaths, st.req.Recipe, st.req.MinSeverity,
			lenses, strings.Join(st.req.Paths, commaSpace))
	}
	return fmt.Sprintf(msgBatchRecipeEmpty, st.req.Recipe, st.req.MinSeverity, lenses)
}

func recipePtr(name string) *string {
	if name == "" {
		return nil
	}
	return &name
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

// batchMode renders the flag that re-runs this batch. A composite
// scope re-runs with every flag it was run with: --selected --recipe <name>
// keeps the next pass narrowed to both scopes instead of widening it.
func (st *state) batchMode() string {
	var flags []string
	if st.req.All {
		flags = append(flags, "--all")
	}
	if st.req.Selected {
		flags = append(flags, "--selected")
	}
	if st.req.Recipe != "" {
		flags = append(flags, "--recipe "+st.req.Recipe)
	}
	if len(flags) == 0 {
		return "--all"
	}
	return strings.Join(flags, " ")
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
	raw, err := noblock.ReadFile(dir + suffixPatchJSON)
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
		Recipe:      recipePtr(st.req.Recipe),
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
		Recipe:      recipePtr(st.req.Recipe),
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
