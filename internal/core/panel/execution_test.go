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
	if !state.Active || readcommon.FieldOf(cross, "status") != "confirmed" || state.Problem() != "" || !EligibleCross(cross) {
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
	if status := readcommon.FieldOf(readCross(entry), "status"); status != "contested" {
		t.Fatalf("prior refutation left status %v, want contested", status)
	}
	if EligibleCross(readCross(entry)) {
		t.Fatal("current confirmation erased a prior refutation")
	}
	receipt := &core.EvidenceReceipt{}
	state.ApplyEvidence(receipt)
	if !receipt.Insufficient {
		t.Fatal("prior refutation became sufficient")
	}
}

func TestCheckerQuoteRejectsUnknownField(t *testing.T) {
	_, err := parseQuote(map[string]any{"path": "notes.txt", "side": "RIGHT", "line": 1, "text": "return unsafeValue", "lien": 1})
	if err == nil {
		t.Fatal("checker quote accepted unknown field lien")
	}
}

func TestExecutionRejectsReceiptWithoutReferences(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	if err := os.Remove(filepath.Join(dir, "launcher.json")); err != nil {
		t.Fatal(err)
	}
	writeExecutionFile(t, dir, "run.json", []byte(`{"schema_version":"1"}`))
	writeExecutionFile(t, dir, "review.json", []byte(`{"schema_version":"1","findings":[]}`))
	assertExecutionIneligible(t, dir)
}

func TestExecutionIgnoresAddressedHistory(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	state := CheckExecution(dir, nil)
	state.Stamp([]any{executionEntry(), jq.NewObj("sig", "old", "status", "addressed", "severity", "important")})
	if gap := state.Problem(); gap != "" {
		t.Fatalf("addressed history blocks execution: %s", gap)
	}
}

func TestFailedResultsDoNotPublishSpecViolations(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	review := strings.TrimSuffix(executionReview, "}") + `,"spec_checks":[{"item":"item-1","status":"violated","finding_sig":"1fd35ceaaa1fc0a98e1486617b35249c646ee577"}]}`
	writeExecutionFile(t, dir, "review.json", []byte(review))
	manifest, _ := readObject(dir, "launcher.json")
	manifest.Set("review_sha256", testDigest([]byte(review)))
	writeExecutionFile(t, dir, "launcher.json", []byte(manifest.MarshalCompact()))
	path := filepath.Join("verification", "verdicts.json")
	receipt, _ := readObject(dir, path)
	receipt.Set("review_sha256", testDigest([]byte(review)))
	rows, _ := receipt.Get("results")
	receipt.Set("results", append(jq.Elements(rows), jq.NewObj("sig", executionSig, "checker", "finder-1",
		"verdict", "insufficient", "evidence", "Unassigned extra result.")))
	writeExecutionFile(t, dir, path, []byte(receipt.MarshalCompact()))
	state := CheckExecution(dir, nil)
	if state.Problem() != "execution: execution received an unexpected checker or duplicate" || len(state.SpecViolations) != 0 {
		t.Fatalf("problem = %q, spec violations = %v", state.Problem(), state.SpecViolations)
	}
}

func TestExecutionReadsDiffAboveReceiptLimit(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	diff := []byte(executionDiff + strings.Repeat(" ", 33554432) + "\n")
	writeExecutionFile(t, dir, "full.diff", diff)
	for _, path := range []string{"launcher.json", filepath.Join("verification", "verdicts.json")} {
		doc, _ := readObject(dir, path)
		doc.Set("diff_sha256", testDigest(diff))
		writeExecutionFile(t, dir, path, []byte(doc.MarshalCompact()))
	}
	if state := CheckExecution(dir, nil); !state.Active || state.Problem() != "" {
		t.Fatalf("large captured diff: %q", state.Problem())
	}
}

func TestExecutionCannotStampOnRefreshedHead(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	state := CheckExecution(dir, nil)
	writeExecutionFile(t, dir, "PR.json", []byte(`{"headRefOid":"next-head"}`))
	entry := executionEntry()
	state.Stamp([]any{entry})
	if EligibleCross(readCross(entry)) || state.Problem() != "execution: bundle head changed before applying cross checks" {
		t.Fatalf("refresh left cross = %v, problem = %q", readCross(entry), state.Problem())
	}
}

func TestExecutionRestoresStrippedCrossFromBoundArtifacts(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	entry := executionEntry()
	state := CheckExecution(dir, nil)
	state.Stamp([]any{entry})
	if readCross(entry) == nil || !EligibleCross(readCross(entry)) || state.Problem() != "" {
		t.Fatalf("bound confirmation was not restored: %v; %s", readCross(entry), state.Problem())
	}
}

func TestExecutionStrippedCrossWithoutReceiptIsInsufficient(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	if err := os.Remove(filepath.Join(dir, "verification", "verdicts.json")); err != nil {
		t.Fatal(err)
	}
	assertExecutionIneligible(t, dir)
}

