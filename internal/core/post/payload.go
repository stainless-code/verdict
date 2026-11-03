package postcore

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/provenance"
)

// buildPayloads ports the payload construction: only GitHub-known comment
// keys ship (triage fields stay in findings.json), and suggestions render as
// fenced blocks inside body. The first payload carries the review (event,
// commit, inline comments); each follow-up carries one over-limit body part
// as a COMMENT with no inline comments.
// Pretty-printed like the shell's `jq -n` (insertion order, 2-space
// indent, no trailing newline — the command substitution strips it).
// The stub-log comparison covers these bytes exactly.
func (st *state) buildPayloads() {
	st.comments = buildPostComments(st.merged.Kept)
	st.embedded = countEmbedded(st.merged.Kept)
	st.payloads = []string{postPayload(st.body, st.event, commitIDValue(st.commitID), st.comments)}
	n := len(st.extraParts) + 1
	for i, extra := range st.extraParts {
		st.payloads = append(st.payloads, postPayload(
			splitFollowHead(i+2, n)+extra, eventComment, commitIDValue(st.commitID), []any{}))
	}
	st.payload = st.payloads[0]
}

// postPayload renders one reviews-POST payload in the shell's wire shape.
func postPayload(body, event string, commitID any, comments []any) string {
	return jq.NewObj(
		"body", body,
		"event", event,
		"commit_id", commitID,
		"comments", comments,
	).MarshalPretty()
}

// payloadBytes joins every posted payload for the duplicate guard: a single
// part joins to itself, so the marker stays byte-identical there.
func (st *state) payloadBytes() []byte {
	return []byte(strings.Join(st.payloads, "\n"))
}

// commitIDValue ports `commit_id:(if $commit=="" then null else $commit end)`.
func commitIDValue(commitID string) any {
	if commitID == "" {
		return nil
	}
	return commitID
}

// hostingBodyLimit caps one posted review body: GitHub rejects review
// bodies past 65,536 characters wholesale (422), so the Go port splits an
// over-limit body across follow-up review comments (audit 02) instead of
// submitting a doomed review.
const hostingBodyLimit = 65536

// maxSplitParts bounds the continuation-note reserve: the 32 MiB
// agent-input cap (R3) fits at most ~500 hosting-sized parts, so three
// digits always suffice.
const maxSplitParts = 999

// splitNoteReserve caps any continuation note, so the split budget needs no
// second pass: the first part always has room for its note, and follow-ups
// always have room for their headers.
var splitNoteReserve = max(len(splitFirstNote(maxSplitParts+1)), len(splitFollowHead(maxSplitParts, maxSplitParts)))

// paraSep splits body parts on paragraph boundaries.
const paraSep = "\n\n"

// splitFirstNote tells the reader the review continues in follow-ups.
func splitFirstNote(n int) string {
	return fmt.Sprintf("\n\n> ⚠️ Note: this review continues in %d follow-up "+
		"comment(s) below (split at the 65,536-char hosting limit).", n-1)
}

// splitFollowHead opens each follow-up part.
func splitFollowHead(k, n int) string {
	return fmt.Sprintf("> ℹ️ Note: review body continued (part %d/%d — split "+
		"at the 65,536-char hosting limit; see findings.json for the full text).\n\n", k, n)
}

// splitPreFooter enforces the hosting body limit as the shared merge's
// pre-footer hook: it runs after the diagram strips (entries already derive
// from the full body) and before the footer receipt, reserving the footer
// block plus its separator so the appended receipt always survives intact.
// An over-limit body splits into hosting-sized parts: the first part returns
// here (the footer still appends to it) and the rest stash for follow-up
// review POSTs. A body that fits with its footer passes through untouched.
func (st *state) splitPreFooter(body string) string {
	block, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir, overrides.At(st.controls.OverridesDir())))
	if ferr != nil {
		block = ""
	}
	sep := ""
	if block != "" {
		sep = "\n\n"
	}
	if len(body)+len(sep)+len(block) <= hostingBodyLimit {
		return body
	}
	budget := max(hostingBodyLimit-len(sep)-len(block)-splitNoteReserve, 1)
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

// splitBody chunks text into parts of at most budget bytes: over-long
// paragraphs split on rune boundaries first, then chunks pack greedily
// onto parts, so every part stays valid UTF-8.
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

// bodyChunks splits text into paragraph chunks of at most budget bytes,
// cutting over-long paragraphs on rune boundaries.
func bodyChunks(text string, budget int) []string {
	var out []string
	for para := range strings.SplitSeq(text, paraSep) {
		out = append(out, runeChunks(para, budget)...)
	}
	return out
}

