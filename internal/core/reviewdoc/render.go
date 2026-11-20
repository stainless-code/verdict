package reviewdoc

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	maxSummaryRunes    = 2000
	maxSpecItemRunes   = 300
	maxBriefTitleRunes = 100
	maxEvidenceGaps    = 5
	checkedShown       = 7
	briefFenceMin      = 4
	hunkTailParts      = 3
	lineBreak          = "\n"
	paraBreak          = "\n\n"
	bulletPrefix       = "- "
	commaSep           = ", "
	spaceStr           = " "
	statusMet          = "met"
	statusViolated     = "violated"
	unverifiedTag      = "(unverified — not counted)"
	pathNotIndexedPref = "path not in diff index"
)

// RenderInput carries settled merge state for one deterministic body render.
type RenderInput struct {
	Review       *jq.Obj
	Approval     *core.ApprovalReceipt
	Evidence     *core.EvidenceReceipt
	Verification *core.PanelSummary
	Risk         *core.RiskClaim
	Entries      []any
	Kept         []any
	Moved        []any
	SpecChecks   []core.SpecCheck
	Banners      []string
	Basis        string
	Security     string
	BaseURL      string
	HeadFull     string
	Now          string
	BundleMeta   BundleMeta
}

// Render renders the deterministic review body from settled findings,
// receipts, spec checks, metadata, and approval state.
func Render(in *RenderInput) string {
	if in == nil {
		return ""
	}
	kept := visibleRanked(in.Kept)
	moved := visibleRanked(in.Moved)
	links := newLinkTable(in.BaseURL, in.HeadFull)

	var blocks []string
	blocks = appendBanners(blocks, in.Banners)
	blocks = append(blocks, renderLeadBlock(in, len(kept)+len(moved)))
	if summary := renderSummaryBlock(in.Review); summary != "" {
		blocks = append(blocks, summary)
	}
	blocks = append(blocks, renderMetadataComment(&in.BundleMeta))
	blocks = appendBodySections(blocks, in, kept, moved, links)
	if defs := links.definitions(); len(defs) > 0 {
		blocks = append(blocks, strings.Join(defs, lineBreak))
	}
	return strings.Join(blocks, paraBreak)
}

func appendBanners(blocks, banners []string) []string {
	for _, b := range banners {
		if trimmed := strings.TrimSpace(b); trimmed != "" {
			blocks = append(blocks, trimmed)
		}
	}
	return blocks
}

func appendBodySections(blocks []string, in *RenderInput, kept, moved []*jq.Obj, links *linkTable) []string {
	if isInsufficient(in.Approval) {
		blocks = append(blocks, renderEvidenceGaps(in.Approval, in.Evidence)...)
	}
	if len(kept) > 0 {
		blocks = append(blocks, renderFindingsBlock(kept, links)...)
	}
	if len(moved) > 0 {
		blocks = append(blocks, renderMovedBlock(moved, links)...)
	}
	if isCleanRender(in.Approval, len(kept)+len(moved)) {
		blocks = append(blocks, renderCheckedBlock(&in.BundleMeta, in.Evidence)...)
	}
	return append(blocks, renderSpecChecksBlock(in.SpecChecks, in.Evidence, links)...)
}

func isInsufficient(a *core.ApprovalReceipt) bool {
	return a != nil && a.Insufficient
}

func isCleanRender(a *core.ApprovalReceipt, visibleCount int) bool {
	if visibleCount > 0 {
		return false
	}
	return a == nil || (a.Approved && !a.Insufficient)
}

func renderLeadBlock(in *RenderInput, visibleCount int) string {
	lines := []string{selectLeadLine(in.Approval, visibleCount)}
	if dec := decisionLine(in.Approval); dec != "" {
		lines = append(lines, dec)
	}
	if panel := panelLine(in.Verification); panel != "" {
		lines = append(lines, panel)
	}
	if sec := safetyLine(in); sec != "" {
		lines = append(lines, sec)
	}
	if risk := riskLine(in.Risk); risk != "" {
		lines = append(lines, risk)
	}
	if basis := basisLine(in); basis != "" {
		lines = append(lines, basis)
	}
	return strings.Join(lines, lineBreak)
}

