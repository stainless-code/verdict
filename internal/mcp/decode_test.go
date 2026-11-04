package mcp

import (
	"encoding/json"
	"testing"
)

// TestFirstKeyDeterministic proves firstKey names the same key across
// repeated map iterations.
func TestFirstKeyDeterministic(t *testing.T) {
	tests := []struct {
		name string
		obj  map[string]json.RawMessage
		want string
	}{
		{name: "empty", obj: map[string]json.RawMessage{}, want: ""},
		{name: "single", obj: map[string]json.RawMessage{"only": nil}, want: "only"},
		{
			name: "multi key picks the lexicographically first",
			obj: map[string]json.RawMessage{
				"zulu":  nil,
				"alpha": nil,
				"mike":  nil,
				"bravo": nil,
				"oscar": nil,
			},
			want: "alpha",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertFirstKeyStable(t, tt.obj, tt.want)
		})
	}
}

func assertFirstKeyStable(t *testing.T, obj map[string]json.RawMessage, want string) {
	t.Helper()
	for range 1000 {
		if got := firstKey(obj); got != want {
			t.Fatalf("firstKey(%v) = %q; want %q", obj, got, want)
		}
	}
}
