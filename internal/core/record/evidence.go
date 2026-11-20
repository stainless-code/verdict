package recordcore

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/approval"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Evidence bundle inputs, the manifest role that turns a context source into
// requirements, the warning bound record prints, and the JSONPaths the evidence
// problems address.
const (
	hunkIndexSuffix   = "/hunks.tsv"
	contextTextSuffix = "/context.txt"
	specRole          = "spec"
	evidenceWarnCap   = 5
	evidencePrefix    = "evidence: "
	verificationKey   = "verification"
	receiptsPath      = "$.receipts"

	findingsQuotePath = "$.findings[%d].quote"
	receiptHunkPath   = "$.receipts[%d].hunk"
	receiptQuotePath  = "$.receipts[%d].quote"
	specItemPath      = "$.spec_checks[%d].item"
	specEvidencePath  = "$.spec_checks[%d].evidence.path"
	specQuotePath     = "$.spec_checks[%d].evidence.quote"
)

// Evidence messages. Every sentence names the exact bundle fact the agent has
// to change, because it is also the repair problem text.
const (
	msgEvidenceDrift     = "hunks.tsv disagrees with full.diff (hunk-index-drift); receipts were checked against the diff"
	msgQuoteMismatch     = "finding %d quote does not match full.diff at %s:%d (%s)"
	msgQuoteNotCheckable = "finding %d at %s:%d cannot be checked from the bundle (%s)"
	msgReceiptUnknown    = "receipt hunk %q is not a changed hunk in hunks.tsv"
	msgReceiptMismatch   = "receipt for %s quotes text that hunk does not carry (%s)"
	msgReceiptLarge      = "receipt for %s quotes more than %d lines"
	msgReceiptsUnkeyable = "full.diff carries changed hunks but none of their paths can be keyed (a path holds a tab or newline)"
	msgSpecItemUnknown   = "spec check item %q is not an item in context.txt"
	msgSpecNotCode       = "spec check for %q cites %s, which is test output rather than code"
	msgSpecNotInDiff     = "spec check for %q cites %s, which is not a path in full.diff"
	msgSpecAmbiguous     = "spec check for %q cites %s, which matches more than one path in full.diff"
	msgSpecMismatch      = "spec check for %q cites %s:%d, but the quote does not match full.diff"

	fixQuoteMismatch  = "copy an exact line from that hunk of full.diff, or delete the finding"
	fixQuoteAnchor    = "anchor the finding to the changed line in full.diff that introduces the defect, and quote that line"
	fixReceiptUnknown = "copy one row from hunks.tsv verbatim"
	fixReceiptQuote   = "quote one exact line from that hunk"
	fixReceiptKeys    = "re-fetch the bundle so full.diff carries keyable paths"
	fixSpecItem       = "quote one bullet, numbered line or table row from the spec verbatim"
	fixSpecEvidence   = "cite a path changed in full.diff and quote the exact line from it"
)

// checkBundleEvidence assembles the evidence input from the bundle's own bytes
// and runs the deterministic checks over one review document: quote matching
// against full.diff (A2), per-hunk read receipts (A3), and the spec checklist
// when the bundle carries `role: spec` sources (A1). Record's check path and
// the shared merge both call it, so `record --dry-run` and `record` cannot
// disagree about what the bundle proves.
func checkBundleEvidence(dir string, review *jq.Obj) *core.EvidenceReceipt {
	receipt, _ := evidencePassOf(dir, review)
	return receipt
}

// evidencePassOf runs the pass and returns both halves of its answer: the
// receipt the bundle stores and the problems the repair loop re-prompts with.
func evidencePassOf(dir string, review *jq.Obj) (*core.EvidenceReceipt, []Problem) {
	execution := panelcore.CheckExecution(dir, review)
	pass := newEvidencePass(dir, review)
	pass.classifyGating()
	pass.checkQuotes()
	pass.checkReceipts()
	pass.checkSpec()
	receipt := pass.result()
	execution.ApplyEvidence(receipt)
	return receipt, pass.problemList()
}

// reviewEvidenceOf runs the evidence pass over normalized review bytes. Bytes
// that do not decode carry no findings to verify, so the checker's own
// invalid-json problem stands alone and the evidence half is empty.
func reviewEvidenceOf(dir string, normalized []byte) (*core.EvidenceReceipt, []Problem) {
	doc, err := jq.DecodeOrdered(normalized)
	if err != nil {
		return nil, nil
	}
	review, ok := doc.(*jq.Obj)
	if !ok {
		return nil, nil
	}
	return evidencePassOf(dir, review)
}

