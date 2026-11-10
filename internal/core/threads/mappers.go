package threadscore

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/forgejo"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	keyAuthor    = "author"
	keyBody      = "body"
	keyComments  = "comments"
	keyCreatedAt = "created_at"
	keyID        = "id"
	keyLine      = "line"
	keyLogin     = "login"
	keyPath      = "path"
)

// FromGitHubThreads maps GraphQL reviewThreads nodes. Resolution and
// outdated state are GitHub's own isResolved/isOutdated; the thread id is the
// GraphQL id a resolve mutation takes, and each comment id prefers the
// numeric databaseId a reply takes. The bool reports a thread whose comment
// list arrived capped (totalCount > returned nodes).
func FromGitHubThreads(nodes []any) ([]Thread, bool) {
	out := make([]Thread, 0, len(nodes))
	capped := false
	for _, node := range nodes {
		th, threadCapped := gitHubThread(obj(node))
		capped = capped || threadCapped
		if done, ok := finishThread(&th); ok {
			out = append(out, done)
		}
	}
	return out, capped
}

func gitHubThread(m map[string]any) (Thread, bool) {
	if m == nil {
		return Thread{}, false
	}
	comments, url, capped := gitHubComments(obj(m[keyComments]))
	t := Thread{
		ID:        idStr(m[keyID]),
		Path:      str(m, keyPath),
		Line:      positiveInt(m[keyLine]),
		StartLine: positiveInt(m["startLine"]),
		Side:      sidePtr(str(m, "diffSide")),
		Resolved:  truthy(m["isResolved"]),
		Outdated:  truthy(m["isOutdated"]),
		Kind:      KindReview,
		Comments:  comments,
	}
	if url != "" {
		t.URL = &url
	}
	return t, capped
}

func gitHubComments(conn map[string]any) ([]Comment, string, bool) {
	var out []Comment
	url := ""
	for _, cn := range list(conn["nodes"]) {
		cm := obj(cn)
		if cm == nil {
			continue
		}
		out = append(out, gitHubComment(cm))
		if url == "" {
			url = str(cm, "url")
		}
	}
	total, ok := intOf(conn["totalCount"])
	return out, url, ok && total > len(out)
}

func gitHubComment(m map[string]any) Comment {
	author := obj(m[keyAuthor])
	login := str(author, keyLogin)
	body, truncated := CapBody(str(m, keyBody))
	id := idStr(m["databaseId"])
	if id == "" {
		id = idStr(m[keyID])
	}
	return Comment{
		ID:         id,
		Author:     login,
		AuthorKind: AuthorKind(login, str(author, "__typename") == "Bot", body),
		Body:       body,
		Truncated:  truncated,
		CreatedAt:  str(m, "createdAt"),
	}
}

// FromReviewBodies maps review summaries (GitHub reviews, Forgejo reviews,
// GitCafe reviews) as issue-comment threads: a review body is conversation,
// not a resolvable diff thread. Empty bodies (bare approvals) are not
// feedback and drop.
func FromReviewBodies(reviews []any) []Thread {
	out := make([]Thread, 0, len(reviews))
	for _, rv := range reviews {
		if th, ok := reviewThread(obj(rv)); ok {
			if done, usable := finishThread(&th); usable {
				out = append(out, done)
			}
		}
	}
	return out
}

func reviewThread(m map[string]any) (Thread, bool) {
	if m == nil {
		return Thread{}, false
	}
	author := obj(m["user"])
	login := str(author, keyLogin)
	if login == "" {
		login = gitCafeAuthor(m[keyAuthor])
	}
	body, truncated := CapBody(str(m, keyBody))
	if body == "" {
		return Thread{}, false
	}
	created := str(m, "submitted_at")
	if created == "" {
		created = str(m, keyCreatedAt, "createdAt")
	}
	t := Thread{
		ID:   idStr(m[keyID]),
		Kind: KindIssue,
		Comments: []Comment{{
			ID:         idStr(m[keyID]),
			Author:     login,
			AuthorKind: AuthorKind(login, str(author, "__typename") == "Bot", body),
			Body:       body,
			Truncated:  truncated,
			CreatedAt:  created,
		}},
	}
	if url := str(m, "html_url"); url != "" {
		t.URL = &url
	}
	return t, true
}

