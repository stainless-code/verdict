package schemagen

import "testing"

// The capability split added approve, request_changes, and unsupported. Those
// keys are optional in the published schema because only provider and fetch
// were universal: an envelope written before the split must keep validating, or
// every stored fetch artifact becomes unreadable by the current schema. New
// writers always emit every key, so the absent case is legacy-only.
func TestCapabilitiesSchemaAcceptsPreSplitEnvelopes(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("envelope schema has no $defs")
	}
	def, ok := defs["fetchCapabilities"].(map[string]any)
	if !ok {
		t.Fatal("envelope schema has no fetchCapabilities definition")
	}
	cases := []struct {
		name  string
		value map[string]any
		ok    bool
	}{
		{
			name: "pre-split github",
			value: map[string]any{
				"provider": "github", "fetch": true, "post": true,
				"inline_anchors": true, "thread_resolution": true, "review_verdicts": true,
			},
			ok: true,
		},
		{
			name:  "pre-split fetch-only host",
			value: map[string]any{"provider": "gitlab", "fetch": true},
			ok:    true,
		},
		{
			name: "current shape",
			value: map[string]any{
				"provider": "gitlab", "fetch": true, "post": true, "inline_anchors": true,
				"approve": true, "request_changes": false, "thread_resolution": false,
				"review_verdicts": false,
				"unsupported":     map[string]any{"request_changes": "no-request-changes-event"},
			},
			ok: true,
		},
		{
			name:  "missing provider cannot validate",
			value: map[string]any{"fetch": true},
			ok:    false,
		},
		{
			name:  "a boolean must stay a boolean",
			value: map[string]any{"provider": "github", "fetch": true, "post": "yes"},
			ok:    false,
		},
		{
			name: "reasons must be strings",
			value: map[string]any{
				"provider": "github", "fetch": true,
				"unsupported": map[string]any{"post": 1},
			},
			ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validationError(doc, def, tc.value)
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected a validation error, got nil")
			}
		})
	}
}