// problemList renders the pass's problems as the published shape: a stable
// copy in the order the checks found them, as the pass's own problemCap already
// capped them.
func (p *evidencePass) problemList() []Problem {
	if len(p.problems) == 0 {
		return nil
	}
	out := make([]Problem, len(p.problems))
	copy(out, p.problems)
	return out
}

// evidencePass carries one pass: the parsed bundle bytes, the review document,
// and the outcomes the receipt and problem list are built from.
type evidencePass struct {
	review    *jq.Obj
	idx       *evidence.DiffIndex
	run       *config.RunState
	spec      *evidence.SpecCoverage
	gating    []bool
	quotes    []core.EvidenceQuote
	problems  []Problem
	errorGaps []string
	warnGaps  []string
	dir       string
	head      string
	sha       string
	receipts  evidence.ReceiptCoverage
	drift     bool
	unkeyable bool
}

// newEvidencePass reads the bundle bytes: full.diff parsed once into the A2
// line index, the hunks.tsv drift check, the reviewed head and diff digest, and
// the captured run state that carries spec_coverage_min and the block clauses.
func newEvidencePass(dir string, review *jq.Obj) *evidencePass {
	diff, err := os.ReadFile(dir + diffSuffix)
	pass := &evidencePass{
		review: review,
		dir:    dir,
		head:   bundleCheckHead(dir),
		sha:    sha256File(dir + diffSuffix),
		run:    bundleRunState(dir),
		idx:    evidence.IndexDiff(diff),
	}
	pass.drift = err == nil && hunkIndexDrift(dir, diff)
	return pass
}

// bundleRunState reads the run state the bundle pinned, or nil when it pinned
// none: an unpinned bundle carries no captured policy to enforce.
func bundleRunState(dir string) *config.RunState {
	rs, found, err := config.ReadRunState(dir)
	if err != nil || !found {
		return nil
	}
	return rs
}

// hunkIndexDrift reports whether the fetch-time hunks.tsv snapshot disagrees
// with full.diff. A missing snapshot asserts nothing, so it never drifts.
func hunkIndexDrift(dir string, diff []byte) bool {
	rows, err := hunks.Load(dir)
	if err != nil || len(rows) == 0 {
		return false
	}
	return evidence.HasHunkIndexDrift(diff, rows)
}

// classifyGating marks the findings that would block approval on their own:
// severity at or above the approval tier, or named by a captured block clause.
// Only a gated finding's evidence gap withholds approval; the rest stay
// warnings.
//
// The severity leg skips a finding the merge will withhold — an excluded path
// or a suppression receipt — because a withheld finding is invisible to the
// decision, which is what decides whether the hunk receipts are still owed. A
// finding a clause names keeps gating whatever withholds it: the decision's
// clause leg reads the unfiltered set, so that finding can still block.
func (p *evidencePass) classifyGating() {
	findings := reviewdoc.Findings(p.review)
	p.gating = make([]bool, len(findings))
	named := p.clauseNamed(findings)
	withheld := newWithheldFindings(p.dir, p.review)
	limit := core.TierRank(approval.DefaultApprovalTier)
	for i, item := range findings {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		severity, _ := m.Get(fieldSeverity)
		gates := core.TierRank(severity) <= limit && !withheld.covers(m)
		p.gating[i] = gates || named[findingViewKey(m)]
	}
}

// clauseNamed returns the finding views a captured block clause names, keyed by
// their locator fields. It reads the same clause table the merge reads, so a
// clause naming an informational finding still makes that finding gate its
// evidence.
func (p *evidencePass) clauseNamed(findings []any) map[string]bool {
	if p.run == nil || p.run.Resolved == nil {
		return nil
	}
	views := make([]config.FindingView, 0, len(findings))
	for _, item := range findings {
		if m, ok := item.(*jq.Obj); ok && m != nil {
			views = append(views, findingViewOf(m))
		}
	}
	blockers := config.CheckCompliance(p.run.Resolved, p.run.Root.Clauses, views)
	named := make(map[string]bool, len(blockers))
	for i := range blockers {
		named[findingViewKeyOf(&blockers[i].Finding)] = true
	}
	return named
}

