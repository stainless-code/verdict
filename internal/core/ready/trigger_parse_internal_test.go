package readycore

import "testing"

// TestParseBaseTriggerNamesTheFileOnce proves a base policy that does not
// parse names .verdict/config.json exactly once: the parse refusal already
// carries the file, so the trigger failure's own words do not repeat it.
func TestParseBaseTriggerNamesTheFileOnce(t *testing.T) {
	_, ref := parseBaseTrigger([]byte(`{}`))
	const want = `could not parse the base policy: bad-config: .verdict/config.json: ` +
		`schema_version: missing format marker, want the string "1"`
	if ref == nil || ref.Message != want {
		t.Fatalf("refusal = %v, want message %q", ref, want)
	}
}
