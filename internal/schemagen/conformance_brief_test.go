package schemagen

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// fixtureBrief is the shared machine brief used by the brief conformance
// checks.
func fixtureBrief() map[string]any {
	return map[string]any{
		"sites": []any{
			map[string]any{"path": "notes.txt", "line": 2},
			map[string]any{"path": "other.txt"},
		},
		"outcome":   "Reject duplicate deliveries before anything is enqueued.",
		"approach":  "Track delivery ids in a bounded LRU checked at the top of the handler.",
		"questions": []any{"Should the dedupe window be 24 hours or 7 days?"},
	}
}

// fixtureReviewDoc renders one review document carrying the fixture brief.
func fixtureReviewDoc(brief any) map[string]any {
	return map[string]any{
		"schema_version": "1",
		"body":           "> ✅ Brief conformance review with enough length.",
		"approved":       false,
		"comments": []any{map[string]any{
			"path": "notes.txt", "line": 2, "side": "RIGHT",
			"body": "Brief-carrying finding.", "rigor": "walked", "brief": brief,
		}},
	}
}

// TestReviewBriefSchema pins the published review contract: a valid brief
// validates, and every documented shape failure is refused.
func TestReviewBriefSchema(t *testing.T) {
	doc := loadSchemaDocument(t, "review.schema.json")
	if err := validationError(doc, doc, fixtureReviewDoc(fixtureBrief())); err != nil {
		t.Fatalf("valid brief does not validate: %v", err)
	}
	cases := map[string]any{
		"missing-outcome": map[string]any{
			"sites": []any{map[string]any{"path": "notes.txt"}},
		},
		"site-without-path": map[string]any{
			"outcome": "Reject duplicate deliveries before anything is enqueued.",
			"sites":   []any{map[string]any{"line": 2}},
		},
		"non-object": "Reject duplicate deliveries before anything is enqueued.",
	}
	for name, brief := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validationError(doc, doc, fixtureReviewDoc(brief)); err == nil {
				t.Error("invalid brief validated")
			}
		})
	}
}

// TestFindingsBriefConformance proves a stored brief validates against the
// findings schema, round-trips through the modeled shape, and that a
// non-object brief is refused.
func TestFindingsBriefConformance(t *testing.T) {
	doc := loadSchemaDocument(t, "findings.schema.json")
	entry := `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body.","suggestion":null,"kb":[],` +
		`"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"brief":{"sites":[{"path":"notes.txt","line":2},{"path":"other.txt"}],` +
		`"outcome":"Reject duplicate deliveries before anything is enqueued.",` +
		`"approach":"Track delivery ids in a bounded LRU checked at the top of the handler.",` +
		`"questions":["Should the dedupe window be 24 hours or 7 days?"]},` +
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`

	ordered, err := decodeOrdered(t, entry)
	if err != nil {
		t.Fatal(err)
	}
	wired := core.FindingEntryOf(ordered).Wire()
	decoded := decodeWire(t, wired)
	validateWire(t, doc, "$", doc, []any{decoded})

	var modeled []FindingEntry
	unmarshalWire(t, []any{decoded}, &modeled)
	assertModeledBrief(t, modeled[0].Brief)

	brokenEntry, err := decodeOrdered(t,
		`{"kind":"inline","path":"notes.txt","body":"Usable body.","brief":"text"}`)
	if err != nil {
		t.Fatal(err)
	}
	broken := decodeWire(t, core.FindingEntryOf(brokenEntry).Wire())
	if err := validationError(doc, doc, []any{broken}); err == nil {
		t.Error("non-object brief validated")
	}
}

// TestShowFixBriefConformance proves show --json carries the stored brief in
// data.fix and validates against the envelope schema.
func TestShowFixBriefConformance(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	t.Chdir(t.TempDir())
	writeFixture(t, ".verdict/state/5201/findings.json", "["+fixtureFindingWithBrief()+"]")
	read := core.ReadRequest{Repo: "acme/app", Pr: "5201"}
	resp, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{
		ReadRequest: read,
		Sig:         "c1111111",
	})
	if ref != nil {
		t.Fatalf("RunShow refusal: %v", ref)
	}
	data := jq.NewObj("item", resp.Item.Raw)
	data.Set("fix", resp.Fix.Wire())
	validateWire(t, doc, "$defs.showData", defOf(t, doc, "showData"), decodeWire(t, data))

	var modeled ShowData
	unmarshalWire(t, data, &modeled)
	assertModeledBrief(t, modeled.Fix.Brief)
}

// fixtureFindingWithBrief is fixtureFinding plus the shared machine brief.
func fixtureFindingWithBrief() string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"brief":{"sites":[{"path":"notes.txt","line":2},{"path":"other.txt"}],` +
		`"outcome":"Reject duplicate deliveries before anything is enqueued.",` +
		`"approach":"Track delivery ids in a bounded LRU checked at the top of the handler.",` +
		`"questions":["Should the dedupe window be 24 hours or 7 days?"]},` +
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// assertModeledBrief checks the modeled brief field for field.
func assertModeledBrief(t *testing.T, b *Brief) {
	t.Helper()
	if b == nil {
		t.Fatal("brief is nil")
	}
	if b.Outcome != "Reject duplicate deliveries before anything is enqueued." {
		t.Errorf("brief outcome = %q", b.Outcome)
	}
	if b.Approach != "Track delivery ids in a bounded LRU checked at the top of the handler." {
		t.Errorf("brief approach = %q", b.Approach)
	}
	if len(b.Sites) != 2 || b.Sites[0].Path != "notes.txt" || b.Sites[0].Line == nil ||
		*b.Sites[0].Line != 2 || b.Sites[1].Path != "other.txt" || b.Sites[1].Line != nil {
		t.Errorf("brief sites = %+v", b.Sites)
	}
	if len(b.Questions) != 1 || b.Questions[0] != "Should the dedupe window be 24 hours or 7 days?" {
		t.Errorf("brief questions = %v", b.Questions)
	}
}
