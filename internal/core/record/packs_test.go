package recordcore_test

import (
	"context"
	"path/filepath"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

// TestRecordRefusesTamperedPackSnapshot proves a repo pack joins the control
// inventory: editing the snapshotted pack after fetch refuses controls-stale
// at record, exactly like any other control file.
func TestRecordRefusesTamperedPackSnapshot(t *testing.T) {
	bundle(t, "local-pack", reviewJSON(""))
	testkit.WriteFile(t, ".verdict/packs/acme/pack.json",
		`{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`)
	testkit.WriteFile(t, ".verdict/packs/acme/lenses/perf.md", "base body\n")
	req := recordRequest(t, &testkit.Collector{}, "local-pack")
	testkit.WriteFile(t, filepath.Join(".verdict", "state", "local-pack", "controls", "packs", "acme", "lenses", "perf.md"),
		"edited\n")
	if _, ref := recordcore.Run(context.Background(), req); ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("tampered pack record = %v, want controls-stale", ref)
	}
}
