package indexcore

import "testing"

// TestReviewAlias preserves the imported caller.
func TestReviewAlias(t *testing.T) {
	assertRows(t, reviewRows("import saver as s; s.save(v)", langPython), []Row{{Path: "uses", Line: 1, Kind: KindMethod, Resolution: "lexical", Language: string(langPython)}})
}
