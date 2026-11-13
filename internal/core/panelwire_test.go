package core

import (
	"github.com/stainless-code/verdict/internal/jq"
	"testing"
)

func TestPanelSummaryWire(t *testing.T) {
	sum := &VerificationSummary{Verifiers: 2, Confirmed: 1, Contested: 1, Single: 2}
	cases := []struct {
		name          string
		legacy, panel Wirable
	}{
		{"record data", &RecordData{}, &RecordData{Verification: sum}},
		{"record receipt", &Record{}, &Record{Verification: sum}},
		{"post data", &PostData{}, &PostData{Verification: sum}},
		{"post receipt", &Post{}, &Post{Verification: sum}},
		{"report", &ReportData{}, &ReportData{Verification: sum}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.legacy.Wire().Has("verification") {
				t.Fatal("legacy gained verification")
			}
			value, ok := tt.panel.Wire().Get("verification")
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
		})
	}
}
