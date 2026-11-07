package recordcore

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
)

type state struct { //nolint:govet // field grouping documents the merge dimensions; alignment is irrelevant here
	confidence   any
	security     any
	review       *jq.Obj
	controls     *controls.Snapshot
	run          *config.RunState
	kb           *kb.Ctx
	forces       *core.GlobalForces
	preFooter    func(string) string
	progress     core.Progress
	effective    core.Effective
	cmd          string
	repo         string
	pr           string
	env          map[string]string
	now          string
	findingsFile string
	marker       string
	dir          string
	reviewFile   string
	root         string
	repoRoot     string
	anchorHunks  string
	headShort    string
	prov         string
	headFull     string
	kept         []any
	keptSigs     []string
	hunkRows     []hunks.Hunk
	moved        []any
	entries      []any
	hunkN        int
	nKept        int
	nMoved       int
	dryRun       bool
	inputChecked bool
	warnings     []string
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

func (st *state) validateAndMerge(body string) (string, diagram.Status, error) {
	st.kb = kb.New(st.warn)
	st.kb.Root = st.repoRoot
	st.kb.ReadFile = st.controls.ReadFile
	st.kb.Policy = kb.PolicyFrom(st.kbThresholds())
	st.kb.Broken = false
	st.kb.QuietShape = st.inputChecked
	st.loadKB()
	st.kb.Staged = []map[string]any{}
	if err := checkReviewSchema(st.warnInput, st.review); err != nil {
		return "", diagram.Status{}, err
	}
	if err := st.validateInputs(); err != nil {
		return "", diagram.Status{}, err
	}
	anchorHunks, err := st.validateChunkMerge()
	if err != nil {
		return "", diagram.Status{}, err
	}
	st.anchorHunks = anchorHunks
	if err := st.parseConfidence(); err != nil {
		return "", diagram.Status{}, err
	}
	body = st.validateAnchors(body)
	body = st.demoteFences(body)
	body = appendMoveSection(body, st.moved)
	st.reportMoves()
	st.buildEntries(body)
	st.keptSigs = freshInlineSigs(st.entries, len(st.kept))
	if err := st.mergeEntries(); err != nil {
		return "", diagram.Status{}, err
	}
	curated, dstat, err := st.applyClaims(body)
	if err != nil {
		return "", diagram.Status{}, err
	}
	st.setMarker(curated)
	st.countHunks()
	removeDerivedSnapshot(st)
	return curated, dstat, nil
}

func (st *state) mergeEntries() error {
	if err := st.mergePrior(); err != nil {
		return err
	}
	st.backfill()
	st.autoReopen()
	return nil
}

func (st *state) applyClaims(body string) (string, diagram.Status, error) {
	st.applyResolved()
	if err := st.applyVerification(); err != nil {
		return "", diagram.Status{}, err
	}
	if err := st.applyTriage(); err != nil {
		return "", diagram.Status{}, err
	}
	return st.garnishChecks(body)
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
	st.human(fmt.Sprintf("» anchors: %d inline, %d moved to body", st.nKept, st.nMoved))
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
		st.human(fmt.Sprintf("  - `%s` (%s): %s", loc, jq.Interp(jq.Alt(reason, "outside reviewed hunks")), body200))
	}
}

func (st *state) garnishChecks(body string) (string, diagram.Status, error) {
	body, basisProblem := provenance.ApplyBasisLine(
		provenance.ReviewBasisCounts(st.review, st.entries, st.now), body)
	if basisProblem != nil {
		st.warn(basisProblem.Path + ": " + basisProblem.Message)
	}
	for _, w := range provenance.ValidateEvidenceLinks(
		body, st.headFull, provenance.EvidenceLinkable(st.dir, st.headFull)) {
		st.warn(w)
	}
	dr := st.applyDiagram(body)
	body = dr.Body
	if st.preFooter != nil {
		body = st.preFooter(body)
	}
	if ref := readcommon.ValidateOverridesAt(st.controls.OverridesDir()); ref != nil {
		//nolint:wrapcheck // the typed bad-override refusal passes through unchanged; the edge renders Code/Message
		return "", dr.Status, ref
	}
	body = st.applyFooter(body)
	st.setMarker(body)
	return body, dr.Status, nil
}

