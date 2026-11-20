package panelcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	executionSig    = "1fd35ceaaa1fc0a98e1486617b35249c646ee577"
	executionReview = `{"schema_version":"2","execution":{"id":"execution-A","finders":2},"findings":[{"path":"notes.txt","line":1,"side":"RIGHT","severity":"important","claim":"Unhandled await error path here.","quote":"return unsafeValue","confidence":"high"}]}`
	executionDiff   = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-return safeValue\n+return unsafeValue\n"
)

func writeExecutionFile(t *testing.T, dir, name string, raw []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func executionFixture(t *testing.T, verdict, checker, quote string, missing bool) string {
	t.Helper()
	dir := t.TempDir()
	writeExecutionFile(t, dir, "run.json", []byte(`{"execution":{"id":"execution-A","finders":2}}`))
	writeExecutionFile(t, dir, "PR.json", []byte(`{"headRefOid":"head"}`))
	writeExecutionFile(t, dir, "review.json", []byte(executionReview))
	writeExecutionFile(t, dir, "full.diff", []byte(executionDiff))
	plan := []byte(`{"checks":[{"sig":"1fd35ceaaa1fc0a98e1486617b35249c646ee577","origin":"finder-1","checker":"finder-2"}]}`)
	plan = []byte(strings.ReplaceAll(string(plan), `"checker":"finder-2"`, `"checker":"`+checker+`"`))
	writeExecutionFile(t, dir, filepath.Join("verification", "plan.json"), plan)
	bindings := map[string]any{
		"schema_version": "2", "execution_id": "execution-A", "status": "complete", "head_sha": "head",
		"diff_sha256": testDigest([]byte(executionDiff)), "review_sha256": testDigest([]byte(executionReview)), "plan_sha256": testDigest(plan),
	}
	data, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	writeExecutionFile(t, dir, "launcher.json", data)
	delete(bindings, "status")
	results := []any{}
	if !missing {
		results = append(results, map[string]any{
			"sig": executionSig, "checker": checker, "verdict": verdict, "evidence": "The selected branch changes the return value.",
			"quote": map[string]any{"path": "notes.txt", "side": "RIGHT", "line": 1, "text": quote},
		})
	}
	bindings["results"] = results
	data, err = json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	writeExecutionFile(t, dir, filepath.Join("verification", "verdicts.json"), data)
	return dir
}

func executionEntry() *jq.Obj {
	return jq.NewObj("sig", executionSig, "status", "open", "severity", "important", "verification", jq.NewObj("quote", "verified"))
}

func TestExecutionConfirmsGroundedOtherFinder(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	state := CheckExecution(dir, nil)
	entry := executionEntry()
	state.Stamp([]any{entry})
	cross := readCross(entry)
	if state.Problem() != "" || !EligibleCross(cross) {
		t.Fatalf("grounded confirmation rejected: %+v, %v", state, cross)
	}
}

func readCross(entry *jq.Obj) any {
	value, _ := entry.Get("verification")
	return readcommon.FieldOf(value, "cross")
}

func TestExecutionRejectsForgedConfirmation(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return imaginaryValue", false)
	assertExecutionIneligible(t, dir)
}

func TestExecutionRejectsSelfCheck(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-1", "return unsafeValue", false)
	assertExecutionIneligible(t, dir)
}

func TestExecutionKeepsVerifiedRefutationIneligible(t *testing.T) {
	dir := executionFixture(t, "refuted", "finder-2", "return unsafeValue", false)
	assertExecutionIneligible(t, dir)
}

func TestExecutionMissingRowsAreInsufficient(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", true)
	assertExecutionIneligible(t, dir)
}

func TestExecutionRejectsStaleDigest(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	writeExecutionFile(t, dir, "full.diff", []byte(executionDiff+"\n"))
	assertExecutionIneligible(t, dir)
}

func TestExecutionRejectsStrippedManifest(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	if err := os.Remove(filepath.Join(dir, "launcher.json")); err != nil {
		t.Fatal(err)
	}
	assertExecutionIneligible(t, dir)
}

func TestExecutionCannotStripReviewReference(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	writeExecutionFile(t, dir, "review.json", []byte(`{"schema_version":"1","findings":[]}`))
	assertExecutionIneligible(t, dir)
}

func assertExecutionIneligible(t *testing.T, dir string) {
	t.Helper()
	state := CheckExecution(dir, nil)
	entry := executionEntry()
	state.Stamp([]any{entry})
	receipt := &core.EvidenceReceipt{}
	state.ApplyEvidence(receipt)
	if EligibleCross(readCross(entry)) {
		t.Fatal("unsafe execution admitted to eligibility")
	}
	if !receipt.Insufficient {
		t.Fatalf("unsafe execution reported sufficient: %+v", state)
	}
}

func TestExecutionRetainsPriorRefutation(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	state := CheckExecution(dir, nil)
	entry := executionEntry()
	verification, _ := entry.Get("verification")
	prior, ok := verification.(*jq.Obj)
	if !ok {
		t.Fatal("fixture verification is not an object")
	}
	prior.Set("panel", []any{jq.NewObj("model", "finder-2", "verdict", "refuted", "evidence", "The earlier check found a live dispute.")})
	state.Stamp([]any{entry})
	if EligibleCross(readCross(entry)) {
		t.Fatal("current confirmation erased a prior refutation")
	}
	receipt := &core.EvidenceReceipt{}
	state.ApplyEvidence(receipt)
	if !receipt.Insufficient {
		t.Fatal("prior refutation became sufficient")
	}
}
