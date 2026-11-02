// Package fix ports the `fix` verb: one pass of the bounded verify-gated
// fix loop over a LOCAL bundle. It picks the top open inline sig (severity
// critical first, path-asc tiebreak) unless --sig names one, enforces the
// 3-pass ceiling and the scope-escape audit against the ledger fix.json,
// prints the finding context with the kb recall, and reports terminal
// completion when no open inline sig remains. Fix writes only the ledger;
// findings.json is never touched.
package fix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved fix invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `fix <owner/repo> <local-slug> [--sig <s>] [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = "./scripts/review.sh fix " + req.Repo + " " + req.Pr
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if flags.sig != "" {
		r.Rentry += " --sig " + flags.sig
	}
	if flags.json {
		r.Rentry += " --json"
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{
			Code:    "canceled",
			Message: "canceled before fix completed",
		}, cli.ExitBroke)
	}
	code, err := runFix(ctx, r, req, flags)
	if err != nil {
		if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{
			Code:    "fix-failed",
			Message: fmt.Sprintf("fix failed: %v", err),
		})
	}
	return code
}

// flags carries the parsed fix options.
type flags struct {
	sig  string
	json bool
}

// parseFlags strips fix's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--sig":
			if i+1 < len(args) {
				i++
				f.sig = args[i]
			} else {
				f.sig = ""
			}
		case "--json":
			f.json = true
		default:
			msg := fmt.Sprintf(
				"unknown flag '%s' for fix (want --sig <sig>, --json)", a)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// runFix mirrors the fix block: start gates, readiness, the ledger, sig
// selection, ceiling and scope audits, then the pass output.
func runFix(
	ctx context.Context,
	r *cli.Runner,
	req Request,
	f flags,
) (int, error) {
	dir, refusal := fixBundleDir(req)
	if refusal != nil {
		return 0, refusal
	}
	if refusal := checkPrGate(dir); refusal != nil {
		return 0, refusal
	}
	if refusal := checkTreeGate(ctx); refusal != nil {
		return 0, refusal
	}
	entries, refusal := loadFindings(dir)
	if refusal != nil {
		return 0, refusal
	}
	now, fixOpen, fixOpenInline := printReadiness(r, dir, entries)
	ledgerPath := dir + suffixFixJSON
	store, err := loadLedger(r, ledgerPath)
	if err != nil {
		return 0, err
	}
	full, rec, refusal := selectRec(entries, f.sig, fixOpenInline, dir)
	if refusal != nil {
		return 0, refusal
	}
	if rec == nil {
		return completeRun(r, dir, ledgerPath, store, entries, fixOpen, now)
	}
	return runPass(ctx, r, req, dir, ledgerPath, store, entries, rec, full,
		fixOpen, fixOpenInline, now)
}

// fixBundleDir resolves the bundle dir, refusing non-local slugs.
func fixBundleDir(req Request) (string, *cli.RefusalError) {
	dir := bundle.Dir(req.Pr)
	if !strings.HasPrefix(dir, prefixLocalReview) {
		return "", &cli.RefusalError{
			Code:    "not-local",
			Message: "fix works on local bundles; fetch-local first",
		}
	}
	return dir, nil
}

// checkPrGate refuses closed or merged bundle PRs.
func checkPrGate(dir string) *cli.RefusalError {
	if !bundle.IsFile(dir + suffixPRJSON) {
		return nil
	}
	st := prState(dir)
	if st == prClosed || st == prMerged {
		return &cli.RefusalError{
			Code: "pr-not-open",
			Message: fmt.Sprintf(
				"bundle PR is %s — refusing fix (re-fetch if bundle stale)", st),
		}
	}
	return nil
}

// checkTreeGate refuses when the worktree is not clean for the audit.
func checkTreeGate(ctx context.Context) *cli.RefusalError {
	out, err := gitLsFilesUnmerged(ctx)
	if err != nil {
		return &cli.RefusalError{
			Code:    "not-a-repo",
			Message: "fix runs inside a git worktree (audits files vs anchor)",
		}
	}
	if out != "" {
		return &cli.RefusalError{
			Code: "conflicted-tree",
			Message: "worktree holds unresolved merge conflicts " +
				"(git ls-files -u non-empty) — resolve or abort, then re-run fix",
		}
	}
	return nil
}

// loadFindings reads and decodes findings.json, refusing when missing.
func loadFindings(dir string) ([]any, *cli.RefusalError) {
	if !bundle.IsFile(dir + suffixFindings) {
		return nil, &cli.RefusalError{
			Code: "missing-findings",
			Message: fmt.Sprintf(
				"missing %s/findings.json (run record first, then fix)", dir),
		}
	}
	raw, err := os.ReadFile(dir + suffixFindings)
	if err != nil {
		return nil, corruptStore(dir)
	}
	entries, err := decodeEntries(raw)
	if err != nil {
		return nil, corruptStore(dir)
	}
	return entries, nil
}

// printReadiness prints the readiness line and returns ledger inputs.
func printReadiness(
	r *cli.Runner,
	dir string,
	entries []any,
) (string, int, int) {
	now := cli.NowUTC(r.Now())
	fixOpen, fixOpenInline := countOpen(entries)
	fmt.Fprintf(r.Human,
		"» readiness: %s — findings %d open (%d inline open); "+
			"threads (stored): %s; PR %s\n",
		dir, fixOpen, fixOpenInline, storedThreads(dir), prInfo(dir))
	return now, fixOpen, fixOpenInline
}

// selectRec resolves the sig to fix, or reports completion selection.
func selectRec(
	entries []any,
	sigFlag string,
	fixOpenInline int,
	dir string,
) (string, *jq.Obj, *cli.RefusalError) {
	switch {
	case sigFlag != "":
		return resolveExplicit(entries, sigFlag)
	case fixOpenInline == 0:
		return "", nil, nil
	default:
		return resolveTop(entries, dir)
	}
}

// resolveExplicit resolves an explicit --sig flag to its entry.
func resolveExplicit(
	entries []any,
	sigFlag string,
) (string, *jq.Obj, *cli.RefusalError) {
	full := bundle.ResolveSig(entries, sigFlag)
	switch full {
	case resolveNone:
		return "", nil, &cli.RefusalError{
			Code: codeBadSig,
			Message: fmt.Sprintf(
				"fix: sig prefix '%s' not in store "+
					"(list open inline sigs with report --status open --kind inline)",
				prefixN(sigFlag, sigPrefixMsgLen)),
		}
	case resolveMany:
		return "", nil, &cli.RefusalError{
			Code: "ambiguous-sig",
			Message: fmt.Sprintf(
				"fix: sig prefix '%s' is ambiguous "+
					"(copy the full prefix from report output)",
				prefixN(sigFlag, sigPrefixMsgLen)),
		}
	}
	rec := findEntry(entries, full)
	if kindOf(rec) != kindInline {
		return "", nil, &cli.RefusalError{
			Code: codeBadSig,
			Message: fmt.Sprintf(
				"fix: sig %s is a %s finding (fix picks inline only)",
				prefix8(full), kindOf(rec)),
		}
	}
	if statusOf(rec) != statusOpen {
		return "", nil, &cli.RefusalError{
			Code: codeBadSig,
			Message: fmt.Sprintf(
				"fix: sig %s is %s (fix iterates open sigs only)",
				prefix8(full), statusOf(rec)),
		}
	}
	return full, rec, nil
}

// resolveTop resolves the default top pick to its entry.
func resolveTop(
	entries []any,
	dir string,
) (string, *jq.Obj, *cli.RefusalError) {
	full := topSig(entries)
	if full == "" {
		return "", nil, &cli.RefusalError{
			Code: "report-failed",
			Message: fmt.Sprintf(
				"fix: open-inline count disagrees with the store "+
					"(re-run record to rebuild %s/findings.json)", dir),
		}
	}
	return full, findEntry(entries, full), nil
}

// runPass enforces ceiling and scope, writes the ledger, and prints.
func runPass(
	ctx context.Context,
	r *cli.Runner,
	req Request,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	rec *jq.Obj,
	full string,
	fixOpen, fixOpenInline int,
	now string,
) (int, error) {
	passes := ledgerPasses(store, full)
	if passes >= maxPasses {
		return ceilingStop(r, dir, ledgerPath, store, entries, full,
			passes, fixOpen, now)
	}
	anchor := interpOf(rec, keyPath)
	if anchor == "" {
		return 0, &cli.RefusalError{
			Code: codeBadSig,
			Message: fmt.Sprintf(
				"fix: sig %s carries no anchor path (cannot scope the pass)",
				prefix8(full)),
		}
	}
	if passes >= 1 {
		if _, first, n := scopeEscape(ctx, store, full, anchor); n > 0 {
			return scopeStop(r, dir, ledgerPath, store, entries, full,
				first, n, anchor, fixOpen, now)
		}
	}
	pass := passes + 1
	store = setPass(store, full, pass, now, worktreeFiles(ctx))
	if err := writeLedger(ledgerPath, store); err != nil {
		return 0, err
	}
	return passOutput(r, req, dir, ledgerPath, store, entries, rec, full,
		anchor, pass, fixOpen, fixOpenInline, now)
}

// corruptStore refuses on an unreadable findings store.
func corruptStore(dir string) *cli.RefusalError {
	return &cli.RefusalError{
		Code: "corrupt-store",
		Message: fmt.Sprintf(
			"findings store %s/findings.json is unreadable or not a "+
				"findings array (re-run record to rebuild, or restore the file)",
			dir),
	}
}

// decodeEntries parses findings.json as a JSON array, order-preserving.
func decodeEntries(raw []byte) ([]any, error) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, sealed(opDecodeFindings, err)
	}
	arr, ok := doc.([]any)
	if !ok {
		return nil, errNotArray
	}
	return arr, nil
}

// prState ports pr_field state for the fix gate: lowercase only when the
// bundle carries no provider (a null or missing provider reads
// providerless; any other value keeps its case, matching `.provider // empty`).
func prState(dir string) string {
	raw, err := os.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	st := jq.StrOf(m[fieldState])
	if pv, has := m[fieldProvider]; has && pv != nil {
		return st
	}
	return bundle.LowerASCII(st)
}

