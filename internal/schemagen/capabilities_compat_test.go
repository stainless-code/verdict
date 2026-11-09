package schemagen

import "testing"

// The capability split added approve, request_changes, and unsupported. Those
// keys are optional in the published schema because only provider and fetch
// were universal: an envelope written before the split must keep validating, or
// every stored fetch artifact becomes unreadable by the current schema. New
// writers always emit every key, so the absent case is legacy-only.
func TestCapabilitiesSchemaAcceptsPreSplitEnvelopes(t *testing.T) {
	doc, def, cases := capabilityCases(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCapabilityCase(t, doc, def, tc)
		})
	}
}

// capabilityCase is one envelope value and whether the schema must accept it.
type capabilityCase struct {
	value map[string]any
	name  string
	ok    bool
}

// capabilityCases loads the published capabilities definition and the
// pre-split/current envelope values the schema must keep accepting.
func capabilityCases(t *testing.T) (map[string]any, map[string]any, []capabilityCase) {
	t.Helper()
	doc := loadSchemaDocument(t, "envelope.schema.json")
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("envelope schema has no $defs")
	}
	def, ok := defs["fetchCapabilities"].(map[string]any)
	if !ok {
		t.Fatal("envelope schema has no fetchCapabilities definition")
	}
	cases := []capabilityCase{
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
	return doc, def, cases
}

// assertCapabilityCase pins one envelope value against the published schema:
// an accepted value must validate clean and a rejected one must not.
func assertCapabilityCase(t *testing.T, doc, def map[string]any, tc capabilityCase) {
	t.Helper()
	err := validationError(doc, def, tc.value)
	if tc.ok && err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if !tc.ok && err == nil {
		t.Fatal("expected a validation error, got nil")
	}
}
