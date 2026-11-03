package fetchcore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// threadRecord is one normalized root comment: the shared contract every
// provider mapper fills. Line and side stay nil for conversation comments.
type threadRecord struct {
	line     *int
	id       string
	path     string
	side     string
	author   string
	kind     string
	body     string
	resolved bool
	outdated bool
}

// Thread kinds in the published threads.json enum.
const (
	threadKindIssue  = "issue-comment"
	threadKindReview = "review-comment"
)

// writeThreadsJSON writes the normalized feed beside the raw provider files.
// threads.json is additive: inline.json/reviews.json keep their exact bytes,
// and no verb reads this file yet.
func writeThreadsJSON(dir, provider string, truncated bool, threads []threadRecord) *core.RefusalError {
	wires := make([]any, 0, len(threads))
	for i := range threads {
		wires = append(wires, threadWire(&threads[i]))
	}
	doc := jq.NewObj(
		keySchema, schemaV1,
		keyProvider, provider,
		"truncated", truncated,
		"threads", wires,
	)
	if err := atomic.WriteFile(dir+suffixThreadsJSON, []byte(jq.PrettyOrdered(doc))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"could not write %s/threads.json: %v", dir, err)}
	}
	return nil
}

// threadWire renders one record in published key order.
func threadWire(t *threadRecord) *jq.Obj {
	var line, side any
	if t.line != nil {
		line = *t.line
	}
	if t.side != "" {
		side = t.side
	}
	return jq.NewObj(
		keyID, t.id,
		keyPath, t.path,
		keyLine, line,
		keySide, side,
		"resolved", t.resolved,
		"outdated", t.outdated,
		keyAuthor, t.author,
		"kind", t.kind,
		keyBody, t.body,
	)
}

// threadArray decodes one raw provider payload as a comment/thread array.
// Undecodable payloads read empty: the raw file is still kept, and a failed
// read already warned at its call site.
func threadArray(raw string) []any {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	return jq.Elements(doc)
}

// threadID renders a host id without the "null" jq fallback: a missing id
// is the empty string, so a record with no id can still be skipped cleanly.
func threadID(v any) string {
	if v == nil {
		return ""
	}
	if s := jq.R(v); s != nullStr {
		return s
	}
	return ""
}

// threadBody is the shared excerpt: headBytes keeps the first bytes and
// strips trailing newlines, so the file stays small while the raw payload
// keeps the full text.
func threadBody(v any) string {
	return headBytes(jq.StrOf(v), threadBodyExcerpt)
}

// threadLine reads a line number from the first numeric member present.
func threadLine(vals ...any) *int {
	for _, v := range vals {
		if v == nil {
			continue
		}
		n, ok := jq.PrefixInt(jq.R(v))
		if !ok {
			continue
		}
		return &n
	}
	return nil
}

// threadSide normalizes a host side label: LEFT/OLD reads LEFT, anything
// else non-empty reads RIGHT (GitHub uses both keySide and "original_side").
func threadSide(vals ...any) string {
	for _, v := range vals {
		switch s := jq.StrOf(v); s {
		case sideLeft, "OLD", "left", "old":
			return sideLeft
		case sideRight, "NEW", "right", "new":
			return sideRight
		}
	}
	return ""
}

// threadAuthor picks the first author handle a host payload carries.
func threadAuthor(m map[string]any) string {
	for _, key := range []string{keyAuthor, "user"} {
		if s := nestedAuthor(m[key]); s != "" {
			return s
		}
	}
	return ""
}