// findingViewOf adapts one review finding to the compliance shape. A review
// finding is open until a triage claim settles it, which happens later.
func findingViewOf(m *jq.Obj) config.FindingView {
	path, _ := m.Get(fieldPath)
	category, _ := m.Get(fieldCategory)
	severity, _ := m.Get(fieldSeverity)
	return config.FindingView{
		Path:     jq.R(path),
		Category: jq.R(category),
		Severity: jq.R(severity),
		Status:   statusOpen,
	}
}

// findingViewKey identifies one finding by the fields a clause reads.
func findingViewKey(m *jq.Obj) string {
	view := findingViewOf(m)
	return findingViewKeyOf(&view)
}

// findingViewKeyOf renders the clause-relevant fields as one map key.
func findingViewKeyOf(view *config.FindingView) string {
	return view.Path + "\x00" + view.Category + "\x00" + view.Severity + "\x00" + view.Status
}

// checkQuotes verifies every finding's quote and stamps the standing S4 stores
// in place of S1's constant unchecked write. The mismatch reason stays in the
// receipt; the enum carries only verified or unverified.
func (p *evidencePass) checkQuotes() {
	findings := reviewdoc.Findings(p.review)
	p.quotes = make([]core.EvidenceQuote, 0, len(findings))
	for i, item := range findings {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		p.checkOneQuote(i, m)
	}
}

// checkOneQuote verifies and stamps one finding, adding its problem when the
// quote does not verify.
func (p *evidencePass) checkOneQuote(index int, m *jq.Obj) {
	path, _ := m.Get(fieldPath)
	side, _ := m.Get(fieldSide)
	quote, _ := m.Get(fieldQuote)
	res := evidence.MatchQuote(p.idx, evidence.QuoteRequest{
		Path:      jq.R(path),
		Side:      canonicalSide(jq.R(side)),
		Quote:     jq.R(quote),
		Line:      intField(m, fieldLine),
		StartLine: intField(m, fieldStartLine),
	})
	m.Set(verificationKey, jq.NewObj(fieldQuote, res.Status))
	p.quotes = append(p.quotes, core.EvidenceQuote{
		Path:   jq.R(path),
		Side:   canonicalSide(jq.R(side)),
		Status: res.Status,
		Reason: res.Reason,
		Line:   intField(m, fieldLine),
	})
	if res.Verified() {
		return
	}
	p.add(fmt.Sprintf(findingsQuotePath, index), core.QuoteProblemCode(res.Reason),
		p.quoteLevel(index, res), p.quoteMessage(index, m, res), quoteFix(res.Reason))
}

// quoteMessage names the line and the machine reason the quote failed.
func (p *evidencePass) quoteMessage(index int, m *jq.Obj, res evidence.QuoteResult) string {
	path, _ := m.Get(fieldPath)
	line := intField(m, fieldLine)
	if core.QuoteProblemCode(res.Reason) == core.ProblemCodeQuoteMismatch {
		return fmt.Sprintf(msgQuoteMismatch, index+1, jq.R(path), line, res.Reason)
	}
	return fmt.Sprintf(msgQuoteNotCheckable, index+1, jq.R(path), line, res.Reason)
}

// quoteFix states the repair for the reason: a text mismatch re-quotes the
// line, and a non-checkable anchor moves to the diff.
func quoteFix(reason string) string {
	if core.QuoteProblemCode(reason) == core.ProblemCodeQuoteMismatch {
		return fixQuoteMismatch
	}
	return fixQuoteAnchor
}

// gatingLevel is error for a finding that would gate and warning for one that
// never could: a warning never withholds approval.
func (p *evidencePass) gatingLevel(index int) string {
	if index < len(p.gating) && p.gating[index] {
		return core.ProblemLevelError
	}
	return core.ProblemLevelWarning
}

// quoteLevel is gatingLevel with one carve-out: a quote the bundle cannot check
// at all, because the path is not in full.diff, stays a warning. The reviewer
// has no repair but deleting a finding worth reporting, and an unverified
// finding never gates on its own, so the escalation would only silence it.
func (p *evidencePass) quoteLevel(index int, res evidence.QuoteResult) string {
	if res.Reason == evidence.ReasonPathNotInBundle {
		return core.ProblemLevelWarning
	}
	return p.gatingLevel(index)
}

// canonicalSide publishes the side the matcher actually reads: an absent side
// means RIGHT, and an unknown side reads as RIGHT too.
func canonicalSide(side string) string {
	if side == evidence.SideLeft {
		return evidence.SideLeft
	}
	return evidence.SideRight
}

