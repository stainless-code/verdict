package recordcore

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/sig"
)

// state carries the validate_and_merge globals for one record run. Findings
// entries are *jq.Obj in shell construction order: jq preserves insertion
// order, so byte parity needs the same field sequence, not sorted keys.
// Fresh entries are built as [core.FindingEntry] values (field order is
// wire order) and converted with Wire; merged priors stay ordered objects
// so prior key order and unknown keys survive byte-identically.
type state struct { //nolint:govet // field grouping documents the merge dimensions; alignment is irrelevant here
	confidence   any
	security     any
	review       *jq.Obj
	controls     *controls.Snapshot
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
	anchorHunks  string
	headShort    string
	prov         string
	headFull     string
	kept         []any
	keptSigs     []string
	moved        []any
	entries      []any
	hunkN        int
	nKept        int
	nMoved       int
	dryRun       bool
	warnings     []string
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	if st.progress != nil {
		st.progress.Warn(msg)
	}
}

// human streams one human output line.
func (st *state) human(line string) {
	if st.progress != nil {
		st.progress.Human(line)
	}
}

// validateAndMerge ports validate_and_merge end to end, returning the
// curated body (move section, diagram strips, and the rendered footer
// appended) plus the diagram disposition for the dry-run envelope, or a
// typed refusal for any fail-closed gate. The record verb discards the body
// and keeps the warnings; the post verb posts the curated body.
func (st *state) validateAndMerge(body string) (string, diagram.Status, error) {
	st.kb = kb.New(st.warn)
	st.kb.ReadFile = st.controls.ReadFile
	st.kb.ResolvePolicy(st.env, st.controls.Has(kb.File))
	st.kb.Broken = false
	st.loadKB()
	st.kb.Staged = []map[string]any{}
	if err := checkReviewSchema(st.warn, st.review); err != nil {
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
	// The verdict marker and hunk count read the curated body, like the
	// shell's validate_and_merge tail; the derived per-chunk snapshot is
	// removed here too, so refusals below never leave it behind.
	st.setMarker(curated)
	st.countHunks()
	removeDerivedSnapshot(st)
	return curated, dstat, nil
}

// mergeEntries ports the re-post merge tail: prior overlay, backfill, and
// delta-run auto-reopen.
func (st *state) mergeEntries() error {
	if err := st.mergePrior(); err != nil {
		return err
	}
	st.backfill()
	st.autoReopen()
	return nil
}

// applyClaims ports the authority and garnish tail: bare resolved proposals,
// verification-gated flips, triage claims, then the garnish checks. It
// returns the curated body (the record verb discards it; the post verb
// posts it) with the diagram disposition.
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

// loadKB reads the KB store, marking it broken (citations ignored) when the
// file exists but does not parse.
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

// reportMoves emits the human anchor table: kept/moved counts plus one line
// per moved comment with its drop reason.
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

// garnishChecks runs the post-merge validators: basis counts, evidence
// links, diagrams, the footer receipt, and the override fragments. It
// returns the curated body: the record verb discards it (persisting only
// findings, the record artifact, and the KB stores), while the post verb
// carries it into the payload. The footer receipt is appended exactly once,
// like the shell's strip-and-replace render.
func (st *state) garnishChecks(body string) (string, diagram.Status, error) {
	for _, w := range provenance.ValidateBasisLine(
		provenance.ReviewBasisCounts(st.review, st.entries, st.now), body) {
		st.warn(w)
	}
	for _, w := range provenance.ValidateEvidenceLinks(
		body, st.headFull, provenance.EvidenceLinkable(st.dir, st.headFull)) {
		st.warn(w)
	}
	dr := st.applyDiagram(body)
	body = dr.Body
	// The post hosting-limit guard splits here: after the diagram strips
	// (entries already derive from the full body) and before the footer
	// receipt, so the appended block always survives intact.
	if st.preFooter != nil {
		body = st.preFooter(body)
	}
	if ref := validateOverridesAt(st.controls.OverridesDir()); ref != nil {
		return "", dr.Status, ref
	}
	body = st.applyFooter(body)
	st.setMarker(body)
	return body, dr.Status, nil
}

// applyDiagram validates behavioral diagrams, surfaces its warnings, and
// returns the validated body with its disposition for the dry-run envelope.
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

// anchorLoc renders the stderr table's path locator.
func anchorLoc(m *jq.Obj) string {
	p, _ := m.Get(fieldPath)
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	return fmt.Sprintf("%s:%s-%s", jq.Interp(p), jq.Interp(jq.Alt(start, line)), jq.Interp(line))
}

// entryBody renders the table body: strings raw, everything else -r style.
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

// validJSON reports whether raw holds complete JSON documents (an empty
// file reads valid, mirroring `jq empty`, which consumes a whole sequence).
func validJSON(raw []byte) bool {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	_, err := jq.DecodeSlurp(bytes.NewReader(raw))
	return err == nil
}

// checkReviewSchema ports the schema_version gate: absent warns (grandfathered
// agents), "1" passes, anything else refuses.
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

// parseConfidence ports the review-level confidence + security summary
// parsing: absent stays null, out-of-range warns and drops.
func (st *state) parseConfidence() error {
	st.confidence = nil
	st.security = nil
	st.parseConfidenceLevel()
	return st.parseSecuritySummary()
}

// parseConfidenceLevel ports the confidence integer parsing.
func (st *state) parseConfidenceLevel() {
	conf, ok := st.review.Get("confidence")
	if !ok || conf == nil || conf == false {
		return
	}
	if s := jq.R(conf); isConfidenceLevel(s) {
		st.confidence = conf
	} else {
		st.warn(fmt.Sprintf("ignoring confidence '%s' (want integer 1-5)", s))
	}
}

// isConfidenceLevel reports whether s is a valid review confidence level.
func isConfidenceLevel(s string) bool {
	switch s {
	case "1", "2", "3", "4", "5":
		return true
	default:
		return false
	}
}

// parseSecuritySummary ports the security_summary string parsing.
func (st *state) parseSecuritySummary() error {
	sec, has := st.review.Get("security_summary")
	if !has || sec == nil {
		return nil
	}
	s, isStr := sec.(string)
	if !isStr {
		st.warn("ignoring non-string security_summary (want a short string)")
		return nil
	}
	if s != "" {
		st.security = sec
	}
	return nil
}

// validateAnchors ports the partitioned keep-biased anchor validation,
// returning the unchanged body (anchors never rewrite prose).
func (st *state) validateAnchors(body string) string {
	st.kept = []any{}
	st.moved = []any{}
	var comments []any
	if c, ok := st.review.Get("comments"); ok {
		comments = jq.Elements(c)
	}
	// The hunk index converts to the shared shape once per run: classifying
	// a comment only scans it, never rebuilds it.
	hs := loadHunkRows(st.anchorHunks)
	for _, c := range comments {
		m, ok := c.(*jq.Obj)
		if !ok {
			continue
		}
		st.classifyComment(m, hs)
	}
	return body
}

// classifyComment keeps or moves one review comment by its diff anchoring.
// A path outside the diff index moves into the body section: GitHub would
// reject the inline anchor, so the point survives as an unanchored
// observation instead of dying at submit. Ambiguity still keeps the run
// green with a warning — the comment is moved, never dropped.
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
		st.warn(fmt.Sprintf("kept despite non-numeric anchor: %s %s", path, anchorSpan(m)))
		st.kept = append(st.kept, m)
		return
	}
	want := "NEW"
	if side == "LEFT" {
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

// moveUnknownPath moves one review comment whose path is absent from the
// diff index into the body section, reusing the move-section rendering the
// out-of-hunk anchors already use.
func (st *state) moveUnknownPath(m *jq.Obj, path string) {
	moved := m.Clone()
	moved.Set(fieldDropReason, fmt.Sprintf("path not in diff index (%s) — posted here instead of inline", path))
	st.moved = append(st.moved, moved)
	st.warn("moved to body: unknown path (not in diff index): " + path)
}

// stripDiffPrefix drops the a//b/ diff-path prefix for index lookup.
func stripDiffPrefix(path string) string {
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

// anchorRange reads the comment's line span and side, returning lo < 0 when
// the anchors are non-numeric or do not fit in an int (a 20-digit line must
// refuse like any other non-numeric anchor, never wrap into a nonsense span).
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
	sv, _ := m.Get("side")
	side = strings.ToUpper(jq.R(jq.Alt(sv, "RIGHT")))
	return lo, hi, side
}

// anchorSpan renders the span for the non-numeric-anchor warning.
func anchorSpan(m *jq.Obj) string {
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	ls, ss := jqLine(line), jqLine(start)
	if ss == "" {
		ss = ls
	}
	return ss + "-" + ls
}

// jqLine renders .line/.start_line the way `jq -r '.line // empty'` does:
// null/false/missing read "", everything else renders raw (containers
// pretty, via jq.R).
func jqLine(v any) string {
	if v == nil || v == false {
		return ""
	}
	return jq.R(v)
}

// loadHunkRows parses a hunks.tsv snapshot, tolerating malformed rows the
// way awk's numeric coercion does.
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

// demoteFences ports the suggestion-fence demotion: comments whose suggestion
// contains a fence move to the body with a fixed dropReason.
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
		st.warn(fmt.Sprintf("demoting %d suggestion(s) with fences to body", len(fenced)))
		st.moved = append(fenced, st.moved...)
		st.kept = kept
	}
	return body
}