// prInfo ports the advisory readiness PR fragment.
func prInfo(dir string) string {
	if !bundle.IsFile(dir + suffixPRJSON) {
		return "n/a (local bundle, PR check skipped)"
	}
	info := "state " + prState(dir)
	if draftOf(dir) {
		info += " draft"
	}
	return info
}

// draftOf ports `[[ "$(jq -r '.draft // false' ...)" == "true" ]]`.
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

// gitLsFilesUnmerged ports the conflicted-tree gate: unmerged entries, or an
// error when the cwd is not a worktree.
func gitLsFilesUnmerged(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-u")
	out, err := cmd.Output()
	if err != nil {
		return "", sealed(opGitLsFiles, err)
	}
	return string(out), nil
}

// storedThreads ports the advisory stored-thread counts.
func storedThreads(dir string) string {
	nr := jsonLen(dir + suffixReviews)
	ni := jsonLen(dir + suffixInline)
	if !bundle.Exists(dir+suffixReviews) && !bundle.Exists(dir+suffixInline) {
		return "none stored"
	}
	return fmt.Sprintf("reviews.json=%d inline.json=%d", nr, ni)
}

// jsonLen reads an array length (missing/unreadable/non-array reads 0).
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

// findEntry returns the findings entry with sig.
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

// statusOf reads an entry status.
func statusOf(m *jq.Obj) string {
	if m == nil {
		return ""
	}
	return jq.Interp(mustGet(m, fieldStatus))
}

