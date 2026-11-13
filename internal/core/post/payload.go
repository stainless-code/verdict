package postcore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const paragraphBreak = "\n\n"

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
	if !st.flags.DryRun {
		st.body += paragraphBreak + fmt.Sprintf(reviewMarkerFormat, st.phash, st.commitID)
		st.payloads[0] = postPayload(st.body, st.event, commitIDValue(st.commitID), st.comments)
	}
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

// hostingBodyLimit caps one posted review body: GitHub rejects review
// bodies past 65,536 characters wholesale (422), so the Go port splits an
// over-limit body across follow-up review comments instead of
// submitting a doomed review.
const hostingBodyLimit = 65536

const maxSplitParts = 999

var splitNoteReserve = max(len(splitFirstNote(maxSplitParts+1)), len(splitFollowHead(maxSplitParts, maxSplitParts)))

const (
	paraSep = paragraphBreak
)

func splitFirstNote(n int) string {
	return fmt.Sprintf("\n\n> ⚠️ Note: this review continues in %d follow-up "+
		"comment(s) below (split at the 65,536-char hosting limit).", n-1)
}

func splitFollowHead(k, n int) string {
	return fmt.Sprintf("> ℹ️ Note: review body continued (part %d/%d — split "+
		"at the 65,536-char hosting limit; see findings.json for the full text).\n\n", k, n)
}

func (st *state) splitPreFooter(body string) string {
	block, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir, overrides.At(st.controls.OverridesDir())))
	if ferr != nil {
		block = ""
	}
	sep := ""
	if block != "" {
		sep = paragraphBreak
	}
	reserve := len(sep) + len(block) + reviewMarkerReserve
	if len(body)+reserve <= hostingBodyLimit {
		return body
	}
	budget := max(hostingBodyLimit-reserve-splitNoteReserve, 1)
	parts := splitBody(body, budget)
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
	budget = max(budget, 1)
	if len(text) <= budget {
		return []string{text}
	}
	var parts []string
	var cur strings.Builder
	for _, c := range bodyChunks(text, budget) {
		sep := ""
		if cur.Len() > 0 {
			sep = paraSep
		}
		if cur.Len()+len(sep)+len(c) > budget {
			parts = append(parts, cur.String())
			cur.Reset()
			sep = ""
		}
		cur.WriteString(sep)
		cur.WriteString(c)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func bodyChunks(text string, budget int) []string {
	var out []string
	for para := range strings.SplitSeq(text, paraSep) {
		out = append(out, runeChunks(para, budget)...)
	}
	return out
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
	return cut
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

func (st *state) dryRunResponse(ctx context.Context) (*core.PostResponse, *core.RefusalError) {
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
		Items:           typedPostItems(payload),
		Coverage:        st.merged.Coverage.Receipt(),
		Summary:         st.summary,
		Confidence:      intOrNilPost(st.merged.Confidence),
		SecuritySummary: strOrNilPost(st.merged.Security),
		PayloadHash:     payloadHash,
		ItemsRaw:        recordcore.EnvelopeItems(payload),
	}
	prov := decodeProvenanceTyped(st.req.RootPath(), st.dir)
	if prov != nil {
		data.Provenance = *prov
	}
	return &core.PostResponse{
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
func RenderPostEnvelope(resp *core.PostResponse) *jq.Obj {
	return core.RenderEnvelope("post", resp.Repo, resp.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil)
}

func panelBody(body string, summary *core.PanelSummary) string {
	if summary == nil {
		return body
	}
	line := ""
	if summary.Confirmed+summary.Contested != 0 {
		line = fmt.Sprintf("> ℹ️ %d findings · %d contested by a verifier",
			summary.Single+summary.Confirmed+summary.Contested, summary.Contested)
	}
	body, found := replacePanelSummary(body, line)
	if found || line == "" {
		return body
	}
	lines := strings.Split(body, newline)
	for i, s := range lines {
		if strings.HasPrefix(s, "> ") {
			return strings.Join(append(append(lines[:i+1:i+1], line), lines[i+1:]...), newline)
		}
	}
	return line + paraSep + body
}

func replacePanelSummary(body, line string) (string, bool) {
	lines := strings.Split(body, newline)
	out := lines[:0]
	found := false
	for _, existing := range lines {
		if !isPanelSummary(existing) {
			out = append(out, existing)
			continue
		}
		if !found && line != "" {
			out = append(out, line)
		}
		found = true
	}
	return strings.Join(out, newline), found
}

func isPanelSummary(line string) bool {
	counts, ok := strings.CutPrefix(line, "> ℹ️ ")
	if !ok {
		return false
	}
	findings, rest, ok := strings.Cut(counts, " findings · ")
	if !ok {
		return false
	}
	contested, ok := strings.CutSuffix(rest, " contested by a verifier")
	return ok && decimalCount(findings) && decimalCount(contested)
}

func decimalCount(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0 && strconv.Itoa(n) == s
}