func (st *state) applyDiagram(body string) diagram.Result {
	dr := diagram.Validate(body,
		st.effective.DiagramsBehavioral == core.FlagOn,
		st.effective.DiagramsExpanded == core.FlagOn,
		core.FooterDivider)
	for _, w := range dr.Warns {
		st.warn(w)
	}
	return dr
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

func checkReviewSchema(warn func(string), review *jq.Obj) error {
	rv, has := review.Get("schema_version")
	if review == nil || !has || rv == nil || rv == false {
		warn("review.json carries no schema_version — assuming 1 (agents: write \"schema_version\":\"1\")")
		return nil
	}
	if s := jq.R(rv); s == schemaVersion1 {
		return nil
	}
	return &core.RefusalError{Code: codeBadReviewSchema, Message: fmt.Sprintf(msgBadSchema, jq.R(rv))}
}

func (st *state) parseConfidence() error {
	st.confidence = nil
	st.security = nil
	st.parseConfidenceLevel()
	return st.parseSecuritySummary()
}

func (st *state) parseConfidenceLevel() {
	conf, ok := st.review.Get(fieldConfidence)
	if !ok || conf == nil || conf == false {
		return
	}
	if s := jq.R(conf); isConfidenceLevel(s) {
		st.confidence = conf
	} else {
		st.warnInput(fmt.Sprintf("ignoring confidence '%s' (want integer 1-5)", s))
	}
}

func isConfidenceLevel(s string) bool {
	switch s {
	case "1", "2", "3", "4", "5":
		return true
	default:
		return false
	}
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

func (st *state) validateAnchors(body string) string {
	st.kept = []any{}
	st.moved = []any{}
	var comments []any
	if c, ok := st.review.Get(fieldComments); ok {
		comments = jq.Elements(c)
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
	return body
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
	raw, err := os.ReadFile(path)
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

func (st *state) demoteFences(body string) string {
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
	return body
}

func hasSuggestionFence(m *jq.Obj) bool {
	s, _ := m.Get(fieldSuggestion)
	if s == nil || s == false {
		return false
	}
	ss, ok := s.(string)
	return ok && strings.Contains(ss, "```")
}

func appendMoveSection(body string, moved []any) string {
	if len(moved) == 0 {
		return body
	}
	lines := make([]string, 0, len(moved))
	for _, c := range moved {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		lines = append(lines, moveLine(m))
	}
	return body + "\n\n" + moveSection(lines)
}

func moveSection(lines []string) string {
	return moveSectionHead + strings.Join(lines, newline)
}

const moveSectionHead = core.MovedSectionHeading + "\n\nThe following points fell outside " +
	"the diff hunks for the reviewed SHA, so they post here instead of inline:\n"

func moveLine(m *jq.Obj) string {
	p, _ := m.Get(fieldPath)
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	loc := fmt.Sprintf("%s:%s-%s", jq.Interp(p), jq.Interp(jq.Alt(start, line)), jq.Interp(line))
	drop, _ := m.Get(fieldDropReason)
	reason := jq.Interp(jq.Alt(drop, "outside reviewed hunks"))
	return fmt.Sprintf("- `%s` — %s (%s)", loc, entryBody(m), reason)
}

func runeSlice(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

func (st *state) buildEntries(body string) {
	var secs []any
	if s, ok := st.review.Get(fieldSections); ok {
		secs = jq.Elements(s)
	}
	var fresh []freshEntry
	fresh = append(fresh, freshInline(st)...)
	fresh = append(fresh, freshSections(body, secs)...)
	kbExists := st.controls.Has(kb.File)
	for _, fe := range fresh {
		st.entries = append(st.entries, stampEntry(st, fe.entry, fe.kbRaw, kbExists).Wire())
	}
}

type freshEntry struct {
	entry *core.FindingEntry
	kbRaw any
}

func freshInlineSigs(entries []any, kept int) []string {
	if kept > len(entries) {
		kept = len(entries)
	}
	out := make([]string, 0, kept)
	for i := range kept {
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

func freshSections(body string, secs []any) []freshEntry {
	var fresh []freshEntry
	for h := range strings.SplitSeq(body, newline) {
		if !strings.HasPrefix(h, "### ") {
			continue
		}
		fresh = append(fresh, freshEntry{entry: buildSection(h, sectionSeverity(secs, h))})
	}
	return fresh
}

func buildInline(st *state, m *jq.Obj) freshEntry {
	inBounds, ok := m.Get(fieldInBoundsInput)
	if !ok || inBounds == nil {
		inBounds = true
	}
	drop, _ := m.Get(fieldDropReason)
	path, _ := m.Get(fieldPath)
	side, _ := m.Get(fieldSide)
	line, _ := m.Get(fieldLine)
	bd, _ := m.Get(fieldBody)
	sugg, _ := m.Get(fieldSuggestion)
	kbc, _ := m.Get(fieldKB)
	rigor, _ := m.Get(fieldRigor)
	sev, _ := m.Get(fieldSeverity)
	cat, hasCat := m.Get(fieldCategory)
	body := jq.StrOf(jq.Alt(bd, ""))
	rigorValue, rigorSource := pickRigor(rigor)
	category := pickCategory(st, m, cat, hasCat)
	entry := &core.FindingEntry{
		Kind:        "inline",
		Path:        path,
		Side:        jq.Alt(side, sideRight),
		Line:        line,
		Anchored:    drop == nil,
		Body:        body,
		Suggestion:  optString(jq.Alt(sugg, nil)),
		InBounds:    inBounds,
		Rigor:       rigorValue,
		RigorSource: rigorSource,
		Severity:    pick(sev, sevAllow, nil),
		Category:    category,
		CategorySet: hasCat,
	}
	entry.Brief = briefOf(st, m)
	return freshEntry{entry: entry, kbRaw: jq.Alt(kbc, nil)}
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

func sectionSeverity(secs []any, h string) any {
	for _, s := range secs {
		sm, ok := s.(*jq.Obj)
		if !ok {
			continue
		}
		if hd, _ := sm.Get(fieldHeading); hd == h {
			sev, _ := sm.Get(fieldSeverity)
			return sev
		}
	}
	return nil
}

func buildSection(h string, sev any) *core.FindingEntry {
	return &core.FindingEntry{
		Kind:        "section",
		Heading:     h,
		Anchored:    fieldBody,
		Body:        h,
		RigorSource: rigorSourceDefault,
		Severity:    pick(sev, sevAllow, nil),
	}
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

func stampEntry(st *state, e *core.FindingEntry, kbRaw any, kbExists bool) *core.FindingEntry {
	key := sigKeyPart(e.Path) + newline + sigKeyPart(e.Heading) + newline + sigBody(e.Body)
	norm := st.kb.NormalizeEntry(jq.NewObj(fieldKB, kbRaw), kbExists)
	e.KB = kbStrings(norm)
	e.Sig = sig.Of(key)
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
	sevAllow   = []string{"critical", "important", "informational"}
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
		return nil, &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
	}
	if doc == nil || doc == false {
		return nil, nil
	}
	if arr, ok := doc.([]any); ok {
		return arr, nil
	}
	return nil, &core.RefusalError{Code: codeCorruptStore, Message: fmt.Sprintf(msgCorruptPrior, path)}
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
	overlayAlt(m, n, pm, fieldSeverity)
	mergeCategory(m, n)
	overlayAlt(m, n, pm, fieldSuggestion)
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
	}
}

// priorRigorSource guesses the provenance of a store entry written before the
// field existed. The pre-change merge defaulted a missing rigor to pointed,
// so pointed reads back as the checker's default; any other recognized rung
// must have come from the review. A model-written pointed is
// indistinguishable from a default-filled one — the manual states that
// ambiguity — and reads as default either way.
func priorRigorSource(m *jq.Obj) string {
	rv, _ := m.Get(fieldRigor)
	if s, ok := rv.(string); ok && s != rungPointed && slices.Contains(rigorAllow, s) {
		return rigorSourceModel
	}
	return rigorSourceDefault
}

func (st *state) autoReopen() {
	raw, err := os.ReadFile(st.dir + "/incremental.diff")
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