// kindOf reads an entry kind.
func kindOf(m *jq.Obj) string {
	if m == nil {
		return ""
	}
	return jq.Interp(mustGet(m, fieldKind))
}

// mustGet reads a field (nil when the entry is nil).
func mustGet(m *jq.Obj, key string) any {
	v, _ := m.Get(key)
	return v
}

// interpOf renders a field the jq-interpolation way (null reads "").
func interpOf(m *jq.Obj, key string) string {
	if m == nil {
		return ""
	}
	return jq.Interp(mustGet(m, key))
}

// countOpen counts open and open-inline findings.
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

// openFlags reports whether an entry counts as open and open-inline.
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

// topSig ports the default pick: severity critical first, then path asc,
// line, sig.
func topSig(entries []any) string {
	cands := collectOpenInline(entries)
	if len(cands) == 0 {
		return ""
	}
	slices.SortStableFunc(cands, compareFinding)
	return sigOf(cands[0])
}

// collectOpenInline gathers open inline candidates in store order.
func collectOpenInline(entries []any) []*jq.Obj {
	var cands []*jq.Obj
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		if statusOf(m) != statusOpen || kindOf(m) != kindInline {
			continue
		}
		cands = append(cands, m)
	}
	return cands
}

// sigOf reads an entry sig, returning "" when missing or non-string.
func sigOf(m *jq.Obj) string {
	if m == nil {
		return ""
	}
	sv, _ := m.Get(keySig)
	s, ok := sv.(string)
	if !ok {
		return ""
	}
	return s
}

