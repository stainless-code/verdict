package threadscore

import (
	"context"
	"testing"
)

// Finding sigs the reaction fixtures agree on.
const (
	sigOne = "0123456789abcdef0123456789abcdef01234567"
	sigTwo = "1123456789abcdef0123456789abcdef01234567"
)

// reactionReadDouble answers one endpoint from a canned map and fails the test
// on an endpoint the case did not script, so a reader that widens its sweep
// cannot pass silently.
type reactionReadDouble struct {
	pages map[string][]any
	fail  map[string]bool
	calls []string
}

func (r *reactionReadDouble) read(_ context.Context, endpoint string) ([]any, bool) {
	r.calls = append(r.calls, endpoint)
	if r.fail[endpoint] {
		return nil, false
	}
	rows, ok := r.pages[endpoint]
	return rows, ok
}

func TestReactionPages(t *testing.T) {
	t.Parallel()
	row := func(id float64) map[string]any { return map[string]any{"id": id} }
	cases := []struct {
		doc  any
		name string
		want int
	}{
		{name: "list pages splice", doc: []any{[]any{row(1), row(2)}, []any{row(3)}}, want: 3},
		{name: "object pages are rows", doc: []any{row(1), row(2)}, want: 2},
		{name: "mixed pages", doc: []any{[]any{row(1)}, row(2)}, want: 2},
		{name: "not an array", doc: map[string]any{"id": 1}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := len(ReactionPages(tc.doc)); got != tc.want {
				t.Fatalf("ReactionPages rows = %d, want %d", got, tc.want)
			}
		})
	}
}

// plusOne is one 👍 reaction by a human login.
func plusOne(login string) map[string]any {
	return map[string]any{"content": "+1", "user": map[string]any{"login": login, "type": "User"}}
}

func TestPlusOneLogins(t *testing.T) {
	t.Parallel()
	reactions := []any{
		plusOne("alice"),
		map[string]any{"content": "+1", "user": map[string]any{"login": "dependabot[bot]", "type": "Bot"}},
		map[string]any{"content": "+1", "user": map[string]any{"login": "renovate[bot]", "type": "User"}},
		plusOne("poster"),
		map[string]any{"content": "-1", "user": map[string]any{"login": "bob", "type": "User"}},
		map[string]any{"content": "+1", "user": map[string]any{"login": "", "type": "User"}},
	}
	got := PlusOneLogins(reactions, "poster")
	if len(got) != 1 || got[0] != "alice" {
		t.Fatalf("PlusOneLogins = %v, want [alice]: bots, the comment's own poster, and -1 never count", got)
	}
}

// verdictThread is one verdict root comment and one reply to it, both carrying
// finding markers: only the root is a comment verdict posted.
func verdictThread() (root, reply map[string]any) {
	body := func(sig string) string {
		return "A finding body.\n\n<!-- verdict-finding sig=" + sig + " -->"
	}
	posted := func(id float64, sig string) map[string]any {
		return map[string]any{
			"id":   id,
			"body": body(sig),
			"user": map[string]any{"login": "verdict", "type": "User"},
		}
	}
	root = posted(11, sigOne)
	reply = posted(12, sigTwo)
	reply[keyInReplyTo] = 11.0
	return root, reply
}

func TestSelectedSigs(t *testing.T) {
	t.Parallel()
	root, reply := verdictThread()
	double := &reactionReadDouble{
		pages: map[string][]any{
			"repos/o/r/pulls/comments/11/reactions": {plusOne("alice")},
			"repos/o/r/pulls/comments/12/reactions": {plusOne("bob")},
		},
		fail: map[string]bool{},
	}
	sigs, ok := SelectedSigs(context.Background(), double.read, "o/r", []any{root, reply})
	if !ok {
		t.Fatal("SelectedSigs reported a failed read")
	}
	if len(sigs) != 1 || sigs[0] != sigOne {
		t.Fatalf("SelectedSigs = %v, want [%s]: a reply is never a root", sigs, sigOne)
	}
	if len(double.calls) != 1 || double.calls[0] != "repos/o/r/pulls/comments/11/reactions" {
		t.Fatalf("reads = %v, want only the root comment's reactions", double.calls)
	}

	double.fail["repos/o/r/pulls/comments/11/reactions"] = true
	if sigs, ok := SelectedSigs(context.Background(), double.read, "o/r", []any{root}); ok || sigs != nil {
		t.Fatalf("SelectedSigs on a failed read = %v, %v; want no partial selection", sigs, ok)
	}
}

func TestCommentReactionLogins(t *testing.T) {
	t.Parallel()
	root, _ := verdictThread()
	double := &reactionReadDouble{
		pages: map[string][]any{"repos/o/r/pulls/comments/11/reactions": {plusOne("alice")}},
		fail:  map[string]bool{},
	}
	logins, ok := CommentReactionLogins(context.Background(), double.read, "o/r", root)
	if !ok || len(logins) != 1 || logins[0] != "alice" {
		t.Fatalf("CommentReactionLogins = %v, %v; want [alice], true", logins, ok)
	}
	if _, joined := root[keyReactionList].([]any); !joined {
		t.Fatalf("the joined comment carries no %s rows", keyReactionList)
	}
	if _, ok := CommentReactionLogins(context.Background(), double.read, "o/r",
		map[string]any{"id": "seven"}); ok {
		t.Fatal("a comment without a numeric id reported a successful read")
	}
}
