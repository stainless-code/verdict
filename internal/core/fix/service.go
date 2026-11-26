// Package fixcore is the fix verb's domain service: the bounded, verify-gated
// fix loop over a local or PR bundle. It writes the ledger and the patch output
// and never writes findings.json; internal/fix owns argv parsing, re-entry,
// envelope rendering, and exit mapping.
package fixcore

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved fix invocation.
type Request struct { //nolint:govet // the transport contract keeps its field order
	core.ReadRequest
	Sig         string
	All         bool
	Selected    bool
	MinSeverity string
	VerifyCmd   string
	Paths       []string
	Test        string
	Commit      bool
	Recipe      string
	// TestSet records that --test was passed, so an empty value refuses
	// instead of silently running an ordinary fix pass.
	TestSet bool
	// RecipeSet records that --recipe was passed, so an empty value refuses
	// instead of silently running an ordinary fix pass.
	RecipeSet bool
	Human     io.Writer
}

// Response carries the completed pass, batch, or completion summary.
type Response struct {
	At       string
	Next     string
	Data     *Data
	Warnings []string
}

// Data is the fix envelope's data profile; the pass, batch, and completion
// shapes share it.
type Data struct { //nolint:govet // field order is the published property order
	Dir         string
	Ledger      string
	Sig         *string
	Locator     *string
	Severity    *string
	Body        *string
	Brief       *core.Brief
	Anchors     *Anchors
	Pass        *int
	MaxPasses   int
	KB          any
	OpenInline  int
	Fixed       int
	Open        int
	Stopped     int
	Complete    bool
	MinSeverity *string
	VerifyCmd   *string
	Patch       *string
	Paths       []string
	Selection   *string
	Batch       []BatchRow
	TestPath    *string
	Committed   bool
	Recipe      *string
}

// BatchRow is one sig's outcome in a fix batch.
type BatchRow struct { //nolint:govet // field order is the published property order
	Sig      string
	Locator  string
	Severity string
	Pass     int
	Outcome  string
	Reason   string
}

// Wire renders one batch row in published wire order.
func (r *BatchRow) Wire() *jq.Obj {
	return jq.NewObj(
		"sig", r.Sig,
		"locator", r.Locator,
		"severity", r.Severity,
		"pass", r.Pass,
		"outcome", r.Outcome,
		"reason", r.Reason,
	)
}

func batchWire(rows []BatchRow) []any {
	out := make([]any, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Wire())
	}
	return out
}

// Anchors is the pass envelope's anchor block.
type Anchors struct {
	Path      string
	Line      string
	StartLine string
	Side      string
	Anchored  string
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	o := jq.NewObj(
		"dir", d.Dir,
		"ledger", d.Ledger,
		"sig", d.Sig,
		"locator", d.Locator,
		"severity", d.Severity,
		"body", d.Body,
	)
	d.wirePick(o)
	d.wireCounts(o)
	d.wireBatch(o)
	return o
}

// wirePick renders the pass-level keys: the brief, the anchors, and the pass
// number a single pass publishes (null on a completion).
func (d *Data) wirePick(o *jq.Obj) {
	if d.Brief == nil {
		o.Set("brief", nil)
	} else {
		o.Set("brief", d.Brief.Wire())
	}
	o.Set("anchors", wireAnchors(d.Anchors))
	o.Set("pass", d.Pass)
}

func wireAnchors(a *Anchors) any {
	if a == nil {
		return nil
	}
	return jq.NewObj(
		"path", a.Path,
		"line", a.Line,
		"start_line", a.StartLine,
		"side", a.Side,
		"anchored", a.Anchored,
	)
}

// wireCounts renders the ceilings, the tallies, and the keys a batch adds.
func (d *Data) wireCounts(o *jq.Obj) {
	o.Set("max_passes", d.MaxPasses)
	o.Set("kb", d.KB)
	o.Set("open_inline", d.OpenInline)
	o.Set("fixed", d.Fixed)
	o.Set("open", d.Open)
	o.Set("stopped", d.Stopped)
	o.Set("complete", d.Complete)
	setString(o, "min_severity", d.MinSeverity)
	setString(o, "verify_cmd", d.VerifyCmd)
	setString(o, "patch", d.Patch)
	if len(d.Paths) > 0 {
		o.Set("paths", core.WireStrings(d.Paths))
	}
	setString(o, "selection", d.Selection)
	if d.TestPath != nil {
		o.Set("test_path", *d.TestPath)
		o.Set("committed", d.Committed)
	}
	setString(o, "recipe", d.Recipe)
}

// setString publishes an optional string key; absence is the contract for a
// nil pointer, not a null value.
func setString(o *jq.Obj, key string, val *string) {
	if val != nil {
		o.Set(key, *val)
	}
}

// wireBatch renders the rows a batch names, absent on a single pass.
func (d *Data) wireBatch(o *jq.Obj) {
	if d.Batch != nil {
		o.Set("batch", batchWire(d.Batch))
	}
}

type state struct { // fix service state is allocated once per invocation
	req       *Request
	warnings  []string
	at        string
	next      string
	data      *Data
	patch     *string
	selection *selection
	dir       string
	// recipeLenses is the lens set --recipe resolved from the bundle's base
	// snapshot; recipeEmpty marks a recipe that owns no lens, so the batch
	// reports the empty scope instead of widening to every open finding.
	recipeLenses []string
	recipeEmpty  bool
}

// Run executes one fix pass, one batch pass, or reports completion.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if msg := req.comboFault(); msg != "" {
		return nil, &core.RefusalError{Code: core.CodeBadFlags, Message: core.Msgf("%s", msg)}
	}
	st := &state{req: req}
	if err := ctx.Err(); err != nil {
		return nil, st.refuse("canceled", "canceled before fix completed")
	}
	if ref := st.runFix(ctx); ref != nil {
		ref.Warnings = st.warnings
		return nil, ref
	}
	return st.response(), nil
}

