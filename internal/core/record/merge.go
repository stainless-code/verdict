package recordcore

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
	"github.com/stainless-code/verdict/internal/untrusted"
	"github.com/stainless-code/verdict/internal/validation"
)

type state struct { //nolint:govet // field grouping documents the merge dimensions; alignment is irrelevant here
	confidence   any
	security     any
	risk         *core.RiskClaim
	review       *jq.Obj
	coverage     *coverage.Summary
	evidence     *core.EvidenceReceipt
	diagram      diagram.Result
	controls     *controls.Snapshot
	run          *config.RunState
	kb           *kb.Ctx
	forces       *core.GlobalForces
	progress     core.Progress
	effective    core.Effective
	minSeverity  string
	cmd          string
	repo         string
	pr           string
	env          map[string]string
	now          string
	findingsFile string
	marker       string
	dir          string
	receipt      *core.ExcludedFile
	reviewFile   string
	root         string
	repoRoot     string
	anchorHunks  string
	headShort    string
	prov         string
	headFull     string
	// readTree reads the graded content's tree; tree, treeGap, and treeRead
	// memoize it for the run (see gradedTree).
	readTree        func() (tree, gap string, err error)
	tree            string
	treeGap         string
	treeErr         error
	treeRead        bool
	kept            []any
	keptSigs        []string
	movedSigs       []string
	hunkRows        []hunks.Hunk
	moved           []any
	entries         []any
	hunkN           int
	nKept           int
	nMoved          int
	dryRun          bool
	noPersist       bool
	inputChecked    bool
	requireCoverage bool
	requireRigor    []string
	bodyOnlyAnchors bool
	policy          validation.Policy
	judged          map[string]core.ValidationResult
	validation      *ValidationResult
	receiptLoaded   bool
	warnings        []string
}

// kbThresholds reads the retirement policy the run resolved: the
// verified typed defaults when the bundle carries them, else the consumed
// environment names over the built-in floor for a bundle that predates
// typed evidence.
func (st *state) kbThresholds() (int, string) {
	if st.run != nil && st.run.Resolved != nil {
		return st.run.Resolved.Defaults.Values.KBPolicy()
	}
	defaults := config.EnvDefaults(st.env, st.warn)
	return defaults.KBPolicy()
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	if st.progress != nil {
		st.progress.Warn(msg)
	}
}

// warnInput emits a merge-time input warning only when the review checker did
// not already report the same fact as a structured problem.
func (st *state) warnInput(msg string) {
	if !st.inputChecked {
		st.warn(msg)
	}
}

func (st *state) human(line string) {
	if st.progress != nil {
		st.progress.Human(line)
	}
}

func (st *state) validateAndMerge() error {
	st.kb = kb.New(st.warn)
	st.kb.Root = st.repoRoot
	st.kb.ReadFile = st.controls.ReadFile
	st.kb.Policy = kb.PolicyFrom(st.kbThresholds())
	st.kb.Broken = false
	st.kb.QuietShape = st.inputChecked
	st.loadKB()
	readcommon.WarnContextSources(st.dir, st.review, st.warn)
	st.kb.Staged = []map[string]any{}
	if err := checkReviewSchema(st.review); err != nil {
		return err
	}
	if err := st.validateInputs(); err != nil {
		return err
	}
	if err := st.evaluateCoverage(); err != nil {
		return err
	}
	anchorHunks, err := st.validateChunkMerge()
	if err != nil {
		return err
	}
	st.anchorHunks = anchorHunks
	if err := st.parseConfidence(); err != nil {
		return err
	}
	st.validateAnchors()
	st.demoteFences()
	st.reportMoves()
	st.buildEntries()
	st.keptSigs = freshInlineSigs(st.entries, 0, len(st.kept))
	st.movedSigs = freshInlineSigs(st.entries, len(st.kept), len(st.moved))
	if err := st.finishEntries(); err != nil {
		return err
	}
	if ref := readcommon.ValidateOverridesAt(st.controls.OverridesDir()); ref != nil {
		//nolint:wrapcheck // the typed bad-override refusal passes through unchanged; the edge renders Code/Message
		return ref
	}
	st.reportCoverage()
	st.countHunks()
	removeDerivedSnapshot(st)
	return nil
}