// compareFinding ports the sort_by comparator: severity, path, line, sig.
func compareFinding(a, b *jq.Obj) int {
	if sa, sb := sevRank(a), sevRank(b); sa != sb {
		return sa - sb
	}
	pa, pb := interpOf(a, keyPath), interpOf(b, keyPath)
	if pa != pb {
		if pa < pb {
			return -1
		}
		return 1
	}
	if la, lb := lineNum(a), lineNum(b); la != lb {
		return la - lb
	}
	sa, sb := interpOf(a, keySig), interpOf(b, keySig)
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	default:
		return 0
	}
}

// sevRank ports the severity map (critical 0, important 1, informational 2,
// else 3).
func sevRank(m *jq.Obj) int {
	switch interpOf(m, keySeverity) {
	case sevCritical:
		return 0
	case sevImportant:
		return 1
	case sevInformational:
		return 2
	default:
		return sevRankOther
	}
}

// lineNum ports `.line // 0` for the tiebreak: numbers read numerically,
// missing reads 0, and non-numeric values sort after every number (jq's
// total order puts strings/containers above numbers; only numbers and
// missing occur in validated stores).
func lineNum(m *jq.Obj) int {
	v, _ := m.Get(keyLine)
	if v == nil {
		return 0
	}
	if n, ok := jq.IntOf(v); ok {
		return n
	}
	return maxLineNum
}

// maxLineNum sorts non-numeric lines after every number.
const maxLineNum = int(^uint(0) >> 1)

// ledgerDoc is the fix.json store: {"sigs": {...}}.
type ledgerDoc struct {
	obj *jq.Obj
}

// loadLedger ports the ledger init: missing files are created with an empty
// store; unreadable ones warn and start fresh.
func loadLedger(r *cli.Runner, path string) (*ledgerDoc, error) {
	if !bundle.Exists(path) {
		return createLedger(path)
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		if doc, ok := decodeLedgerDoc(raw); ok {
			return ledgerWithSigs(r, path, doc), nil
		}
	}
	r.Warn(fmt.Sprintf(msgLedgerUnreadable, path))
	return freshLedger(), nil
}

// createLedger writes and returns an empty ledger for a missing path.
func createLedger(path string) (*ledgerDoc, error) {
	fresh := freshLedger()
	if err := writeLedger(path, fresh); err != nil {
		return nil, err
	}
	return freshLedger(), nil
}

// decodeLedgerDoc decodes ledger bytes to an object, reporting validity.
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