// intField reads one integer field, zero when it is absent or not a number.
func intField(m *jq.Obj, key string) int {
	value, _ := m.Get(key)
	n, _ := jq.IntOf(value)
	return n
}

// checkReceipts verifies the receipts and, when nothing else already withholds
// approval, requires a matched receipt for every non-exempt changed hunk.
func (p *evidencePass) checkReceipts() {
	rows := reviewdoc.ReceiptsTyped(p.review)
	inputs := make([]evidence.ReceiptInput, len(rows))
	for i := range rows {
		inputs[i] = evidence.ReceiptInput{Hunk: rows[i].Hunk, Quote: rows[i].Quote}
	}
	p.receipts = evidence.VerifyReceipts(p.idx, inputs, readcommon.Generated)
	p.receiptProblems()
	p.unkeyableDiffProblem()
}

// unkeyableDiffProblem reports a diff whose hunks carry no receipt key at all:
// a path with a tab or newline cannot be written as a hunks.tsv row, so full
// coverage of zero keys would certify a bundle nobody could read.
func (p *evidencePass) unkeyableDiffProblem() {
	if p.idx.HunkCount() == 0 || p.receipts.Total > 0 || p.receipts.Exempt > 0 {
		return
	}
	p.unkeyable = true
	p.add(receiptsPath, core.ProblemCodeReceiptMissing, core.ProblemLevelError, msgReceiptsUnkeyable, fixReceiptKeys)
}

// receiptProblems reports the receipt shortfall and every receipt that does not
// verify. A gated run reports them as warnings: they are stored and shaped, but
// findings already withhold the approval.
func (p *evidencePass) receiptProblems() {
	level := p.receiptLevel()
	for _, key := range p.receipts.Missing {
		problem := core.ReceiptMissingProblem(key)
		p.add(problem.Path, problem.Code, level, problem.Message, problem.Fix)
	}
	for i := range p.receipts.Checks {
		p.receiptProblem(&p.receipts.Checks[i])
	}
}

// receiptLevel is error while receipts are the last thing withholding approval
// and warning once the findings already do.
func (p *evidencePass) receiptLevel() string {
	if p.needsReceipts() {
		return core.ProblemLevelError
	}
	return core.ProblemLevelWarning
}

// needsReceipts reports whether a matched receipt per hunk is the only thing
// still withholding approval: no gating finding and no violated spec check.
func (p *evidencePass) needsReceipts() bool {
	return !p.anyGating() && !reviewdoc.HasSpecViolation(p.review)
}

// anyGating reports whether any finding would withhold approval on its own.
func (p *evidencePass) anyGating() bool {
	for _, gating := range p.gating {
		if gating {
			return true
		}
	}
	return false
}

// receiptProblem adds the problem for one receipt that does not verify. A
// duplicate row whose hunk another receipt already covers stays a warning.
func (p *evidencePass) receiptProblem(check *evidence.ReceiptCheck) {
	level := p.receiptLevel()
	if check.Matched || check.Reason == "" {
		return
	}
	if check.DuplicateMismatch {
		level = core.ProblemLevelWarning
	}
	switch check.Reason {
	case evidence.ReasonReceiptHunkUnknown:
		p.add(fmt.Sprintf(receiptHunkPath, check.Index), core.ProblemCodeReceiptHunkUnknown, level,
			fmt.Sprintf(msgReceiptUnknown, check.Hunk), fixReceiptUnknown)
	case evidence.ReasonQuoteTooLarge:
		p.add(fmt.Sprintf(receiptQuotePath, check.Index), core.ProblemCodeReceiptMismatch, level,
			fmt.Sprintf(msgReceiptLarge, check.Hunk, evidence.MaxQuoteLines), fixReceiptQuote)
	default:
		p.add(fmt.Sprintf(receiptQuotePath, check.Index), core.ProblemCodeReceiptMismatch, level,
			fmt.Sprintf(msgReceiptMismatch, check.Hunk, check.Reason), fixReceiptQuote)
	}
}