// FromGitLab maps MR discussions. GitLab's native resolution lives on the
// root note's resolved/resolvable pair; a diff note's position carries the
// path, line, side, and the head_sha it was written against, so a position
// from an older head reads outdated. System notes (label changes, pushes)
// are never feedback and drop.
func FromGitLab(discussions []any, headSHA string) []Thread {
	out := make([]Thread, 0, len(discussions))
	for _, d := range discussions {
		if th, ok := gitLabThread(obj(d), headSHA); ok {
			out = append(out, th)
		}
	}
	return out
}

func gitLabThread(dm map[string]any, headSHA string) (Thread, bool) {
	if dm == nil {
		return Thread{}, false
	}
	notes := gitLabNotes(dm)
	if len(notes) == 0 {
		return Thread{}, false
	}
	t := Thread{
		ID:       idStr(dm[keyID]),
		Resolved: truthy(notes[0]["resolved"]),
		Kind:     KindIssue,
		Comments: gitLabComments(notes),
	}
	gitLabAnchor(notes[0], &t, headSHA)
	return finishThread(&t)
}

func gitLabNotes(dm map[string]any) []map[string]any {
	var notes []map[string]any
	for _, n := range list(dm["notes"]) {
		nm := obj(n)
		if nm == nil || truthy(nm["system"]) {
			continue
		}
		notes = append(notes, nm)
	}
	return notes
}

func gitLabComments(notes []map[string]any) []Comment {
	comments := make([]Comment, 0, len(notes))
	for _, nm := range notes {
		comments = append(comments, gitLabComment(nm))
	}
	return comments
}

func gitLabAnchor(root map[string]any, t *Thread, headSHA string) {
	pos := obj(root["position"])
	if pos == nil || (pos["new_line"] == nil && pos["old_line"] == nil) {
		return
	}
	t.Kind = KindReview
	t.Line = positiveInt(pos["new_line"])
	t.Side = sidePtr(SideRight)
	t.Path = str(pos, "new_path")
	if t.Line == nil {
		t.Line = positiveInt(pos["old_line"])
		t.Side = sidePtr(SideLeft)
		t.Path = str(pos, "old_path")
	}
	if t.Path == "" {
		t.Path = str(pos, "new_path", "old_path")
	}
	head := str(pos, "head_sha")
	t.Outdated = head != "" && headSHA != "" && head != headSHA
}

func gitLabComment(m map[string]any) Comment {
	author := obj(m[keyAuthor])
	login := str(author, "username", "name")
	body, truncated := CapBody(str(m, keyBody))
	return Comment{
		ID:         idStr(m[keyID]),
		Author:     login,
		AuthorKind: AuthorKind(login, truthy(author["bot"]), body),
		Body:       body,
		Truncated:  truncated,
		CreatedAt:  str(m, keyCreatedAt),
	}
}

// FromForgejo maps review summaries plus review comments. A review comment's
// native resolved signal is the resolver object; position 0 with a non-zero
// original_position is the anchor that no longer maps to the current diff
// (Forgejo's stale-position signal), which reads outdated and keeps the
// original line/side.
func FromForgejo(reviews, comments []any, _ string) []Thread {
	out := FromReviewBodies(reviews)
	for _, cn := range comments {
		if th, ok := forgejoThread(obj(cn)); ok {
			if done, usable := finishThread(&th); usable {
				out = append(out, done)
			}
		}
	}
	return out
}

func forgejoThread(m map[string]any) (Thread, bool) {
	if m == nil {
		return Thread{}, false
	}
	author := obj(m["user"])
	login := str(author, keyLogin)
	body, truncated := CapBody(str(m, keyBody))
	t := Thread{
		ID:       idStr(m[keyID]),
		Path:     str(m, keyPath),
		Resolved: forgejo.Resolved(m),
		Kind:     KindReview,
		Comments: []Comment{plainComment(m, login, body, truncated)},
	}
	forgejoAnchor(m, &t)
	if url := str(m, "html_url"); url != "" {
		t.URL = &url
	}
	return t, true
}