// hasSuggestionFence reports whether the comment's suggestion carries a
// fence. A non-string suggestion would fail the shell's `contains` and abort
// the run under set -e; the Go port treats it as unfenced (documented
// divergence: bash aborts, Go keeps).
func hasSuggestionFence(m *jq.Obj) bool {
	s, _ := m.Get(fieldSuggestion)
	if s == nil || s == false {
		return false
	}
	ss, ok := s.(string)
	return ok && strings.Contains(ss, "```")
}

// appendMoveSection ports the unanchored-observations section appended when
// comments moved to the body.
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

// moveSection renders the unanchored-observations section header.
func moveSection(lines []string) string {
	return moveSectionHead + strings.Join(lines, newline)
}

// moveSectionHead is the unanchored-observations section header.
const moveSectionHead = "### ℹ️ Unanchored observations\n\nThe following points fell outside " +
	"the diff hunks for the reviewed SHA, so they post here instead of inline:\n"

// moveLine renders one moved comment as a body bullet.
func moveLine(m *jq.Obj) string {
	p, _ := m.Get(fieldPath)
	line, _ := m.Get(fieldLine)
	start, _ := m.Get(fieldStartLine)
	loc := fmt.Sprintf("%s:%s-%s", jq.Interp(p), jq.Interp(jq.Alt(start, line)), jq.Interp(line))
	drop, _ := m.Get(fieldDropReason)
	reason := jq.Interp(jq.Alt(drop, "outside reviewed hunks"))
	return fmt.Sprintf("- `%s` — %s (%s)", loc, entryBody(m), reason)
}

