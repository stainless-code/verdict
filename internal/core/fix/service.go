// Package fixcore is the fix verb's domain service: one pass of the bounded
// verify-gated fix loop over a local bundle. It picks the top open inline sig
// with the same eight-key ranking next and report use unless --sig names one,
// enforces the 3-pass ceiling and the scope-escape audit against the ledger
// fix.json, prints the finding context with the kb recall, and reports
// terminal completion when no open inline sig remains. Fix writes only the
// ledger; findings.json is never touched. internal/fix owns argv parsing,
// rentry, envelope rendering, and exit mapping.
package fixcore

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/statedir"
)

// prefixReview filters state-owned paths from scope audits.
func prefixReview() string { return statedir.Resolve().RelPrefix() }

// Request carries the resolved fix invocation: the shared runtime (env,
// clock, progress, commander) plus the --sig override and the adapter-routed
// human stream. Human bytes are written verbatim so the kb recall block keeps
// its exact layout.
type Request struct { //nolint:govet // the transport contract keeps its field order
	core.ReadRequest
	Sig   string
	Human io.Writer
}

// Response carries the completed pass or completion summary: the envelope
// inputs plus the accumulated warnings.
type Response struct {
	At       string
	Next     string
	Data     *Data
	Warnings []string
}

// Data is the fix envelope's data profile. The pass and completion shapes
// share one type; completion leaves the pass fields null and sets complete.
type Data struct { //nolint:govet // field order is the published property order
	Dir        string
	Ledger     string
	Sig        *string
	Locator    *string
	Severity   *string
	Body       *string
	Brief      *core.Brief
	Anchors    *Anchors
	Pass       *int
	MaxPasses  int
	KB         any
	OpenInline int
	Fixed      int
	Open       int
	Stopped    int
	Complete   bool
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
	if d.Brief == nil {
		o.Set("brief", nil)
	} else {
		o.Set("brief", d.Brief.Wire())
	}
	if d.Anchors != nil {
		o.Set("anchors", jq.NewObj(
			"path", d.Anchors.Path,
			"line", d.Anchors.Line,
			"start_line", d.Anchors.StartLine,
			"side", d.Anchors.Side,
			"anchored", d.Anchors.Anchored,
		))
	} else {
		o.Set("anchors", nil)
	}
	o.Set("pass", d.Pass)
	o.Set("max_passes", d.MaxPasses)
	o.Set("kb", d.KB)
	o.Set("open_inline", d.OpenInline)
	o.Set("fixed", d.Fixed)
	o.Set("open", d.Open)
	o.Set("stopped", d.Stopped)
	o.Set("complete", d.Complete)
	return o
}

type state struct { //nolint:govet // fix service state is allocated once per invocation
	req      *Request
	warnings []string
	at       string
	next     string
	data     *Data
}

// Run executes one fix pass (or reports completion) over a local bundle.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
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

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

func (st *state) humanf(format string, args ...any) {
	if st.req.Human != nil {
		fmt.Fprintf(st.req.Human, format, args...)
	}
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
	if ref := st.checkPrGate(dir); ref != nil {
		return ref
	}
	if ref := st.checkTreeGate(ctx); ref != nil {
		return ref
	}
	entries, ref := st.loadFindings(dir)
	if ref != nil {
		return ref
	}
	now, fixOpen, fixOpenInline := st.printReadiness(dir, entries)
	ledgerPath := dir + suffixFixJSON
	store, ref := st.loadLedger(ledgerPath)
	if ref != nil {
		return ref
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

func (st *state) fixBundleDir() (string, *core.RefusalError) {
	dir := readcommon.ResolveBundleDir(st.req.Pr)
	if !readcommon.IsLocalBundle(dir) {
		return "", st.refuse("not-local", "fix works on local bundles; fetch-local first")
	}
	return dir, nil
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
			"missing %s/findings.json (run record first, then fix)", dir))
	}
	raw, err := os.ReadFile(dir + suffixFindings)
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
	st.humanf("» readiness: %s — findings %d open (%d inline open); "+
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
				"(re-run record to rebuild %s/findings.json)", dir))
	}
	rec, ok := picked.(*jq.Obj)
	if !ok || rec == nil {
		return "", nil, st.refuse("report-failed", fmt.Sprintf(
			"fix: open-inline pick is not a finding object "+
				"(re-run record to rebuild %s/findings.json)", dir))
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
		if _, first, n := st.scopeEscape(ctx, store, full, anchor); n > 0 {
			return st.scopeStop(dir, ledgerPath, store, entries, full,
				first, n, anchor, fixOpen, now)
		}
	}
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
			"findings array (re-run record to rebuild, or restore the file)",
		dir))
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
	raw, err := os.ReadFile(dir + suffixPRJSON)
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
	res, err := st.runGit(ctx, "git", "ls-files", "-u")
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

