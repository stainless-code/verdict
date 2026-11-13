package calibratecore

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestReactionProposals pins the published reaction or rule contract.
func TestReactionProposals(t *testing.T) {
	entries := []any{jq.NewObj("kind", "inline", "sig", "abc", "body", "finding", "path", "x.go", "status", "open")}
	raw := `[[{"pull_request_review_id":7,"body":"finding","path":"x.go","reactions":{"+1":1,"-1":1}},{"pull_request_review_id":8,"body":"finding","path":"x.go","reactions":{"-1":1}}]]`
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	pages, ok := doc.([]any)
	if !ok {
		t.Fatal("pages must be an array")
	}
	got := reactionJSON(t, reactionProposals(entries, pages, "7", ""))
	if got != `[{"sig":"abc","proposed":"helpful","claim":null}]` {
		t.Fatalf("unreasoned proposals = %s", got)
	}
	got = reactionJSON(t, reactionProposals(entries, pages, "7", "The cited branch is unreachable."))
	want := `[{"sig":"abc","proposed":"false-positive","claim":{"sig":"abc","status":"false-positive","rationale":"The cited branch is unreachable."}},{"sig":"abc","proposed":"helpful","claim":null}]`
	if got != want {
		t.Fatalf("reasoned proposals = %s; want %s", got, want)
	}
	if got := reactionJSON(t, reactionProposals(entries, pages, "9", "reason")); got != `[]` {
		t.Fatalf("unposted review proposals = %s", got)
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