func (r *Request) comboFault() string {
	if msg := r.modeFault(); msg != "" {
		return msg
	}
	if r.Commit && r.Test == "" {
		return "--commit commits the promoted regression test: pass it with --test"
	}
	if r.All || r.Selected || r.Recipe != "" {
		return ""
	}
	switch {
	case r.MinSeverity != "":
		return "--min-severity narrows a batch: pass it with --all, --selected, or --recipe"
	case r.VerifyCmd != "":
		return "--verify-cmd proves a batch: pass it with --all, --selected, or --recipe"
	case len(r.Paths) > 0:
		return "--paths scopes a batch: pass it with --all, --selected, or --recipe"
	}
	return ""
}

// modeFault refuses a pick that names two scopes at once, and an explicitly
// empty --test or --recipe, which would otherwise fall through to an
// ordinary fix pass.
func (r *Request) modeFault() string {
	if r.TestSet && r.Test == "" {
		return "fix --test needs a sig (an empty value would run an ordinary pass): " +
			"pass --test <sig>"
	}
	if msg := r.recipeModeFault(); msg != "" {
		return msg
	}
	switch {
	case r.All && r.Sig != "":
		return "fix takes --sig or --all, not both (--all selects every open inline finding)"
	case r.Selected && r.Sig != "":
		return "fix takes --sig or --selected, not both (--selected addresses the sigs a human reacted 👍 to)"
	case r.All && r.Selected:
		return "fix takes --all or --selected, not both (--selected is a batch scoped by fetch's selected.json)"
	}
	return r.testModeFault()
}

// recipeModeFault refuses a --recipe pick that is empty or names another scope.
func (r *Request) recipeModeFault() string {
	if r.RecipeSet && r.Recipe == "" {
		return "fix --recipe needs a name (an empty value would run an ordinary pass): " +
			"pass --recipe <name>"
	}
	switch {
	case r.Recipe != "" && r.Sig != "":
		return "fix takes --sig or --recipe, not both (--recipe selects the findings a named recipe owns)"
	case r.Recipe != "" && r.Test != "":
		return "fix takes --test or --recipe, not both (--recipe selects the findings a named recipe owns)"
	}
	return ""
}

// testModeFault refuses a --test pick that names another scope.
func (r *Request) testModeFault() string {
	switch {
	case r.Test != "" && r.All:
		return "fix takes --test or --all, not both (--test promotes one reproduced finding's artifact)"
	case r.Test != "" && r.Selected:
		return "fix takes --test or --selected, not both (--test promotes one reproduced finding's artifact)"
	case r.Test != "" && r.Sig != "":
		return "fix takes --sig or --test, not both (--test promotes one reproduced finding's artifact)"
	}
	return ""
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: core.Msgf("%s", msg), Warnings: st.warnings}
}

func (st *state) humanf(format string, args ...any) {
	if st.req.Human != nil {
		fmt.Fprintf(st.req.Human, format, args...)
	}
}

// progressf writes one `»` progress line, each value folded to that line by
// core.Msgf: a path or a sig cannot start a line of its own.
func (st *state) progressf(format string, args ...any) {
	if st.req.Human != nil {
		fmt.Fprint(st.req.Human, core.Msgf(format, args...))
	}
}

func (st *state) humanRaw(text string) {
	st.humanf("%s", text)
}

func (st *state) response() *Response {
	return &Response{
		At:       st.at,
		Next:     st.next,
		Data:     st.data,
		Warnings: st.warnings,
	}
}

func (st *state) runFix(ctx context.Context) *core.RefusalError {
	dir, ref := st.fixBundleDir()
	if ref != nil {
		return ref
	}
	st.dir = dir
	if ref := st.bundleGates(ctx, dir); ref != nil {
		return ref
	}
	entries, ref := st.loadFindings(dir)
	if ref != nil {
		return ref
	}
	now, fixOpen, fixOpenInline := st.printReadiness(dir, entries)
	ledgerPath := dir + suffixFixJSON
	store := st.loadLedger(ledgerPath)
	return st.runDispatch(ctx, dir, ledgerPath, store, entries,
		fixOpen, fixOpenInline, now)
}

// runDispatch routes a loaded bundle to promotion, batch, or single pass.
func (st *state) runDispatch(
	ctx context.Context,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	if st.req.Test != "" {
		return st.runPromotion(ctx, dir, ledgerPath, store, entries,
			fixOpen, fixOpenInline)
	}
	if st.req.All || st.req.Selected || st.req.Recipe != "" {
		return st.runBatch(ctx, dir, ledgerPath, store, entries,
			fixOpen, fixOpenInline, now)
	}
	full, rec, ref := st.selectRec(entries, fixOpenInline, dir)
	if ref != nil {
		return ref
	}
	if rec == nil {
		return st.completeRun(dir, ledgerPath, store, entries, fixOpen)
	}
	return st.runPass(ctx, dir, ledgerPath, store, entries, rec, full,
		fixOpen, fixOpenInline, now)
}

// fixBundleDir resolves the bundle dir. Local and PR bundles both run: fix
// only writes its ledger and patch beside the bundle and issues read-only git
// commands, and the instructions name the landing verb each bundle owns. Corpus
// bundles refuse: they are benchmark measurement inputs, not review worktrees.
func (st *state) fixBundleDir() (string, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if ref != nil {
		return "", st.refuse(ref.Code, ref.Message)
	}
	if readcommon.IsCorpusBundleDir(dir) {
		return "", st.refuse("not-local", "fix does not run on corpus bundles (benchmark inputs)")
	}
	return dir, nil
}