func (st *state) runGit(ctx context.Context, argv ...string) (core.Result, error) {
	if st.req.Commander == nil {
		return core.Result{}, errNoCommander
	}
	res, err := st.req.Commander.Run(ctx, core.Command{Argv: argv})
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
	raw, err := os.ReadFile(path)
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

func (st *state) loadLedger(path string) (*ledgerDoc, *core.RefusalError) {
	if !readcommon.Exists(path) {
		return st.createLedger(path)
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		if doc, ok := decodeLedgerDoc(raw); ok {
			return st.ledgerWithSigs(path, doc), nil
		}
	}
	st.warn(fmt.Sprintf(msgLedgerUnreadable, path))
	return freshLedger(), nil
}

func (st *state) createLedger(path string) (*ledgerDoc, *core.RefusalError) {
	fresh := freshLedger()
	if ref := writeLedger(path, fresh); ref != nil {
		return nil, ref
	}
	return freshLedger(), nil
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
			Code:    "fix-failed",
			Message: fmt.Sprintf("could not write %s: %v", path, err),
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
	row := sigStore(store, full)
	ensurePasses(row, passes)
	row.Set(keyLastAt, now)
	if v, _ := row.Get(keyStopped); v == nil {
		row.Set(keyStopped, stoppedCeiling)
	}
	putSig(store, full, row)
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

func numOf(v any) (int, bool) {
	return jq.IntOf(v)
}

func (st *state) scopeEscape(
	ctx context.Context,
	store *ledgerDoc,
	sig, anchor string,
) (bool, string, int) {
	base := baselineOf(store, sig)
	work := st.worktreeFiles(ctx)
	baseSet := newPathSet(base)
	first := ""
	n := 0
	for _, p := range work {
		if isEscape(p, anchor, baseSet) {
			if first == "" {
				first = p
			}
			n++
		}
	}
	return n > 0, first, n
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
	if strings.HasPrefix(p, prefixReview()) {
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
	full, first string,
	n int,
	anchor string,
	fixOpen int,
	now string,
) *core.RefusalError {
	row := sigStore(store, full)
	p, _ := row.Get(keyPasses)
	if _, ok := numOf(p); !ok {
		row.Set(keyPasses, 1)
	}
	row.Set(keyLastAt, now)
	if v, _ := row.Get(keyStopped); v == nil {
		row.Set(keyStopped, codeScopeEscape)
	}
	putSig(store, full, row)
	if ref := writeLedger(ledgerPath, store); ref != nil {
		return ref
	}
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
	res, err := st.runGit(ctx, "git", "status", "--porcelain", "-z")
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
	st.humanf("» ledger: %s — sig %s pass %d/3\n",
		ledgerPath, readcommon.Prefix(full), pass)
	locator := locatorOf(rec)
	sev := interpOf(rec, keySeverity)
	if sev == "" {
		sev = sevUntagged
	}
	body := interpOf(rec, keyBody)
	side := interpOf(rec, keySide)
	if side == "" {
		side = sideRight
	}
	ln := interpOf(rec, keyLine)
	if ln == "" {
		ln = litUnknown
	}
	sl := interpOf(rec, keyStartLine)
	if sl == "" {
		sl = ln
	}
	an := interpOf(rec, keyAnchored)
	st.humanf("fix: %s [%s] %s (pass %d/3, ledger %s)\n",
		readcommon.Prefix(full), sev, locator, pass, ledgerPath)
	st.humanf("finding: %s\n", body)
	st.humanf("anchors: %s:%s-%s side=%s anchored=%s\n",
		anchor, sl, ln, side, an)
	machineBrief := brief.FromRecord(rec)
	printBrief(st, machineBrief)
	kbVal := readKbValue(dir)
	st.printKbAndInstructions(dir, anchor)
	st.next = nextPass
	st.data = &Data{
		Dir:        dir,
		Ledger:     ledgerPath,
		Sig:        &full,
		Locator:    &locator,
		Severity:   &sev,
		Body:       &body,
		Brief:      machineBrief,
		Anchors:    &Anchors{Path: anchor, Line: ln, StartLine: sl, Side: side, Anchored: an},
		Pass:       &pass,
		MaxPasses:  maxPasses,
		KB:         kbVal,
		OpenInline: fixOpenInline,
		Fixed:      fixedCount(entries),
		Open:       fixOpen,
		Stopped:    stoppedCount(store),
		Complete:   false,
	}
	return nil
}

func readKbValue(dir string) any {
	if !readcommon.IsFile(dir + suffixKB) {
		return nil
	}
	kbRaw, err := os.ReadFile(dir + suffixKB)
	if err != nil {
		kbRaw = nil
	}
	trimmed := strings.TrimRight(string(kbRaw), "\n")
	return jq.NewObj(keyFile, dir+suffixKB, keyContent, trimmed)
}

func (st *state) printKbAndInstructions(dir, anchor string) {
	if readcommon.IsFile(dir + suffixKB) {
		kbRaw, err := os.ReadFile(dir + suffixKB)
		if err != nil {
			kbRaw = nil
		}
		st.humanf("--- kb.txt (%s/kb.txt, full context) ---\n", dir)
		st.humanf("%s", string(kbRaw))
		st.humanf("--- end kb.txt ---\n")
	}
	st.humanf(instructionsFmt,
		anchor, dir, st.req.Repo, st.req.Pr, st.req.Repo, st.req.Pr)
	st.humanf("%s", gateMsg)
	st.humanf("%s", budgetMsg)
	st.humanf("next: patch %s, write verification.json, "+
		"run record to land it, then re-run fix\n", anchor)
}

// printBrief writes the stored brief between the anchor line and the kb
// recall: one block per finding, and nothing at all when none is stored.
func printBrief(st *state, b *core.Brief) {
	if b == nil {
		return
	}
	st.humanf("brief:\n")
	st.humanf("  outcome: %s\n", b.Outcome)
	if len(b.Sites) > 0 {
		st.humanf("  sites: %s\n", briefSites(b.Sites))
	}
	if b.Approach != "" {
		st.humanf("  approach: %s\n", b.Approach)
	}
	if len(b.Questions) == 0 {
		return
	}
	st.humanf("  questions:\n")
	for _, q := range b.Questions {
		st.humanf("    - %s\n", q)
	}
}

// briefSites renders the sites one line: path[:line] joined by commas.
func briefSites(sites []core.BriefSite) string {
	out := make([]string, 0, len(sites))
	for _, site := range sites {
		if site.Line == nil {
			out = append(out, site.Path)
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d", site.Path, *site.Line))
	}
	return strings.Join(out, ", ")
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
				"dispositioned — triage or record to settle them", fixOpen)
	}
	st.humanf("%s\n", next)
	st.next = nextTriage
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
	keyAnchored = "anchored"
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
	suffixFixJSON = "/fix.json"
	// suffixKB is the kb recall file suffix.
	suffixKB = "/kb.txt"
	// suffixReviews is the stored reviews file suffix.
	suffixReviews = "/reviews.json"
	// suffixInline is the stored inline file suffix.
	suffixInline = "/inline.json"
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
	nextPass = "patch the anchor file, write verification.json, " +
		"run record to land it, then re-run fix"
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
	instructionsFmt = "instructions: patch the anchor file (%s), " +
		"then run its verify command yourself and write %s/verification.json " +
		"[{sig, verdict, resultHead, verifyCmd}] with verdict VERIFIED and " +
		"resultHead matching HEAD (this script never runs it — the " +
		"resultHead==HEAD match in the record/post verification gate is the " +
		"freshness proof); land it with record %s %s; re-run fix %s %s " +
		"for the next pass\n"
	// gateMsg constrains verify commands to safe shapes.
	gateMsg = "gate: keep the verify command repo-scoped, non-destructive, " +
		"and offline — the verify verb refuses " +
		"destructive/networked/credential-reading/repo-escaping shapes " +
		"before running anything (typed verify-* errors with a try: hint), " +
		"so run anything stronger in your own shell instead\n"
	// budgetMsg caps review spend.
	budgetMsg = "loops multiply agent spend — cap review spend with " +
		"REVIEW_BUDGET_USD or .review/budget (see docs/manual.md)\n"
)

var errNotArray = storeError("not a findings array")

var errNoCommander = storeError("no commander injected")

type storeError string

func (e storeError) Error() string { return string(e) }