// checkSpec verifies the checklist when the bundle carries spec sources. A
// bundle without a spec role has no checklist, so nothing is required of it.
func (p *evidencePass) checkSpec() {
	blocks := specBlocks(p.dir)
	if len(blocks) == 0 {
		return
	}
	items := evidence.EnumerateSpecItems(blocks)
	rows := reviewdoc.SpecChecksTyped(p.review)
	inputs := make([]evidence.SpecCheckInput, len(rows))
	for i := range rows {
		inputs[i] = evidence.SpecCheckInput{
			Item:         rows[i].Item,
			Status:       rows[i].Status,
			EvidencePath: rows[i].Evidence.Path,
			Quote:        rows[i].Evidence.Quote,
			Line:         rows[i].Evidence.Line,
		}
	}
	cov := evidence.VerifySpecChecks(p.idx, items, inputs, p.coverageMin())
	p.spec = &cov
	p.specProblems(&cov)
}

// specProblems reports every check that does not verify and every enumerated
// item no check covers. Coverage below the threshold is that shortfall, so it
// needs no second problem.
func (p *evidencePass) specProblems(cov *evidence.SpecCoverage) {
	for i := range cov.Checks {
		p.specProblem(&cov.Checks[i])
	}
	for i, item := range cov.Missing {
		problem := core.SpecCheckMissingProblem(i+1, item.Text)
		p.add(problem.Path, problem.Code, problem.Level, problem.Message, problem.Fix)
	}
}

// specProblem adds one invalid check's problem: an unknown item, non-code
// evidence, or a quote that does not match the cited code.
func (p *evidencePass) specProblem(check *evidence.SpecCheckResult) {
	if check.Valid {
		return
	}
	switch check.Reason {
	case evidence.ReasonSpecItemUnknown:
		p.add(fmt.Sprintf(specItemPath, check.Index), core.ProblemCodeSpecItemUnknown,
			core.ProblemLevelError, fmt.Sprintf(msgSpecItemUnknown, check.Item), fixSpecItem)
	case evidence.ReasonSpecEvidenceNotCode:
		p.add(fmt.Sprintf(specEvidencePath, check.Index), core.ProblemCodeSpecEvidenceNotCode,
			core.ProblemLevelError, p.specNotCodeMessage(check), fixSpecEvidence)
	default:
		p.add(fmt.Sprintf(specQuotePath, check.Index), core.ProblemCodeSpecEvidenceMismatch,
			core.ProblemLevelError, p.specMismatchMessage(check), fixSpecEvidence)
	}
}

// specNotCodeMessage names the cited path and says which way it fails A8b: a
// test-output path is named as such, a path the diff does not carry is named as
// absent, and an ambiguous one is named as matching several diff sections.
func (p *evidencePass) specNotCodeMessage(check *evidence.SpecCheckResult) string {
	path := p.specEvidencePath(check.Index)
	switch check.QuoteReason {
	case evidence.ReasonPathNotInBundle:
		return fmt.Sprintf(msgSpecNotInDiff, check.Item, path)
	case evidence.ReasonAmbiguousPath:
		return fmt.Sprintf(msgSpecAmbiguous, check.Item, path)
	default:
		return fmt.Sprintf(msgSpecNotCode, check.Item, path)
	}
}

// specEvidencePath is the path one spec check cites, empty when the review
// carries no row at that index.
func (p *evidencePass) specEvidencePath(index int) string {
	rows := reviewdoc.SpecChecksTyped(p.review)
	if index < len(rows) {
		return rows[index].Evidence.Path
	}
	return ""
}

// specMismatchMessage names the cited code location and the failure reason.
func (p *evidencePass) specMismatchMessage(check *evidence.SpecCheckResult) string {
	rows := reviewdoc.SpecChecksTyped(p.review)
	if check.Index < len(rows) {
		return fmt.Sprintf(msgSpecMismatch, check.Item, p.specEvidencePath(check.Index), rows[check.Index].Evidence.Line)
	}
	return fmt.Sprintf(msgSpecMismatch, check.Item, "", 0)
}

// coverageMin is the captured spec threshold, falling back to the built-in
// default when the pinned config carries no positive percentage: a captured
// zero would silently disable the rule the operator believes is enforced.
func (p *evidencePass) coverageMin() int {
	if p.run != nil && p.run.Resolved != nil {
		if captured := p.run.Resolved.Defaults.Values.SpecCoverageMin; captured > 0 {
			return captured
		}
	}
	return evidence.DefaultSpecCoverageMin
}

// specBlocks returns the context.txt blocks whose manifest role is spec, in
// file order, indexed by their position in the file. A bundle with no spec
// source yields no block and therefore no checklist.
func specBlocks(dir string) []evidence.SpecBlock {
	raw, err := os.ReadFile(dir + contextTextSuffix)
	if err != nil {
		return nil
	}
	roles := contextRoles(dir)
	var blocks []evidence.SpecBlock
	for i, block := range parseContextBlocks(string(raw)) {
		if roles[block.source] != specRole {
			continue
		}
		blocks = append(blocks, evidence.SpecBlock{Index: i, Text: block.body})
	}
	return blocks
}