func (st *state) bundleGates(ctx context.Context, dir string) *core.RefusalError {
	if ref := st.checkPrGate(dir); ref != nil {
		return ref
	}
	if ref := st.checkTreeGate(ctx); ref != nil {
		return ref
	}
	return st.checkHeadGate(ctx, dir)
}

// checkHeadGate binds a PR bundle's fix to its reviewed head. The anchors, the
// patch diff, the ledger baseline, and `--test --commit` all act on the
// operator's checkout, so a checkout on another commit (main, a stale branch)
// would diff, record, and commit against the wrong tree. Local bundles review
// the working tree itself and the loop may commit between passes, so they skip.
func (st *state) checkHeadGate(ctx context.Context, dir string) *core.RefusalError {
	if !st.providerBundle() {
		return nil
	}
	reviewed := readcommon.OpenBundle(dir).Head()
	if reviewed == "" {
		return st.refuse(codeHeadMismatch, fmt.Sprintf(
			"bundle %s carries no reviewed head to bind fix to (re-fetch it)", dir))
	}
	if st.req.Commander == nil {
		return st.refuse("not-a-repo", "fix runs inside a git worktree (audits files vs anchor)")
	}
	head, exit, err := readcommon.CheckoutHead(ctx, st.req.Commander, st.req.RepoRootPath())
	if err != nil || exit != 0 {
		return st.refuse("not-a-repo", "fix runs inside a git worktree (audits files vs anchor)")
	}
	if head != reviewed {
		return st.refuse(codeHeadMismatch, fmt.Sprintf(
			"checked-out head %s is not the head %s this bundle reviewed — fix patches the "+
				"checkout, so check out the PR head (git checkout %s) and re-run",
			readcommon.ShortSHA(head), readcommon.ShortSHA(reviewed), reviewed))
	}
	return nil
}

func (st *state) checkPrGate(dir string) *core.RefusalError {
	if !readcommon.IsFile(dir + suffixPRJSON) {
		return nil
	}
	state := readcommon.PrField(dir, "state")
	if state == prClosed || state == prMerged {
		return st.refuse("pr-not-open", fmt.Sprintf(
			"bundle PR is %s — refusing fix (re-fetch if bundle stale)", state))
	}
	return nil
}

func (st *state) checkTreeGate(ctx context.Context) *core.RefusalError {
	out, err := st.gitLsFilesUnmerged(ctx)
	if err != nil {
		return st.refuse("not-a-repo", "fix runs inside a git worktree (audits files vs anchor)")
	}
	if out != "" {
		return st.refuse("conflicted-tree", "worktree holds unresolved merge conflicts "+
			"(git ls-files -u non-empty) — resolve or abort, then re-run fix")
	}
	return nil
}

func (st *state) loadFindings(dir string) ([]any, *core.RefusalError) {
	if !readcommon.IsFile(dir + suffixFindings) {
		return nil, st.refuse("missing-findings", fmt.Sprintf(
			"missing %s/findings.json (run %s first, then fix)", dir, st.landVerb()))
	}
	raw, err := noblock.ReadFile(dir + suffixFindings)
	if err != nil {
		return nil, st.corruptStore(dir)
	}
	entries, err := decodeEntries(raw)
	if err != nil {
		return nil, st.corruptStore(dir)
	}
	return entries, nil
}

func (st *state) printReadiness(dir string, entries []any) (string, int, int) {
	now := st.req.TimeUTC()
	st.at = now
	fixOpen, fixOpenInline := countOpen(entries)
	st.progressf("» readiness: %s — findings %d open (%d inline open); "+
		"threads (stored): %s; PR %s\n",
		dir, fixOpen, fixOpenInline, storedThreads(dir), prInfo(dir))
	return now, fixOpen, fixOpenInline
}

func (st *state) selectRec(entries []any, fixOpenInline int, dir string) (string, *jq.Obj, *core.RefusalError) {
	switch {
	case st.req.Sig != "":
		return st.resolveExplicit(entries)
	case fixOpenInline == 0:
		return "", nil, nil
	default:
		return st.resolveTop(entries, dir)
	}
}

func (st *state) resolveExplicit(entries []any) (string, *jq.Obj, *core.RefusalError) {
	sigFlag := st.req.Sig
	full := readcommon.ResolveSig(entries, sigFlag)
	switch full {
	case resolveNone:
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix: sig prefix '%s' not in store "+
				"(list open inline sigs with report --status open --kind inline)",
			readcommon.PrefixN(sigFlag, sigPrefixMsgLen)))
	case resolveMany:
		return "", nil, st.refuse("ambiguous-sig", fmt.Sprintf(
			"fix: sig prefix '%s' is ambiguous "+
				"(copy the full prefix from report output)",
			readcommon.PrefixN(sigFlag, sigPrefixMsgLen)))
	}
	rec := findEntry(entries, full)
	if kindOf(rec) != kindInline {
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix: sig %s is a %s finding (fix picks inline only)",
			readcommon.Prefix(full), kindOf(rec)))
	}
	if statusOf(rec) != statusOpen {
		return "", nil, st.refuse(codeBadSig, fmt.Sprintf(
			"fix: sig %s is %s (fix iterates open sigs only)",
			readcommon.Prefix(full), statusOf(rec)))
	}
	return full, rec, nil
}