// finishEntries runs the pass and then the floors, in that order and for two
// separate reasons: a withheld entry must exist before the moved and section
// bodies are curated from it, and a floor must read statuses the run's own
// claims and triage already settled. Floors move severity and history only,
// never withheld, so they cannot release anything the pass withheld.
func (st *state) finishEntries() error {
	if err := st.sealEntries(); err != nil {
		return err
	}
	return st.settleEntries()
}

func (st *state) sealEntries() error {
	if err := st.mergeEntries(); err != nil {
		return err
	}
	if ref := st.applyValidation(); ref != nil {
		return ref
	}
	return nil
}

func (st *state) mergeEntries() error {
	if err := st.mergePrior(); err != nil {
		return err
	}
	st.backfill()
	st.autoReopen()
	return st.applySilence()
}

func (st *state) settleEntries() error {
	st.applyResolved()
	if err := st.applyVerification(); err != nil {
		return err
	}
	if err := st.applyTriage(); err != nil {
		return err
	}
	st.sealFloors()
	st.settleValidation()
	return nil
}

func (st *state) loadKB() {
	if !st.controls.Has(kb.File) {
		return
	}
	raw, ok := st.controls.ReadFile(kb.File)
	if !ok || !validJSON(raw) {
		st.warn(fmt.Sprintf("kb store %s unreadable — citations ignored for this run (fix the bad line)", kb.File))
		st.kb.Broken = true
	}
}

func (st *state) reportMoves() {
	st.nKept = len(st.kept)
	st.nMoved = len(st.moved)
	st.human(core.Msgf("» anchors: %d inline, %d moved to body", st.nKept, st.nMoved))
	if st.nMoved == 0 {
		return
	}
	for _, c := range st.moved {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		loc := anchorLoc(m)
		body200 := runeSlice(entryBody(m), maxBodyRunes)
		reason, _ := m.Get(fieldDropReason)
		st.human(core.Msgf("  - `%s` (%s): %s", loc, jq.Interp(jq.Alt(reason, "outside reviewed hunks")), body200))
	}
}

func anchorLoc(m *jq.Obj) string {
	p, _ := m.Get(fieldPath)
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	return fmt.Sprintf("%s:%s-%s", jq.Interp(p), jq.Interp(jq.Alt(start, line)), jq.Interp(line))
}

func entryBody(m *jq.Obj) string {
	b, _ := m.Get(fieldBody)
	if b == nil || b == false {
		return ""
	}
	if s, ok := b.(string); ok {
		return s
	}
	return jq.Str(b)
}

func validJSON(raw []byte) bool {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	_, err := jq.DecodeSlurp(bytes.NewReader(raw))
	return err == nil
}

func checkReviewSchema(review *jq.Obj) error {
	if review == nil {
		return nil
	}
	rv, has := review.Get("schema_version")
	execution, _ := review.Get("execution")
	if !has || rv == nil || (rv != schemaVersion1 && (rv != "2" || execution == nil)) {
		return &core.RefusalError{Code: codeBadReviewSchema, Message: core.Msgf(msgBadSchema, jq.R(rv))}
	}
	findings, has := review.Get("findings")
	if _, ok := findings.([]any); !has || !ok {
		return &core.RefusalError{Code: codeBadReviewInput, Message: "findings must be an array"}
	}
	return nil
}

func (st *state) parseConfidence() error {
	st.confidence = nil
	st.security = nil
	st.risk = nil
	if err := st.parseSecuritySummary(); err != nil {
		return err
	}
	st.parseRisk()
	return nil
}

func (st *state) parseRisk() {
	raw, has := st.review.Get(fieldRisk)
	if !has || raw == nil {
		return
	}
	obj, ok := raw.(*jq.Obj)
	if !ok {
		st.warnInput("ignoring non-object risk claim (want {level, rationale, assessed_head})")
		return
	}
	claim, valid := parseRiskClaimObj(obj)
	if !valid {
		st.warnInput("ignoring invalid risk claim (want valid level, 10-500 char rationale, assessed_head)")
		return
	}
	stale := st.headFull != "" && st.headFull != headDirty && st.headFull != headUnknown && claim.AssessedHead != st.headFull
	if stale {
		st.warnInput(fmt.Sprintf("risk assessed_head %q != bundle head %q — marking risk claim stale", claim.AssessedHead, st.headFull))
	}
	claim.Stale = stale
	st.risk = claim
}

