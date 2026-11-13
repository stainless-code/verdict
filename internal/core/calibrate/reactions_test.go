package calibratecore

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestReactionProposals filters and attributes human signals.
func TestReactionProposals(t *testing.T) {
	tests := []struct{ name, login, kind, content, reason, want string }{
		{"self", "Poster", "User", "+1", "", `{"proposals":[],"excluded":{"bot":0,"self":1}}`},
		{"bot type", "automation", "Bot", "+1", "", `{"proposals":[],"excluded":{"bot":1,"self":0}}`},
		{"bot suffix", "automation[bot]", "User", "-1", "reason", `{"proposals":[],"excluded":{"bot":1,"self":0}}`},
		{"PR author", "alice", "User", "-1", "The branch is unreachable.", `{"proposals":[{"sig":"abc","proposed":"false-positive","claim":{"sig":"abc","status":"false-positive","rationale":"The branch is unreachable."},"reactor":"alice","role":"pr-author"}],"excluded":{"bot":0,"self":0}}`},
		{"reason required", "alice", "User", "-1", "", `{"proposals":[],"excluded":{"bot":0,"self":0}}`},
		{"other human", "bob", "User", "+1", "", `{"proposals":[{"sig":"abc","proposed":"helpful","claim":null,"reactor":"bob","role":"other"}],"excluded":{"bot":0,"self":0}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, comments := reactionFixture(tt.login, tt.kind, tt.content)
			got := reactionJSON(t, reactionProposals(entries, comments, "7", tt.reason, "alice"))
			if got != tt.want {
				t.Fatalf("reaction result = %s; want %s", got, tt.want)
			}
		})
	}
}

func reactionFixture(login, kind, content string) ([]any, []any) {
	entries := []any{jq.NewObj("kind", "inline", "sig", "abc", "body", "finding", "path", "x.go")}
	comments := []any{map[string]any{
		"pull_request_review_id": float64(7), "body": "finding", "path": "x.go",
		"user":           map[string]any{"login": "poster", "type": "User"},
		"reaction_users": []any{map[string]any{"content": content, "user": map[string]any{"login": login, "type": kind}}},
	}}
	return entries, comments
}

// TestReactionReviewerRole preserves known human reviewer identity.
func TestReactionReviewerRole(t *testing.T) {
	entries, comments := reactionFixture("bob", "User", "+1")
	comments = append(comments, map[string]any{"user": map[string]any{"login": "bob", "type": "User"}})
	got := reactionJSON(t, reactionProposals(entries, comments, "7", "", "alice"))
	want := `{"proposals":[{"sig":"abc","proposed":"helpful","claim":null,"reactor":"bob","role":"reviewer"}],"excluded":{"bot":0,"self":0}}`
	if got != want {
		t.Fatalf("reviewer result = %s; want %s", got, want)
	}
}

func reactionJSON(t *testing.T, v any) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(jq.PrettyOrdered(v))); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestReactionUnsupportedLocal pins the published reaction or rule contract.
func TestReactionUnsupportedLocal(t *testing.T) {
	st := &state{req: &Request{}}
	_, ref := st.runReactions(t.Context(), ".verdict/state/local-demo")
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("local refusal = %v", ref)
	}
}

// TestReactionModeConflicts pins the published reaction or rule contract.
func TestReactionModeConflicts(t *testing.T) {
	for _, req := range []*Request{{Reactions: true, Probe: true}, {ReactionReason: "reason"}} {
		_, ref := Run(t.Context(), req)
		if ref == nil || ref.Code != "bad-judgment" {
			t.Fatalf("mode refusal = %v; want bad-judgment", ref)
		}
	}
}

// TestReactionOtherReview excludes comments from unposted reviews.
func TestReactionOtherReview(t *testing.T) {
	entries, comments := reactionFixture("alice", "User", "+1")
	got := reactionJSON(t, reactionProposals(entries, comments, "8", "reason", "alice"))
	if got != `{"proposals":[],"excluded":{"bot":0,"self":0}}` {
		t.Fatalf("unposted result = %s", got)
	}
}
