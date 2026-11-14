package core

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestTruncationReceipt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		receipt *Truncation
		want    string
	}{
		{"complete", nil, `{"items":[]}`},
		{"empty scan", &Truncation{Truncated: true, TruncationReason: "scan_cap"}, `{"items":[],"truncated":true,"truncation_reason":"scan_cap","returned":0,"total":null,"next_offset":null}`},
		{"page", &Truncation{Truncated: true, TruncationReason: "response_cap", Returned: 2, Total: new(5), NextOffset: new(2)}, `{"items":[],"truncated":true,"truncation_reason":"response_cap","returned":2,"total":5,"next_offset":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.receipt.Apply(jq.NewObj("items", []any{})).MarshalCompact()
			if got != tc.want {
				t.Fatalf("receipt = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestForResultRestatesInResultDomain proves a source receipt restated for a
// result domain keeps its reason, reports the result count, and drops the
// scan's total and continuation (unknown in the new domain). A false source
// receipt stays nil, and nil stays nil.
func TestForResultRestatesInResultDomain(t *testing.T) {
	t.Parallel()
	total, next := 9, 4
	source := ResultCut("scan_cap", 3, &total, &next)
	got := source.ForResult(7)
	if got == nil || got.TruncationReason != "scan_cap" || got.Returned != 7 ||
		got.Total != nil || got.NextOffset != nil {
		t.Fatalf("ForResult = %+v", got)
	}
	if ResultCut("chunk_cap", 1, nil, nil).ForResult(0) == nil {
		t.Fatal("a truncated source lost its receipt")
	}
	if (&Truncation{}).ForResult(2) != nil {
		t.Fatal("a complete source invented a receipt")
	}
	var nilReceipt *Truncation
	if nilReceipt.ForResult(2) != nil {
		t.Fatal("nil receipt invented a receipt")
	}
}
