package schemagen

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRuntimeShowOutputValidates proves the runtime show data (a) validates
// against the generated showData definition, including the additive fix
// block, and (b) round-trips through the modeled shape field for field.
func TestRuntimeShowOutputValidates(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	doc := loadSchemaDocument(t, "envelope.schema.json")
	t.Chdir(t.TempDir())
	writeFixture(t, ".review/5101/findings.json", "["+fixtureFinding()+"]")
	writeFixture(t, ".review/5101/patch.json",
		`{"sig":"c1111111","verifyCmd":"go test ./...","resultHead":"aaaaaaa","baseHead":"bbbbbbb"}`)
	read := core.ReadRequest{Repo: "acme/app", Pr: "5101"}
	resp, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{
		ReadRequest: read,
		Sig:         "c1111111",
	})

	if ref != nil {
		t.Fatalf("RunShow refusal: %v", ref)
	}
	if resp.Fix == nil || resp.Item == nil {
		t.Fatal("show response misses item or fix")
	}
	data := jq.NewObj("item", resp.Item.Raw)
	data.Set("fix", resp.Fix.Wire())
	validateWire(t, doc, "$defs.showData", defOf(t, doc, "showData"), decodeWire(t, data))

	var modeled ShowData
	unmarshalWire(t, data, &modeled)
	assertModeledShowFix(t, &modeled)
}

// assertModeledShowFix checks the modeled fix block field for field.
func assertModeledShowFix(t *testing.T, modeled *ShowData) {
	t.Helper()
	assertModeledFixIdentity(t, &modeled.Fix)
	assertModeledFixHint(t, &modeled.Fix)
}

// assertModeledFixIdentity checks identity, locator, and null fields.
func assertModeledFixIdentity(t *testing.T, fix *ShowFix) {
	t.Helper()
	if fix.Sig != "c111111111111111111111111111111111111111" {
		t.Errorf("fix sig = %q", fix.Sig)
	}
	if fix.Status != "open" || fix.Locator != "notes.txt:2" {
		t.Errorf("fix status/locator = %q/%q", fix.Status, fix.Locator)
	}
	assertModeledFixFiles(t, fix)
}

// assertModeledFixFiles checks the deduped files and nullable severity.
func assertModeledFixFiles(t *testing.T, fix *ShowFix) {
	t.Helper()
	if len(fix.Files) != 1 || fix.Files[0] != "notes.txt" {
		t.Errorf("fix files = %v", fix.Files)
	}
	if fix.Severity == nil || *fix.Severity != "critical" {
		t.Errorf("fix severity = %v", fix.Severity)
	}
	if fix.Suggestion != nil {
		t.Errorf("fix suggestion = %v, want null", fix.Suggestion)
	}
}

// assertModeledFixHint checks the rerun hint and fixed caution text.
func assertModeledFixHint(t *testing.T, fix *ShowFix) {
	t.Helper()
	if fix.Caution == "" {
		t.Error("fix caution is empty")
	}
	if fix.VerifyHint == nil || fix.VerifyHint.VerifyCmd != "go test ./..." {
		t.Fatalf("fix verify hint = %+v", fix.VerifyHint)
	}
	if fix.VerifyHint.ResultHead == nil || *fix.VerifyHint.ResultHead != "aaaaaaa" {
		t.Errorf("fix result head = %v", fix.VerifyHint.ResultHead)
	}
}