func (st *state) resolveTop(entries []any, dir string) (string, *jq.Obj, *core.RefusalError) {
	full, picked := readcommon.TopOpenInline(entries)
	if full == "" {
		return "", nil, st.refuse("report-failed", fmt.Sprintf(
			"fix: open-inline count disagrees with the store "+
				"(re-run %s to rebuild %s/findings.json)", st.landVerb(), dir))
	}
	rec, ok := picked.(*jq.Obj)
	if !ok || rec == nil {
		return "", nil, st.refuse("report-failed", fmt.Sprintf(
			"fix: open-inline pick is not a finding object "+
				"(re-run %s to rebuild %s/findings.json)", st.landVerb(), dir))
	}
	return full, rec, nil
}

func (st *state) runPass(
	ctx context.Context,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	rec *jq.Obj,
	full string,
	fixOpen, fixOpenInline int,
	now string,
) *core.RefusalError {
	passes := ledgerPasses(store, full)
	if passes >= maxPasses {
		return st.ceilingStop(dir, ledgerPath, store, entries, full,
			passes, fixOpen, now)
	}
	anchor := interpOf(rec, keyPath)
	if anchor == "" {
		return st.refuse(codeBadSig, fmt.Sprintf(
			"fix: sig %s carries no anchor path (cannot scope the pass)",
			readcommon.Prefix(full)))
	}
	if passes >= 1 {
		if escapes := st.scopeEscape(ctx, store, full, anchor); len(escapes) > 0 {
			return st.scopeStop(dir, ledgerPath, store, entries, full,
				escapes, anchor, fixOpen, now)
		}
	}
	patch, ref := st.writePatch(ctx, dir)
	if ref != nil {
		return ref
	}
	st.patch = patch
	pass := passes + 1
	store = setPass(store, full, pass, now, st.worktreeFiles(ctx))
	if ref := writeLedger(ledgerPath, store); ref != nil {
		return ref
	}
	return st.passOutput(dir, ledgerPath, store, entries, rec, full,
		anchor, pass, fixOpen, fixOpenInline)
}

func (st *state) corruptStore(dir string) *core.RefusalError {
	return st.refuse("corrupt-store", fmt.Sprintf(
		"findings store %s/findings.json is unreadable or not a "+
			"findings array (re-run %s to rebuild, or restore the file)",
		dir, st.landVerb()))
}

func decodeEntries(raw []byte) ([]any, error) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, jq.Sealed(opDecodeFindings, err)
	}
	arr, ok := doc.([]any)
	if !ok {
		return nil, errNotArray
	}
	return arr, nil
}

func prInfo(dir string) string {
	if !readcommon.IsFile(dir + suffixPRJSON) {
		return "n/a (local bundle, PR check skipped)"
	}
	info := "state " + readcommon.PrField(dir, "state")
	if draftOf(dir) {
		info += " draft"
	}
	return info
}

func draftOf(dir string) bool {
	raw, err := noblock.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return false
	}
	return jq.R(m[fieldDraft]) == litTrue
}

func (st *state) gitLsFilesUnmerged(ctx context.Context) (string, error) {
	res, err := st.runGit(ctx, "ls-files", "-u")
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// runGit runs one git subcommand against the repository root, hook-free and
// fsmonitor-free, with no external diff or textconv driver ([gitcmd.Argv]).
// Clean and process filters stay on: status, the worktree diff, and add must
// hash a file as git itself does, or an LFS-tracked file would read as
// changed and be committed as its raw bytes. Either is a driver the operator
// configured, run locally on the operator's own checkout.
func (st *state) runGit(ctx context.Context, args ...string) (core.Result, error) {
	if st.req.Commander == nil {
		return core.Result{}, errNoCommander
	}
	argv := gitcmd.Argv(st.req.RepoRootPath(), args...)
	res, err := st.req.Commander.Run(ctx, &core.Command{Argv: argv, Env: gitcmd.Env(nil)})
	if err != nil {
		//nolint:wrapcheck // the commander error is only a non-zero signal; the refusal carries the contract
		return core.Result{}, err
	}
	if res.ExitCode != 0 {
		return core.Result{}, res.ExitFailure(argv[0]) //nolint:wrapcheck // locally constructed exit-status error
	}
	return res, nil
}

func storedThreads(dir string) string {
	nr := jsonLen(dir + suffixReviews)
	ni := jsonLen(dir + suffixInline)
	if !readcommon.Exists(dir+suffixReviews) && !readcommon.Exists(dir+suffixInline) {
		return "none stored"
	}
	return fmt.Sprintf("reviews.json=%d inline.json=%d", nr, ni)
}

func jsonLen(path string) int {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return 0
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return 0
	}
	arr, ok := doc.([]any)
	if !ok {
		return 0
	}
	return len(arr)
}

func findEntry(entries []any, sig string) *jq.Obj {
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sv, _ := m.Get(keySig)
		if s, ok := sv.(string); ok && s == sig {
			return m
		}
	}
	return nil
}

func statusOf(m *jq.Obj) string {
	if m == nil {
		return ""
	}
	return jq.Interp(mustGet(m, fieldStatus))
}

func kindOf(m *jq.Obj) string {
	if m == nil {
		return ""
	}
	return jq.Interp(mustGet(m, fieldKind))
}

func mustGet(m *jq.Obj, key string) any {
	v, _ := m.Get(key)
	return v
}

func interpOf(m *jq.Obj, key string) string {
	if m == nil {
		return ""
	}
	return jq.Interp(mustGet(m, key))
}

func countOpen(entries []any) (open, inline int) {
	for _, e := range entries {
		isOpen, isInline := openFlags(e)
		if isOpen {
			open++
		}
		if isInline {
			inline++
		}
	}
	return open, inline
}

func openFlags(e any) (bool, bool) {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false, false
	}
	if statusOf(m) != statusOpen {
		return false, false
	}
	return true, kindOf(m) == kindInline
}

type ledgerDoc struct {
	obj *jq.Obj
}

