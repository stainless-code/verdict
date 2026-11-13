package postcore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	summaryBodyLimit   = 65536
	summarySnippetCap  = 120
	methodPatch        = "PATCH"
	errEmptySummary    = "refusing to write an empty summary comment body"
	msgSummaryNoAuthor = "cannot manage summary comment: failed to resolve authenticated login"
)

type summaryRemoteComment struct {
	ID        string
	Author    string
	Body      string
	URL       string
	CreatedAt string
	UpdatedAt string
	System    bool
}

type summaryInlineLink struct {
	Path string
	Body string
	URL  string
	Line int
}

type summaryTarget struct {
	comment        *summaryRemoteComment
	duplicateCount int
}

// hostSummary isolates PR-level issue/note summary comment CRUD and inline URL
// lookup per code host.
type hostSummary interface {
	listSummaryComments(ctx context.Context, cmdr Commander, env map[string]string,
		repo, pr string) ([]summaryRemoteComment, error)
	createSummaryComment(ctx context.Context, cmdr Commander, env map[string]string,
		repo, pr, body string) (summaryRemoteComment, error)
	updateSummaryComment(ctx context.Context, cmdr Commander, env map[string]string,
		repo, pr, commentID, body string) (summaryRemoteComment, error)
	postedInlineURLs(ctx context.Context, cmdr Commander, env map[string]string,
		repo, pr string, review *jq.Obj) ([]summaryInlineLink, error)
}

func summaryHostFor(provider string) (hostSummary, error) {
	switch provider {
	case providerGitHub:
		return githubHost{}, nil
	case providerGitLab:
		return gitlabHost{}, nil
	case providerForgejo:
		return forgejoHost{}, nil
	default:
		return nil, &core.RefusalError{
			Code:    "provider-unimplemented",
			Message: fmt.Sprintf("summary comment is not supported for provider %q", provider),
		}
	}
}

// cachedSelfLogin resolves and caches the authenticated host login so gates and
// summary comment discovery do not duplicate the /user request.
func (st *state) cachedSelfLogin(ctx context.Context) (string, error) {
	if st.meLoaded {
		return st.me, st.meErr
	}
	me, err := st.selfLogin(ctx)
	st.me = strings.TrimSpace(me)
	st.meErr = err
	st.meLoaded = true
	return st.me, st.meErr
}

// resolveSummaryTarget reads the live PR-level comments and selects the existing
// verdict summary comment owned by the authenticated user.
func (st *state) resolveSummaryTarget(ctx context.Context, host hostSummary) (summaryTarget, *core.RefusalError) {
	me, err := st.cachedSelfLogin(ctx)
	if err != nil || me == "" {
		if err != nil {
			return summaryTarget{}, &core.RefusalError{
				Code:    codePostFailed,
				Message: fmt.Sprintf("%s: %v", msgSummaryNoAuthor, err),
			}
		}
		return summaryTarget{}, &core.RefusalError{Code: codePostFailed, Message: msgSummaryNoAuthor}
	}
	comments, err := host.listSummaryComments(ctx, st.commanderOrNil(), st.env(), st.req.Repo, st.req.Pr)
	if err != nil {
		return summaryTarget{}, &core.RefusalError{
			Code:    codePostFailed,
			Message: fmt.Sprintf("failed reading live summary comments: %v", err),
		}
	}
	return pickSummaryTarget(comments, me), nil
}

// planSummary determines the dry-run summary action without modifying the host.
func (st *state) planSummary(ctx context.Context) (*core.PostSummary, *core.RefusalError) {
	host, err := summaryHostFor(st.provider())
	if err != nil {
		var ref *core.RefusalError
		if asRef, ok := err.(*core.RefusalError); ok {
			ref = asRef
		} else {
			ref = refusalOrFailed(err)
		}
		return nil, ref
	}
	target, ref := st.resolveSummaryTarget(ctx, host)
	if ref != nil {
		return nil, ref
	}
	marker := threadscore.SummaryMarker(st.commitID)
	body := st.renderSummaryBody("", nil, marker)
	if strings.TrimSpace(body) == "" {
		return &core.PostSummary{Action: summaryActionNone, Marker: marker}, nil
	}
	return st.buildSummaryOutcome(target, marker, summaryRemoteComment{}, true), nil
}

