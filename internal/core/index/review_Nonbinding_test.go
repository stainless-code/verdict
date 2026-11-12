package indexcore

import "testing"

// TestReviewNonbinding preserves the imported caller.
func TestReviewNonbinding(t *testing.T) {
	assertRows(t, reviewRows("import * as m from './saver'; m.save(v);", langTypeScript), []Row{{Path: "uses", Line: 1, Kind: KindMethod, Resolution: "lexical", Language: string(langTypeScript)}})
	x := &tsExtractor{tokens: []tsToken{{text: punctBrace, kind: tsPunct}, {text: "m", kind: tsIdent}, {text: punctDot, kind: tsPunct}}}
	if x.bindingActive(1, 2) {
		t.Fatal("qualifier use became a binding")
	}
}
