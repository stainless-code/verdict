package pack

import (
	"testing"
	"testing/fstest"
)

func TestStructuredRuleObject(t *testing.T) {
	p, ref := Load(fstest.MapFS{
		"pack.json":          {Data: []byte(`{"schema_version":"1","name":"test/rules","version":"1.0.0","lenses":[{"id":"check"}],"severity":{"must":"important"},"rules":[{"id":"safe-rule","severity":"must","globs":["*.go"],"category":"safety","description":"Check errors","examples":["return err"]}]}`)},
		"lenses/check.md":    {Data: []byte("Check code")},
		"rules/safe-rule.md": {Data: []byte("Check errors")},
	}, ".")
	if ref != nil {
		t.Fatal(ref)
	}
	r := p.Rules[0]
	if r.Category != "safety" || r.Description != "Check errors" || len(r.Examples) != 1 || r.Examples[0] != "return err" || r.Level != "important" {
		t.Fatalf("structured rule lost: %+v", r)
	}
}
