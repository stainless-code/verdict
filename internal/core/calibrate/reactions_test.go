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
		{"self", "Poster", "User", "+1", "", `{"proposals":[],"excluded":{"bot":0,"self":1},"unmatched":0}`},
		{"bot type", "automation", "Bot", "+1", "", `{"proposals":[],"excluded":{"bot":1,"self":0},"unmatched":0}`},
		{"bot suffix", "automation[bot]", "User", "-1", "reason", `{"proposals":[],"excluded":{"bot":1,"self":0},"unmatched":0}`},
		{"PR author", "alice", "User", "-1", "The branch is unreachable.", `{"proposals":[{"sig":"0123456789abcdef0123456789abcdef01234567","proposed":"false-positive","claim":{"sig":"0123456789abcdef0123456789abcdef01234567","status":"false-positive","rationale":"The branch is unreachable."},"reactor":"alice","role":"pr-author"}],"excluded":{"bot":0,"self":0},"unmatched":0}`},
		{"reason required", "alice", "User", "-1", "", `{"proposals":[],"excluded":{"bot":0,"self":0},"unmatched":0}`},
		{"other human", "bob", "User", "+1", "", `{"proposals":[{"sig":"0123456789abcdef0123456789abcdef01234567","proposed":"helpful","claim":null,"reactor":"bob","role":"other"}],"excluded":{"bot":0,"self":0},"unmatched":0}`},
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
	entries := []any{jq.NewObj("kind", "inline", "sig", "0123456789abcdef0123456789abcdef01234567", "body", "finding", "suggestion", "fixed", "path", "x.go")}
	comments := []any{map[string]any{
		"pull_request_review_id": float64(7), "body": "finding\n\n```suggestion\nfixed\n```\n\n<!-- verdict-finding sig=0123456789abcdef0123456789abcdef01234567 -->", "path": "x.go",
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
	want := `{"proposals":[{"sig":"0123456789abcdef0123456789abcdef01234567","proposed":"helpful","claim":null,"reactor":"bob","role":"reviewer"}],"excluded":{"bot":0,"self":0},"unmatched":1}`
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
	if got != `{"proposals":[],"excluded":{"bot":0,"self":0},"unmatched":1}` {
		t.Fatalf("unposted result = %s", got)
	}
}

// TestReactionJudgmentsRefused prevents silently discarded core judgments.
func TestReactionJudgmentsRefused(t *testing.T) {
	for _, req := range []*Request{{Reactions: true, Specs: []string{"sig:false"}}, {Reactions: true, JudgeFile: "judgments.json"}} {
		_, ref := Run(t.Context(), req)
		if ref == nil {
			t.Fatal("reaction judgments accepted")
		}
		if ref.Code != "bad-judgment" || ref.Message != "--judge and --judge-file need --probe (judgments only merge on a probe run)" {
			t.Fatalf("judgment refusal = %v", ref)
		}
	}
}

// TestReactionIdentity rejects absent markers and incorrect finding paths.
func TestReactionIdentity(t *testing.T) {
	tests := []struct {
		name, key, value string
	}{
		{"no marker", "body", "finding"},
		{"different sig", "body", "finding\n\n<!-- verdict-finding sig=ffffffffffffffffffffffffffffffffffffffff -->"},
		{"different path", "path", "other.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, comments := reactionFixture("alice", "User", "+1")
			m := reactionFixtureComment(t, comments)
			m[tt.key] = tt.value
			got := reactionJSON(t, reactionProposals(entries, comments, "7", "reason", "alice"))
			if got != `{"proposals":[],"excluded":{"bot":0,"self":0},"unmatched":1}` {
				t.Fatalf("unmatched result = %s", got)
			}
		})
	}
}

func reactionFixtureComment(t *testing.T, comments []any) map[string]any {
	t.Helper()
	m, ok := comments[0].(map[string]any)
	if !ok {
		t.Fatal("fixture must be object")
	}
	return m
}
