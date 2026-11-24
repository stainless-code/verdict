package postcore

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const paragraphBreak = "\n\n"

var evRefRe = regexp.MustCompile(`\]\[ev-(\d+)\]`)

// finalizeBody renders the settled body once all banners and approval
// decisions are known, splits at the hosting limit with per-part `[ev-N]`
// definitions, and appends the canonical footer.
func (st *state) finalizeBody() {
	if renderIn := st.merged.RenderInput; renderIn != nil {
		renderIn.Banners = append([]string{}, st.banners...)
		renderIn.Approval = st.merged.Approval
		body := reviewdoc.Render(renderIn)
		body = recordcore.AppendCoverageLine(body, st.merged.Coverage)
		body = st.splitPreFooter(body)
		opts := recordcore.FooterOpts(&st.req.Effective, st.req.Forces, st.req.RootPath(),
			st.dir, overrides.At(st.controls.OverridesDir()))
		body = recordcore.ApplyFooter(body, opts, st.warn)
		st.body = body
		st.merged.Body = body
		st.merged.Marker = recordcore.BodyMarker(body)
	}
}

func (st *state) buildPayloads() {
	kept, sigs := recordcore.PublicComments(st.merged.Entries, st.merged.Kept, st.merged.KeptSigs)
	st.comments = buildPostComments(kept, sigs)
	st.embedded = countEmbedded(kept)
	st.payloads = []string{postPayload(st.body, st.event, commitIDValue(st.commitID), st.comments)}
	n := len(st.extraParts) + 1
	for i, extra := range st.extraParts {
		st.payloads = append(st.payloads, postPayload(
			splitFollowHead(i+2, n)+extra, eventComment, commitIDValue(st.commitID), []any{}))
	}
	st.phash = contentDigest(strings.Join(append([]string{st.body}, st.extraParts...), "\n"), st.commitID, st.comments)
	// marked is the first comment exactly as a real post sends it: the body and
	// the hidden review marker. A dry run builds it too, so the digest a preview
	// pins is taken over the final payloads, never over the body before the
	// marker is appended.
	st.marked = st.body + paragraphBreak + fmt.Sprintf(reviewMarkerFormat, st.phash, st.commitID)
	if !st.flags.DryRun {
		st.body = st.marked
		st.payloads[0] = postPayload(st.body, st.event, commitIDValue(st.commitID), st.comments)
	}
	final := append([]string{postPayload(st.marked, st.event, commitIDValue(st.commitID), st.comments)}, st.payloads[1:]...)
	st.postedHash = payloadsDigest(final)
	st.payload = st.payloads[0]
}

func postPayload(body any, event string, commitID any, comments []any) string {
	obj := jq.NewObj(
		"body", body,
		"event", event,
		"commit_id", commitID,
		"comments", comments,
	)
	if body == nil {
		obj.Delete("body")
	}
	return obj.MarshalPretty()
}

func commitIDValue(commitID string) any {
	if commitID == "" {
		return nil
	}
	return commitID
}

// hostingBodyLimit caps one posted review body at GitHub's 65,536-byte limit.
const hostingBodyLimit = 65536

const maxSplitParts = 999

var splitNoteReserve = max(len(splitFirstNote(maxSplitParts+1)), len(splitFollowHead(maxSplitParts, maxSplitParts)))

const paraSep = paragraphBreak

func splitFirstNote(n int) string {
	return fmt.Sprintf("\n\n> ⚠️ Note: this review continues in %d follow-up "+
		"comment(s) below (split at the 65,536-char hosting limit).", n-1)
}

func splitFollowHead(k, n int) string {
	return fmt.Sprintf("> ℹ️ Note: review body continued (part %d/%d — split "+
		"at the 65,536-char hosting limit; see findings.json for the full text).\n\n", k, n)
}

func (st *state) splitPreFooter(body string) string {
	st.extraParts = nil
	block, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir, overrides.At(st.controls.OverridesDir())))
	if ferr != nil {
		block = ""
	}
	sep := ""
	if block != "" {
		sep = paragraphBreak
	}
	part1Reserve := len(sep) + len(block) + reviewMarkerReserve
	if len(body)+part1Reserve <= hostingBodyLimit {
		return body
	}
	budget := max(hostingBodyLimit-part1Reserve-splitNoteReserve, 1)
	content, defs := extractLinkDefs(body)
	parts := splitBodyWithLinks(content, defs, budget)
	if len(parts) <= 1 {
		return body
	}
	st.extraParts = parts[1:]
	head := parts[0] + splitFirstNote(len(parts))
	st.warn(fmt.Sprintf("body exceeds the 65,536-char hosting limit — split into %d "+
		"review comments (%d chars kept in the first)", len(parts), len(head)))
	return head
}

