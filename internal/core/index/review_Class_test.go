package indexcore

import "testing"

// TestReviewClass preserves the imported caller.
func TestReviewClass(t *testing.T) {
	assertRows(t, reviewRows("import m\nclass C:\n    m = other\nm.save(v)", langPython), []Row{{Path: "uses", Line: 4, Kind: KindMethod, Resolution: "lexical", Language: string(langPython)}})
}