// publishSummary writes or updates the single PR-level summary comment after
// the review has landed on the host.
func (st *state) publishSummary(ctx context.Context, cmdr Commander, reviewObj *jq.Obj) (*core.PostSummary, *core.RefusalError) {
	host, err := summaryHostFor(st.provider())
	if err != nil {
		return nil, refusalOrFailed(err)
	}
	target, ref := st.resolveSummaryTarget(ctx, host)
	if ref != nil {
		st.warn("review posted, but summary comment lookup failed")
		return nil, ref
	}
	reviewURL := summaryReviewURL(reviewObj)
	inlineLinks := st.collectInlineLinks(ctx, host, reviewObj)
	marker := threadscore.SummaryMarker(st.commitID)
	body := st.renderSummaryBody(reviewURL, inlineLinks, marker)
	if strings.TrimSpace(body) == "" {
		return &core.PostSummary{Action: summaryActionNone, Marker: marker}, nil
	}
	var written summaryRemoteComment
	if target.comment == nil {
		written, err = host.createSummaryComment(ctx, cmdr, st.env(), st.req.Repo, st.req.Pr, body)
	} else {
		written, err = host.updateSummaryComment(ctx, cmdr, st.env(), st.req.Repo, st.req.Pr, target.comment.ID, body)
	}
	if err != nil {
		st.warn("review posted, but summary comment write failed")
		return nil, &core.RefusalError{
			Code:    codePostFailed,
			Message: fmt.Sprintf("review posted, but summary comment write failed: %v", err),
		}
	}
	if written.URL == "" {
		written.URL = summaryCommentURLFallback(st.provider(), reviewURL, target.comment, written.ID)
	}
	return st.buildSummaryOutcome(target, marker, written, false), nil
}

func summaryCommentURLFallback(provider, reviewURL string, prior *summaryRemoteComment, writtenID string) string {
	if prior != nil && writtenID == prior.ID && prior.URL != "" {
		return prior.URL
	}
	if provider == providerGitLab && reviewURL != "" && writtenID != "" {
		base, _, _ := strings.Cut(reviewURL, "#")
		if base != "" {
			return base + "#note_" + writtenID
		}
	}
	return ""
}

func (st *state) buildSummaryOutcome(
	target summaryTarget, marker string, written summaryRemoteComment, dryRun bool,
) *core.PostSummary {
	out := &core.PostSummary{
		Action: summaryActionCreate,
		Marker: marker,
	}
	if target.comment != nil {
		out.Action = summaryActionUpdate
		out.CommentID = target.comment.ID
		out.URL = target.comment.URL
	}
	if !dryRun {
		if written.ID != "" {
			out.CommentID = written.ID
		}
		if written.URL != "" {
			out.URL = written.URL
		}
	}
	if target.duplicateCount > 1 {
		out.DuplicateCount = target.duplicateCount
		if dryRun {
			out.Note = fmt.Sprintf(
				"found %d marked summary comments by %s; would update newest (%s) without deleting",
				target.duplicateCount, st.me, out.CommentID,
			)
		} else {
			out.Note = fmt.Sprintf(
				"found %d marked summary comments by %s; updated newest (%s) without deleting",
				target.duplicateCount, st.me, out.CommentID,
			)
		}
		st.warn(out.Note)
	}
	return out
}

func (st *state) collectInlineLinks(ctx context.Context, host hostSummary, reviewObj *jq.Obj) []summaryInlineLink {
	var links []summaryInlineLink
	if len(st.inlineLinks) > 0 {
		links = append(links, st.inlineLinks...)
	}
	if len(links) == 0 && len(st.comments) > 0 {
		if fetched, err := host.postedInlineURLs(ctx, st.commanderOrNil(), st.env(), st.req.Repo, st.req.Pr, reviewObj); err == nil {
			links = fetched
		}
	}
	return links
}

