package indexcore

import "testing"

// TestReviewRequire preserves the imported caller.
func TestReviewRequire(t *testing.T) {
	assertRows(t, reviewRows("const m = require('./saver'); m.save(v);", langTypeScript), []Row{{Path: "uses", Line: 1, Kind: KindMethod, Resolution: "lexical", Language: string(langTypeScript)}})
}