func parseRiskClaimObj(obj *jq.Obj) (*core.RiskClaim, bool) {
	levelVal, _ := obj.Get(fieldLevel)
	rationaleVal, _ := obj.Get(problemRationale)
	headVal, _ := obj.Get(fieldAssessedHead)
	level, okL := levelVal.(string)
	rationale, okR := rationaleVal.(string)
	assessedHead, okH := headVal.(string)
	if !okL || !okR || !okH {
		return nil, false
	}
	level = strings.ToLower(strings.TrimSpace(level))
	rationale = strings.TrimSpace(rationale)
	assessedHead = strings.TrimSpace(assessedHead)
	n := utf8.RuneCountInString(rationale)
	if !slices.Contains(riskLevels, level) || n < minRiskRationale || n > maxRiskRationale || assessedHead == "" {
		return nil, false
	}
	return &core.RiskClaim{
		Level:        level,
		Rationale:    rationale,
		AssessedHead: assessedHead,
	}, true
}

func (st *state) parseSecuritySummary() error {
	sec, has := st.review.Get(fieldSecuritySummary)
	if !has || sec == nil {
		return nil
	}
	s, isStr := sec.(string)
	if !isStr {
		st.warnInput("ignoring non-string security_summary (want a short string)")
		return nil
	}
	if s != "" {
		st.security = sec
	}
	return nil
}

func (st *state) validateAnchors() {
	st.kept = []any{}
	st.moved = []any{}
	var comments []any
	if c, ok := st.review.Get("findings"); ok {
		comments = jq.Elements(c)
	}
	if st.bodyOnlyAnchors {
		st.moveEveryAnchor(comments)
		return
	}
	hs := st.hunkRows
	if hs == nil {
		hs = loadHunkRows(st.anchorHunks)
	}
	st.hunkRows = hs
	for _, c := range comments {
		m, ok := c.(*jq.Obj)
		if !ok {
			continue
		}
		st.classifyComment(m, hs)
	}
}

// moveEveryAnchor sends every finding to the body instead of an inline anchor:
// the provider advertises inline_anchors=false, so the position it would refuse
// is dropped and the finding kept (keep-biased). The drop reason names the
// published code, so the body explains its own shape.
func (st *state) moveEveryAnchor(comments []any) {
	for _, c := range comments {
		m, ok := c.(*jq.Obj)
		if !ok {
			continue
		}
		moved := m.Clone()
		moved.Set(fieldDropReason, "provider takes no inline anchors (no-inline-anchors) — posted in the body instead")
		st.moved = append(st.moved, moved)
	}
}

func (st *state) classifyComment(m *jq.Obj, hs []hunks.Hunk) {
	p, _ := m.Get(fieldPath)
	path := jq.Str(p)
	key := stripDiffPrefix(path)
	if !hunks.Indexed(hs, key) {
		st.moveUnknownPath(m, path)
		return
	}
	lo, hi, side := anchorRange(m)
	if lo < 0 {
		if anchorAbsent(m) {
			moved := m.Clone()
			moved.Set(fieldDropReason, "no line/start_line — posted in the body instead of inline")
			st.moved = append(st.moved, moved)
			return
		}
		st.warnInput(fmt.Sprintf("kept despite non-numeric anchor: %s %s", path, anchorSpan(m)))
		st.kept = append(st.kept, m)
		return
	}
	want := "NEW"
	if side == sideLeft {
		want = "OLD"
	}
	if hunks.Contains(hs, key, want, lo, hi) {
		st.kept = append(st.kept, m)
		return
	}
	moved := m.Clone()
	moved.Set(fieldDropReason, fmt.Sprintf("anchor %d-%d (%s) outside reviewed hunks", lo, hi, side))
	st.moved = append(st.moved, moved)
}

