package recordcore_test

import (
	"context"
	"reflect"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRecordStampsTheAuthorPair proves record.json carries the fetch
// fingerprint as `author` and the reviewer models cost.json already names as
// `review_models`, and that a bundle naming neither stamps null and [] rather
// than inventing a human author.
func TestRecordStampsTheAuthorPair(t *testing.T) {
	bundle(t, "local-pair", reviewJSON(""))
	writeFile(t, ".verdict/state/local-pair/author.json",
		`{"schema_version":"1","agent":"claude","model":"Opus 4.5","confidence":"high",`+
			`"evidence":[{"field":"commits","signal":"Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}]}`)
	writeFile(t, ".verdict/state/local-pair/cost.json",
		`{"schema_version":"2","models":[{"name":"gpt-5.1-codex","input_tokens":10},`+
			`{"name":"gpt-5.1-codex","input_tokens":4},{"input_tokens":2}]}`)
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-pair")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	rec := decodeObject(t, ".verdict/state/local-pair/record.json")
	wantAuthor := map[string]any{"agent": "claude", "model": "Opus 4.5"}
	if got := rec["author"]; !reflect.DeepEqual(got, wantAuthor) {
		t.Errorf("author = %#v, want %#v", got, wantAuthor)
	}
	if got, ok := rec["review_models"].([]any); !ok || !reflect.DeepEqual(got, []any{"gpt-5.1-codex"}) {
		t.Errorf("review_models = %#v, want [gpt-5.1-codex]", rec["review_models"])
	}
}

// TestRecordIgnoresNonObjectCostModels proves a models[] entry that is not an
// object contributes no reviewer name instead of panicking the record: a null
// entry passes the shared shape gate, and a list of non-objects fails it.
func TestRecordIgnoresNonObjectCostModels(t *testing.T) {
	cases := []struct{ name, models, want string }{
		{name: "local-null-entry", models: `[null,{"name":"gpt-5.1-codex"}]`, want: `["gpt-5.1-codex"]`},
		{name: "local-string-entry", models: `["x",1]`, want: `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCostModelsRecord(t, tc.name, tc.models, tc.want) })
	}
}

func assertCostModelsRecord(t *testing.T, slug, models, want string) {
	t.Helper()
	bundle(t, slug, reviewJSON(""))
	writeFile(t, ".verdict/state/"+slug+"/cost.json", `{"schema_version":"2","models":`+models+`}`)
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, slug)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	rec := decodeObject(t, ".verdict/state/"+slug+"/record.json")
	if got := jq.Compact(rec["review_models"]); got != want {
		t.Errorf("review_models = %s, want %s", got, want)
	}
}

// TestRecordStampsNoAuthorWithoutAFingerprint holds the absent case to the
// published null: an unknown author is never guessed as human.
func TestRecordStampsNoAuthorWithoutAFingerprint(t *testing.T) {
	bundle(t, "local-nopair", reviewJSON(""))
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-nopair")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	rec := decodeObject(t, ".verdict/state/local-nopair/record.json")
	if got, present := rec["author"]; !present || got != nil {
		t.Errorf("author = %#v (present %v), want an explicit null", got, present)
	}
	if got, ok := rec["review_models"].([]any); !ok || len(got) != 0 {
		t.Errorf("review_models = %#v, want []", rec["review_models"])
	}
}