func TestExecutionStrippedCrossWithoutExpectedRowIsInsufficient(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", true)
	assertExecutionIneligible(t, dir)
}

func TestExecutionIgnoresForgedStoredCross(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", true)
	entry := executionEntry()
	verification, _ := entry.Get("verification")
	stamp, ok := verification.(*jq.Obj)
	if !ok {
		t.Fatal("fixture verification is not an object")
	}
	stamp.Set("cross", jq.NewObj("execution_id", "execution-A", "status", "confirmed", "expected", []any{"finder-2"}, "checks", []any{jq.NewObj("checker", "finder-2", "verdict", "confirmed", "quote_status", "verified")}))
	state := CheckExecution(dir, nil)
	state.Stamp([]any{entry})
	receipt := &core.EvidenceReceipt{}
	state.ApplyEvidence(receipt)
	if EligibleCross(readCross(entry)) || !receipt.Insufficient {
		t.Fatalf("forged stamp survived: %v; %+v", readCross(entry), receipt)
	}
}

func TestExecutionPartialRetainsChecksWithoutEligibility(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	manifest, _ := readObject(dir, "launcher.json")
	manifest.Set("status", "partial")
	writeExecutionFile(t, dir, "launcher.json", []byte(manifest.MarshalCompact()))
	state := CheckExecution(dir, nil)
	entry := executionEntry()
	state.Stamp([]any{entry})
	cross := readCross(entry)
	if EligibleCross(cross) || readcommon.FieldOf(cross, "status") != "pending" {
		t.Fatalf("partial execution was eligible: %v", cross)
	}
	checks := jq.Elements(readcommon.FieldOf(cross, "checks"))
	if len(checks) != 1 || readcommon.FieldOf(checks[0], "checker") != "finder-2" || readcommon.FieldOf(checks[0], "verdict") != "confirmed" {
		t.Fatalf("partial check details lost: %v", checks)
	}
	if state.Problem() != "execution: execution manifest is incomplete" {
		t.Fatalf("partial problem = %q", state.Problem())
	}
}

func TestExecutionCannotStampAfterRetirement(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	state := CheckExecution(dir, nil)
	writeExecutionFile(t, dir, "run.json", []byte(`{"schema_version":"1","finders":1,"superseded":"retired-execution"}`))
	entry := executionEntry()
	state.Stamp([]any{entry})
	if EligibleCross(readCross(entry)) || state.Problem() != "execution: captured execution changed before applying cross checks" {
		t.Fatalf("retired execution stamped: cross=%v problem=%s", readCross(entry), state.Problem())
	}
}