// ledgerWithSigs extracts the sigs table, warning on corrupt tables.
func ledgerWithSigs(r *cli.Runner, path string, m *jq.Obj) *ledgerDoc {
	sigs := jq.NewObj()
	sigsRaw, hasSigs := m.Get(keySigs)
	sm, ok := sigsRaw.(*jq.Obj)
	switch {
	case ok && sm != nil:
		sigs = sm
	case hasSigs && sigsRaw != nil:
		// A corrupt sigs table (present but not an object) reads fresh
		// with a warning, matching the shell's unreadable-ledger path;
		// an explicit null reads as the empty store with no warning.
		r.Warn(fmt.Sprintf(msgLedgerUnreadable, path))
	default:
	}
	return &ledgerDoc{obj: jq.NewObj(keySigs, sigs)}
}

// freshLedger returns an empty ledger store.
func freshLedger() *ledgerDoc {
	return &ledgerDoc{obj: jq.NewObj(keySigs, jq.NewObj())}
}

// writeLedger persists the ledger (compact, trailing newline).
func writeLedger(path string, store *ledgerDoc) error {
	if err := atomic.WriteFile(path, []byte(store.obj.MarshalCompact()+"\n")); err != nil {
		return &cli.RefusalError{
			Code:    "fix-failed",
			Message: fmt.Sprintf("could not write %s: %v", path, err),
		}
	}
	return nil
}

// sigStore returns the sig's ledger row, creating an empty view when absent.
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

// ledgerPasses reads the sig's pass count (non-numeric reads 0).
func ledgerPasses(store *ledgerDoc, sig string) int {
	v, _ := sigStore(store, sig).Get(keyPasses)
	if n, ok := jq.IntOf(v); ok {
		return n
	}
	return 0
}

// putSig writes the sig's ledger row back.
func putSig(store *ledgerDoc, sig string, row *jq.Obj) {
	sigs, _ := store.obj.Get(keySigs)
	sm, ok := sigs.(*jq.Obj)
	if !ok || sm == nil {
		sm = jq.NewObj()
		store.obj.Set(keySigs, sm)
	}
	sm.Set(sig, row)
}

// stoppedCount counts sig rows carrying a stopped tombstone.
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

// isStoppedRow reports whether a ledger row carries a stopped tombstone.
func isStoppedRow(raw any) bool {
	m, ok := raw.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	v, _ := m.Get(keyStopped)
	return v != nil
}

// fixedCount counts addressed findings.
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

// ceilingStop ports the 3-pass ceiling: tombstone once, refuse typed.
func ceilingStop(
	r *cli.Runner,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	full string,
	passes, fixOpen int,
	now string,
) (int, error) {
	row := sigStore(store, full)
	ensurePasses(row, passes)
	row.Set(keyLastAt, now)
	if v, _ := row.Get(keyStopped); v == nil {
		row.Set(keyStopped, stoppedCeiling)
	}
	putSig(store, full, row)
	if err := writeLedger(ledgerPath, store); err != nil {
		return 0, err
	}
	fmt.Fprintf(r.Human, fmtFixComplete,
		dir, fixedCount(entries), fixOpen, stoppedCount(store), ledgerPath)
	fmt.Fprintf(r.Human,
		"next: sig %s hit the 3-pass ceiling — triage it or split the fix\n",
		prefix8(full))
	return 0, &cli.RefusalError{
		Code: "fix-ceiling",
		Message: fmt.Sprintf(
			"fix: sig %s already used %d/3 passes — ceiling reached "+
				"(stopped: ceiling)", prefix8(full), passes),
	}
}

// ensurePasses backfills a missing non-numeric passes value.
func ensurePasses(row *jq.Obj, passes int) {
	p, _ := row.Get(keyPasses)
	if _, ok := numOf(p); !ok {
		row.Set(keyPasses, passes)
	}
}

// numOf reads a numeric ledger value.
func numOf(v any) (int, bool) {
	return jq.IntOf(v)
}