func selectLeadLine(a *core.ApprovalReceipt, visibleCount int) string {
	switch {
	case a != nil && a.Insufficient:
		return strings.TrimSuffix(EvidenceInsufficientLead, lineBreak)
	case a != nil && !a.Approved:
		return strings.TrimSuffix(AttentionLead, lineBreak)
	case visibleCount > 0 && a != nil && a.Approved:
		return strings.TrimSuffix(MinorLead, lineBreak)
	case visibleCount > 0:
		return strings.TrimSuffix(AttentionLead, lineBreak)
	default:
		return strings.TrimSuffix(CleanLead, lineBreak)
	}
}

func decisionLine(a *core.ApprovalReceipt) string {
	if a == nil || (a.Approved && !a.Insufficient) {
		return ""
	}
	reason := untrusted.InlineProse(a.Reason)
	if reason == "" {
		reason = "approval withheld"
	}
	if clauses := cleanClauses(a.Clauses); len(clauses) > 0 {
		return "> Decision: withheld — " + reason + " (clauses: " + strings.Join(clauses, commaSep) + ")"
	}
	return "> Decision: withheld — " + reason
}

func cleanClauses(clauses []string) []string {
	var clean []string
	for _, c := range clauses {
		if s := untrusted.InlineProse(c); s != "" {
			clean = append(clean, s)
		}
	}
	return clean
}

func panelLine(summary *core.PanelSummary) string {
	if summary == nil || summary.Confirmed+summary.Contested == 0 {
		return ""
	}
	total := summary.Single + summary.Confirmed + summary.Contested
	return fmt.Sprintf("> ℹ️ %d findings · %d contested by a verifier", total, summary.Contested)
}

func safetyLine(in *RenderInput) string {
	raw := in.Security
	if raw == "" && in.Review != nil {
		if v, ok := in.Review.Get("security_summary"); ok {
			raw = jq.StrOf(v)
		}
	}
	if clean := untrusted.InlineProse(raw); clean != "" {
		return "> Safety: " + clean
	}
	return ""
}

func riskLine(r *core.RiskClaim) string {
	if r == nil {
		return ""
	}
	level := untrusted.InlineProse(r.Level)
	if level == "" {
		return ""
	}
	line := "> Risk: " + level
	if rat := untrusted.InlineProse(r.Rationale); rat != "" {
		line += " — " + rat
	}
	if r.Stale {
		line += " (stale)"
	}
	return line
}

func basisLine(in *RenderInput) string {
	basis := in.Basis
	if basis == "" && in.Review != nil {
		basis = provenance.FormatBasisLine(in.Review, in.Entries, in.Now)
	}
	if basis == "" {
		return ""
	}
	return "> Basis: " + basis
}

func renderSummaryBlock(review *jq.Obj) string {
	raw := Summary(review)
	if raw == "" {
		return ""
	}
	return untrusted.Paragraph(untrusted.CapRunes(raw, maxSummaryRunes))
}

func visibleRanked(items []any) []*jq.Obj {
	if len(items) == 0 {
		return nil
	}
	visible := filterVisibleObjects(items)
	ranked := readcommon.Rank(visible, readcommon.StatusAll, readcommon.KindAll)
	return unpackRankedObjects(ranked)
}

func filterVisibleObjects(items []any) []any {
	visible := make([]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil || readcommon.WithheldSource(m) != "" {
			continue
		}
		visible = append(visible, m)
	}
	return visible
}

func unpackRankedObjects(ranked []readcommon.Ranked) []*jq.Obj {
	out := make([]*jq.Obj, 0, len(ranked))
	for _, r := range ranked {
		if m, ok := r.Entry.(*jq.Obj); ok {
			out = append(out, m)
		}
	}
	return out
}

