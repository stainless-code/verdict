package threadscore

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// ReactionRead reads one paginated GitHub API endpoint and returns its rows
// with every page flattened. false is a failed read: callers map it onto
// their verb's refusal or warning, never onto an empty result.
type ReactionRead func(ctx context.Context, endpoint string) ([]any, bool)

const (
	keyReactionUser = "user"
	keyReactionList = "reaction_users"
	keyInReplyTo    = "in_reply_to_id"
	reactionPlusOne = "+1"
)

// ReactionPages flattens a `--paginate --slurp` payload into its rows: one
// page per slurped document. A list endpoint pages arrays, so a page is
// spliced; a single-object endpoint (`pulls/<n>`) pages objects, so the page
// is the row.
func ReactionPages(doc any) []any {
	pages, ok := doc.([]any)
	if !ok {
		return nil
	}
	var items []any
	for _, page := range pages {
		if rows, ok := page.([]any); ok {
			items = append(items, rows...)
			continue
		}
		items = append(items, page)
	}
	return items
}

// ReviewComments reads every review comment on the PR: the reaction join's
// input, carrying each body, path, poster, and thread root flag.
func ReviewComments(ctx context.Context, read ReactionRead, repo, pr string) ([]any, bool) {
	return read(ctx, "repos/"+repo+"/pulls/"+pr+"/comments")
}

// CommentReactions reads one review comment's reaction rows.
func CommentReactions(ctx context.Context, read ReactionRead, repo string, id int) ([]any, bool) {
	return read(ctx, fmt.Sprintf("repos/%s/pulls/comments/%d/reactions", repo, id))
}

// ReactionUser reads a GitHub user object: its login and whether the host (or
// GitHub's login suffix) marks it a bot.
func ReactionUser(raw any) (string, bool) {
	user, ok := jq.AsMap(raw)
	if !ok {
		return "", false
	}
	login := jq.StrOf(user[keyLogin])
	return login, user["type"] == hostTypeBot || strings.HasSuffix(strings.ToLower(login), "[bot]")
}

// RootComment reports whether a review comment starts its thread: a reply
// carries in_reply_to_id, and only the root is the comment verdict posted.
func RootComment(comment map[string]any) bool {
	return comment[keyInReplyTo] == nil
}

// FindingSigOf reads the finding sig a review comment body carries as its
// final-line verdict marker.
func FindingSigOf(comment map[string]any) (string, bool) {
	return FindingSig(jq.StrOf(comment["body"]))
}

// PlusOneLogins returns the human logins that reacted 👍 on one comment, less
// the comment's own poster (a self-reaction says nothing) and bots.
func PlusOneLogins(reactions []any, poster string) []string {
	var logins []string
	for _, raw := range reactions {
		reaction, ok := jq.AsMap(raw)
		if !ok || reaction["content"] != reactionPlusOne {
			continue
		}
		login, bot := ReactionUser(reaction[keyReactionUser])
		if bot || login == "" || strings.EqualFold(login, poster) {
			continue
		}
		logins = append(logins, login)
	}
	return logins
}

// WithReactions attaches one comment's reaction rows under reaction_users, the
// key every reader of a joined comment reads them from.
func WithReactions(comment map[string]any, reactions []any) {
	comment[keyReactionList] = reactions
}

// CommentReactionLogins reads a comment's reaction rows through read and
// returns its human 👍 logins.
func CommentReactionLogins(ctx context.Context, read ReactionRead, repo string, comment map[string]any) ([]string, bool) {
	id, ok := readcommon.NumVal(comment["id"])
	if !ok || id <= 0 {
		return nil, false
	}
	reactions, ok := CommentReactions(ctx, read, repo, int(id))
	if !ok {
		return nil, false
	}
	WithReactions(comment, reactions)
	poster, _ := ReactionUser(comment[keyReactionUser])
	return PlusOneLogins(reactions, poster), true
}

// SelectedSigs returns the sorted, deduplicated finding sigs whose root
// verdict comment carries at least one human 👍. false reports a failed
// reaction read, so the caller writes no partial selection.
func SelectedSigs(ctx context.Context, read ReactionRead, repo string, comments []any) ([]string, bool) {
	var sigs []string
	seen := make(map[string]bool, len(comments))
	for _, raw := range comments {
		sig, ok := reactedSig(ctx, read, repo, raw)
		if !ok {
			return nil, false
		}
		if sig == "" || seen[sig] {
			continue
		}
		seen[sig] = true
		sigs = append(sigs, sig)
	}
	slices.Sort(sigs)
	return sigs, true
}

// reactedSig reads one comment's sig when it is a reacted verdict root (""
// for every other comment). false reports a failed reaction read.
func reactedSig(ctx context.Context, read ReactionRead, repo string, raw any) (string, bool) {
	comment, ok := jq.AsMap(raw)
	if !ok || !RootComment(comment) {
		return "", true
	}
	sig, marked := FindingSigOf(comment)
	if !marked {
		return "", true
	}
	logins, ok := CommentReactionLogins(ctx, read, repo, comment)
	if !ok {
		return "", false
	}
	if len(logins) == 0 {
		return "", true
	}
	return sig, true
}