func (st *state) loadLedger(path string) *ledgerDoc {
	if !readcommon.Exists(path) {
		return freshLedger()
	}
	raw, err := noblock.ReadFile(path)
	if err == nil {
		if doc, ok := decodeLedgerDoc(raw); ok {
			return st.ledgerWithSigs(path, doc)
		}
	}
	st.warn(fmt.Sprintf(msgLedgerUnreadable, path))
	return freshLedger()
}

func decodeLedgerDoc(raw []byte) (*jq.Obj, bool) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return nil, false
	}
	return m, true
}

func (st *state) ledgerWithSigs(path string, m *jq.Obj) *ledgerDoc {
	sigs := jq.NewObj()
	sigsRaw, hasSigs := m.Get(keySigs)
	sm, ok := sigsRaw.(*jq.Obj)
	switch {
	case ok && sm != nil:
		sigs = sm
	case hasSigs && sigsRaw != nil:
		st.warn(fmt.Sprintf(msgLedgerUnreadable, path))
	default:
	}
	return &ledgerDoc{obj: jq.NewObj(keySigs, sigs)}
}

func freshLedger() *ledgerDoc {
	return &ledgerDoc{obj: jq.NewObj(keySigs, jq.NewObj())}
}

func writeLedger(path string, store *ledgerDoc) *core.RefusalError {
	if err := atomic.WriteFile(path, []byte(store.obj.MarshalCompact()+"\n")); err != nil {
		return &core.RefusalError{
			Code:    codeFixFailed,
			Message: core.Msgf("could not write %s: %v", path, err),
		}
	}
	return nil
}

func sigStore(store *ledgerDoc, sig string) *jq.Obj {
	sigs, _ := store.obj.Get(keySigs)
	sm, ok := sigs.(*jq.Obj)
	if !ok || sm == nil {
		return jq.NewObj()
	}
	raw, _ := sm.Get(sig)
	m, ok := raw.(*jq.Obj)
	if !ok || m == nil {
		return jq.NewObj()
	}
	return m
}

func ledgerPasses(store *ledgerDoc, sig string) int {
	v, _ := sigStore(store, sig).Get(keyPasses)
	if n, ok := jq.IntOf(v); ok {
		return n
	}
	return 0
}

func putSig(store *ledgerDoc, sig string, row *jq.Obj) {
	sigs, _ := store.obj.Get(keySigs)
	sm, ok := sigs.(*jq.Obj)
	if !ok || sm == nil {
		sm = jq.NewObj()
		store.obj.Set(keySigs, sm)
	}
	sm.Set(sig, row)
}

func stoppedCount(store *ledgerDoc) int {
	n := 0
	sigs, _ := store.obj.Get(keySigs)
	sm, ok := sigs.(*jq.Obj)
	if !ok || sm == nil {
		return 0
	}
	for _, k := range sm.Keys {
		raw, _ := sm.Get(k)
		if isStoppedRow(raw) {
			n++
		}
	}
	return n
}

func isStoppedRow(raw any) bool {
	m, ok := raw.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	v, _ := m.Get(keyStopped)
	return v != nil
}

func fixedCount(entries []any) int {
	n := 0
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		if statusOf(m) == statusAddressed {
			n++
		}
	}
	return n
}

func (st *state) ceilingStop(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	full string,
	passes, fixOpen int,
	now string,
) *core.RefusalError {
	tombstone(store, full, stoppedCeiling, passes, now)
	if ref := writeLedger(ledgerPath, store); ref != nil {
		return ref
	}
	st.humanf(fmtFixComplete,
		dir, fixedCount(entries), fixOpen, stoppedCount(store), ledgerPath)
	st.humanf("next: sig %s hit the 3-pass ceiling — triage it or split the fix\n",
		readcommon.Prefix(full))
	return st.refuse("fix-ceiling", fmt.Sprintf(
		"fix: sig %s already used %d/3 passes — ceiling reached "+
			"(stopped: ceiling)", readcommon.Prefix(full), passes))
}

func ensurePasses(row *jq.Obj, passes int) {
	p, _ := row.Get(keyPasses)
	if _, ok := numOf(p); !ok {
		row.Set(keyPasses, passes)
	}
}

func tombstone(store *ledgerDoc, full, reason string, passes int, now string) {
	row := sigStore(store, full)
	ensurePasses(row, passes)
	row.Set(keyLastAt, now)
	if v, _ := row.Get(keyStopped); v == nil {
		row.Set(keyStopped, reason)
	}
	putSig(store, full, row)
}

func numOf(v any) (int, bool) {
	return jq.IntOf(v)
}

func (st *state) scopeEscape(
	ctx context.Context,
	store *ledgerDoc,
	sig, anchor string,
) []string {
	return escapePaths(st.worktreeFiles(ctx), baselineOf(store, sig), anchor)
}

func escapePaths(work, baseline []string, anchor string) []string {
	baseSet := newPathSet(baseline)
	var out []string
	for _, p := range work {
		if isEscape(p, anchor, baseSet) {
			out = append(out, p)
		}
	}
	return out
}

func newPathSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set
}

func isEscape(p, anchor string, baseSet map[string]bool) bool {
	if baseSet[p] {
		return false
	}
	if p == collapsedState || strings.HasPrefix(p, prefixState) {
		return false
	}
	return p != anchor
}

func baselineOf(store *ledgerDoc, sig string) []string {
	old, _ := baselineKept(store, sig)
	return old
}