func renderEvidenceGaps(a *core.ApprovalReceipt, ev *core.EvidenceReceipt) []string {
	if ev == nil {
		msg := "evidence receipt missing"
		if a != nil && strings.TrimSpace(a.Reason) != "" {
			msg = untrusted.InlineProse(a.Reason)
		}
		return []string{"**Evidence gaps**", bulletPrefix + msg}
	}
	bullets := appendEvidenceTallies(nil, ev)
	bullets = appendGapList(bullets, ev.Gaps)
	if len(bullets) == 0 {
		bullets = append(bullets, bulletPrefix+"evidence checks insufficient")
	}
	return []string{"**Evidence gaps**", strings.Join(bullets, lineBreak)}
}

func appendEvidenceTallies(bullets []string, ev *core.EvidenceReceipt) []string {
	if line := quoteTallyLine(ev.Quotes); line != "" {
		bullets = append(bullets, line)
	}
	if line := receiptTallyLine(ev.Receipts); line != "" {
		bullets = append(bullets, line)
	}
	if ev.Spec.Total > 0 {
		bullets = append(bullets, fmt.Sprintf("- Spec: %d/%d items covered (min %d)", ev.Spec.Covered, ev.Spec.Total, ev.Spec.Min))
	}
	return bullets
}

func quoteTallyLine(quotes []core.EvidenceQuote) string {
	if len(quotes) == 0 {
		return ""
	}
	verified := 0
	for i := range quotes {
		if quotes[i].Status == "verified" {
			verified++
		}
	}
	return fmt.Sprintf("- Quotes: %d/%d finding quotes verified", verified, len(quotes))
}

func receiptTallyLine(r core.EvidenceReceipts) string {
	if r.Total <= 0 {
		return ""
	}
	line := fmt.Sprintf("- Receipts: %d/%d changed hunks verified", r.Covered, r.Total)
	if r.Exempt > 0 {
		line += fmt.Sprintf(" (%d exempt)", r.Exempt)
	}
	return line
}

func appendGapList(bullets, gaps []string) []string {
	if len(gaps) == 0 {
		return bullets
	}
	bullets = append(bullets, "- Gaps:")
	shown := min(len(gaps), maxEvidenceGaps)
	for i := range shown {
		bullets = append(bullets, "  - "+formatGapText(gaps[i]))
	}
	if len(gaps) > maxEvidenceGaps {
		bullets = append(bullets, fmt.Sprintf("  - (+%d more — see record.json)", len(gaps)-maxEvidenceGaps))
	}
	return bullets
}

func formatGapText(gap string) string {
	if !strings.Contains(gap, "\t") {
		return untrusted.InlineProse(gap)
	}
	return untrusted.InlineProse(formatTabHunkKey(gap))
}

// formatTabHunkKey rewrites `path\tSIDE\tstart\tend` fragments inside a gap
// string into readable `path:SIDE:start-end` form before prose sanitization.
func formatTabHunkKey(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		tab := strings.IndexByte(s[i:], '\t')
		if tab < 0 {
			out.WriteString(s[i:])
			break
		}
		out.WriteString(s[i : i+tab])
		rest := s[i+tab+1:]
		nextParts := strings.SplitN(rest, "\t", hunkTailParts)
		if len(nextParts) == hunkTailParts {
			endTok, tail := splitFirstSpace(nextParts[2])
			out.WriteString(":" + nextParts[0] + ":" + nextParts[1] + "-" + endTok)
			i = len(s) - len(tail)
			continue
		}
		out.WriteByte(' ')
		i += tab + 1
	}
	return out.String()
}

func splitFirstSpace(s string) (head, tail string) {
	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		return s[:idx], s[idx:]
	}
	return s, ""
}

func renderFindingsBlock(kept []*jq.Obj, links *linkTable) []string {
	return []string{"**Findings**", joinFindingItems(kept, links, false)}
}

func renderMovedBlock(moved []*jq.Obj, links *linkTable) []string {
	intro := "The following points fell outside the diff hunks for the reviewed SHA, so they post here instead of inline:"
	return []string{core.MovedSectionHeading, intro, joinFindingItems(moved, links, true)}
}