// forgejoAnchor reads the comment's current/original diff position: position
// 0 against a non-zero original is the stale anchor, which reads outdated and
// keeps the original line on the left side.
func forgejoAnchor(m map[string]any, t *Thread) {
	position, hasPosition := intOf(m["position"])
	original, hasOriginal := intOf(m["original_position"])
	leftSide := hasPosition && position == 0 && hasOriginal && original > 0
	t.Outdated = leftSide
	line := position
	if leftSide {
		line = original
	}
	if line > 0 {
		t.Line = &line
	}
	if !hasPosition {
		return
	}
	t.Side = sidePtr(SideRight)
	if leftSide {
		t.Side = sidePtr(SideLeft)
	}
}

// FromBitbucket maps comments into threads: replies group under their root
// across page boundaries, deleted and unsubmitted (pending) comments drop,
// the root's resolution object is the thread state, and inline.outdated is
// the host's stale-anchor flag.
func FromBitbucket(comments []any) []Thread {
	kept, byID := bitbucketKept(comments)
	groups, order := bitbucketGroups(kept, byID)
	out := make([]Thread, 0, len(order))
	for _, id := range order {
		root := bitbucketRoot(groups[id][0], byID)
		th := bitbucketThread(root, groups[id])
		if done, ok := finishThread(&th); ok {
			out = append(out, done)
		}
	}
	return out
}

func plainComment(m map[string]any, login, body string, truncated bool) Comment {
	return Comment{
		ID:         idStr(m[keyID]),
		Author:     login,
		AuthorKind: AuthorKind(login, false, body),
		Body:       body,
		Truncated:  truncated,
		CreatedAt:  str(m, keyCreatedAt),
	}
}

func bitbucketKept(comments []any) ([]map[string]any, map[string]map[string]any) {
	kept := make([]map[string]any, 0, len(comments))
	byID := make(map[string]map[string]any, len(comments))
	for _, cn := range comments {
		m := obj(cn)
		if m == nil || truthy(m["deleted"]) || truthy(m["pending"]) {
			continue
		}
		kept = append(kept, m)
		byID[idStr(m[keyID])] = m
	}
	return kept, byID
}

func bitbucketGroups(
	kept []map[string]any, byID map[string]map[string]any,
) (map[string][]map[string]any, []string) {
	groups := make(map[string][]map[string]any, len(kept))
	order := make([]string, 0, len(kept))
	for _, m := range kept {
		root := bitbucketRoot(m, byID)
		if root == nil {
			continue
		}
		id := idStr(root[keyID])
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], m)
	}
	return groups, order
}

func bitbucketRoot(m map[string]any, byID map[string]map[string]any) map[string]any {
	current := m
	for range len(byID) + 1 {
		parent := obj(current["parent"])
		if parent == nil {
			return current
		}
		next, ok := byID[idStr(parent[keyID])]
		if !ok {
			return current
		}
		current = next
	}
	return current
}

func bitbucketThread(root map[string]any, group []map[string]any) Thread {
	inline := obj(root["inline"])
	slices.SortStableFunc(group, func(a, b map[string]any) int {
		return strings.Compare(str(a, "created_on"), str(b, "created_on"))
	})
	t := Thread{
		ID:       idStr(root[keyID]),
		Resolved: root["resolution"] != nil,
		Outdated: truthy(inline["outdated"]),
		Kind:     KindIssue,
		Comments: bitbucketComments(group),
	}
	if inline != nil {
		t.Kind = KindReview
		t.Path = str(inline, keyPath)
		t.Line = positiveInt(inline["to"])
		t.Side = sidePtr(SideRight)
		if t.Line == nil {
			t.Line = positiveInt(inline["from"])
			t.Side = sidePtr(SideLeft)
		}
	}
	if url := str(obj(obj(root["links"])["html"]), "href"); url != "" {
		t.URL = &url
	}
	return t
}

