package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
)

// TestFetchLocalScanRejectsNull proves an explicit `"scan": null` refuses at
// decode time while an omitted key keeps the default diff selector, and the
// neighboring array flags decode unchanged.
func TestFetchLocalScanRejectsNull(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		deny string
		scan []string
	}{
		{name: "null refuses", raw: `{"flags":{"scan":null}}`, deny: "must be an array of strings, not null"},
		{name: "array decodes", raw: `{"flags":{"scan":["internal/auth"]}}`, scan: []string{"internal/auth"}},
		{name: "omitted keeps the default diff", raw: `{"flags":{}}`},
		{
			name: "sibling arrays survive",
			raw:  `{"flags":{"context":["spec.md"],"ignore":["*.min.js"],"scan":["cmd"]}}`,
			scan: []string{"cmd"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertScanDecode(t, c.raw, c.deny, c.scan)
		})
	}
}

// assertScanDecode pins one decode outcome: an error carrying deny, or the
// expected scan operands.
func assertScanDecode(t *testing.T, raw, deny string, scan []string) {
	t.Helper()
	req, err := decodeScanArgs(t, raw)
	if deny != "" {
		if err == nil || !strings.Contains(err.Error(), deny) {
			t.Fatalf("decode = %v; want an error carrying %q", err, deny)
		}
		return
	}
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(req.Flags.Scan, scan) {
		t.Errorf("scan = %v; want %v", req.Flags.Scan, scan)
	}
}

// decodeScanArgs decodes one fetch-local argument object, returning the request
// and the decode error.
func decodeScanArgs(t *testing.T, raw string) (*fetchcore.LocalRequest, error) {
	t.Helper()
	a, err := newArgs(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("newArgs: %v", err)
	}
	req := &fetchcore.LocalRequest{}
	decodeFetchLocal(req, a)
	return req, a.done()
}
