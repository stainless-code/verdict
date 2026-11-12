package indexcore

import "testing"

// TestReviewConditions preserves the imported caller.
func TestReviewConditions(t *testing.T) {
	assertRows(t, reviewRows("import * as m from './saver'; if (m) { m.save(v); }", langTypeScript), []Row{{Path: "uses", Line: 1, Kind: KindMethod, Resolution: "lexical", Language: string(langTypeScript)}})
}

// TestReviewWhile preserves the imported caller.
func TestReviewWhile(t *testing.T) {
	assertRows(t, reviewRows("import * as m from './saver'; while (m) { m.save(v); }", langTypeScript), []Row{{Path: "uses", Line: 1, Kind: KindMethod, Resolution: "lexical", Language: string(langTypeScript)}})
}
