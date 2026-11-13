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