func TestExecutionCapturedTopologyCannotBecomeManual(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	writeExecutionFile(t, dir, "run.json", []byte(`{"schema_version":"1","finders":2}`))
	writeExecutionFile(t, dir, "review.json", []byte(`{"schema_version":"1","findings":[],"summary":"No findings."}`))
	for _, path := range []string{"launcher.json", filepath.Join("verification", "verdicts.json")} {
		if err := os.Remove(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}
	state := CheckExecution(dir, nil)
	entry := executionEntry()
	state.Stamp([]any{entry})
	receipt := &core.EvidenceReceipt{}
	state.ApplyEvidence(receipt)
	if !state.Active || !receipt.Insufficient || EligibleCross(readCross(entry)) {
		t.Fatalf("captured topology became manual: state=%+v receipt=%+v cross=%v", state, receipt, readCross(entry))
	}
}

func TestStampRefusesAReceiptChangedAfterCheck(t *testing.T) {
	cases := []struct {
		tamper func(t *testing.T, dir string)
		name   string
	}{
		{name: "removed", tamper: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Remove(filepath.Join(dir, "verification", "verdicts.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "replaced", tamper: func(t *testing.T, dir string) {
			t.Helper()
			path := filepath.Join(dir, "verification", "verdicts.json")
			if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertReceiptChangeRefused(t, tc.tamper)
		})
	}
}

func assertReceiptChangeRefused(t *testing.T, tamper func(t *testing.T, dir string)) {
	t.Helper()
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	state := CheckExecution(dir, nil)
	if state.Problem() != "" {
		t.Fatalf("fixture problem = %q", state.Problem())
	}
	tamper(t, dir)
	entry := executionEntry()
	state.Stamp([]any{entry})
	if cross := readCross(entry); readcommon.FieldOf(cross, "status") == "confirmed" {
		t.Fatalf("stamped confirmed cross after the receipt changed: %v", cross)
	}
	if state.Problem() != "execution: execution panel receipt changed before applying cross checks" {
		t.Fatalf("problem = %q", state.Problem())
	}
}

// writeStoredFinders records the finder summary a stored receipt states, the
// capture that outlives the execution it summarized.
func writeStoredFinders(t *testing.T, dir string, completed int) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"verification": map[string]any{"finders": map[string]any{"completed": completed}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeExecutionFile(t, dir, "record.json", raw)
}

// retireBoundArtifacts leaves the shape a finished execution can reach: the
// run capture and the bound artifacts are gone, and the stored receipt is the
// only record that two finders ran.
func retireBoundArtifacts(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"launcher.json", filepath.Join("verification", "verdicts.json"), filepath.Join("verification", "plan.json")} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutionFile(t, dir, "run.json", []byte(`{"schema_version":"1"}`))
	writeExecutionFile(t, dir, "review.json", []byte(`{"schema_version":"1","findings":[]}`))
}

func TestExecutionReceiptKeepsCaptureWithoutArtifacts(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	retireBoundArtifacts(t, dir)
	writeStoredFinders(t, dir, 2)
	state := CheckExecution(dir, nil)
	if state.Active || !state.CrossRequired {
		t.Fatalf("state = %+v, want a captured execution with no live artifacts", state)
	}
	entry := executionEntry()
	state.Stamp([]any{entry})
	cross := readCross(entry)
	if readcommon.FieldOf(cross, "status") != "pending" || EligibleCross(cross) {
		t.Fatalf("stripped cross with no bound artifact = %v, want pending and ineligible", cross)
	}
	receipt := &core.EvidenceReceipt{}
	state.ApplyEvidence(receipt)
	if !receipt.Insufficient {
		t.Fatalf("receipt = %+v, want insufficient", receipt)
	}
	if problem := state.Problem(); problem != "execution: captured execution artifacts missing" {
		t.Fatalf("problem = %q", problem)
	}
}

func TestExecutionUsesLargestStoredFinderCapture(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	retireBoundArtifacts(t, dir)
	writeStoredFinders(t, dir, 1)
	writeExecutionFile(t, dir, "post.json", []byte(`{"verification":{"finders":{"completed":2}}}`))

	state := CheckExecution(dir, nil)
	if !state.CrossRequired {
		t.Fatalf("CrossRequired = false; want the post receipt's two-finder capture (state=%+v)", state)
	}
	entry := executionEntry()
	state.Stamp([]any{entry})
	if cross := readCross(entry); readcommon.FieldOf(cross, "status") != "pending" || EligibleCross(cross) {
		t.Fatalf("cross = %v; want a two-finder pending, ineligible capture", cross)
	}
}

func TestExecutionHonorsCapturedOneFinderDefault(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	retireBoundArtifacts(t, dir)
	writeExecutionFile(t, dir, "run.json", []byte(`{"schema_version":"1","finders":0,"resolved":{"defaults":{"values":{"finders":1}}}}`))
	writeStoredFinders(t, dir, 2)
	writeExecutionFile(t, dir, "post.json", []byte(`{"verification":{"finders":{"completed":2}}}`))

	state := CheckExecution(dir, nil)
	if state.CrossRequired {
		t.Fatalf("CrossRequired = true; want the captured one-finder policy to remain manual (state=%+v)", state)
	}
	entry := executionEntry()
	verification, _ := entry.Get("verification")
	stamp, ok := verification.(*jq.Obj)
	if !ok {
		t.Fatal("fixture verification is not an object")
	}
	stamp.Set("cross", jq.NewObj("execution_id", "execution-A", "status", "confirmed",
		"expected", []any{"finder-2"}, "checks", []any{jq.NewObj("checker", "finder-2", "verdict", "confirmed", "quote_status", "verified")}))
	state.Stamp([]any{entry})
	if cross := readCross(entry); cross != nil {
		t.Fatalf("manual capture kept stale cross = %v", cross)
	}
}

func TestExecutionManualCaptureClearsStoredCross(t *testing.T) {
	dir := executionFixture(t, "confirmed", "finder-2", "return unsafeValue", false)
	retireBoundArtifacts(t, dir)
	writeExecutionFile(t, dir, "run.json", []byte(`{"schema_version":"1","finders":1}`))
	entry := executionEntry()
	verification, _ := entry.Get("verification")
	stamp, ok := verification.(*jq.Obj)
	if !ok {
		t.Fatal("fixture verification is not an object")
	}
	stamp.Set("cross", jq.NewObj("execution_id", "execution-A", "status", "confirmed",
		"expected", []any{"finder-2"}, "checks", []any{jq.NewObj("checker", "finder-2", "verdict", "confirmed", "quote_status", "verified")}))
	state := CheckExecution(dir, nil)
	if state.Active || state.CrossRequired {
		t.Fatalf("state = %+v, want a manual capture", state)
	}
	state.Stamp([]any{entry})
	if cross := readCross(entry); cross != nil {
		t.Fatalf("manual capture kept cross = %v", cross)
	}
}