func joinFindingItems(items []*jq.Obj, links *linkTable, moved bool) string {
	var body strings.Builder
	prevHadDetails := false
	for i, m := range items {
		line, details := formatFindingItem(i+1, m, links, moved)
		if i > 0 {
			writeItemSep(&body, prevHadDetails || details != "")
		}
		body.WriteString(line)
		if details != "" {
			body.WriteString(paraBreak)
			body.WriteString(details)
		}
		prevHadDetails = details != ""
	}
	return body.String()
}

func writeItemSep(body *strings.Builder, blankLine bool) {
	if blankLine {
		body.WriteString(paraBreak)
		return
	}
	body.WriteByte('\n')
}

func formatFindingItem(num int, m *jq.Obj, links *linkTable, moved bool) (line, details string) {
	path := jq.StrOf(fieldVal(m, "path"))
	startLine, _ := jq.IntOf(fieldVal(m, "start_line"))
	endLine, _ := jq.IntOf(fieldVal(m, "line"))
	dropReason := untrusted.InlineProse(jq.StrOf(fieldVal(m, "dropReason")))
	allowLink := !moved || !strings.HasPrefix(dropReason, pathNotIndexedPref)
	anchor := links.anchor(path, startLine, endLine, allowLink)
	claim := untrusted.InlineProse(findingClaim(m))

	line = buildFindingLine(num, m, anchor, claim, dropReason, moved)
	indent := findingDetailsIndent(moved)
	return line, renderBriefDetails(m, path, startLine, endLine, claim, indent, links)
}

func buildFindingLine(num int, m *jq.Obj, anchor, claim, dropReason string, moved bool) string {
	var sb strings.Builder
	writeItemPrefix(&sb, num, moved)
	writeSeverity(&sb, jq.StrOf(fieldVal(m, "severity")))
	sb.WriteString(anchor)
	if meta := findingMetaParen(m); meta != "" {
		sb.WriteString(" (" + meta + ")")
	}
	if isUnverifiedQuote(m) {
		sb.WriteString(spaceStr + unverifiedTag)
	}
	sb.WriteString(" — " + claim)
	if moved {
		sb.WriteString(" (" + firstNonEmpty(dropReason, "outside reviewed hunks") + ")")
	}
	return sb.String()
}

func writeItemPrefix(sb *strings.Builder, num int, moved bool) {
	if moved {
		sb.WriteString(bulletPrefix)
		return
	}
	sb.WriteString(strconv.Itoa(num) + ". ")
}

func findingDetailsIndent(moved bool) string {
	if moved {
		return "  "
	}
	return "   "
}

func writeSeverity(sb *strings.Builder, sev string) {
	clean := untrusted.InlineProse(sev)
	if clean == "" {
		return
	}
	if emoji := severityEmoji(clean); emoji != "" {
		sb.WriteString(emoji + spaceStr)
	}
	sb.WriteString("**" + clean + "** ")
}

func severityEmoji(sev string) string {
	switch sev {
	case "critical":
		return "🚨"
	case "important":
		return "⚠️"
	case "informational":
		return "ℹ️"
	default:
		return ""
	}
}

func findingMetaParen(m *jq.Obj) string {
	var parts []string
	if rigor := jq.StrOf(fieldVal(m, "rigor")); rigor != "" {
		parts = append(parts, untrusted.CodeSpan(rigor))
	}
	if cat := jq.StrOf(fieldVal(m, "category")); cat != "" {
		parts = append(parts, untrusted.CodeSpan(cat))
	}
	if rules := findingKBRules(m); len(rules) > 0 {
		parts = append(parts, "kb: "+strings.Join(rules, commaSep))
	}
	return strings.Join(parts, commaSep)
}