// contextRoles maps each manifest source to its role.
func contextRoles(dir string) map[string]string {
	entries := readcommon.ContextEntries(dir)
	roles := make(map[string]string, len(entries))
	for _, entry := range entries {
		roles[entry.Source] = entry.Role
	}
	return roles
}

// contextBlock is one fenced context.txt block: the source line the fetch
// writer put first, and the file bytes behind it.
type contextBlock struct {
	source string
	body   string
}

// parseContextBlocks splits context.txt on its untrusted fences. The first
// content line of each block is the source name the manifest keys.
func parseContextBlocks(text string) []contextBlock {
	const (
		openPrefix  = "<<<UNTRUSTED "
		closePrefix = "<<<END UNTRUSTED "
	)
	var blocks []contextBlock
	var current []string
	inBlock := false
	for line := range strings.SplitSeq(text, newline) {
		switch {
		case strings.HasPrefix(line, closePrefix):
			blocks = append(blocks, contextBlockOf(current))
			current, inBlock = nil, false
		case strings.HasPrefix(line, openPrefix):
			current, inBlock = nil, true
		case inBlock:
			current = append(current, line)
		}
	}
	return blocks
}

// contextBlockOf turns one block's collected lines into its source and body.
func contextBlockOf(lines []string) contextBlock {
	if len(lines) == 0 {
		return contextBlock{}
	}
	return contextBlock{source: lines[0], body: strings.Join(lines[1:], newline)}
}

// result assembles the receipt. Insufficiency is exactly the presence of an
// error-level problem: every level is set at its emission site from the gating
// decision, so the receipt cannot disagree with the problems the agent would be
// shown.
func (p *evidencePass) result() *core.EvidenceReceipt {
	if p.drift {
		p.errorGaps = append(p.errorGaps, msgEvidenceDrift)
	}
	return &core.EvidenceReceipt{
		Head:         p.head,
		DiffSHA256:   p.sha,
		Quotes:       p.quotes,
		Gaps:         p.gapList(),
		Receipts:     p.receiptSummary(),
		Spec:         p.specSummary(),
		Insufficient: p.insufficient(),
	}
}

// gapList is every gap sentence, errors first: the decision names the first gap
// as its reason, and a warning may never speak for a refusal. The set is the
// problem set either way, so a reader who reads all of them loses nothing.
func (p *evidencePass) gapList() []string {
	out := make([]string, 0, len(p.errorGaps)+len(p.warnGaps))
	out = append(out, p.errorGaps...)
	return append(out, p.warnGaps...)
}

// insufficient reports whether any evidence problem is an error: only an error
// withholds approval.
func (p *evidencePass) insufficient() bool {
	for i := range p.problems {
		if p.problems[i].Level == core.ProblemLevelError {
			return true
		}
	}
	return false
}

// receiptSummary renders the receipt coverage for the receipt.
func (p *evidencePass) receiptSummary() core.EvidenceReceipts {
	return core.EvidenceReceipts{
		Total:    p.receipts.Total,
		Covered:  p.receipts.Covered,
		Exempt:   p.receipts.Exempt,
		Complete: p.receipts.Complete && !p.unkeyable,
	}
}

// specSummary renders the checklist coverage for the receipt.
func (p *evidencePass) specSummary() core.EvidenceSpec {
	if p.spec == nil {
		return core.EvidenceSpec{Met: true}
	}
	return core.EvidenceSpec{
		Total:   p.spec.Total,
		Covered: p.spec.Covered,
		Min:     p.spec.Threshold,
		Met:     p.spec.CoverageMet,
	}
}

// add records one problem and its gap sentence, kept in the level the emission
// site decided. Past the problem cap the receipt still carries the counts and
// the gap list stops growing.
func (p *evidencePass) add(path, code, level, message, fix string) {
	if len(p.problems) >= problemCap {
		return
	}
	p.problems = append(p.problems, Problem{
		Path:    path,
		Code:    code,
		Level:   level,
		Message: message,
		Fix:     fix,
	})
	if level == core.ProblemLevelError {
		p.errorGaps = append(p.errorGaps, message)
		return
	}
	p.warnGaps = append(p.warnGaps, message)
}