func splitBody(text string, budget int) []string {
	return splitBodyWithLinks(text, nil, budget)
}

type splitAcc struct {
	defs   map[int]string
	refs   map[int]bool
	cur    strings.Builder
	parts  []string
	budget int
}

func (a *splitAcc) add(chunk string) {
	cRefs := referencedLinkIDs(chunk, a.defs)
	sep := ""
	if a.cur.Len() > 0 {
		sep = paraSep
	}
	if a.cur.Len() > 0 && a.cur.Len()+len(sep)+len(chunk)+mergedDefsLen(a.refs, cRefs, a.defs) > a.budget {
		a.flush()
		sep = ""
	}
	a.cur.WriteString(sep)
	a.cur.WriteString(chunk)
	for _, id := range cRefs {
		a.refs[id] = true
	}
}

func (a *splitAcc) flush() {
	if a.cur.Len() == 0 {
		return
	}
	a.parts = append(a.parts, finishSplitPart(a.cur.String(), a.refs, a.defs))
	a.cur.Reset()
	a.refs = map[int]bool{}
}

func splitBodyWithLinks(text string, defs map[int]string, budget int) []string {
	budget = max(budget, 1)
	if len(defs) == 0 && len(text) <= budget {
		return []string{text}
	}
	acc := splitAcc{defs: defs, refs: map[int]bool{}, budget: budget}
	for _, c := range bodyChunksWithLinks(text, defs, budget) {
		acc.add(c)
	}
	acc.flush()
	return acc.parts
}

// bodyChunksWithLinks splits text into the chunks a part may hold. A maximal
// <details>…</details> region is one chunk candidate, so a part boundary never
// lands inside the block and GitHub never renders half a diagram.
func bodyChunksWithLinks(text string, defs map[int]string, budget int) []string {
	var out []string
	for _, unit := range bodyUnits(text) {
		out = append(out, paraChunksWithLinks(unit, defs, budget)...)
	}
	return out
}

// bodyUnits partitions text into the units the chunker works on: one paragraph
// outside a <details> region, one whole region while inside one. A region
// wider than the budget cannot stay whole, so the per-paragraph splitter cuts
// it like any oversized paragraph; an unbalanced closing tag never starts a
// region.
func bodyUnits(text string) []string {
	var units []string
	depth := 0
	for para := range strings.SplitSeq(text, paraSep) {
		if depth == 0 {
			units = append(units, para)
			depth = max(detailsDepth(para), 0)
			continue
		}
		units[len(units)-1] += paraSep + para
		depth = max(depth+detailsDepth(para), 0)
	}
	return units
}

// detailsDepth is a paragraph's net <details>/</details> line count, so a
// region spanning blank lines joins into one unit.
func detailsDepth(para string) int {
	depth := 0
	for line := range strings.SplitSeq(para, newline) {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "<details"):
			depth++
		case strings.HasPrefix(t, "</details"):
			depth--
		}
	}
	return depth
}

func paraChunksWithLinks(para string, defs map[int]string, budget int) []string {
	if len(defs) == 0 {
		return runeChunks(para, budget)
	}
	var out []string
	for len(para) > 0 {
		cut := fitChunkWithDefs(para, defs, budget)
		out = append(out, para[:cut])
		para = para[cut:]
	}
	return out
}