// scopeEscape ports the scope-escape audit: worktree files outside the anchor
// that appeared since the baseline stop the loop.
func scopeEscape(
	ctx context.Context,
	store *ledgerDoc,
	sig, anchor string,
) (bool, string, int) {
	base := baselineOf(store, sig)
	work := worktreeFiles(ctx)
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

// newPathSet builds a lookup set for baseline paths.
func newPathSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set
}

// isEscape reports whether a worktree path escapes the anchor scope.
func isEscape(p, anchor string, baseSet map[string]bool) bool {
	if baseSet[p] {
		return false
	}
	if strings.HasPrefix(p, prefixReview) {
		return false
	}
	return p != anchor
}

// baselineOf reads the sig's recorded baseline (missing reads empty).
func baselineOf(store *ledgerDoc, sig string) []string {
	old, _ := baselineKept(store, sig)
	return old
}

// baselineKept reads the sig's recorded baseline when one is stored: a
// present array (even empty) is kept; missing or non-array snapshots anew.
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

// scopeStop ports the scope-escape tombstone and refusal.
func scopeStop(
	r *cli.Runner,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	full, first string,
	n int,
	anchor string,
	fixOpen int,
	now string,
) (int, error) {
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
	if err := writeLedger(ledgerPath, store); err != nil {
		return 0, err
	}
	fmt.Fprintf(r.Human, fmtFixComplete,
		dir, fixedCount(entries), fixOpen, stoppedCount(store), ledgerPath)
	fmt.Fprintf(r.Human,
		"next: worktree escaped the anchor — revert or stash files "+
			"outside '%s', then re-run fix\n", anchor)
	if n > 1 {
		return 0, &cli.RefusalError{
			Code: codeScopeEscape,
			Message: fmt.Sprintf(
				"fix: worktree touches '%s' and %d more outside anchor '%s' "+
					"— revert or stash them, then re-run fix", first, n-1, anchor),
		}
	}
	return 0, &cli.RefusalError{
		Code: codeScopeEscape,
		Message: fmt.Sprintf(
			"fix: worktree touches '%s' outside anchor '%s' "+
				"— revert or stash it, then re-run fix", first, anchor),
	}
}

// setPass records a successful pass, clearing a prior stopped flag and
// snapshotting the baseline on the first pass. A recorded baseline (even an
// empty one) is kept: only a missing/non-array baseline snapshots anew,
// matching `.baseline // $base`.
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

