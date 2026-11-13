package calibratecore

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const reactionUserKey = "user"

func (st *state) runReactions(ctx context.Context, dir string) (*Response, *core.RefusalError) {
	if reactionsUnsupported(dir) {
		return nil, st.withWarnings(&core.RefusalError{
			Code: codeProviderUnimplemented, Message: "calibrate --reactions supports posted GitHub reviews only",
		})
	}
	entries, reviewID, _, ref := st.liveBundle(dir)
	if ref != nil {
		return nil, ref
	}
	comments, author, ref := st.readReactionComments(ctx, entries, reviewID)
	if ref != nil {
		return nil, ref
	}
	result := reactionProposals(entries, comments, reviewID, strings.TrimSpace(st.req.ReactionReason), author)
	st.human(jq.PrettyOrdered(result))
	st.human("reaction proposals only — review helpful signals; submit reasoned false-positive claims through triage")
	return &Response{
		Repo: st.req.Repo, Pr: st.req.Pr, At: st.req.TimeUTC(), Next: "review proposals before triage",
		Warnings: st.warnings, Data: &Data{reactions: result},
	}, nil
}

func reactionsUnsupported(dir string) bool {
	return isLocalDir(dir) || readcommon.ProviderOf(dir) != "" && readcommon.ProviderOf(dir) != providerGitHub
}

func (st *state) reactionRead(ctx context.Context, endpoint string) (any, *core.RefusalError) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "api", endpoint, "--paginate", "--slurp")
	if !ok {
		return nil, st.reactionReadFailed()
	}
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return nil, st.reactionReadFailed()
	}
	return doc, nil
}

func (st *state) reactionReadFailed() *core.RefusalError {
	return st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: "could not read posted reaction identities"})
}

func reactionPages(doc any) []any {
	pages, ok := doc.([]any)
	if !ok {
		return nil
	}
	var items []any
	for _, page := range pages {
		if rows, ok := page.([]any); ok {
			items = append(items, rows...)
		}
	}
	return items
}

func (st *state) readReactionComments(ctx context.Context, entries []any, reviewID string) ([]any, string, *core.RefusalError) {
	base := "repos/" + st.req.Repo
	doc, ref := st.reactionRead(ctx, base+"/pulls/"+st.req.Pr+"/comments")
	if ref != nil {
		return nil, "", ref
	}
	comments := reactionPages(doc)
	author, ref := st.reactionPRAuthor(ctx, base)
	if ref != nil {
		return nil, "", ref
	}
	if ref := st.attachMatchedReactions(ctx, base, comments, entries, reviewID); ref != nil {
		return nil, "", ref
	}

	return comments, author, nil
}

func (st *state) reactionPRAuthor(ctx context.Context, base string) (string, *core.RefusalError) {
	doc, ref := st.reactionRead(ctx, base+"/pulls/"+st.req.Pr)
	if ref != nil {
		return "", ref
	}
	pages, ok := doc.([]any)
	if !ok || len(pages) != 1 {
		return "", st.reactionReadFailed()
	}
	pr, ok := pages[0].(map[string]any)
	if !ok {
		return "", st.reactionReadFailed()
	}
	author, _ := reactionUser(pr[reactionUserKey])
	if author == "" {
		return "", st.reactionReadFailed()
	}
	return author, nil
}

func (st *state) attachReactionUsers(ctx context.Context, base string, m map[string]any) *core.RefusalError {
	author, _ := reactionUser(m[reactionUserKey])
	id, ok := readcommon.NumVal(m["id"])
	if author == "" || !ok || id <= 0 {
		return st.reactionReadFailed()
	}
	doc, ref := st.reactionRead(ctx, fmt.Sprintf("%s/pulls/comments/%.0f/reactions", base, id))
	if ref != nil {
		return ref
	}
	m["reaction_users"] = reactionPages(doc)
	return nil
}

func matchedReactionComment(m map[string]any, entries []any, reviewID string) bool {
	for _, entry := range entries {
		if reactionCommentMatches(m, entry, reviewID) {
			return true
		}
	}
	return false
}