func fitChunkWithDefs(para string, defs map[int]string, budget int) int {
	if len(para)+defsLen(referencedLinkIDs(para, defs), defs) <= budget {
		return len(para)
	}
	lo := minNonSplittingCut(para, budget)
	hi := min(len(para), budget)
	best := fitRunes(para, lo)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		cut := fitRunes(para, mid)
		if cut+defsLen(referencedLinkIDs(para[:cut], defs), defs) <= budget {
			best = cut
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

func minNonSplittingCut(s string, budget int) int {
	spans := evRefTokenSpans(s)
	if len(spans) > 0 && spans[0][0] == 0 && spans[0][1] <= budget {
		return spans[0][1]
	}
	return 1
}

func extractLinkDefs(body string) (string, map[int]string) {
	paras := strings.Split(body, paraSep)
	var kept []string
	defs := map[int]string{}
	for _, p := range paras {
		if parsed, ok := parseLinkDefBlock(p); ok {
			maps.Copy(defs, parsed)
			continue
		}
		kept = append(kept, p)
	}
	if len(defs) == 0 {
		return body, nil
	}
	return strings.Join(kept, paraSep), defs
}

func parseLinkDefBlock(para string) (map[int]string, bool) {
	if para == "" {
		return nil, false
	}
	out := map[int]string{}
	for line := range strings.SplitSeq(para, newline) {
		id, ok := parseLinkDefLine(line)
		if !ok {
			return nil, false
		}
		out[id] = line
	}
	return out, len(out) > 0
}

func parseLinkDefLine(line string) (int, bool) {
	rest, ok := strings.CutPrefix(line, "[ev-")
	if !ok {
		return 0, false
	}
	numStr, _, ok := strings.Cut(rest, "]: ")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(numStr)
	return n, err == nil && n > 0
}

func referencedLinkIDs(s string, defs map[int]string) []int {
	if len(defs) == 0 || !strings.Contains(s, "][ev-") {
		return nil
	}
	seen := map[int]bool{}
	var ids []int
	for _, m := range evRefRe.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] || defs[n] == "" {
			continue
		}
		seen[n] = true
		ids = append(ids, n)
	}
	slices.Sort(ids)
	return ids
}

func mergedDefsLen(existing map[int]bool, add []int, defs map[int]string) int {
	if len(defs) == 0 || (len(existing) == 0 && len(add) == 0) {
		return 0
	}
	ids := make([]int, 0, len(existing)+len(add))
	for id := range existing {
		ids = append(ids, id)
	}
	for _, id := range add {
		if !existing[id] {
			ids = append(ids, id)
		}
	}
	return defsLen(ids, defs)
}

func defsLen(ids []int, defs map[int]string) int {
	if len(ids) == 0 {
		return 0
	}
	total := len(paraSep)
	for i, id := range ids {
		if i > 0 {
			total++
		}
		total += len(defs[id])
	}
	return total
}

func finishSplitPart(content string, refs map[int]bool, defs map[int]string) string {
	if len(refs) == 0 || len(defs) == 0 {
		return content
	}
	ids := make([]int, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		lines = append(lines, defs[id])
	}
	return content + paraSep + strings.Join(lines, newline)
}

func runeChunks(para string, budget int) []string {
	var out []string
	for len(para) > 0 {
		cut := fitRunes(para, budget)
		out = append(out, para[:cut])
		para = para[cut:]
	}
	return out
}

func fitRunes(s string, space int) int {
	if len(s) <= space {
		return len(s)
	}
	cut := space
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	if cut == 0 {
		_, w := utf8.DecodeRuneInString(s)
		return w
	}
	return adjustCutForEvRefs(s, cut)
}

func adjustCutForEvRefs(s string, cut int) int {
	for _, span := range evRefTokenSpans(s) {
		if span[0] < cut && cut < span[1] {
			if span[0] > 0 {
				return span[0]
			}
			return cut
		}
	}
	return cut
}

func evRefTokenSpans(s string) [][2]int {
	matches := evRefRe.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return nil
	}
	var spans [][2]int
	for _, m := range matches {
		if start, ok := findRefLabelStart(s, m[0]); ok {
			spans = append(spans, [2]int{start, m[1]})
		}
	}
	return spans
}

func findRefLabelStart(s string, closeIdx int) (int, bool) {
	for i := closeIdx - 1; i >= 0; i-- {
		switch {
		case s[i] == '\n':
			return 0, false
		case isEscapedAt(s, i):
		case s[i] == ']':
			return 0, false
		case s[i] == '[':
			return i, i < closeIdx-1
		}
	}
	return 0, false
}

func isEscapedAt(s string, i int) bool {
	bs := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		bs++
	}
	return bs%2 == 1
}

func buildPostComments(kept []any, sigs []string) []any {
	out := make([]any, 0, len(kept))
	for i, c := range kept {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sig := ""
		if i < len(sigs) {
			sig = sigs[i]
		}
		out = append(out, projectComment(m, sig))
	}
	return out
}

func projectComment(m *jq.Obj, sig string) *jq.Obj {
	out := jq.NewObj()
	for _, k := range []string{fieldPath, fieldLine, "side", fieldBody, fieldStartLine} {
		if v, has := m.Get(k); has && v != nil {
			out.Set(k, v)
		}
	}
	sugg, _ := m.Get(fieldSuggestion)
	if jq.R(jq.Alt(sugg, "")) != "" {
		bd, _ := m.Get(fieldBody)
		out.Set(fieldBody, jq.R(jq.Alt(bd, ""))+"\n\n```suggestion\n"+jq.R(sugg)+"\n```")
	}
	if line := readcommon.SourcesLine(m); line != "" {
		body, _ := out.Get(fieldBody)
		out.Set(fieldBody, jq.R(body)+paragraphBreak+line)
	}
	if sig != "" {
		body, _ := out.Get(fieldBody)
		out.Set(fieldBody, jq.R(jq.Alt(body, ""))+paragraphBreak+threadscore.FindingMarker(sig))
	}
	return out
}