func summaryReviewURL(obj *jq.Obj) string {
	if obj == nil {
		return ""
	}
	v, ok := obj.Get("html_url")
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func formatSummaryDryRun(s *core.PostSummary) string {
	if s == nil {
		return "summary: none"
	}
	switch s.Action {
	case summaryActionCreate:
		return "summary: create"
	case summaryActionUpdate:
		if s.DuplicateCount > 1 {
			return fmt.Sprintf("summary: update %s (%d marked comments found; newest selected)", s.CommentID, s.DuplicateCount)
		}
		if s.CommentID != "" {
			return "summary: update " + s.CommentID
		}
		return "summary: update"
	default:
		return "summary: none"
	}
}

// pickSummaryTarget selects the newest summary comment authored by me carrying
// the verdict-summary marker, ignoring comments by any other author.
func pickSummaryTarget(comments []summaryRemoteComment, me string) summaryTarget {
	if me == "" {
		return summaryTarget{}
	}
	var matches []summaryRemoteComment
	for _, c := range comments {
		if c.System || c.ID == "" {
			continue
		}
		if !strings.EqualFold(c.Author, me) {
			continue
		}
		if !threadscore.HasSummaryMarker(c.Body) {
			continue
		}
		matches = append(matches, c)
	}
	if len(matches) == 0 {
		return summaryTarget{}
	}
	best := matches[0]
	for _, cand := range matches[1:] {
		if isNewerSummaryComment(cand, best) {
			best = cand
		}
	}
	return summaryTarget{comment: &best, duplicateCount: len(matches)}
}

func isNewerSummaryComment(cand, best summaryRemoteComment) bool {
	ct, cOK := parseSummaryTimestamp(cand.UpdatedAt, cand.CreatedAt)
	bt, bOK := parseSummaryTimestamp(best.UpdatedAt, best.CreatedAt)
	if cOK && bOK {
		if ct.After(bt) {
			return true
		}
		if ct.Before(bt) {
			return false
		}
	} else if cOK != bOK {
		return cOK
	}
	ci, ciOK := parseCommentNumericID(cand.ID)
	bi, biOK := parseCommentNumericID(best.ID)
	if ciOK && biOK && ci != bi {
		return ci > bi
	}
	return true
}

func parseSummaryTimestamp(updated, created string) (time.Time, bool) {
	for _, raw := range []string{updated, created} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseCommentNumericID(id string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	return n, err == nil
}

// renderSummaryBody builds the PR-level summary comment Markdown and ends with
// the hidden verdict-summary marker on its own final line.
func (st *state) renderSummaryBody(reviewURL string, inlineLinks []summaryInlineLink, marker string) string {
	var sections []string
	sections = append(sections, "## Verdict Summary")

	short := readcommon.ShortSHA(st.commitID)
	if short == "" {
		short = "unknown"
	}
	statusLine := fmt.Sprintf("Verdict: **%s** at `%s`", st.event, short)
	if reviewURL != "" {
		statusLine += fmt.Sprintf(" — [view review](%s)", reviewURL)
	}
	sections = append(sections, statusLine)

	payload, _, _ := st.floorPayload()
	nOpen, nDeferred, nAddressed := postCounts(payload)
	sections = append(sections, fmt.Sprintf("Findings: %d open · %d deferred · %d addressed", nOpen, nDeferred, nAddressed))

	if st.driftStale {
		sections = append(sections, fmt.Sprintf(msgStaleNote, st.req.Repo, st.req.Pr, readcommon.ShortSHA(st.fetchedSHA)))
	}
	if prose := cleanSummaryProse(st.merged.Body); prose != "" {
		sections = append(sections, prose)
	}
	if inlineSection := renderSummaryInlineSection(st.comments, inlineLinks); inlineSection != "" {
		sections = append(sections, inlineSection)
	}
	content := strings.Join(sections, "\n\n")
	return capSummaryWithMarker(content, marker, summaryBodyLimit)
}

func cleanSummaryProse(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(last, "<!-- verdict-") && strings.HasSuffix(last, "-->") {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(recordcore.NeutralizeMentions(strings.Join(lines, "\n")))
}

func renderSummaryInlineSection(comments []any, links []summaryInlineLink) string {
	if len(comments) == 0 && len(links) == 0 {
		return ""
	}
	used := make([]bool, len(links))
	var lines []string
	lines = append(lines, "### Inline Comments")
	for _, raw := range comments {
		c, ok := raw.(*jq.Obj)
		if !ok || c == nil {
			continue
		}
		path, _ := strVal(c, fieldPath)
		line := intVal(c, fieldLine)
		body, _ := strVal(c, fieldBody)
		url := matchInlineLink(path, line, body, links, used)
		lines = append(lines, formatInlineLinkBullet(path, line, body, url))
	}
	if len(comments) == 0 {
		for _, link := range links {
			lines = append(lines, formatInlineLinkBullet(link.Path, link.Line, link.Body, link.URL))
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func strVal(obj *jq.Obj, key string) (string, bool) {
	v, ok := obj.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func intVal(obj *jq.Obj, key string) int {
	v, ok := obj.Get(key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		parsed, err := strconv.Atoi(jq.Interp(v))
		if err != nil {
			return 0
		}
		return parsed
	}
}

func matchInlineLink(path string, line int, body string, links []summaryInlineLink, used []bool) string {
	sig, hasSig := threadscore.FindingSig(body)
	if hasSig {
		for i, link := range links {
			if used[i] || link.URL == "" {
				continue
			}
			if lsig, ok := threadscore.FindingSig(link.Body); ok && lsig == sig {
				used[i] = true
				return link.URL
			}
		}
	}
	for i, link := range links {
		if used[i] || link.URL == "" {
			continue
		}
		if link.Path == path && (line <= 0 || link.Line <= 0 || link.Line == line) {
			used[i] = true
			return link.URL
		}
	}
	return ""
}

func formatInlineLinkBullet(path string, line int, body, url string) string {
	loc := path
	if line > 0 {
		loc = fmt.Sprintf("%s:%d", path, line)
	}
	loc = strings.NewReplacer("[", "(", "]", ")").Replace(loc)
	snippet := inlineSnippet(body)
	prefix := "- `" + loc + "`"
	if url != "" {
		prefix = fmt.Sprintf("- [%s](%s)", loc, url)
	}
	if snippet == "" {
		return prefix
	}
	return prefix + " — " + snippet
}

func inlineSnippet(body string) string {
	clean := cleanSummaryProse(body)
	if clean == "" {
		return ""
	}
	firstLine, _, _ := strings.Cut(clean, "\n")
	firstLine = strings.Join(strings.Fields(firstLine), " ")
	runes := []rune(firstLine)
	if len(runes) > summarySnippetCap {
		return string(runes[:summarySnippetCap-1]) + "…"
	}
	return firstLine
}

func capSummaryWithMarker(content, marker string, limit int) string {
	suffix := "\n\n" + marker
	maxContent := limit - len(suffix)
	if maxContent <= 0 {
		return marker
	}
	content = strings.TrimSpace(content)
	if len(content) > maxContent {
		notice := "\n\n_(truncated)_"
		cutAt := maxContent - len(notice)
		if cutAt < 0 {
			cutAt = maxContent
			notice = ""
		}
		cut := content[:cutAt]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		content = strings.TrimRight(cut, "\r\n \t") + notice
	}
	return content + suffix
}

func summaryCommentsFromMaps(items []map[string]any, fallbackPRURL string) []summaryRemoteComment {
	out := make([]summaryRemoteComment, 0, len(items))
	for _, m := range items {
		out = append(out, decodeSummaryCommentMap(m, fallbackPRURL))
	}
	return out
}

func decodeSummaryCommentObject(raw string, fallbackPRURL string) (summaryRemoteComment, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(raw))
	if err != nil {
		return summaryRemoteComment{}, jq.Sealed("decode summary comment JSON", err)
	}
	if len(docs) == 0 {
		return summaryRemoteComment{}, shapeError("empty summary comment response")
	}
	m, ok := docs[0].(map[string]any)
	if !ok {
		return summaryRemoteComment{}, shapeError("summary comment response is not an object")
	}
	return decodeSummaryCommentMap(m, fallbackPRURL), nil
}

func decodeSummaryCommentMap(m map[string]any, fallbackPRURL string) summaryRemoteComment {
	id := jq.Interp(m["id"])
	author := nestedLogin(m["user"], "login")
	if author == "" {
		author = nestedLogin(m["author"], "username")
	}
	if author == "" {
		author = nestedLogin(m["user"], "username")
	}
	url := jq.StrOf(m["html_url"])
	if url == "" {
		url = jq.StrOf(m["web_url"])
	}
	if url == "" && fallbackPRURL != "" && id != "" {
		url = fallbackPRURL + "#note_" + id
	}
	system, _ := m["system"].(bool)
	return summaryRemoteComment{
		ID:        id,
		Author:    author,
		Body:      jq.StrOf(m[fieldBody]),
		URL:       url,
		CreatedAt: jq.StrOf(m["created_at"]),
		UpdatedAt: jq.StrOf(m["updated_at"]),
		System:    system,
	}
}

func nestedLogin(v any, key string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	return jq.StrOf(m[key])
}

func decodeSummaryInlineComments(raw string) ([]summaryInlineLink, error) {
	items, err := publicationList(raw)
	if err != nil {
		return nil, err
	}
	return inlineLinksFromMaps(items), nil
}

func inlineLinksFromMaps(items []map[string]any) []summaryInlineLink {
	out := make([]summaryInlineLink, 0, len(items))
	for _, m := range items {
		url := jq.StrOf(m["html_url"])
		if url == "" {
			url = jq.StrOf(m["web_url"])
		}
		line := intFromAny(m[fieldLine])
		if line <= 0 {
			line = intFromAny(m["original_line"])
		}
		out = append(out, summaryInlineLink{
			Path: jq.StrOf(m[fieldPath]),
			Body: jq.StrOf(m[fieldBody]),
			URL:  url,
			Line: line,
		})
	}
	return out
}

func intFromAny(v any) int {
	if v == nil {
		return 0
	}
	n, err := strconv.Atoi(jq.Interp(v))
	if err != nil {
		return 0
	}
	return n
}

func summaryReviewID(obj *jq.Obj) string {
	if obj == nil {
		return ""
	}
	v, ok := obj.Get("id")
	if !ok {
		return ""
	}
	id := jq.Interp(v)
	if id == "" || id == "0" {
		return ""
	}
	return id
}