func bitbucketComments(group []map[string]any) []Comment {
	comments := make([]Comment, 0, len(group))
	for _, m := range group {
		author := obj(m["user"])
		login := str(author, "nickname", "display_name", "username")
		body, truncated := CapBody(str(obj(m["content"]), "raw"))
		comments = append(comments, Comment{
			ID:         idStr(m[keyID]),
			Author:     login,
			AuthorKind: AuthorKind(login, str(author, "type") == "bot", body),
			Body:       body,
			Truncated:  truncated,
			CreatedAt:  str(m, "created_on"),
		})
	}
	return comments
}

// FromAzure maps Azure comment threads. The thread status is the native
// resolution state: active and pending are open, the settled statuses
// (fixed, wontFix, closed, byDesign) are resolved. Deleted threads,
// system/deleted/unpublished comments, and empty bodies drop, and the
// threadContext right/left start feeds path, line, and side (a context with
// no line is Azure's stale anchor).
func FromAzure(rawThreads []any) []Thread {
	out := make([]Thread, 0, len(rawThreads))
	for _, tn := range rawThreads {
		if th, ok := azureThread(obj(tn)); ok {
			out = append(out, th)
		}
	}
	return out
}

func azureThread(m map[string]any) (Thread, bool) {
	if m == nil || truthy(m["isDeleted"]) {
		return Thread{}, false
	}
	comments := azureComments(m)
	if len(comments) == 0 {
		return Thread{}, false
	}
	t := Thread{
		ID:       idStr(m[keyID]),
		Resolved: azureResolved(str(m, "status")),
		Kind:     KindIssue,
		Comments: comments,
	}
	if ctx := obj(m["threadContext"]); ctx != nil {
		t.Kind = KindReview
		t.Path = strings.TrimPrefix(str(ctx, "filePath"), "/")
		azureAnchor(ctx, &t)
	}
	if url := str(obj(obj(m["_links"])["html"]), "href"); url != "" {
		t.URL = &url
	}
	return finishThread(&t)
}

func azureComments(m map[string]any) []Comment {
	comments := make([]Comment, 0, len(list(m[keyComments])))
	for _, cn := range list(m[keyComments]) {
		cm := obj(cn)
		if !azureCommentUsable(cm) {
			continue
		}
		author := obj(cm[keyAuthor])
		login := str(author, "displayName", "uniqueName")
		body, truncated := CapBody(str(cm, "content"))
		comments = append(comments, Comment{
			ID:         idStr(cm[keyID]),
			Author:     login,
			AuthorKind: AuthorKind(login, false, body),
			Body:       body,
			Truncated:  truncated,
			CreatedAt:  str(cm, "publishedDate"),
		})
	}
	return comments
}

func azureCommentUsable(cm map[string]any) bool {
	if cm == nil || truthy(cm["isDeleted"]) || str(cm, "commentType") == "system" {
		return false
	}
	if str(cm, "publishedDate") == "" && str(cm, keyID) == "" {
		return false
	}
	return str(cm, "content") != "" || str(obj(cm[keyAuthor]), "displayName", "uniqueName") != ""
}

func azureAnchor(ctx map[string]any, t *Thread) {
	if right := obj(ctx["rightFileStart"]); right != nil {
		azureRightAnchor(ctx, right, t)
		return
	}
	if left := obj(ctx["leftFileStart"]); left != nil {
		t.Side = sidePtr(SideLeft)
		t.Line = positiveInt(left[keyLine])
	}
	if t.Line == nil {
		t.Outdated = true
	}
}

func azureRightAnchor(ctx, right map[string]any, t *Thread) {
	t.Side = sidePtr(SideRight)
	t.Line = positiveInt(right[keyLine])
	end := obj(ctx["rightFileEnd"])
	if end == nil || t.Line == nil {
		return
	}
	last, ok := intOf(end[keyLine])
	if !ok || last <= *t.Line {
		return
	}
	start := *t.Line
	t.StartLine = &start
	t.Line = &last
}

