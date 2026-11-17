package config

import (
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestParseTriggerPredicates(t *testing.T) {
	cfg, ref := Parse([]byte(`{"schema_version":"1","trigger":{
		"authors":["ana","bo"],
		"paths_include":["src/**","go.mod"],
		"paths_exclude":["**/generated/**"],
		"labels":["security"],
		"title_matches":"^fix: (retry|auth)",
		"base_branches":["main","release/*"],
		"draft":false
	}}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	draft := false
	want := &core.ConfigTrigger{
		Authors:      []string{"ana", "bo"},
		PathsInclude: []string{"src/**", "go.mod"},
		PathsExclude: []string{"**/generated/**"},
		Labels:       []string{"security"},
		TitleMatches: "^fix: (retry|auth)",
		BaseBranches: []string{"main", "release/*"},
		Draft:        &draft,
	}
	if !reflect.DeepEqual(cfg.Trigger, want) {
		t.Errorf("trigger = %#v, want %#v", cfg.Trigger, want)
	}
}

func TestParseTriggerRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		give     string
		fragment string
	}{
		{"unknown key", `{"schema_version":"1","trigger":{"bogus":1}}`, "$.trigger.bogus: unknown field"},
		{"bad regexp", `{"schema_version":"1","trigger":{"title_matches":"("}}`, "$.trigger.title_matches"},
		{"absolute glob", `{"schema_version":"1","trigger":{"paths_include":["/etc/**"]}}`, "absolute"},
		{"absolute base glob", `{"schema_version":"1","trigger":{"base_branches":["/main"]}}`, "absolute"},
		{"wrong draft type", `{"schema_version":"1","trigger":{"draft":"yes"}}`, "$.trigger.draft"},
		{"empty authors", `{"schema_version":"1","trigger":{"authors":[]}}`, "$.trigger.authors"},
		{"null trigger", `{"schema_version":"1","trigger":null}`, "$.trigger"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkParseRefusal(t, tc.give, CodeBadConfig, tc.fragment)
		})
	}
}