func findingKBRules(m *jq.Obj) []string {
	arr, ok := fieldVal(m, "kb").([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s := untrusted.InlineProse(jq.StrOf(item)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func findingClaim(m *jq.Obj) string {
	if v, ok := m.Get("claim"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return jq.StrOf(fieldVal(m, "body"))
}

func isUnverifiedQuote(m *jq.Obj) bool {
	v, ok := m.Get("verification")
	if !ok || v == nil {
		return false
	}
	rec, ok := v.(*jq.Obj)
	if !ok || rec == nil {
		return false
	}
	q, _ := rec.Get("quote")
	s, ok := q.(string)
	return !ok || s != "verified"
}

func renderBriefDetails(m *jq.Obj, findingPath string, findingStart, findingEnd int, claim, indent string, links *linkTable) string {
	b := brief.FromRecord(m)
	if b == nil {
		return ""
	}
	inner := renderBriefMarkdown(b, findingPath, findingStart, findingEnd, claim, links)
	fence := untrusted.FenceFor(inner, briefFenceMin)
	lines := []string{
		indent + "<details><summary>Technical details</summary>",
		"",
		indent + fence + "markdown",
	}
	for ln := range strings.SplitSeq(inner, lineBreak) {
		if ln == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, indent+ln)
	}
	lines = append(lines, indent+fence, "", indent+"</details>")
	return strings.Join(lines, lineBreak)
}

func renderBriefMarkdown(b *core.Brief, findingPath string, findingStart, findingEnd int, claim string, links *linkTable) string {
	sections := []string{"# " + briefTitle(claim)}
	if sites := renderBriefSites(b.Sites, findingPath, findingStart, findingEnd, links); sites != "" {
		sections = append(sections, sites)
	}
	sections = append(sections, "## Required outcome\n- "+untrusted.InlineProse(b.Outcome))
	if app := untrusted.InlineProse(b.Approach); app != "" {
		sections = append(sections, "## Suggested approach\n- "+app)
	}
	if qs := renderBriefQuestions(b.Questions); qs != "" {
		sections = append(sections, qs)
	}
	return strings.Join(sections, paraBreak)
}

func renderBriefSites(sites []core.BriefSite, findingPath string, findingStart, findingEnd int, links *linkTable) string {
	if len(sites) == 0 {
		return ""
	}
	lines := make([]string, 0, len(sites)+1)
	lines = append(lines, "## Affected sites")
	for _, site := range sites {
		start, end := briefSiteRange(site, findingPath, findingStart, findingEnd)
		lines = append(lines, bulletPrefix+links.anchor(site.Path, start, end, true))
	}
	return strings.Join(lines, lineBreak)
}

func renderBriefQuestions(questions []string) string {
	if len(questions) == 0 {
		return ""
	}
	lines := make([]string, 0, len(questions)+1)
	lines = append(lines, "## Open questions for human")
	for _, q := range questions {
		if clean := untrusted.InlineProse(q); clean != "" {
			lines = append(lines, bulletPrefix+clean)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, lineBreak)
}

func briefSiteRange(site core.BriefSite, findingPath string, findingStart, findingEnd int) (start, end int) {
	if site.Line == nil {
		return 0, 0
	}
	end = *site.Line
	if site.Path == findingPath && end == findingEnd && findingStart > 0 {
		start = findingStart
	}
	return start, end
}

func briefTitle(claim string) string {
	title := strings.TrimRight(strings.TrimSpace(claim), ".")
	if idx := strings.Index(title, ". "); idx > 0 {
		title = title[:idx]
	}
	if idx := strings.Index(title, commaSep); idx > 0 && len(title) > maxBriefTitleRunes {
		title = title[:idx]
	}
	title = untrusted.CapRunes(title, maxBriefTitleRunes)
	if title == "" {
		return "Finding details"
	}
	return title
}

func renderCheckedBlock(meta *BundleMeta, ev *core.EvidenceReceipt) []string {
	hasReceipts := ev != nil && ev.Receipts.Total > 0
	if len(meta.Files) == 0 && !hasReceipts {
		return nil
	}
	blocks := []string{"**What we checked**"}
	if len(meta.Files) > 0 {
		blocks = append(blocks, renderCheckedFiles(meta.Files))
	}
	if hasReceipts {
		line := fmt.Sprintf("Hunk receipts: %d/%d changed hunks verified.", ev.Receipts.Covered, ev.Receipts.Total)
		if ev.Receipts.Exempt > 0 {
			line = fmt.Sprintf("Hunk receipts: %d/%d changed hunks verified (%d exempt).",
				ev.Receipts.Covered, ev.Receipts.Total, ev.Receipts.Exempt)
		}
		blocks = append(blocks, line)
	}
	return blocks
}

func renderCheckedFiles(files []MetaFile) string {
	shown := min(len(files), checkedShown)
	lines := make([]string, 0, shown+1)
	for i := range shown {
		f := files[i]
		role := readcommon.PathRole(f.Path)
		if f.HasStats {
			lines = append(lines, fmt.Sprintf("- %s (+%d/-%d, %s)", untrusted.CodeSpan(f.Path), f.Additions, f.Deletions, role))
		} else {
			lines = append(lines, fmt.Sprintf("- %s (%s)", untrusted.CodeSpan(f.Path), role))
		}
	}
	if len(files) > checkedShown {
		lines = append(lines, fmt.Sprintf("(+%d more — see toc.txt)", len(files)-checkedShown))
	}
	return strings.Join(lines, lineBreak)
}

func renderSpecChecksBlock(checks []core.SpecCheck, ev *core.EvidenceReceipt, links *linkTable) []string {
	hasSpecEv := ev != nil && ev.Spec.Total > 0
	if len(checks) == 0 && !hasSpecEv {
		return nil
	}
	met, violated, na := countSpecStatuses(checks)
	head := fmt.Sprintf("**Spec checks** — %d met · %d violated · %d n/a", met, violated, na)
	if hasSpecEv {
		head += fmt.Sprintf(" (%d/%d spec items covered, min %d)", ev.Spec.Covered, ev.Spec.Total, ev.Spec.Min)
	}
	blocks := []string{head}
	if violated > 0 {
		blocks = append(blocks, renderViolatedSpecs(checks, links))
	}
	if len(checks) > 0 {
		blocks = append(blocks, renderSpecChecklistDetails(checks, links))
	}
	return blocks
}

func countSpecStatuses(checks []core.SpecCheck) (met, violated, na int) {
	for i := range checks {
		switch checks[i].Status {
		case statusMet:
			met++
		case statusViolated:
			violated++
		default:
			na++
		}
	}
	return met, violated, na
}

func renderViolatedSpecs(checks []core.SpecCheck, links *linkTable) string {
	var lines []string
	for i := range checks {
		if checks[i].Status == statusViolated {
			lines = append(lines, formatSpecCheckLine(&checks[i], links))
		}
	}
	return strings.Join(lines, lineBreak)
}

func renderSpecChecklistDetails(checks []core.SpecCheck, links *linkTable) string {
	label := "items"
	if len(checks) == 1 {
		label = "item"
	}
	lines := make([]string, 0, len(checks))
	for i := range checks {
		lines = append(lines, formatSpecCheckLine(&checks[i], links))
	}
	return fmt.Sprintf("<details><summary>Spec checklist (%d %s)</summary>\n\n%s\n\n</details>",
		len(checks), label, strings.Join(lines, lineBreak))
}

func formatSpecCheckLine(sc *core.SpecCheck, links *linkTable) string {
	icon := "➖"
	switch sc.Status {
	case statusMet:
		icon = "✅"
	case statusViolated:
		icon = "❌"
	}
	item := untrusted.InlineProse(untrusted.CapRunes(sc.Item, maxSpecItemRunes))
	if sc.Evidence.Path == "" {
		return bulletPrefix + icon + spaceStr + item
	}
	anchor := links.anchor(sc.Evidence.Path, 0, sc.Evidence.Line, true)
	return bulletPrefix + icon + spaceStr + item + " — " + anchor
}

func fieldVal(m *jq.Obj, key string) any {
	v, _ := m.Get(key)
	return v
}