func reactionCommentMatches(m map[string]any, entry any, reviewID string) bool {
	return readcommon.FieldOf(entry, "kind") == "inline" && jq.R(m["pull_request_review_id"]) == reviewID &&
		m["body"] == postedBody(entry) && readcommon.Cmp(m["path"], readcommon.FieldOf(entry, "path")) == 0
}

type reactionSignals struct {
	proposals []any
	bot, self int
}

func reactionProposals(entries, comments []any, reviewID, reason, author string) *jq.Obj {
	result := &reactionSignals{proposals: []any{}}
	reviewers := reactionReviewers(comments)
	for _, comment := range comments {
		result.matchEntries(comment, entries, reviewID, reason, author, reviewers)
	}

	slices.SortStableFunc(result.proposals, func(a, b any) int { return strings.Compare(jq.PrettyOrdered(a), jq.PrettyOrdered(b)) })
	return jq.NewObj("proposals", result.proposals, "excluded", jq.NewObj("bot", result.bot, "self", result.self))
}

func reactionReviewers(comments []any) map[string]bool {
	reviewers := map[string]bool{}
	for _, comment := range comments {
		if m, ok := comment.(map[string]any); ok {
			login, _ := reactionUser(m[reactionUserKey])
			reviewers[strings.ToLower(login)] = true
		}
	}
	return reviewers
}

func reactionUser(raw any) (string, bool) {
	user, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	login := jq.StrOf(user["login"])
	return login, user["type"] == "Bot" || strings.HasSuffix(strings.ToLower(login), "[bot]")
}

func (r *reactionSignals) addComment(m map[string]any, entry any, reason, author string, reviewers map[string]bool) {
	poster, _ := reactionUser(m[reactionUserKey])
	users, ok := m["reaction_users"].([]any)
	if !ok {
		return
	}
	for _, raw := range users {
		if reaction, ok := raw.(map[string]any); ok {
			r.addReaction(reaction, entry, poster, reason, author, reviewers)
		}
	}
}

func (r *reactionSignals) addReaction(reaction map[string]any, entry any, poster, reason, author string, reviewers map[string]bool) {
	login, bot := reactionUser(reaction[reactionUserKey])
	if bot {
		r.bot++
		return
	}
	if strings.EqualFold(login, poster) {
		r.self++
		return
	}
	if login == "" {
		return
	}
	proposed, claim := reactionClaim(reaction["content"], entry, reason)
	if proposed == "" {
		return
	}
	role := reactionRole(login, author, reviewers)
	r.proposals = append(r.proposals, jq.NewObj("sig", readcommon.FieldOf(entry, "sig"), "proposed", proposed,
		"claim", claim, "reactor", login, "role", role))
}

func reactionRole(login, author string, reviewers map[string]bool) string {
	if strings.EqualFold(login, author) {
		return "pr-author"
	}
	if reviewers[strings.ToLower(login)] {
		return "reviewer"
	}
	return "other"
}

func reactionClaim(content any, entry any, reason string) (string, any) {
	switch content {
	case "+1":
		return "helpful", nil
	case "-1":
		if reason != "" {
			return "false-positive", core.TriageClaim{
				Sig: jq.StrOf(readcommon.FieldOf(entry, "sig")), Status: "false-positive", Rationale: reason,
			}
		}
	}
	return "", nil
}

func (st *state) attachMatchedReactions(ctx context.Context, base string, comments, entries []any, reviewID string) *core.RefusalError {
	for _, comment := range comments {
		m, ok := comment.(map[string]any)
		if !ok || !matchedReactionComment(m, entries, reviewID) {
			continue
		}
		if ref := st.attachReactionUsers(ctx, base, m); ref != nil {
			return ref
		}
	}
	return nil
}

func (r *reactionSignals) matchEntries(comment any, entries []any, reviewID, reason, author string, reviewers map[string]bool) {
	m, ok := comment.(map[string]any)
	if !ok {
		return
	}
	for _, entry := range entries {
		if reactionCommentMatches(m, entry, reviewID) {
			r.addComment(m, entry, reason, author, reviewers)
		}
	}
}
