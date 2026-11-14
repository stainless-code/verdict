package trendcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func seedTrendProvenanceBundle(t *testing.T, bundleDir, provJSON string) {
	t.Helper()
	writeListedStore(t, bundleDir, "["+trendOpenFinding+"]")
	if provJSON == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "provenance.json"), []byte(provJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrendProvenanceFallbackAndDegrade(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)

	seedTrendProvenanceBundle(t, filepath.Join(state, "101"), `{
  "schema_version": "1",
  "run_id": "run101abc",
  "prompts_hash": "hash101xyz",
  "inputs": [],
  "truncated": [{"input": "kb", "status": "truncated", "reason": "cap", "truncated": true, "truncation_reason": "response_cap", "returned": 50, "total": 100, "next_offset": null}]
}`)
	seedTrendProvenanceBundle(t, filepath.Join(state, "102"), "")
	seedTrendProvenanceBundle(t, filepath.Join(state, "103"), "{not json")

	log := &progressLog{}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: log}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("trend refused: %s: %s", ref.Code, ref.Message)
	}
	wire := jq.PrettyOrdered(resp.Data.Wire())
	assertTrendCarriesReceipt(t, wire)
	assertTrendCountsUnreadableBundle(t, wire, log.warns)
}

func assertTrendCarriesReceipt(t *testing.T, wire string) {
	t.Helper()
	for _, want := range []string{`"run_id": "run101abc"`, `"hash101xyz"`, `"truncated_inputs": 1`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("trend wire missing %s:\n%s", want, wire)
		}
	}
	if strings.Count(wire, `"run_id"`) != 1 {
		t.Fatalf("expected only 1 bundle run_id in wire:\n%s", wire)
	}
}

// assertTrendCountsUnreadableBundle pins the warning copy to what happens: the
// bundle is aggregated, so the receipt is forgotten rather than skipped.
func assertTrendCountsUnreadableBundle(t *testing.T, wire string, warnings []string) {
	t.Helper()
	warns := strings.Join(warnings, "\n")
	if !strings.Contains(warns, "trend counts 103 without its provenance receipt") {
		t.Fatalf("expected a counted-without-receipt warning for bundle 103, got: %v", warnings)
	}
	if strings.Contains(warns, "trend skips") {
		t.Fatalf("bundle 103 is counted, so the warning must not claim a skip: %v", warnings)
	}
	if !strings.Contains(wire, `"bundle": "103"`) {
		t.Fatalf("bundle 103 should still be counted:\n%s", wire)
	}
}