// anchorAbsent reports a finding that carries no usable line: only a
// document that skipped the checker can reach it.
func anchorAbsent(m *jq.Obj) bool {
	for _, key := range []string{fieldLine, fieldStartLine} {
		if v, has := m.Get(key); has && v != nil && v != false {
			return false
		}
	}
	return true
}

func (st *state) moveUnknownPath(m *jq.Obj, path string) {
	moved := m.Clone()
	moved.Set(fieldDropReason, fmt.Sprintf("path not in diff index (%s) — posted here instead of inline", path))
	st.moved = append(st.moved, moved)
	st.warnInput("moved to body: unknown path (not in diff index): " + path)
}

func stripDiffPrefix(path string) string {
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func anchorRange(m *jq.Obj) (lo, hi int, side string) {
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	ls, ss := jqLine(line), jqLine(start)
	if ss == "" {
		ss = ls
	}
	if !jq.Digits(ls) || !jq.Digits(ss) {
		return -1, -1, ""
	}
	hi, errLine := strconv.Atoi(ls)
	lo, errStart := strconv.Atoi(ss)
	if errLine != nil || errStart != nil {
		return -1, -1, ""
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	sv, _ := m.Get(fieldSide)
	side = strings.ToUpper(jq.R(jq.Alt(sv, sideRight)))
	return lo, hi, side
}

func anchorSpan(m *jq.Obj) string {
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	ls, ss := jqLine(line), jqLine(start)
	if ss == "" {
		ss = ls
	}
	return ss + "-" + ls
}

func jqLine(v any) string {
	if v == nil || v == false {
		return ""
	}
	return jq.R(v)
}

func loadHunkRows(path string) []hunks.Hunk {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []hunks.Hunk
	for ln := range strings.SplitSeq(string(raw), newline) {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) < hunkFieldCount {
			continue
		}
		start, _ := jq.PrefixInt(f[hunkStartIdx])
		end, _ := jq.PrefixInt(f[hunkEndIdx])
		out = append(out, hunks.Hunk{File: f[0], Side: f[1], Start: start, End: end})
	}
	return out
}

func (st *state) demoteFences() {
	kept := st.kept[:0:0]
	var fenced []any
	for _, c := range st.kept {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			kept = append(kept, c)
			continue
		}
		if hasSuggestionFence(m) {
			cp := m.Clone()
			cp.Set(fieldDropReason, "suggestion contains a fence — posted as plain text instead")
			fenced = append(fenced, cp)
		} else {
			kept = append(kept, c)
		}
	}
	if len(fenced) > 0 {
		st.warnInput(fmt.Sprintf("demoting %d suggestion(s) with fences to body", len(fenced)))
		st.moved = append(fenced, st.moved...)
		st.kept = kept
	}
}

func hasSuggestionFence(m *jq.Obj) bool {
	s, _ := m.Get(fieldSuggestion)
	if s == nil || s == false {
		return false
	}
	ss, ok := s.(string)
	return ok && strings.Contains(ss, "```")
}

