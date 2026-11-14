package indexcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestListReceipt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want              string
		returned, hidden, depth int
	}{
		{name: "complete", returned: 2, hidden: 0, depth: 0, want: `{"rows":[]}`},
		{name: "response", returned: 2, hidden: 3, depth: 0, want: `{"rows":[],"truncated":true,"truncation_reason":"response_cap","returned":2,"total":5,"next_offset":null}`},
		{name: "empty", returned: 0, hidden: 0, depth: 1, want: `{"rows":[],"truncated":true,"truncation_reason":"work_budget","returned":0,"total":null,"next_offset":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := listReceipt(jq.NewObj("rows", []any{}), tc.returned, tc.hidden, tc.depth).MarshalCompact()
			if got != tc.want {
				t.Fatalf("receipt = %s, want %s", got, tc.want)
			}
		})
	}
}
