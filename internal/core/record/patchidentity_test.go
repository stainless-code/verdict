package recordcore_test

import (
	"context"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

const recordTestPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

// TestRunRecordStampsPatchIdentity proves record.json carries the bundle
// patch identity when one exists, and omits both keys on legacy bundles.
func TestRunRecordStampsPatchIdentity(t *testing.T) {
	bundle(t, "local-id", reviewJSON(""))
	writeFile(t, ".review/local-id/identity.json",
		`{"schema_version":"1","patch_id":"`+recordTestPatch+`","patch_id_kind":"git-patch-id",`+
			`"diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`)
	c := &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-id")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	rec := readRecord(t, "local-id")
	if rec["patch_id"] != recordTestPatch {
		t.Errorf("record.json patch_id = %v, want the bundle patch", rec["patch_id"])
	}
	if rec["patch_id_kind"] != "git-patch-id" {
		t.Errorf("record.json patch_id_kind = %v", rec["patch_id_kind"])
	}

	bundle(t, "local-legacy", reviewJSON(""))
	c = &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-legacy")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	legacy := readRecord(t, "local-legacy")
	if _, ok := legacy["patch_id"]; ok {
		t.Errorf("legacy record.json carries patch_id: %v", legacy["patch_id"])
	}
	if _, ok := legacy["patch_id_kind"]; ok {
		t.Errorf("legacy record.json carries patch_id_kind: %v", legacy["patch_id_kind"])
	}
}