// runeSlice renders `\(.body // "" | .[0:200])`: jq slices strings by
// codepoint.
func runeSlice(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// buildEntries ports the new-entries construction plus the sig loop: sigs
// are line-number-free (comments carry lines, findings do not), and every
// comment cite normalizes through the KB policy. Fresh entries are built as
// typed values (field order is wire order); the merge downstream keeps them
// as ordered objects.
func (st *state) buildEntries(body string) {
	var secs []any
	if s, ok := st.review.Get("sections"); ok {
		secs = jq.Elements(s)
	}
	var fresh []freshEntry
	fresh = append(fresh, freshInline(st.kept, st.moved)...)
	fresh = append(fresh, freshSections(body, secs)...)
	kbExists := st.controls.Has(kb.File)
	for _, fe := range fresh {
		st.entries = append(st.entries, stampEntry(st, fe.entry, fe.kbRaw, kbExists).Wire())
	}
}

// freshEntry is one fresh finding plus its raw kb cite for normalization.
type freshEntry struct {
	entry *core.FindingEntry
	kbRaw any
}

// freshInlineSigs reads the fresh inline entries' sigs in kept order. The
// entries slice starts with freshInline output (kept first, then moved), so
// the first len(kept) entries pair with the comments post sends inline.
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

// freshInline builds the inline entries from kept/moved comments.
func freshInline(kept, moved []any) []freshEntry {
	var fresh []freshEntry
	comments := append(append([]any{}, kept...), moved...)
	for _, c := range comments {
		m, mok := c.(*jq.Obj)
		if !mok || m == nil {
			continue
		}
		fresh = append(fresh, buildInline(m))
	}
	return fresh
}

// freshSections builds the section entries from body headings.
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

// buildInline ports the inline-entry construction for one kept/moved comment.
func buildInline(m *jq.Obj) freshEntry {
	inBounds, ok := m.Get("in-bounds")
	if !ok || inBounds == nil {
		inBounds = true
	}
	drop, _ := m.Get(fieldDropReason)
	path, _ := m.Get(fieldPath)
	side, _ := m.Get("side")
	line, _ := m.Get(fieldLine)
	bd, _ := m.Get(fieldBody)
	sugg, _ := m.Get(fieldSuggestion)
	kbc, _ := m.Get(fieldKB)
	rigor, _ := m.Get(fieldRigor)
	sev, _ := m.Get(fieldSeverity)
	body := jq.StrOf(jq.Alt(bd, ""))
	return freshEntry{
		entry: &core.FindingEntry{
			Kind:       "inline",
			Path:       path,
			Side:       jq.Alt(side, "RIGHT"),
			Line:       line,
			Anchored:   drop == nil,
			Body:       body,
			Suggestion: optString(jq.Alt(sugg, nil)),
			InBounds:   inBounds,
			Rigor:      pick(rigor, rigorAllow, "pointed"),
			Severity:   pick(sev, sevAllow, nil),
		},
		kbRaw: jq.Alt(kbc, nil),
	}
}

// sectionSeverity matches a body section heading to its review severity.
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

// buildSection ports the section-entry construction for one body heading.
func buildSection(h string, sev any) *core.FindingEntry {
	return &core.FindingEntry{
		Kind:     "section",
		Heading:  h,
		Anchored: fieldBody,
		Body:     h,
		Severity: pick(sev, sevAllow, nil),
	}
}

// optString renders an optional string (nil reads null).
func optString(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// stampEntry stamps one fresh entry with its sig, status, KB normalization,
// and initial history.
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

// kbStrings converts a normalized kb id list: absent reads nil (the
// backfill turns it into []), present reads the id strings.
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

// sigKeyPart renders a sig-key path/heading segment: null/false/missing read
// "" (via //), everything else renders -r style. A non-string segment would
// fail the shell's `+` and abort the run; the Go port coerces (documented).
func sigKeyPart(v any) string {
	if v == nil || v == false {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return jq.Str(v)
}

// sigBody renders the sig-key body segment the same way.
func sigBody(v any) string {
	return sigKeyPart(v)
}

var (
	rigorAllow = []string{"said", "pointed", "walked", "ran", "reproduced"}
	sevAllow   = []string{"critical", "important", "informational"}
)

// pick ports the jq allowlist pick: membership wins, else the default.
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

// mergePrior ports the re-post merge: explicit null kb keeps the prior cite,
// explicit [] clears it, present arrays overwrite; everything else from the
// new entry wins except status/history/identity fields.
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

// appendFresh appends fresh entries whose sigs are absent from the prior store.
func appendFresh(merged, entries []any, oldSigs map[string]bool) []any {
	for _, n := range entries {
		if m, ok := n.(*jq.Obj); ok && isFreshSig(m, oldSigs) {
			merged = append(merged, n)
		}
	}
	return merged
}

// loadPrior reads the prior findings store: missing, empty, null, or false
// read as no prior (matching the shell's slurp); a list reads as the prior
// entries; anything else refuses as corrupt-store (never start fresh).
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

// indexBySig indexes entries by their string sig.
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

// sigString reads an entry's string sig.
func sigString(m *jq.Obj) (string, bool) {
	s, _ := m.Get(fieldSig)
	if s == nil {
		return "", false
	}
	ss, ok := s.(string)
	return ss, ok
}

// isFreshSig reports whether m carries a sig absent from the prior store.
func isFreshSig(m *jq.Obj, oldSigs map[string]bool) bool {
	s, _ := m.Get(fieldSig)
	if s == nil {
		return false
	}
	ss, ok := s.(string)
	return ok && !oldSigs[ss]
}

// mergeOne merges one prior entry against its fresh twin. It records the
// prior sig and reports ok=false when the prior stands alone.
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

// mergeFields overlays the fresh entry's fields on a clone of the prior.
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
	overlayAlt(m, n, pm, fieldSeverity)
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
	return m
}

// overlayAlt sets field from the fresh entry, falling back to the prior.
func overlayAlt(m, n, pm *jq.Obj, field string) {
	nv, _ := n.Get(field)
	pv, _ := pm.Get(field)
	m.Set(field, jq.Alt(nv, pv))
}

// backfill ports the sig_version/kb backfill: entries that predate versioning
// get "1" (never clobbering), and null/false kb reads [].
func (st *state) backfill() {
	for _, e := range st.entries {
		m, ok := e.(*jq.Obj)
		if !ok {
			continue
		}
		if kbv, _ := m.Get(fieldKB); jq.Falsey(kbv) {
			m.Set(fieldKB, []any{})
		}
		if !m.Has(fieldSigVersion) {
			m.Set(fieldSigVersion, sigVersion1)
		}
	}
}

// autoReopen ports the delta-run auto-reopen: addressed entries on a path the
// incremental diff touches go back to open.
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

// touchedPaths parses the incremental diff's touched file set.
func touchedPaths(raw string) map[string]bool {
	sections := hunks.Files(raw)
	files := map[string]bool{}
	for i := range sections {
		files[sections[i].Path] = true
	}
	return files
}

// maybeReopen reopens one addressed entry whose path the delta touched.
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

// appendHistory appends an event to an entry's history, returning the array.
func appendHistory(m *jq.Obj, ev *jq.Obj) []any {
	h, _ := m.Get(fieldHistory)
	hist, ok := h.([]any)
	if !ok {
		hist = nil
	}
	return append(hist, ev)
}

// applyResolved ports the authority step: bare resolved[] sigs are proposals
// (history only, never a flip). Shapes are validated upfront; non-strings
// here are unreachable and skipped.
func (st *state) applyResolved() {
	for _, s := range resolvedItems(st.review) {
		st.recordResolvedClaim(s)
	}
}

// resolvedItems reads the bare resolved sig proposals. Shapes are validated
// upfront, so non-arrays and non-strings here are unreachable and skipped.
func resolvedItems(review *jq.Obj) []string {
	var items []any
	if rv, ok := review.Get("resolved"); ok {
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

// recordResolvedClaim records one bare resolved sig as a history-only claim.
func (st *state) recordResolvedClaim(s string) {
	full, code := resolveSig(st.entries, s)
	switch code {
	case sigNone:
		st.warn(fmt.Sprintf("resolved claim %s ignored (not in store)", prefix8(s)))
	case sigMany:
		st.warn(fmt.Sprintf("resolved claim %s ignored (ambiguous prefix)", prefix8(s)))
	default:
		target := findEntry(st.entries, full)
		target.Set(fieldHistory, appendHistory(target, jq.NewObj(
			fieldAt, st.now, fieldEvent, "claim", fieldActor, "agent",
			"note", "claimed in review", fieldSHA, st.headShort,
		)))
	}
}

// prefix8 renders the ${sig:0:8} short form.
func prefix8(s string) string {
	if len(s) > sigPrefixLen {
		return s[:sigPrefixLen]
	}
	return s
}

// resolveSig ports resolve_sig: a sig prefix resolves to the unique full
// sig, or the NONE/MANY sentinels. Non-string sigs never match (the shell
// aborts there; the Go port skips — documented divergence).
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

// findEntry returns the entry with the full sig.
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