func runeSlice(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

func (st *state) buildEntries() {
	fresh := freshInline(st)
	kbExists := st.controls.Has(kb.File)
	for _, fe := range fresh {
		entry := stampEntry(st, fe.entry, fe.kbRaw, kbExists).Wire()
		if fe.reproduction != nil {
			entry.Set("reproduction", fe.reproduction)
		}
		st.entries = append(st.entries, entry)
	}
}

type freshEntry struct {
	entry        *core.FindingEntry
	kbRaw        any
	reproduction any
}

func freshInlineSigs(entries []any, offset, count int) []string {
	if offset >= len(entries) || count <= 0 {
		return nil
	}
	end := min(offset+count, len(entries))
	out := make([]string, 0, end-offset)
	for i := offset; i < end; i++ {
		value := ""
		if m, ok := entries[i].(*jq.Obj); ok && m != nil {
			value, _ = sigString(m)
		}
		out = append(out, value)
	}
	return out
}

func freshInline(st *state) []freshEntry {
	var fresh []freshEntry
	comments := append(append([]any{}, st.kept...), st.moved...)
	for _, c := range comments {
		m, mok := c.(*jq.Obj)
		if !mok || m == nil {
			continue
		}
		fresh = append(fresh, buildInline(st, m))
	}
	return fresh
}

func buildInline(st *state, m *jq.Obj) freshEntry {
	drop, _ := m.Get(fieldDropReason)
	path, _ := m.Get(fieldPath)
	side, _ := m.Get(fieldSide)
	line, _ := m.Get(fieldLine)
	bd, _ := m.Get(fieldBody)
	quote, _ := m.Get("quote")
	finderConfidence, _ := m.Get("finder_confidence")
	sugg, _ := m.Get(fieldSuggestion)
	kbc, _ := m.Get(fieldKB)
	rigor, _ := m.Get(fieldRigor)
	sev, _ := m.Get(fieldSeverity)
	cat, hasCat := m.Get(fieldCategory)
	body := untrusted.NeutralizeMentions(jq.StrOf(jq.Alt(bd, "")))
	rigorValue, rigorSource := pickRigor(rigor)
	category := pickCategory(st, m, cat, hasCat)
	entry := &core.FindingEntry{
		Kind:             "inline",
		Path:             path,
		Side:             jq.Alt(side, sideRight),
		Line:             line,
		Anchored:         drop == nil,
		Body:             body,
		Quote:            jq.R(quote),
		FinderConfidence: jq.R(finderConfidence),
		Verification:     &core.Verify{Quote: entryQuoteStatus(m)},
		Suggestion:       optString(jq.Alt(sugg, nil)),
		Lens:             lensOf(m),
		InBounds:         drop == nil,
		Rigor:            rigorValue,
		RigorSource:      rigorSource,
		Severity:         pick(sev, sevAllow, nil),
		Category:         category,
		CategorySet:      hasCat,
	}
	entry.Brief = briefOf(st, m)
	if v, ok := m.Get(fieldSources); ok {
		entry.Sources = kbStrings(v)
	}
	reproduction, _ := m.Get("reproduction")
	return freshEntry{entry: entry, kbRaw: jq.Alt(kbc, nil), reproduction: reproduction}
}

// briefOf validates a comment's optional machine brief against the anchor
// hunks: a bad part warns and drops, never refuses the merge.
func briefOf(st *state, m *jq.Obj) *core.Brief {
	raw, ok := m.Get(fieldBrief)
	if !ok || jq.Falsey(raw) {
		return nil
	}
	b, warns := brief.Validate(raw, st.hunkRows)
	for _, w := range warns {
		st.warnInput(w)
	}
	return b
}

// entryQuoteStatus reads the quote standing the evidence pass stamped on one
// finding. Only an explicit verified stamp counts: a finding the pass never saw
// is unverified, never a silent pass.
func entryQuoteStatus(m *jq.Obj) string {
	value, _ := m.Get(verificationKey)
	record, ok := value.(*jq.Obj)
	if !ok {
		return evidence.StatusUnverified
	}
	quote, _ := record.Get(fieldQuote)
	if quote == evidence.StatusVerified {
		return evidence.StatusVerified
	}
	return evidence.StatusUnverified
}

// pickCategory normalizes one comment's category: a published value reads
// through, absent and null read null (untagged), and an unknown or
// non-string value warns once and reads null — a taxonomy typo never refuses
// the review. The merge warning fires only on paths the checker did not
// already cover, so record reports it once through the checker.
func pickCategory(st *state, m *jq.Obj, cat any, hasCat bool) any {
	picked := pick(cat, core.CategoryValues, nil)
	if hasCat && cat != nil && picked == nil {
		loc := anchorLoc(m)
		st.warnInput(fmt.Sprintf("unknown category %q on %s — storing null (want %s)",
			jq.Str(cat), loc, strings.Join(core.CategoryValues, "|")))
	}
	return picked
}

// pickRigor records the rung and where it came from: a recognized string is
// the model's, anything absent or unusable is the checker's said default.
func pickRigor(v any) (any, string) {
	s, ok := v.(string)
	if ok && slices.Contains(rigorAllow, s) {
		return s, rigorSourceModel
	}
	return rungSaid, rigorSourceDefault
}

func optString(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// lensOf reads one comment's optional lens provenance, or "" when the
// comment spells none. The checker has already refused a malformed value, so
// a non-string here reads as absent, never as provenance.
func lensOf(m *jq.Obj) string {
	v, ok := m.Get(fieldLens)
	if !ok {
		return ""
	}
	if s, isStr := v.(string); isStr {
		return s
	}
	return ""
}

func stampEntry(st *state, e *core.FindingEntry, kbRaw any, kbExists bool) *core.FindingEntry {
	key := sig.Finding(sigKeyPart(e.Path), sigKeyPart(e.Heading), sigBody(e.Body))
	norm := st.kb.NormalizeEntry(jq.NewObj(fieldKB, kbRaw), kbExists)
	e.KB = kbStrings(norm)
	e.Sig = key
	e.SigVersion = sigVersion1
	e.FirstSeen = st.headShort
	e.LastSeen = st.headShort
	e.Status = statusOpen
	e.Rationale = ""
	e.History = []core.HistoryEvent{{At: st.now, Event: "reported"}}
	return e
}

func kbStrings(norm any) *[]string {
	if norm == nil {
		return nil
	}
	arr, ok := norm.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return &out
}

func sigKeyPart(v any) string {
	if v == nil || v == false {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return jq.Str(v)
}

func sigBody(v any) string {
	return sigKeyPart(v)
}

var (
	rigorAllow = []string{rungSaid, rungPointed, rungWalked, rungRan, rungReproduced}
	sevAllow   = []string{tierCritical, "important", "informational"}
)

func pick(v any, allowed []string, dflt any) any {
	s, ok := v.(string)
	if !ok {
		return dflt
	}
	if slices.Contains(allowed, s) {
		return s
	}
	return dflt
}

func (st *state) mergePrior() error {
	old, err := loadPrior(st.dir)
	if err != nil {
		return err
	}
	if old == nil {
		return nil
	}
	byNew := indexBySig(st.entries)
	oldSigs := map[string]bool{}
	merged := make([]any, 0, len(old)+len(st.entries))
	for _, prev := range old {
		pm, ok := prev.(*jq.Obj)
		if !ok {
			merged = append(merged, prev)
			continue
		}
		if m, ok := mergeOne(st, byNew, oldSigs, pm); ok {
			merged = append(merged, m)
		} else {
			merged = append(merged, prev)
		}
	}
	merged = appendFresh(merged, st.entries, oldSigs)
	st.entries = merged
	return nil
}

func appendFresh(merged, entries []any, oldSigs map[string]bool) []any {
	for _, n := range entries {
		if m, ok := n.(*jq.Obj); ok && isFreshSig(m, oldSigs) {
			merged = append(merged, n)
		}
	}
	return merged
}

func loadPrior(dir string) ([]any, error) {
	path := dir + "/findings.json"
	raw, ok := ReadOptional(path)
	if !ok {
		return nil, nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil, &core.RefusalError{Code: codeCorruptStore, Message: core.Msgf(msgCorruptPrior, path)}
	}
	if doc == nil || doc == false {
		return nil, nil
	}
	arr, ok := doc.([]any)
	if !ok {
		return nil, &core.RefusalError{Code: codeCorruptStore, Message: core.Msgf(msgCorruptPrior, path)}
	}
	if ref := readcommon.OutdatedEntries(dir, arr); ref != nil {
		return nil, ref //nolint:wrapcheck // typed refusal passes through unchanged
	}
	return arr, nil
}

func indexBySig(entries []any) map[string]*jq.Obj {
	bySig := map[string]*jq.Obj{}
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok {
			continue
		}
		if ss, ok := sigString(m); ok {
			bySig[ss] = m
		}
	}
	return bySig
}

func sigString(m *jq.Obj) (string, bool) {
	s, _ := m.Get(fieldSig)
	if s == nil {
		return "", false
	}
	ss, ok := s.(string)
	return ss, ok
}

func isFreshSig(m *jq.Obj, oldSigs map[string]bool) bool {
	s, _ := m.Get(fieldSig)
	if s == nil {
		return false
	}
	ss, ok := s.(string)
	return ok && !oldSigs[ss]
}

func mergeOne(st *state, byNew map[string]*jq.Obj, oldSigs map[string]bool, pm *jq.Obj) (any, bool) {
	psig, _ := pm.Get(fieldSig)
	ps := jq.StrOf(psig)
	oldSigs[ps] = true
	n, ok := byNew[ps]
	if !ok {
		return nil, false
	}
	return mergeFields(st, pm, n), true
}

func mergeFields(st *state, pm, n *jq.Obj) *jq.Obj {
	m := pm.Clone()
	m.Set(fieldLastSeen, st.headShort)
	overlayAlt(m, n, pm, fieldPath)
	overlayAlt(m, n, pm, fieldLine)
	nbody, _ := n.Get(fieldBody)
	m.Set(fieldBody, nbody)
	nanch, _ := n.Get(fieldAnchored)
	m.Set(fieldAnchored, nanch)
	overlayAlt(m, n, pm, fieldRigor)
	overlayAlt(m, n, pm, fieldRigorSource)
	overlayAlt(m, n, pm, "reproduction")
	overlayAlt(m, n, pm, fieldSeverity)
	mergeCategory(m, n)
	mergeLens(m, n)
	overlayAlt(m, n, pm, fieldSuggestion)
	if v, ok := n.Get(fieldSources); ok {
		m.Set(fieldSources, v)
	}
	nib, _ := n.Get(fieldInBounds)
	if nib == nil {
		pib, _ := pm.Get(fieldInBounds)
		m.Set(fieldInBounds, jq.Alt(pib, true))
	} else {
		m.Set(fieldInBounds, nib)
	}
	nkb, _ := n.Get(fieldKB)
	pkb, _ := pm.Get(fieldKB)
	m.Set(fieldKB, jq.Alt(jq.Alt(nkb, pkb), []any{}))
	mergeBrief(m, n, pm)
	return m
}

// mergeBrief replaces the prior brief with a fresh one and preserves it when
// the fresh comment carries none; an absent pair writes no key at all.
func mergeBrief(m, n, pm *jq.Obj) {
	if nb, ok := n.Get(fieldBrief); ok && !jq.Falsey(nb) {
		m.Set(fieldBrief, nb)
		return
	}
	if pb, ok := pm.Get(fieldBrief); ok && !jq.Falsey(pb) {
		m.Set(fieldBrief, pb)
	}
}

// mergeCategory carries the fresh category onto the merged entry: a present
// fresh key wins (an explicit null clears the prior label), an absent one
// preserves the prior value. A fresh unknown value already normalized to a
// present null, so it clears like an explicit null.
func mergeCategory(m, n *jq.Obj) {
	if n.Has(fieldCategory) {
		nv, _ := n.Get(fieldCategory)
		m.Set(fieldCategory, nv)
	}
}

// mergeLens carries the fresh lens onto the merged entry: a spelled fresh
// lens wins, an absent one preserves the prior provenance.
func mergeLens(m, n *jq.Obj) {
	if nv, ok := n.Get(fieldLens); ok && nv != nil {
		m.Set(fieldLens, nv)
	}
}

func overlayAlt(m, n, pm *jq.Obj, field string) {
	nv, _ := n.Get(field)
	pv, _ := pm.Get(field)
	m.Set(field, jq.Alt(nv, pv))
}

func (st *state) backfill() {
	for _, e := range st.entries {
		m, ok := e.(*jq.Obj)
		if !ok {
			continue
		}
		BackfillEntry(m)
	}
}

// BackfillEntry restores the additive fields a store entry predates: an
// empty kb list, the sig version, and the rigor provenance the field's
// arrival backfills from the recorded rung. Adopt shares it so handed-off
// findings carry rigor_source without waiting for a later post merge.
func BackfillEntry(m *jq.Obj) {
	if kbv, _ := m.Get(fieldKB); jq.Falsey(kbv) {
		m.Set(fieldKB, []any{})
	}
	if !m.Has(fieldSigVersion) {
		m.Set(fieldSigVersion, sigVersion1)
	}
	if !m.Has(fieldRigorSource) {
		m.Set(fieldRigorSource, priorRigorSource(m))
	} else if src, _ := m.Get("source"); jq.StrOf(src) == "tool" {
		if rs, _ := m.Get(fieldRigorSource); jq.StrOf(rs) == "verified" {
			m.Set(fieldRigorSource, rigorSourceDefault)
		}
	}
}

// priorRigorSource guesses the provenance of a store entry written before the
// field existed. Tool findings never carry model or verified rigor provenance
// and always backfill to default. The pre-change merge defaulted a missing
// rigor to pointed, so pointed reads back as the checker's default; any other
// recognized rung must have come from the review. A model-written pointed is
// indistinguishable from a default-filled one — the manual states that
// ambiguity — and reads as default either way.
func priorRigorSource(m *jq.Obj) string {
	if src, _ := m.Get("source"); jq.StrOf(src) == "tool" {
		return rigorSourceDefault
	}
	rv, _ := m.Get(fieldRigor)
	if s, ok := rv.(string); ok && s != rungPointed && slices.Contains(rigorAllow, s) {
		return rigorSourceModel
	}
	return rigorSourceDefault
}

func (st *state) autoReopen() {
	raw, err := noblock.ReadFile(st.dir + "/incremental.diff")
	if err != nil {
		return
	}
	files := touchedPaths(string(raw))
	for _, e := range st.entries {
		if m, ok := e.(*jq.Obj); ok {
			st.maybeReopen(m, files)
		}
	}
}

func touchedPaths(raw string) map[string]bool {
	sections := hunks.Files(raw)
	files := map[string]bool{}
	for i := range sections {
		files[sections[i].Path] = true
	}
	return files
}

func (st *state) maybeReopen(m *jq.Obj, files map[string]bool) {
	if stt, _ := m.Get(fieldStatus); stt != outcomeAddressed {
		return
	}
	p, _ := m.Get(fieldPath)
	if p == nil || !files[jq.StrOf(p)] {
		return
	}
	if ls, _ := m.Get(fieldLastSeen); ls == st.headShort {
		return
	}
	m.Set(fieldStatus, statusOpen)
	m.Set(fieldHistory, appendHistory(m, jq.NewObj(
		fieldAt, st.now, fieldEvent, "reopened", fieldActor, "script",
		"note", "delta touched path", fieldSHA, st.headShort,
	)))
}

func appendHistory(m *jq.Obj, ev *jq.Obj) []any {
	h, _ := m.Get(fieldHistory)
	hist, ok := h.([]any)
	if !ok {
		hist = nil
	}
	return append(hist, ev)
}

func (st *state) applyResolved() {
	for _, s := range resolvedItems(st.review) {
		st.recordResolvedClaim(s)
	}
}

func resolvedItems(review *jq.Obj) []string {
	var items []any
	if rv, ok := review.Get(fieldResolved); ok {
		if arr, isArr := rv.([]any); isArr {
			items = arr
		}
	}
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (st *state) recordResolvedClaim(s string) {
	full, code := resolveSig(st.entries, s)
	switch code {
	case sigNone:
		st.warn(fmt.Sprintf("resolved claim %s ignored (not in store)", readcommon.Prefix(s)))
	case sigMany:
		st.warn(fmt.Sprintf("resolved claim %s ignored (ambiguous prefix)", readcommon.Prefix(s)))
	default:
		target := findEntry(st.entries, full)
		target.Set(fieldHistory, appendHistory(target, jq.NewObj(
			fieldAt, st.now, fieldEvent, "claim", fieldActor, "agent",
			"note", "claimed in review", fieldSHA, st.headShort,
		)))
	}
}

func resolveSig(entries []any, prefix string) (string, string) {
	var match string
	n := 0
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok {
			continue
		}
		sv, _ := m.Get(fieldSig)
		s, ok := sv.(string)
		if !ok || !strings.HasPrefix(s, prefix) {
			continue
		}
		n++
		match = s
	}
	switch n {
	case 0:
		return "", sigNone
	case 1:
		return match, "OK"
	default:
		return "", sigMany
	}
}

func findEntry(entries []any, full string) *jq.Obj {
	for _, e := range entries {
		if m, ok := e.(*jq.Obj); ok {
			if sv, _ := m.Get(fieldSig); sv == full {
				return m
			}
		}
	}
	return nil
}
