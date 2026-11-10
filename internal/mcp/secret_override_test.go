package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestDecodePostCarriesTheSecretOverride proves the MCP surface reaches the
// service flag. The schema documents `allowSecretValues` as the way an agent
// publishes a review whose text carries a secret-shaped value; a decoder that
// dropped it would leave that agent no override and no error saying so.
func TestDecodePostCarriesTheSecretOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args string
		want bool
	}{
		{name: "override set", args: `{"flags":{"allowSecretValues":true}}`, want: true},
		{name: "override absent", args: `{"flags":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := decodePostOverride(t, tc.args); got != tc.want {
				t.Errorf("decodePost(%s).AllowSecretValues = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// decodePostOverride decodes one post argument object and returns the override
// flag it produced, failing on an argument the decoder refuses.
func decodePostOverride(t *testing.T, args string) bool {
	t.Helper()
	a, err := newArgs(json.RawMessage(args))
	if err != nil {
		t.Fatalf("newArgs(%s): %v", args, err)
	}
	var req core.PostRequest
	decodePost(&req, a)
	if err := a.done(); err != nil {
		t.Fatalf("decodePost(%s): %v", args, err)
	}
	return req.Flags.AllowSecretValues
}
