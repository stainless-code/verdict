package config

import (
	"reflect"
	"testing"
)

// TestBuiltinDefaultsMatchFloor keeps the published row list and the built-in
// layer in agreement: the rows are what a docs table renders, the layer is what
// the resolver merges, and a default that moves in only one of them publishes a
// table the binary does not follow. The keys are the published spelling, so a
// renamed key fails here before it reaches a reader.
func TestBuiltinDefaultsMatchFloor(t *testing.T) {
	got := map[string]any{}
	for _, row := range BuiltinDefaults() {
		if _, dup := got[row.Key]; dup {
			t.Fatalf("BuiltinDefaults lists %q twice", row.Key)
		}
		got[row.Key] = row.Value
	}
	want := map[string]any{
		"panel":                 []string(nil),
		"budget_usd":            nil,
		"max_bytes":             int64(512000),
		"chunk":                 false,
		"effort":                "standard",
		"kb_policy.min_judged":  5,
		"kb_policy.max_fp_rate": 0.5,
		"kb_policy.max_chars":   nil,
		"display_min_severity":  nil,
		"post_min_severity":     nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuiltinDefaults = %#v; want %#v", got, want)
	}
}

// TestBuiltinDefaultsNameVerbs keeps every row attributable: a key added to the
// floor without a verb renders an empty cell instead of telling a reader what a
// bare run of that verb does.
func TestBuiltinDefaultsNameVerbs(t *testing.T) {
	for _, row := range BuiltinDefaults() {
		if len(row.Verbs) == 0 {
			t.Errorf("%s names no verb", row.Key)
		}
	}
}