// runeChunks cuts one paragraph into chunks of at most budget bytes on
// rune boundaries.
func runeChunks(para string, budget int) []string {
	var out []string
	for len(para) > 0 {
		cut := fitRunes(para, budget)
		out = append(out, para[:cut])
		para = para[cut:]
	}
	return out
}

// fitRunes returns the longest byte prefix of s that fits space and ends on
// a rune boundary. Callers keep space at or above the widest rune, so the
// walk-back below always lands.
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

// buildPostComments ports the post_comments projection: only GitHub-known
// comment keys ship, suggestions render as fenced blocks inside body, and
// null-valued keys drop (false values ship, like the shell's with_entries).
func buildPostComments(kept []any) []any {
	out := make([]any, 0, len(kept))
	for _, c := range kept {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		out = append(out, projectComment(m))
	}
	return out
}

// projectComment projects one kept comment onto the posted keys.
func projectComment(m *jq.Obj) *jq.Obj {
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
	return out
}

// countEmbedded ports the embedded-suggestion count over kept comments.
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

// splitNote names the follow-up count on the dry-run preview, empty when
// the review posts as one part.
func splitNote(st *state) string {
	if len(st.payloads) <= 1 {
		return ""
	}
	return fmt.Sprintf(" (split into %d review comments at the hosting limit)", len(st.payloads))
}

// postCommentKeys ports the dry-run comment-keys summary: the first
// comment's unsorted keys joined by commas ("" when no comments posted).
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

// dryRunResponse ports the dry-run exit: the payload preview on the human
// stream, or the dry-run envelope inputs (payload hash, footer and diagram
// disposition included). Nothing posts, nothing arms.
//
//nolint:revive // the JSON/human envelope branches mirror the shell's dry-run fork
func (st *state) dryRunResponse() (*core.PostResponse, *core.RefusalError) {
	footerText, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir, overrides.At(st.controls.OverridesDir())))
	if ferr != nil {
		footerText = ""
	}
	var footerVal any
	if footerText != "" {
		footerVal = footerText
	}
	if st.req.JSONMode {
		st.phash = sigSumOf(st.payloadBytes())
		nOpen, nDeferred, nAddressed := postCounts(st.merged.Entries)
		data := &core.PostData{
			DryRun:   true,
			Event:    st.event,
			CommitID: st.commitID,
			Body: core.PostBody{
				Marker:  st.merged.Marker,
				Footer:  footerVal,
				Diagram: postDiagramValue(&st.diagram),
			},
			Anchors:     core.AnchorsSummary{Kept: st.merged.NKept, Moved: st.merged.NMoved},
			Suggestions: core.Suggestions{Embedded: st.embedded},
			Findings: core.PostFindings{
				Open: stOpen(nOpen), Deferred: nDeferred, Addressed: nAddressed, File: st.findingsFile,
			},
			Items:           typedPostItems(st.merged.Entries),
			Confidence:      intOrNilPost(st.merged.Confidence),
			SecuritySummary: strOrNilPost(st.merged.Security),
			PayloadHash:     st.phash,
			ItemsRaw:        recordcore.EnvelopeItems(st.merged.Entries),
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
	st.human("dry-run: payload NOT posted. body chars: " + strconv.Itoa(len(st.body)) + splitNote(st) + ", payload comment keys: " + postCommentKeys(st.comments))
	if footerText != "" {
		st.human("footer preview:\n" + footerText)
	}
	if st.diagram.State != "absent" {
		st.human("diagram: " + st.diagram.State + " — " + st.diagram.Detail)
	}
	nOpen, nDeferred, nAddressed := postCounts(st.merged.Entries)
	data := &core.PostData{
		DryRun:   true,
		Event:    st.event,
		CommitID: st.commitID,
		Body: core.PostBody{
			Marker:  st.merged.Marker,
			Footer:  footerVal,
			Diagram: postDiagramValue(&st.diagram),
		},
		Anchors:     core.AnchorsSummary{Kept: st.merged.NKept, Moved: st.merged.NMoved},
		Suggestions: core.Suggestions{Embedded: st.embedded},
		Findings: core.PostFindings{
			Open: stOpen(nOpen), Deferred: nDeferred, Addressed: nAddressed, File: st.findingsFile,
		},
		Items:           typedPostItems(st.merged.Entries),
		Confidence:      intOrNilPost(st.merged.Confidence),
		SecuritySummary: strOrNilPost(st.merged.Security),
		PayloadHash:     "",
		ItemsRaw:        recordcore.EnvelopeItems(st.merged.Entries),
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
