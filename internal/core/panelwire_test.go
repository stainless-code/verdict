package core

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestPanelSummaryWire(t *testing.T) {
	sum := &PanelSummary{Verifiers: 2, Confirmed: 1, Contested: 1, Single: 2}
	cases := []struct {
		legacy Wirable
		panel  Wirable
		name   string
	}{
		{name: "record data", legacy: &RecordData{}, panel: &RecordData{Verification: sum}},
		{name: "record receipt", legacy: &Record{}, panel: &Record{Verification: sum}},
		{name: "post data", legacy: &PostData{}, panel: &PostData{Verification: sum}},
		{name: "post receipt", legacy: &Post{}, panel: &Post{Verification: sum}},
		{name: "report", legacy: &ReportData{}, panel: &ReportData{Verification: sum}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) { assertSummaryWire(t, tt.legacy, tt.panel) })
	}
}

func assertSummaryWire(t *testing.T, legacy, panel Wirable) {
	t.Helper()
	if legacy.Wire().Has("verification") {
		t.Fatal("legacy gained verification")
	}
	value, ok := panel.Wire().Get("verification")
	if !ok {
		t.Fatal("missing verification summary")
	}
	obj, ok := Wire(value).(*jq.Obj)
	if !ok {
		t.Fatalf("summary type %T", Wire(value))
	}
	if got := obj.MarshalCompact(); got != `{"verifiers":2,"confirmed":1,"contested":1,"single":2}` {
		t.Fatalf("summary=%s", got)
	}
}

func TestPanelDeclarationOrder(t *testing.T) {
	cases := []struct {
		typ    reflect.Type
		fields []string
	}{
		{reflect.TypeFor[FindingEntry](), []string{"Rationale", "Verification", "Confidence", "TriagedAt", "TriagedBy", "History"}},
		{reflect.TypeFor[RecordData](), []string{"SecuritySummary", "Verification", "Items"}},
		{reflect.TypeFor[Record](), []string{"Repairs", "Verification", "Coverage"}},
	}
	for _, tc := range cases {
		t.Run(tc.typ.Name(), func(t *testing.T) {
			assertPanelDeclarationOrder(t, tc.typ, tc.fields)
		})
	}
}

func TestRecordPanelOrderGolden(t *testing.T) {
	confidence, percent := 3, 100
	security, model, patch := "reviewed", "verifier", "patch"
	record := &Record{
		SchemaVersion: "1", Status: "local", Head: "head", DiffSHA256: "diff", RecordedAt: "2026-11-13T12:00:00Z",
		Findings: 1, Open: 1, Confidence: &confidence, ConfidenceRaw: 3, SecuritySummary: &security,
		ToolVersion: "version", PromptsHash: "hash", Model: &model, PatchID: &patch, PatchIDKind: "git",
		Repairs:      []Repair{{Code: "enum-synonym", Path: "$.comments"}},
		Verification: &PanelSummary{Verifiers: 2, Confirmed: 1, Contested: 0, Single: 0},
		Coverage:     &CoverageSummary{Percent: &percent, Attested: 1, Total: 1, Files: 1}, ChunkCoverage: &ChunkCoverage{Attested: 1, Total: 1},
	}
	golden.Check(t, "record-panel-order", record.Wire().MarshalCompact()+"\n")
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	golden.Check(t, "record-panel-order", string(raw)+"\n")
}

func assertPanelDeclarationOrder(t *testing.T, typ reflect.Type, fields []string) {
	t.Helper()
	previous := -1
	for _, name := range fields {
		field, ok := typ.FieldByName(name)
		if !ok || field.Index[0] <= previous {
			t.Fatalf("%s declaration is out of published wire order", name)
		}
		previous = field.Index[0]
	}
}
