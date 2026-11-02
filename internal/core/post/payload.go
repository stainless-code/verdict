package postcore

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

// buildPayload ports the payload construction: only GitHub-known comment
// keys ship (triage fields stay in findings.json), and suggestions render as
// fenced blocks inside body.
func (st *state) buildPayload() {
	st.comments = buildPostComments(st.merged.Kept)
	st.embedded = countEmbedded(st.merged.Kept)
	payload := jq.NewObj(
		"body", st.body,
		"event", st.event,
		"commit_id", commitIDValue(st.commitID),
		"comments", st.comments,
	)
	// Pretty-printed like the shell's `jq -n` (insertion order, 2-space
	// indent, no trailing newline — the command substitution strips it).
	// The stub-log comparison covers these bytes exactly.
	st.payload = payload.MarshalPretty()
}

// commitIDValue ports `commit_id:(if $commit=="" then null else $commit end)`.
func commitIDValue(commitID string) any {
	if commitID == "" {
		return nil
	}
	return commitID
}

// hostingBodyLimit caps a posted review body: GitHub rejects review bodies
// past 65,536 characters wholesale (422), so the Go port trims with a notice
// instead of submitting a doomed review. The shell keeps today's behavior
// (audit 02 folds into this port; graded Go-only until ticket 08).
const hostingBodyLimit = 65536

// trimNotice names the trim so the posted body stays actionable: the full
// text lives in findings.json, which the run already persisted.
const trimNotice = "\n\n> ⚠️ Note: body exceeded the 65,536-char hosting limit and was trimmed — see findings.json for the full text."

// trimmedBody enforces the hosting body limit as the shared merge's
// pre-footer hook: it runs after the diagram strips (entries already derive
// from the full body) and before the footer receipt, reserving the footer
// block plus its separator so the appended receipt always survives intact.
func (st *state) trimmedBody(body string) string {
	if len(body) <= hostingBodyLimit {
		return body
	}
	block, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir))
	if ferr != nil {
		block = ""
	}
	sep := ""
	if block != "" {
		sep = "\n\n"
	}
	budget := hostingBodyLimit - len(sep) - len(block) - len(trimNotice)
	head := body
	if len(head) > budget {
		head = head[:budget]
	}
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	st.warn(fmt.Sprintf("body exceeds the 65,536-char hosting limit — trimmed with a notice (%d chars kept)", len(head)))
	return head + trimNotice
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
		&st.req.Effective, st.req.Forces, st.req.RootPath(), st.dir))
	if ferr != nil {
		footerText = ""
	}
	var footerVal any
	if footerText != "" {
		footerVal = footerText
	}
	if st.req.JSONMode {
		st.phash = sigSumOf([]byte(st.payload))
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
	st.human("dry-run: payload NOT posted. body chars: " + itoa(len(st.body)) + ", payload comment keys: " + postCommentKeys(st.comments))
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
