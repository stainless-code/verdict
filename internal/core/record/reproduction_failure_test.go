package recordcore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

func TestFailedFindingsWritePreservesStoredEvidence(t *testing.T) { checkReproductionFailure(t) }

func storedReproduction(t *testing.T) *state {
	t.Helper()
	dir := t.TempDir()
	st := &state{dir: dir, cmd: recordCommand, findingsFile: filepath.Join(dir, "findings.json"), kb: &kb.Ctx{}, entries: []any{failureFinding("false")}}
	st.captureReproductions()
	if err := st.persistMerged(); err != nil {
		t.Fatal(err)
	}
	return st
}

func failureFinding(command string) *jq.Obj {
	return jq.NewObj("sig", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "rigor", "reproduced",
		"reproduction", jq.NewObj("path", "", "command", command))
}

func checkReproductionFailure(t *testing.T) {
	t.Helper()
	st := storedReproduction(t)
	st.entries = []any{failureFinding("true")}
	st.captureReproductions()
	checkFailedPublication(t, st)
	old := readRetainedFile(t, filepath.Join(st.dir, "tests", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2ed27c1421e6.sh"))
	if old != "false\n" {
		t.Fatalf("previously stored evidence changed: %q", old)
	}
}

func checkFailedPublication(t *testing.T, st *state) {
	t.Helper()
	st.findingsFile = filepath.Join(st.dir, "blocked")
	if err := os.Mkdir(st.findingsFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.persistMerged(); err == nil {
		t.Fatal("injected findings write did not fail")
	}
}