// azureResolved folds Azure's CommentThreadStatus: only the settled statuses
// read resolved; active, pending, and a missing status stay open (fail-closed
// for a re-review).
func azureResolved(status string) bool {
	switch strings.ToLower(status) {
	case "fixed", "wontfix", "closed", "bydesign":
		return true
	default:
		return false
	}
}

// FromGitCafe maps GitCafe reviews and comments best-effort: the payload is
// not documented in this repo, so the generic fields the agent already sees
// in reviews.json/inline.json drive the record, including the resolved/
// resolvedAt and outdated flags when the host publishes them.
func FromGitCafe(reviews, comments []any) []Thread {
	out := FromReviewBodies(reviews)
	for _, cn := range comments {
		if th, ok := gitCafeThread(obj(cn)); ok {
			out = append(out, th)
		}
	}
	return out
}

func gitCafeThread(m map[string]any) (Thread, bool) {
	if m == nil {
		return Thread{}, false
	}
	body, truncated := CapBody(str(m, keyBody))
	login := gitCafeAuthor(m[keyAuthor])
	t := Thread{
		ID:       idStr(firstNonNil(m[keyID], m["threadId"])),
		Path:     str(m, keyPath),
		Line:     positiveInt(m[keyLine]),
		Side:     sidePtr(str(m, "side")),
		Resolved: truthy(m["resolved"]) || str(m, "resolvedAt") != "",
		Outdated: truthy(m["outdated"]),
		Kind:     KindIssue,
		Comments: []Comment{{
			ID:         idStr(m[keyID]),
			Author:     login,
			AuthorKind: AuthorKind(login, false, body),
			Body:       body,
			Truncated:  truncated,
			CreatedAt:  str(m, "createdAt", keyCreatedAt),
		}},
	}
	if t.Path != "" {
		t.Kind = KindReview
	}
	return finishThread(&t)
}

func gitCafeAuthor(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	m := obj(v)
	return str(m, "handle", "login", "username", "name")
}

// finishThread folds the root comment into the thread-level fields: author
// and author_kind mirror comments[0], and finding_sig links only a
// verdict-authored root whose final line is verdict's own marker. An empty
// record (no anchor and no text) is not a thread.
func finishThread(t *Thread) (Thread, bool) {
	if len(t.Comments) == 0 {
		t.Comments = []Comment{}
	}
	t.foldRoot()
	if t.Kind == "" {
		t.Kind = KindIssue
	}
	if t.Path != "" && t.Kind == KindIssue {
		t.Kind = KindReview
	}
	if t.Path == "" && (len(t.Comments) == 0 || t.Comments[0].Body == "") {
		return Thread{}, false
	}
	return *t, true
}

// foldRoot copies the root comment's identity onto the thread and links its
// finding marker.
func (t *Thread) foldRoot() {
	if len(t.Comments) == 0 {
		t.AuthorKind = AuthorKind("", false, "")
		return
	}
	root := t.Comments[0]
	t.Author = root.Author
	t.AuthorKind = root.AuthorKind
	if root.AuthorKind != AuthorVerdict {
		return
	}
	if sig, ok := FindingSig(root.Body); ok {
		t.FindingSig = &sig
	}
}

func obj(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case *jq.Obj:
		if t == nil {
			return nil
		}
		out := make(map[string]any, len(t.Keys))
		for _, k := range t.Keys {
			out[k] = t.Fields[k]
		}
		return out
	default:
		return nil
	}
}

func list(v any) []any { return jq.Elements(v) }

func idStr(v any) string {
	switch t := v.(type) {
	case nil, bool:
		return ""
	case string:
		return t
	default:
		s := jq.R(v)
		if s == "null" {
			return ""
		}
		return s
	}
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func str(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func intOf(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	n, ok := jq.PrefixInt(jq.R(v))
	if !ok {
		return 0, false
	}
	return n, true
}

func positiveInt(v any) *int {
	n, ok := intOf(v)
	if !ok || n <= 0 {
		return nil
	}
	return &n
}

func sidePtr(v string) *string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case SideLeft, "OLD":
		s := SideLeft
		return &s
	case SideRight, "NEW":
		s := SideRight
		return &s
	default:
		return nil
	}
}
