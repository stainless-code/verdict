package calibratecore

import (
	"bytes"
	"encoding/json"
	"github.com/stainless-code/verdict/internal/jq"
	"testing"
)

func TestReactionProposals(t *testing.T) {
	entries := []any{jq.NewObj("kind", "inline", "sig", "abc", "body", "finding", "path", "x.go", "status", "open")}
	raw := `[[{"pull_request_review_id":7,"body":"finding","path":"x.go","reactions":{"+1":1,"-1":1}},{"pull_request_review_id":8,"body":"finding","path":"x.go","reactions":{"-1":1}}]]`
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := doc.([]any)
	got := reactionJSON(reactionProposals(entries, pages, "7", ""))
	if got != `[{"sig":"abc","proposed":"helpful","claim":null}]` {
		t.Fatalf("unreasoned proposals = %s", got)
	}
	got = reactionJSON(reactionProposals(entries, pages, "7", "The cited branch is unreachable."))
	want := `[{"sig":"abc","proposed":"false-positive","claim":{"sig":"abc","status":"false-positive","rationale":"The cited branch is unreachable."}},{"sig":"abc","proposed":"helpful","claim":null}]`
	if got != want {
		t.Fatalf("reasoned proposals = %s; want %s", got, want)
	}
	if got := reactionJSON(reactionProposals(entries, pages, "9", "reason")); got != `[]` {
		t.Fatalf("unposted review proposals = %s", got)
	}
	if status, _ := entries[0].(*jq.Obj).Get("status"); status != "open" {
		t.Fatalf("status changed to %v", status)
	}
}

func reactionJSON(v any) string {
	var out bytes.Buffer
	_ = json.Compact(&out, []byte(jq.PrettyOrdered(v)))
	return out.String()
}