// nestedAuthor reads one author object/string in the host field order.
func nestedAuthor(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	m := objOrMap(v)
	for _, key := range []string{keyLogin, "nickname", "handle", "displayName", "display_name", "uniqueName"} {
		if s, ok := m[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// githubThreadRecords maps GitHub's REST reviews + review comments. REST
// carries no thread resolution, so resolved stays false; roots are the
// comments without in_reply_to_id, which keeps the agent's reply/re-resolve
// ledger anchored on the same ids it already uses.
func githubThreadRecords(reviews, inline string) []threadRecord {
	var out []threadRecord
	for _, e := range threadArray(inline) {
		if m := objOrMap(e); m != nil && m["in_reply_to_id"] == nil {
			t := githubInlineThread(m)
			out = appendThread(out, &t)
		}
	}
	out = appendBodyThreads(out, threadArray(reviews))
	return out
}

// githubInlineThread maps one GitHub review-comment root.
func githubInlineThread(m map[string]any) threadRecord {
	return threadRecord{
		id:       threadID(m[keyID]),
		path:     strOrEmpty(m, keyPath),
		line:     threadLine(m[keyLine], m["original_line"]),
		side:     threadSide(m[keySide], m["original_side"]),
		outdated: m["position"] == nil && m["original_position"] != nil,
		author:   threadAuthor(m),
		kind:     threadKindReview,
		body:     threadBody(m[keyBody]),
	}
}

// gitlabThreadRecords maps GitLab notes: system notes drop, DiffNotes carry
// the position, and the note's resolved flag is the thread state.
func gitlabThreadRecords(notes string) []threadRecord {
	list, ok := gitlabNoteList(notes)
	if !ok {
		return nil
	}
	var out []threadRecord
	for _, e := range list {
		if t, ok := gitlabThread(objOrMap(e)); ok {
			out = append(out, t)
		}
	}
	return out
}

// gitlabThread maps one GitLab note, reporting false when it is a system note
// or carries nothing a reader can use.
func gitlabThread(m map[string]any) (threadRecord, bool) {
	if m == nil || truthy(m["system"]) {
		return threadRecord{}, false
	}
	t := threadRecord{
		id: threadID(m[keyID]), resolved: truthy(m["resolved"]),
		author: threadAuthor(m), kind: threadKindIssue, body: threadBody(m[keyBody]),
	}
	if pos := nestedMap(m, "position"); pos != nil {
		t.kind = threadKindReview
		t.path = normStr(pos, "new_path", "old_path")
		t.line = threadLine(pos["new_line"], pos["old_line"])
		t.side = sideLabel(pos["new_line"] != nil, pos["old_line"] != nil)
		t.outdated = t.line == nil
	}
	return t, threadUsable(&t)
}

// bitbucketThreadRecords maps Bitbucket comments: deleted and unsubmitted
// (pending) comments drop, replies group under their root, and the root's
// resolution object is the thread state.
func bitbucketThreadRecords(comments []any) []threadRecord {
	var out []threadRecord
	for _, e := range comments {
		if t, ok := bitbucketThread(objOrMap(e)); ok {
			out = append(out, t)
		}
	}
	return out
}

// bitbucketThread maps one Bitbucket root comment.
func bitbucketThread(m map[string]any) (threadRecord, bool) {
	if m == nil || truthy(m["deleted"]) || truthy(m["pending"]) || nestedMap(m, "parent") != nil {
		return threadRecord{}, false
	}
	t := threadRecord{
		id: threadID(m[keyID]), resolved: truthy(m["resolution"]), author: threadAuthor(m),
		kind: threadKindIssue, body: threadBody(nestedMap(m, "content")["raw"]),
	}
	if inline := nestedMap(m, "inline"); inline != nil {
		t.kind = threadKindReview
		t.path = normStr(inline, keyPath)
		t.line = threadLine(inline["to"], inline["from"])
		t.side = sideLabel(inline["to"] != nil, inline["from"] != nil)
		t.outdated = truthy(inline["outdated"])
	}
	return t, threadUsable(&t)
}

// forgejoThreadRecords maps Forgejo reviews + their inline comments. Forgejo
// has no resolve/reply surface, so resolved stays false and the payload's
// null position marks an outdated anchor.
func forgejoThreadRecords(reviews, inline string) []threadRecord {
	out := appendBodyThreads(nil, threadArray(reviews))
	for _, e := range threadArray(inline) {
		if m := objOrMap(e); m != nil {
			t := forgejoInlineThread(m)
			out = appendThread(out, &t)
		}
	}
	return out
}

// forgejoInlineThread maps one Forgejo review comment.
func forgejoInlineThread(m map[string]any) threadRecord {
	return threadRecord{
		id: threadID(m[keyID]), path: strOrEmpty(m, keyPath),
		line:     threadLine(m[keyLine], m["original_line"]),
		side:     threadSide(m[keySide]),
		outdated: m["position"] == nil && m["original_position"] != nil,
		author:   threadAuthor(m),
		kind:     threadKindReview,
		body:     threadBody(m[keyBody]),
	}
}

// azureThreadRecords maps Azure threads: deleted threads drop, system or
// unpublished comments drop, and the thread status is the resolution state.
func azureThreadRecords(raw string) []threadRecord {
	var out []threadRecord
	for _, e := range threadArray(raw) {
		if t, ok := azureThread(objOrMap(e)); ok {
			out = append(out, t)
		}
	}
	return out
}

// azureThread maps one Azure thread.
func azureThread(m map[string]any) (threadRecord, bool) {
	if m == nil || truthy(m["isDeleted"]) {
		return threadRecord{}, false
	}
	root, ok := azureRootComment(m)
	if !ok {
		return threadRecord{}, false
	}
	t := threadRecord{
		id: threadID(m[keyID]), resolved: azureResolved(m["status"]),
		author: threadAuthor(root), kind: threadKindIssue, body: threadBody(root["content"]),
	}
	if ctx := nestedMap(m, "threadContext"); ctx != nil {
		right, left := nestedMap(ctx, "rightFileStart"), nestedMap(ctx, "leftFileStart")
		t.kind = threadKindReview
		t.path = normStr(ctx, "filePath")
		t.line = threadLine(right[keyLine], left[keyLine])
		t.side = sideLabel(right != nil, left != nil)
		t.outdated = t.line == nil
	}
	return t, true
}

// azureRootComment returns the first publishable human comment in a thread.
func azureRootComment(thread map[string]any) (map[string]any, bool) {
	for _, e := range jq.Elements(thread["comments"]) {
		m := objOrMap(e)
		if m == nil || truthy(m["isDeleted"]) || jq.StrOf(m["commentType"]) == "system" {
			continue
		}
		if jq.StrOf(m["publishedDate"]) == "" || threadBody(m["content"]) == "" {
			continue
		}
		return m, true
	}
	return nil, false
}

// azureResolved folds Azure's status: anything but active is settled. A
// missing status reads unresolved (fail-closed for a re-review).
func azureResolved(v any) bool {
	s := jq.StrOf(v)
	return s != "" && s != stateActive
}

// gitcafeThreadRecords maps GitCafe reviews + comments best-effort: the
// payload is not documented in this repo, so the generic fields the agent
// already sees in reviews.json/inline.json drive the record.
func gitcafeThreadRecords(reviews, comments string) []threadRecord {
	var out []threadRecord
	for _, raw := range []string{reviews, comments} {
		for _, e := range threadArray(raw) {
			if t, ok := gitcafeThread(objOrMap(e)); ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// gitcafeThread maps one GitCafe review/comment object.
func gitcafeThread(m map[string]any) (threadRecord, bool) {
	if m == nil || threadBody(m[keyBody]) == "" {
		return threadRecord{}, false
	}
	t := threadRecord{
		id: threadID(m[keyID]), path: strOrEmpty(m, keyPath),
		line:   threadLine(m[keyLine], m["new_line"], m["old_line"]),
		side:   threadSide(m[keySide]),
		author: threadAuthor(m), kind: threadKindIssue,
		body: threadBody(m[keyBody]),
	}
	if t.path != "" {
		t.kind = threadKindReview
	}
	t.resolved = truthy(m["resolved"])
	t.outdated = truthy(m["outdated"])
	return t, true
}

// appendBodyThreads appends the conversation comments that carry a body.
func appendBodyThreads(out []threadRecord, elems []any) []threadRecord {
	for _, e := range elems {
		if t, ok := bodyThread(objOrMap(e)); ok {
			out = append(out, t)
		}
	}
	return out
}

// bodyThread maps one conversation comment, requiring a body so an empty
// review record never becomes a phantom thread.
func bodyThread(m map[string]any) (threadRecord, bool) {
	body := threadBody(m[keyBody])
	if body == "" {
		return threadRecord{}, false
	}
	return threadRecord{
		id: threadID(m[keyID]), author: threadAuthor(m),
		kind: threadKindIssue, body: body,
	}, true
}

// appendThread drops an all-empty record and appends the rest.
func appendThread(out []threadRecord, t *threadRecord) []threadRecord {
	if !threadUsable(t) {
		return out
	}
	return append(out, *t)
}

// threadUsable reports whether a record carries an anchor or text; an id
// alone names nothing a reader can act on.
func threadUsable(t *threadRecord) bool {
	return t.path != "" || t.body != ""
}

// sideLabel folds host presence flags to the published side label.
func sideLabel(hasRight, hasLeft bool) string {
	switch {
	case hasRight:
		return sideRight
	case hasLeft:
		return sideLeft
	default:
		return ""
	}
}