// stringsOf converts paths to JSON strings.
func stringsOf(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// worktreeFiles ports fix_worktree_files: sorted unique worktree-changed
// paths, renames contributing both sides, own .review/ included here (the
// anchor audit filters it).
func worktreeFiles(ctx context.Context) []string {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	recs := strings.Split(string(out), sepNul)
	paths := parsePorcelain(recs)
	slices.Sort(paths)
	return slices.Compact(paths)
}

// parsePorcelain extracts changed paths, renames contributing both sides.
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

// porcelainEntry parses one porcelain record and its rename follower.
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

// porcelainPath splits a porcelain record into its path, if well-formed.
func porcelainPath(rec string) (string, bool) {
	if len(rec) < porcelainPathOff {
		return "", false
	}
	return rec[porcelainPathOff:], true
}

// isRename reports whether a porcelain status renames or copies.
func isRename(rec string) bool {
	if len(rec) < porcelainStatusLen {
		return false
	}
	st := rec[:porcelainStatusLen]
	return strings.HasPrefix(st, statusRename) ||
		strings.HasPrefix(st, statusCopy)
}

// passOutput ports the pass print plus the machine envelope.
func passOutput(
	r *cli.Runner,
	req Request,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	rec *jq.Obj,
	full, anchor string,
	pass, fixOpen, fixOpenInline int,
	now string,
) (int, error) {
	fmt.Fprintf(r.Human, "» ledger: %s — sig %s pass %d/3\n",
		ledgerPath, prefix8(full), pass)
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
	fmt.Fprintf(r.Human, "fix: %s [%s] %s (pass %d/3, ledger %s)\n",
		prefix8(full), sev, locator, pass, ledgerPath)
	fmt.Fprintf(r.Human, "finding: %s\n", body)
	fmt.Fprintf(r.Human, "anchors: %s:%s-%s side=%s anchored=%s\n",
		anchor, sl, ln, side, an)
	kbVal := readKbValue(dir)
	printKbAndInstructions(r, req, dir, anchor)
	if r.JSONMode {
		printPassEnvelope(r, dir, ledgerPath, store, entries, full,
			locator, sev, body, anchor, ln, sl, side, an,
			pass, fixOpen, fixOpenInline, kbVal, now)
	}
	return 0, nil
}

// readKbValue reads kb.txt context for the envelope, if present.
func readKbValue(dir string) any {
	if !bundle.IsFile(dir + suffixKB) {
		return nil
	}
	kbRaw, err := os.ReadFile(dir + suffixKB)
	if err != nil {
		kbRaw = nil
	}
	trimmed := strings.TrimRight(string(kbRaw), "\n")
	return jq.NewObj(keyFile, dir+suffixKB, keyContent, trimmed)
}

// printKbAndInstructions prints kb context plus the pass instructions.
func printKbAndInstructions(
	r *cli.Runner,
	req Request,
	dir, anchor string,
) {
	if bundle.IsFile(dir + suffixKB) {
		kbRaw, err := os.ReadFile(dir + suffixKB)
		if err != nil {
			kbRaw = nil
		}
		fmt.Fprintf(r.Human, "--- kb.txt (%s/kb.txt, full context) ---\n", dir)
		fmt.Fprintf(r.Human, "%s", string(kbRaw))
		fmt.Fprintf(r.Human, "--- end kb.txt ---\n")
	}
	fmt.Fprintf(r.Human, instructionsFmt,
		anchor, dir, req.Repo, req.Pr, req.Repo, req.Pr)
	fmt.Fprintf(r.Human, "%s", gateMsg)
	fmt.Fprintf(r.Human, "%s", budgetMsg)
	fmt.Fprintf(r.Human,
		"next: patch %s, write verification.json, "+
			"run record to land it, then re-run fix\n", anchor)
}

// printPassEnvelope renders the machine envelope for a pass.
func printPassEnvelope(
	r *cli.Runner,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	full, locator, sev, body, anchor, ln, sl, side, an string,
	pass, fixOpen, fixOpenInline int,
	kbVal any,
	now string,
) {
	anchors := jq.NewObj(
		keyPath, anchor,
		keyLine, ln,
		keyStartLine, sl,
		keySide, side,
		keyAnchored, an,
	)
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "fix",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", now,
		"next", nextPass,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"dir", dir,
			"ledger", ledgerPath,
			keySig, full,
			"locator", locator,
			keySeverity, sev,
			keyBody, body,
			"anchors", anchors,
			"pass", pass,
			"max_passes", maxPasses,
			"kb", kbVal,
			"open_inline", fixOpenInline,
			"fixed", fixedCount(entries),
			keyOpen, fixOpen,
			keyStopped, stoppedCount(store),
			"complete", false,
		),
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// locatorOf ports the locator render (path:line or heading fallback).
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

// completeRun ports terminal completion: no open inline sigs remain.
func completeRun(
	r *cli.Runner,
	dir, ledgerPath string,
	store *ledgerDoc,
	entries []any,
	fixOpen int,
	now string,
) (int, error) {
	fixed := fixedCount(entries)
	stopped := stoppedCount(store)
	fmt.Fprintf(r.Human, fmtFixComplete,
		dir, fixed, fixOpen, stopped, ledgerPath)
	next := nextDone
	if fixOpen > 0 {
		next = fmt.Sprintf(
			"next: %d open finding(s) left are non-inline or "+
				"dispositioned — triage or record to settle them", fixOpen)
	}
	fmt.Fprintf(r.Human, "%s\n", next)
	if r.JSONMode {
		printCompleteEnvelope(r, dir, ledgerPath, fixed, fixOpen, stopped, now)
	}
	return 0, nil
}

// printCompleteEnvelope renders the machine envelope for completion.
func printCompleteEnvelope(
	r *cli.Runner,
	dir, ledgerPath string,
	fixed, fixOpen, stopped int,
	now string,
) {
	envNext := nextTriage
	if fixOpen == 0 {
		envNext = nextDoneShort
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "fix",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", now,
		"next", envNext,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"dir", dir,
			"ledger", ledgerPath,
			keySig, nil,
			"locator", nil,
			keySeverity, nil,
			keyBody, nil,
			"anchors", nil,
			"pass", nil,
			"max_passes", maxPasses,
			"kb", nil,
			"open_inline", 0,
			"fixed", fixed,
			keyOpen, fixOpen,
			keyStopped, stopped,
			"complete", true,
		),
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// prefixN truncates to n runes (the shell's ${var:0:n} under UTF-8).
func prefixN(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// prefix8 renders the 8-char sig prefix for summaries.
func prefix8(sig string) string {
	return prefixN(sig, sigPrefixLen)
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// sealed annotates err without exposing it, per GO-ERRORS-03 (see jq.Sealed).
func sealed(op string, err error) error {
	//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
	return fmt.Errorf("%s: %v", op, err)
}

// Fix-loop constants.
const (
	// maxPasses is the hard-coded 3-pass ceiling per sig.
	maxPasses = 3
	// sigPrefixLen is the human sig-prefix width.
	sigPrefixLen = 8
	// sigPrefixMsgLen is the prefix width shown in sig error messages.
	sigPrefixMsgLen = 12
	// sevRankOther ranks unknown severities after every known one.
	sevRankOther = 3
	// porcelainPathOff is the byte offset of the path in porcelain -z records.
	porcelainPathOff = 3
	// porcelainStatusLen is the status field width in porcelain records.
	porcelainStatusLen = 2
)

// Field and code constants.
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
	// keyOpen is the envelope open-count field.
	keyOpen = "open"
	// fieldStatus is the entry status field.
	fieldStatus = "status"
	// fieldKind is the entry kind field.
	fieldKind = "kind"
	// fieldState is the PR state field.
	fieldState = "state"
	// fieldProvider is the PR provider field.
	fieldProvider = "provider"
	// fieldDraft is the PR draft field.
	fieldDraft = "draft"
)

// Bundle path and git constants.
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
	// prefixLocalReview matches local bundle dirs.
	prefixLocalReview = ".review/local-"
	// prefixReview filters review-owned paths from scope audits.
	prefixReview = ".review/"
	// resolveNone reports no sig match.
	resolveNone = "NONE"
	// resolveMany reports an ambiguous sig prefix.
	resolveMany = "MANY"
	// prClosed refuses closed bundle PRs.
	prClosed = "closed"
	// prMerged refuses merged bundle PRs.
	prMerged = "merged"
	// litTrue matches the shell true literal.
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
	// sevCritical ranks first in the default pick.
	sevCritical = "critical"
	// sevImportant ranks second in the default pick.
	sevImportant = "important"
	// sevInformational ranks third in the default pick.
	sevInformational = "informational"
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

// Message formats.
const (
	// msgLedgerUnreadable warns when the ledger reads fresh.
	msgLedgerUnreadable = "fix ledger %s unreadable — starting fresh " +
		"(prior pass counts lost)"
	// fmtFixComplete renders the fix-complete summary line.
	fmtFixComplete = "fix complete: %s — fixed=%d open=%d stopped=%d (ledger %s)\n"
	// opDecodeFindings names the findings decode in sealed errors.
	opDecodeFindings = "decode findings"
	// opGitLsFiles names the unmerged-entries read in sealed errors.
	opGitLsFiles = "git ls-files"
)

// Long human messages kept byte-identical to the shell port.
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

// errNotArray is the corrupt-store signal for non-array findings.
var errNotArray = storeError("not a findings array")

// storeError is a plain error value for store-shape failures.
type storeError string

// Error implements error.
func (e storeError) Error() string { return string(e) }
