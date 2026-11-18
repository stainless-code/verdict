package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/provenance"
)

const recordedPromptsHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestStampProvenancePromptsHashFromBundleReceipt proves the receipt names the
// Procedure the agent got, as recorded at fetch time, and falls back to the
// posting binary's embedded digest only for a bundle without that field.
func TestStampProvenancePromptsHashFromBundleReceipt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var warnings []string
	st := postTestState(".verdict/state/7", "", core.PostFlags{}, &warnings)

	assertStampedPromptsHash(t, st, provenance.PromptsHash(), "legacy bundle without provenance.json")

	if err := writeTestFile(st.dir+"/provenance.json",
		`{"schema_version":"1","prompts_hash":"`+recordedPromptsHash+`"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	assertStampedPromptsHash(t, st, recordedPromptsHash, "bundle with a recorded prompts_hash")
	if len(recordedPromptsHash) != 64 || strings.Trim(recordedPromptsHash, "0123456789abcdef") != "" {
		t.Fatal("test fixture is not a 64-hex hash")
	}
}

func assertStampedPromptsHash(t *testing.T, st *state, want, label string) {
	t.Helper()
	receipt := &core.Post{}
	if err := stampProvenance(st, receipt); err != nil {
		t.Fatalf("%s: stampProvenance: %v", label, err)
	}
	if got := receipt.PromptsHash; got != want {
		t.Fatalf("%s: prompts_hash = %q, want %q", label, got, want)
	}
}