func baselineKept(store *ledgerDoc, sig string) ([]string, bool) {
	v, _ := sigStore(store, sig).Get(keyBaseline)
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

func (st *state) scopeStop(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	full string,
	escapes []string,
	anchor string,
	fixOpen int,
	now string,
) *core.RefusalError {
	tombstone(store, full, codeScopeEscape, 1, now)
	if ref := writeLedger(ledgerPath, store); ref != nil {
		return ref
	}
	first, n := escapes[0], len(escapes)
	st.humanf(fmtFixComplete,
		dir, fixedCount(entries), fixOpen, stoppedCount(store), ledgerPath)
	st.humanf("next: worktree escaped the anchor — revert or stash files "+
		"outside '%s', then re-run fix\n", anchor)
	if n > 1 {
		return st.refuse(codeScopeEscape, fmt.Sprintf(
			"fix: worktree touches '%s' and %d more outside anchor '%s' "+
				"— revert or stash them, then re-run fix", first, n-1, anchor))
	}
	return st.refuse(codeScopeEscape, fmt.Sprintf(
		"fix: worktree touches '%s' outside anchor '%s' "+
			"— revert or stash it, then re-run fix", first, anchor))
}

func setPass(
	store *ledgerDoc,
	sig string,
	pass int,
	now string,
	base []string,
) *ledgerDoc {
	row := jq.NewObj(keyPasses, pass, keyLastAt, now)
	if old, ok := baselineKept(store, sig); ok {
		row.Set(keyBaseline, stringsOf(old))
	} else {
		row.Set(keyBaseline, stringsOf(base))
	}
	putSig(store, sig, row)
	return store
}

func stringsOf(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func (st *state) worktreeFiles(ctx context.Context) []string {
	res, err := st.runGit(ctx, "status", "--porcelain", "-z")
	if err != nil {
		return nil
	}
	recs := strings.Split(res.Stdout, sepNul)
	paths := parsePorcelain(recs)
	slices.Sort(paths)
	return slices.Compact(paths)
}

func parsePorcelain(recs []string) []string {
	var paths []string
	for i := 0; i < len(recs); {
		primary, extra, next := porcelainEntry(recs, i)
		if primary != "" {
			paths = append(paths, primary)
		}
		if extra != "" {
			paths = append(paths, extra)
		}
		i = next
	}
	return paths
}

func porcelainEntry(recs []string, i int) (string, string, int) {
	if i >= len(recs) {
		return "", "", i + 1
	}
	rec := recs[i]
	if rec == "" {
		return "", "", i + 1
	}
	path, ok := porcelainPath(rec)
	if !ok {
		return "", "", i + 1
	}
	if isRename(rec) && i+1 < len(recs) && recs[i+1] != "" {
		return path, recs[i+1], i + 2
	}
	return path, "", i + 1
}

func porcelainPath(rec string) (string, bool) {
	if len(rec) < porcelainPathOff {
		return "", false
	}
	return rec[porcelainPathOff:], true
}

func isRename(rec string) bool {
	if len(rec) < porcelainStatusLen {
		return false
	}
	st := rec[:porcelainStatusLen]
	return strings.HasPrefix(st, statusRename) ||
		strings.HasPrefix(st, statusCopy)
}

func (st *state) passOutput(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	rec *jq.Obj,
	full, anchor string,
	pass, fixOpen, fixOpenInline int,
) *core.RefusalError {
	st.progressf("» ledger: %s — sig %s pass %d/3\n",
		ledgerPath, readcommon.Prefix(full), pass)
	block := readPassBlock(rec, full, anchor)
	st.printPassBlock(block, pass, ledgerPath)
	kbVal := readKbValue(dir)
	st.printKB(dir)
	st.printInstructions(dir, anchor)
	st.next = nextPass
	if st.providerBundle() {
		st.next = nextPassPR
	}
	st.data = &Data{
		Dir:        dir,
		Ledger:     ledgerPath,
		Sig:        &full,
		Locator:    &block.locator,
		Severity:   &block.severity,
		Body:       &block.body,
		Brief:      block.brief,
		Anchors:    block.anchors,
		Pass:       &pass,
		MaxPasses:  maxPasses,
		KB:         kbVal,
		OpenInline: fixOpenInline,
		Fixed:      fixedCount(entries),
		Open:       fixOpen,
		Stopped:    stoppedCount(store),
		Complete:   false,
		Patch:      st.patch,
	}
	return nil
}

type passBlock struct { //nolint:govet // field order mirrors the printed block
	full     string
	locator  string
	severity string
	body     string
	anchors  *Anchors
	brief    *core.Brief
}

func readPassBlock(rec *jq.Obj, full, anchor string) *passBlock {
	severity := interpOf(rec, keySeverity)
	if severity == "" {
		severity = sevUntagged
	}
	side := interpOf(rec, keySide)
	if side == "" {
		side = sideRight
	}
	line := interpOf(rec, keyLine)
	if line == "" {
		line = litUnknown
	}
	start := interpOf(rec, keyStartLine)
	if start == "" {
		start = line
	}
	return &passBlock{
		full:     full,
		locator:  locatorOf(rec),
		severity: severity,
		body:     interpOf(rec, keyBody),
		anchors:  &Anchors{Path: anchor, Line: line, StartLine: start, Side: side, Anchored: interpOf(rec, keyAnchored)},
		brief:    brief.FromRecord(rec),
	}
}

func (st *state) printPassBlock(block *passBlock, pass int, ledgerPath string) {
	st.humanf("fix: %s [%s] %s (pass %d/3, ledger %s)\n",
		readcommon.Prefix(block.full), block.severity, block.locator, pass, ledgerPath)
	st.humanf("finding: %s\n", block.body)
	st.humanf("anchors: %s:%s-%s side=%s anchored=%s\n",
		block.anchors.Path, block.anchors.StartLine, block.anchors.Line,
		block.anchors.Side, block.anchors.Anchored)
	printBrief(st, block.brief)
}

func readKbValue(dir string) any {
	if !readcommon.IsFile(dir + suffixKB) {
		return nil
	}
	kbRaw, err := noblock.ReadFile(dir + suffixKB)
	if err != nil {
		kbRaw = nil
	}
	trimmed := strings.TrimRight(string(kbRaw), "\n")
	return jq.NewObj(keyFile, dir+suffixKB, keyContent, trimmed)
}

func (st *state) printKB(dir string) {
	if !readcommon.IsFile(dir + suffixKB) {
		return
	}
	kbRaw, err := noblock.ReadFile(dir + suffixKB)
	if err != nil {
		kbRaw = nil
	}
	st.humanf("--- kb.txt (%s/kb.txt, full context) ---\n", dir)
	st.humanRaw(string(kbRaw))
	st.humanf("--- end kb.txt ---\n")
}

func (st *state) printInstructions(dir, anchor string) {
	if st.providerBundle() {
		st.humanf(instructionsPRFmt, anchor, st.req.Repo, st.req.Pr, dir,
			st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr)
	} else {
		st.humanf(instructionsFmt,
			anchor, dir, st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr)
	}
	st.humanRaw(gateMsg)
	st.humanRaw(budgetMsg)
	if st.providerBundle() {
		st.humanf("next: patch %s, commit and push the fix, re-fetch, run verdict verify, write verification.json, "+
			"run post to land it, then re-run fix\n", anchor)
		return
	}
	st.humanf("next: patch %s, re-run fetch-local, run verdict verify, write verification.json, "+
		"run record to land it, then re-run fix\n", anchor)
}

// printBrief writes the stored brief between the anchor line and the kb
// recall: one block per finding, and nothing at all when none is stored.
func printBrief(st *state, b *core.Brief) {
	if text := brief.Render(b); text != "" {
		st.humanRaw(text)
	}
}

func locatorOf(m *jq.Obj) string {
	pv, _ := m.Get(keyPath)
	if jq.Falsey(pv) {
		hv, _ := m.Get(keyHeading)
		return jq.Interp(jq.Alt(hv, litUnknownPath))
	}
	locator := jq.Interp(pv)
	lv, _ := m.Get(keyLine)
	if !jq.Falsey(lv) {
		locator += ":" + jq.Interp(lv)
	}
	return locator
}

func (st *state) completeRun(
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen int,
) *core.RefusalError {
	fixed := fixedCount(entries)
	stopped := stoppedCount(store)
	st.humanf(fmtFixComplete, dir, fixed, fixOpen, stopped, ledgerPath)
	next := nextDone
	if fixOpen > 0 {
		next = fmt.Sprintf(
			"next: %d open finding(s) left are non-inline or "+
				"dispositioned — triage or %s to settle them", fixOpen, st.landVerb())
	}
	st.humanf("%s\n", next)
	st.next = strings.ReplaceAll(nextTriage, "record", st.landVerb())
	if fixOpen == 0 {
		st.next = nextDoneShort
	}
	st.data = &Data{
		Dir:        dir,
		Ledger:     ledgerPath,
		MaxPasses:  maxPasses,
		OpenInline: 0,
		Fixed:      fixed,
		Open:       fixOpen,
		Stopped:    stopped,
		Complete:   true,
	}
	return nil
}

const (
	// maxPasses is the hard-coded 3-pass ceiling per sig.
	maxPasses = 3
	// sigPrefixMsgLen is the prefix width shown in sig error messages.
	sigPrefixMsgLen = 12
	// porcelainPathOff is the byte offset of the path in porcelain -z records.
	porcelainPathOff = 3
	// porcelainStatusLen is the status field width in porcelain records.
	porcelainStatusLen = 2
)

const (
	// codeBadSig is the refusal code for unknown or non-inline sigs.
	codeBadSig = "bad-sig"
	// codeScopeEscape is the refusal code for anchor scope escapes.
	codeScopeEscape = "scope-escape"
	codeNoVerifyCmd = "no-verify-cmd"
	// kindInline matches inline findings only.
	kindInline = "inline"
	// statusOpen matches open findings only.
	statusOpen = "open"
	// statusAddressed counts fixed findings.
	statusAddressed = "addressed"
	// keySigs is the ledger sigs table field.
	keySigs = "sigs"
	// keySig is the finding sig field.
	keySig = "sig"
	// keyPasses is the ledger pass-count field.
	keyPasses = "passes"
	// keyStopped is the ledger tombstone field.
	keyStopped = "stopped"
	// keyPath is the anchor path field.
	keyPath = "path"
	// keyLine is the anchor line field.
	keyLine = "line"
	// keySeverity is the finding severity field.
	keySeverity = "severity"
	// keyBody is the finding body field.
	keyBody = "body"
	// keyBaseline is the ledger baseline field.
	keyBaseline = "baseline"
	// keyLastAt is the ledger timestamp field.
	keyLastAt = "last_at"
	// keySide is the anchor side field.
	keySide = "side"
	// keyStartLine is the anchor start-line field.
	keyStartLine = "start_line"
	// keyAnchored is the anchor anchored-flag field.
	keyAnchored  = "anchored"
	keyVerifyCmd = "verifyCmd"
	// keyHeading is the section heading fallback field.
	keyHeading = "heading"
	// keyFile is the kb file field.
	keyFile = "file"
	// keyContent is the kb content field.
	keyContent = "content"
	// fieldStatus is the entry status field.
	fieldStatus = "status"
	// fieldKind is the entry kind field.
	fieldKind = "kind"
	// fieldDraft is the PR draft field.
	fieldDraft = "draft"
)

const (
	// suffixPRJSON is the bundle PR file suffix.
	suffixPRJSON = "/PR.json"
	// suffixFindings is the bundle findings file suffix.
	suffixFindings = "/findings.json"
	// suffixFixJSON is the ledger file suffix.
	suffixFixJSON  = "/fix.json"
	suffixFixPatch = "/fix.patch"
	// suffixPatchJSON is the bundle patch claim file suffix.
	suffixPatchJSON = "/patch.json"
	// suffixSelected is the fetch-written reaction selection suffix.
	suffixSelected = "/selected.json"
	// suffixKB is the kb recall file suffix.
	suffixKB = "/kb.txt"
	// suffixReviews is the stored reviews file suffix.
	suffixReviews = "/reviews.json"
	// suffixInline is the stored inline file suffix.
	suffixInline = "/inline.json"
	// prefixState filters the generated bundle state tree from scope
	// audits: run data never counts as fix scope.
	prefixState = ".verdict/state/"
	// collapsedState is the single entry git prints for an untracked state
	// tree: porcelain's default untracked mode reports the directory, not
	// each file under it, so prefixState alone never matches. Exempting it
	// keeps the tool's own run data out of the escape list without hiding
	// tracked controls like .verdict/kb.jsonl, which git always lists by
	// name.
	collapsedState = ".verdict/"
	// resolveNone reports no sig match.
	resolveNone = "NONE"
	// resolveMany reports an ambiguous sig prefix.
	resolveMany = "MANY"
	// prClosed refuses closed bundle PRs.
	prClosed = "closed"
	// prMerged refuses merged bundle PRs.
	prMerged = "merged"
	// litTrue is the true literal.
	litTrue = "true"
	// litUnknown renders missing line numbers.
	litUnknown = "?"
	// litUnknownPath renders missing path locators.
	litUnknownPath = "?"
	// sepNul splits porcelain -z records.
	sepNul = "\x00"
	// statusRename marks renamed porcelain entries.
	statusRename = "R"
	// statusCopy marks copied porcelain entries.
	statusCopy = "C"
	// sevUntagged renders missing severities.
	sevUntagged = "untagged"
	// sideRight defaults missing sides.
	sideRight = "RIGHT"
	// stoppedCeiling tombstones ceiling stops.
	stoppedCeiling = "ceiling"
	// nextPass is the envelope next hint for passes.
	nextPass = "patch the anchor file, re-run fetch-local, run verdict verify, write verification.json, " +
		"run record to land it, then re-run fix"
	// nextPassPR is nextPass for a PR bundle, which lands through post.
	nextPassPR = "patch the anchor file, commit and push the fix, re-fetch, run verdict verify, " +
		"write verification.json, and run post to land it, then re-run fix"
	// nextDone is the human next hint when done.
	nextDone = "next: done — no open findings"
	// nextTriage is the envelope next hint when open remains.
	nextTriage = "triage or record to settle the remaining open findings"
	// nextDoneShort is the envelope next hint when done.
	nextDoneShort = "done — no open findings"
)

const (
	// msgLedgerUnreadable warns when the ledger reads fresh.
	msgLedgerUnreadable = "fix ledger %s unreadable — starting fresh " +
		"(prior pass counts lost)"
	// fmtFixComplete renders the fix-complete summary line.
	fmtFixComplete = "fix complete: %s — fixed=%d open=%d stopped=%d (ledger %s)\n"
	// opDecodeFindings names the findings decode in sealed errors.
	opDecodeFindings = "decode findings"
)

const (
	// instructionsFmt guides the next manual pass.
	instructionsFmt = "instructions: patch the anchor file (%s) — git add -N " +
		"any file the fix creates — and re-run fetch-local so the bundle " +
		"captures the patched content (no commit needed), then run its verify command (verifyCmd in %s/patch.json) " +
		"through verdict verify %s %s and write verification.json beside it " +
		"[{sig, verdict, verifyCmd}] with verdict VERIFIED and that verifyCmd " +
		"(fix itself runs nothing — a claim flips only on the verify-receipt.json " +
		"verify writes: exit 0 for that command on the content the bundle " +
		"grades); land it with record %s %s; re-run fix %s %s for the next pass\n"
	// instructionsPRFmt is instructionsFmt for a PR bundle, whose landing path
	// is a push and a re-fetch followed by post — record refuses a PR bundle.
	// verify runs after the re-fetch, so its receipt covers the head post grades.
	instructionsPRFmt = "instructions: patch the anchor file (%s), commit and " +
		"push the fix, re-fetch with fetch %s %s, then run its verify command " +
		"(verifyCmd in %s/patch.json) through verdict verify %s %s and write " +
		"verification.json beside it [{sig, verdict, verifyCmd}] with verdict " +
		"VERIFIED and that verifyCmd (fix itself runs nothing — a claim flips only " +
		"on the verify-receipt.json verify writes: exit 0 for that command on the " +
		"pushed head's content); land it with post %s %s; re-run fix %s %s for the " +
		"next pass\n"
	// gateMsg constrains verify commands to safe shapes.
	gateMsg = "gate: keep the verify command repo-scoped, non-destructive, " +
		"and offline — the verify verb refuses " +
		"destructive/networked/credential-reading/repo-escaping shapes " +
		"before running anything (typed verify-* errors with a try: hint), " +
		"so run anything stronger in your own shell — it writes no receipt, " +
		"so settle that finding with verdict triage\n"
	// budgetMsg caps review spend.
	budgetMsg = "loops multiply agent spend — cap review spend with " +
		"REVIEW_BUDGET_USD or the typed budget_usd default (see docs/manual.md)\n"
)

var errNotArray = storeError("not a findings array")

var errNoCommander = storeError("no commander injected")

type storeError string

func (e storeError) Error() string { return string(e) }
