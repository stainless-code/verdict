package indexcore

import "testing"

// TestReviewIdentity preserves the imported caller.
func TestReviewIdentity(t *testing.T) {
	assertRows(t, reviewRows("import m; m = m; m.save(v)", langPython), []Row{{Path: "uses", Line: 1, Kind: KindMethod, Resolution: "lexical", Language: string(langPython)}})
}
