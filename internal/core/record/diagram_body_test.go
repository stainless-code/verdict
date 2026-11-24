package recordcore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

// diagramReview is one clean review over bundle()'s diff carrying the named
// diagram block, with the receipt that diff requires.
func diagramReview(diagram string) string {
	return `{"schema_version":"1","summary":"Behavior diagram probe.","findings":[],` +
		`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]` + diagram + `}`
}

// anchoredReview is the diagram every body case starts from: one node and one
// edge, both quoting the changed line the bundle carries.
const anchoredReview = `,"diagram":{"type":"sequence","title":"Record flow",` +
	`"nodes":[{"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"new line"}},` +
	`{"id":"b","label":"Service","evidence":{"path":"notes.txt","line":3,"quote":"bottom"}}],` +
	`"edges":[{"from":"a","to":"b","label":"calls","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}`

// forgedReview is the same shape with every quote the diff does not carry.
const forgedReview = `,"diagram":{"type":"flow",` +
	`"nodes":[{"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"forged line"}}]}`

// runDiagram records one bundle carrying the named block and returns the
// response. anchored is the knob value the request runs with.
func runDiagram(t *testing.T, name, slug, diagram, anchored string) *recordcore.Response {
	t.Helper()
	bundle(t, slug, diagramReview(diagram))
	c := &testkit.Collector{}
	req := recordRequest(t, c, slug)
	req.Effective.DiagramsAnchored = anchored
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("%s: Run refusal: %v", name, ref)
	}
	return resp
}

// TestRecordBodyCarriesTheVerifiedDiagram proves the knob is the whole gate and
// that the body carries the verified block with the counts the receipt stores.
func TestRecordBodyCarriesTheVerifiedDiagram(t *testing.T) {
	resp := runDiagram(t, "anchored", "local-anchored", anchoredReview, core.FlagOn)
	bodyContains(t, resp.Body,
		"<summary>Behavior flow (verified against the diff — 3/3 elements anchored)</summary>\n\n"+
			"```mermaid\nsequenceDiagram\n    accTitle: Record flow\n"+
			"    participant n1 as Caller\n"+
			"    participant n2 as Service\n"+
			"    n1->>n2: calls\n```\n\n</details>",
	)
	want := core.EvidenceDiagram{Type: "sequence", Title: "Record flow", Supplied: 3, Verified: 3}
	if got := resp.Data.Evidence.Diagram; got == nil || *got != want {
		t.Errorf("evidence.diagram = %+v, want the settled 3/3 sequence receipt %+v", got, want)
	}
	// record.json persists the same receipt, so a later reader never re-guesses.
	raw, err := os.ReadFile(".verdict/state/local-anchored/record.json")
	if err != nil {
		t.Fatal(err)
	}
	bodyContains(t, string(raw),
		`"diagram":{"type":"sequence","title":"Record flow","supplied":3,"verified":3,"dropped":0}`)
}

// TestRecordBodyOmitsTheDiagramWithTheKnobOff proves the opt-in default: with
// the knob off the body keeps the bytes it had before the block existed, while
// the receipt still records what the diff proved.
func TestRecordBodyOmitsTheDiagramWithTheKnobOff(t *testing.T) {
	resp := runDiagram(t, "knob off", "local-diagramoff", anchoredReview, core.FlagOff)
	if strings.Contains(resp.Body, "Behavior flow") || strings.Contains(resp.Body, "```mermaid") {
		t.Errorf("body carries a diagram with the knob off:\n%s", resp.Body)
	}
	if resp.Data.Evidence.Diagram == nil || resp.Data.Evidence.Diagram.Verified != 3 {
		t.Errorf("evidence.diagram = %+v, want the verified counts even when not drawn", resp.Data.Evidence.Diagram)
	}
}

