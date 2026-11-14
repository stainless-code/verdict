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

func TestTrendProvenanceFallbackAndDegrade(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)

	withProv := filepath.Join(state, "101")
	writeListedStore(t, withProv, "["+trendOpenFinding+"]")
	provBody := `{
  "schema_version": "1",
  "run_id": "run101abc",
  "prompts_hash": "hash101xyz",
  "inputs": [],
  "truncated": [{"input": "kb", "status": "truncated", "reason": "cap", "truncated": true, "truncation_reason": "response_cap", "returned": 50, "total": 100, "next_offset": null}]
}`
	if err := os.WriteFile(filepath.Join(withProv, "provenance.json"), []byte(provBody), 0o600); err != nil {
		t.Fatal(err)
	}

	legacy := filepath.Join(state, "102")
	writeListedStore(t, legacy, "["+trendOpenFinding+"]")

	malformed := filepath.Join(state, "103")
	writeListedStore(t, malformed, "["+trendOpenFinding+"]")
	if err := os.WriteFile(filepath.Join(malformed, "provenance.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	log := &progressLog{}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: log}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("trend refused: %s: %s", ref.Code, ref.Message)
	}
	wire := jq.PrettyOrdered(resp.Data.Wire())
	if !strings.Contains(wire, `"run_id": "run101abc"`) || !strings.Contains(wire, `"hash101xyz"`) || !strings.Contains(wire, `"truncated_inputs": 1`) {
		t.Fatalf("trend wire missed provenance summary:\n%s", wire)
	}
	// Legacy bundle 102 must omit run_id.
	if strings.Contains(wire, `"102"`) && strings.Count(wire, `"run_id"`) != 1 {
		t.Fatalf("expected only 1 bundle run_id in wire:\n%s", wire)
	}
	// Malformed bundle 103 warns without failing trend.
	joined := strings.Join(log.warns, "\n")
	if !strings.Contains(joined, "provenance.json") {
		t.Fatalf("expected provenance.json warning for bundle 103, got: %v", log.warns)
	}
}
