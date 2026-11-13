package core

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestPanelSummaryWire pins summary counts and legacy omission.
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