// TestRecordBodyOmitsAForgedDiagram proves a diagram the diff cannot prove is
// left out whole with one note, and that the run still records: a diagram never
// fails a review.
func TestRecordBodyOmitsAForgedDiagram(t *testing.T) {
	resp := runDiagram(t, "forged", "local-forged", forgedReview, core.FlagOn)
	const want = "> Behavioral diagram omitted: none of the 1 supplied elements verified against the diff"
	if !strings.Contains(resp.Body, want) {
		t.Errorf("body is missing %q:\n%s", want, resp.Body)
	}
	if strings.Contains(resp.Body, "```mermaid") {
		t.Errorf("omitted diagram still rendered a fence:\n%s", resp.Body)
	}
	got := resp.Data.Evidence.Diagram
	if got == nil || got.Omitted == "" || got.Dropped != 1 {
		t.Errorf("evidence.diagram = %+v, want a dropped element and the omission reason", got)
	}
}

// TestRecordBodyOmitsAnOutOfScopeType proves a type outside the vocabulary
// renders the note, records no type, and never refuses the review.
func TestRecordBodyOmitsAnOutOfScopeType(t *testing.T) {
	diagram := `,"diagram":{"type":"gantt","nodes":[{"id":"a","label":"A",` +
		`"evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}`
	resp := runDiagram(t, "out of scope", "local-gantt", diagram, core.FlagOn)
	if !strings.Contains(resp.Body, `> Behavioral diagram omitted: type "gantt" is out of scope`) {
		t.Errorf("body is missing the out-of-scope note:\n%s", resp.Body)
	}
	got := resp.Data.Evidence.Diagram
	if got == nil || got.Type != "" || got.Omitted == "" {
		t.Errorf("evidence.diagram = %+v, want no type and the omission reason", got)
	}
}

// TestRecordBodyDropsOnlyTheMalformedElement proves the decoder is per element:
// a node whose line is the wrong JSON type counts as supplied and drops, while
// the rest of the diagram still verifies and renders.
func TestRecordBodyDropsOnlyTheMalformedElement(t *testing.T) {
	diagram := `,"diagram":{"type":"flow","nodes":[` +
		`{"id":"a","label":"Caller","evidence":{"path":"notes.txt","line":2,"quote":"new line"}},` +
		`{"id":"b","label":"Service","evidence":{"path":"notes.txt","line":3,"quote":"bottom"}},` +
		`{"id":"c","label":"Broken","evidence":{"path":"notes.txt","line":"2","quote":"new line"}}]}`
	resp := runDiagram(t, "malformed element", "local-malformed", diagram, core.FlagOn)
	for _, want := range []string{
		"<summary>Behavior flow (verified against the diff — 2/3 elements anchored)</summary>",
		"    n1[\"Caller\"]\n    n2[\"Service\"]\n```",
	} {
		if !strings.Contains(resp.Body, want) {
			t.Errorf("body is missing %q:\n%s", want, resp.Body)
		}
	}
	if strings.Contains(resp.Body, "Broken") {
		t.Errorf("body drew the malformed node:\n%s", resp.Body)
	}
	got := resp.Data.Evidence.Diagram
	if got == nil || got.Supplied != 3 || got.Verified != 2 || got.Dropped != 1 {
		t.Errorf("evidence.diagram = %+v, want 3 supplied, 2 verified, 1 dropped", got)
	}
}

// TestRecordBodyNotesAnUnreadableDiagram proves a diagram value that is not an
// object leaves one honest line and a receipt, never silence and never a
// refusal.
func TestRecordBodyNotesAnUnreadableDiagram(t *testing.T) {
	resp := runDiagram(t, "unreadable", "local-unreadable", `,"diagram":"sequenceDiagram\n  A->>B: hi"`, core.FlagOn)
	const want = "> Behavioral diagram omitted: the diagram value is not an object"
	if !strings.Contains(resp.Body, want) {
		t.Errorf("body is missing %q:\n%s", want, resp.Body)
	}
	if strings.Contains(resp.Body, "A->>B") {
		t.Errorf("raw mermaid reached the body:\n%s", resp.Body)
	}
	got := resp.Data.Evidence.Diagram
	if got == nil || got.Omitted == "" || got.Supplied != 0 {
		t.Errorf("evidence.diagram = %+v, want an omitted receipt with nothing supplied", got)
	}
}

// bodyContains fails the test for every wanted fragment text does not carry.
func bodyContains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}