func countEmbedded(kept []any) int {
	n := 0
	for _, c := range kept {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sugg, _ := m.Get(fieldSuggestion)
		if jq.R(jq.Alt(sugg, "")) != "" {
			n++
		}
	}
	return n
}

func splitNote(st *state) string {
	if len(st.payloads) <= 1 {
		return ""
	}
	return fmt.Sprintf(" (split into %d review comments at the hosting limit)", len(st.payloads))
}

func postCommentKeys(comments []any) string {
	if len(comments) == 0 {
		return ""
	}
	m, ok := comments[0].(*jq.Obj)
	if !ok || m == nil {
		return ""
	}
	return strings.Join(m.Keys, ",")
}

func (st *state) previewDryRun(footerText string) {
	st.human("dry-run: payload NOT posted. body chars: " +
		strconv.Itoa(len(st.body)) + splitNote(st) + ", payload comment keys: " + postCommentKeys(st.comments))
	if footerText != "" {
		st.human("footer preview:\n" + footerText)
	}
	if st.diagram.State != "absent" {
		st.human("diagram: " + st.diagram.State + " — " + st.diagram.Detail)
	}
	if st.summary != nil {
		st.human(formatSummaryDryRun(st.summary))
	}
}

func (st *state) dryRunResponse(ctx context.Context) (*Response, *core.RefusalError) {
	if st.summaryMode() == summaryComment {
		sum, ref := st.planSummary(ctx)
		if ref != nil {
			return nil, st.withWarnings(ref)
		}
		st.summary = sum
	}
	footerText, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir, overrides.At(st.controls.OverridesDir())))
	if ferr != nil {
		footerText = ""
	}
	var footerVal any
	if footerText != "" {
		footerVal = footerText
	}
	payloadHash := ""
	if st.req.JSONMode {
		payloadHash = st.phash
	} else {
		st.previewDryRun(footerText)
		st.printProvenanceTable()
	}
	payload, _, _ := st.floorPayload()
	nOpen, nDeferred, nAddressed := postCounts(payload)
	data := &core.PostData{
		Verification: st.merged.Verification,
		Withheld:     st.withholdingSummary(),
		DryRun:       true,
		Event:        st.event,
		CommitID:     st.commitID,
		Body: core.PostBody{
			Marker:  st.merged.Marker,
			Footer:  footerVal,
			Diagram: postDiagramValue(&st.diagram),
		},
		Anchors:     core.AnchorsSummary{Kept: st.merged.NKept, Moved: st.merged.NMoved},
		Suggestions: core.Suggestions{Embedded: st.embedded},
		Findings: core.PostFindings{
			Open: nOpen, Deferred: nDeferred, Addressed: nAddressed, File: st.findingsFile,
		},
		Items:            typedPostItems(payload),
		Coverage:         st.merged.Coverage.Receipt(),
		Summary:          st.summary,
		Confidence:       intOrNilPost(st.merged.Confidence),
		SecuritySummary:  strOrNilPost(st.merged.Security),
		Risk:             st.merged.Risk,
		PayloadHash:      payloadHash,
		Validation:       st.validationData(),
		ItemsRaw:         recordcore.EnvelopeItems(payload),
		RenderedComments: st.renderedComments(),
		RenderedHash:     st.postedHash,
	}
	prov := decodeProvenanceTyped(st.req.RootPath(), st.dir)
	if prov != nil {
		data.Provenance = *prov
	}
	st.applyProvenance(data)
	return &Response{
		Repo: st.req.Repo, Pr: st.req.Pr, At: st.now,
		Data:     data,
		Warnings: st.warnings,
		Next:     "payload NOT posted (dry-run)",
	}, nil
}

// RenderPostEnvelope builds the `post --json` envelope from a finished
// response. The CLI prints through this function and the tests decode the same
// bytes, so a change to the argument list moves both instead of letting a test
// render an approximation the CLI never emits.
func RenderPostEnvelope(resp *Response) *jq.Obj {
	return core.RenderEnvelope("post", resp.Repo, resp.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil)
}
