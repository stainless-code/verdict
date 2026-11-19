package config

import (
	"reflect"
	"testing"
)

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
		"spec_coverage_min":     80,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuiltinDefaults = %#v; want %#v", got, want)
	}
}

func TestBuiltinDefaultsNameVerbs(t *testing.T) {
	for _, row := range BuiltinDefaults() {
		if len(row.Verbs) == 0 {
			t.Errorf("%s names no verb", row.Key)
		}
	}
}
