package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func completedExecution(t *testing.T) (*core.RunRequest, *phaseCommander) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	req.FindersRaw = "2"
	dir := filepath.Join(filepath.Dir(commander.dir), "local-supersede")
	if err := os.Rename(commander.dir, dir); err != nil {
		t.Fatal(err)
	}
	commander.dir = dir
	if err := os.Remove(filepath.Join(dir, "PR.json")); err != nil {
		t.Fatal(err)
	}
	req.Pr = "local-supersede"
	writeUnder(t, dir, "source.json", `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	root := req.RepoRoot
	req.Root = &root
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: commander.dir, Root: root, BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", AllowHead: true})
	if ref != nil {
		t.Fatal(ref)
	}
	capture, _, err := config.ReadRunState(commander.dir)
	if err != nil {
		t.Fatal(err)
	}
	capture.BaseSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	capture.Inventory = config.InventoryHashes(snap)
	if err := config.WriteRunState(commander.dir, capture); err != nil {
		t.Fatal(err)
	}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	return req, commander
}

func TestSingleFinderSupersedesPreviousExecution(t *testing.T) {
	req, previous := completedExecution(t)
	before := recordSuperseded(t, req)
	if before.Verification == nil || before.Verification.Finders == nil {
		t.Fatal("two-finder record has no summary")
	}
	dir := previous.dir
	poisonPriorCross(t, dir)
	commander := &scripted{write: func() { writeUnder(t, dir, "review.json", coordinatorReview) }}
	req.Commander = commander
	req.FindersRaw = "1"
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if commander.calls != 1 {
		t.Fatalf("single finder launched %d processes", commander.calls)
	}
	assertSupersededArtifacts(t, dir)
	assertSupersededReport(t, req)
	assertSingleRecord(t, req, dir)
}

func recordSuperseded(t *testing.T, req *core.RunRequest) *core.RecordData {
	t.Helper()
	response, ref := recordcore.Run(t.Context(), &recordcore.Request{ReadRequest: req.ReadRequest, Effective: core.Defaults()})
	if ref != nil {
		t.Fatal(ref)
	}
	return response.Data
}

func assertSingleRecord(t *testing.T, req *core.RunRequest, dir string) {
	t.Helper()
	after := recordSuperseded(t, req)
	if after.Approval == nil || after.Approval.Insufficient || len(after.Approval.GatingSigs) != 1 {
		t.Fatalf("stale cross standing affected single finder: %+v", after.Approval)
	}
	entries := supersededEntries(t, dir)
	if cross := readcommon.FieldOf(readcommon.FieldOf(entries[0], "verification"), "cross"); cross != nil {
		t.Fatalf("retired cross stamp remains: %v", cross)
	}
}

func supersededEntries(t *testing.T, dir string) []any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	entries := jq.Elements(value)
	if len(entries) != 1 {
		t.Fatalf("findings = %s", raw)
	}
	return entries
}

func poisonPriorCross(t *testing.T, dir string) {
	t.Helper()
	entries := supersededEntries(t, dir)
	cross, ok := readcommon.FieldOf(readcommon.FieldOf(entries[0], "verification"), "cross").(*jq.Obj)
	if !ok {
		t.Fatal("prior cross stamp missing")
	}
	cross.Set("status", "pending")
	cross.Set("checks", []any{})
	writeUnder(t, dir, "findings.json", jq.PrettyOrdered(entries))
}

func assertSupersededArtifacts(t *testing.T, dir string) {
	t.Helper()
	capture := readPhaseObject(t, filepath.Join(dir, "run.json"))
	location := jq.StrOf(readcommon.FieldOf(capture, "superseded"))
	if !strings.HasPrefix(location, "x-superseded"+string(filepath.Separator)) {
		t.Fatalf("superseded location = %q", location)
	}
	for _, path := range []string{"launcher.json", "review.json", filepath.Join("verification", "verdicts.json"), filepath.Join("verification", "plan.json"), filepath.Join("x-finder-1", "review.json"), filepath.Join("x-finder-2", "review.json"), filepath.Join("x-cross-1", "verdicts.json"), filepath.Join("x-cross-2", "verdicts.json")} {
		if _, err := os.Stat(filepath.Join(dir, location, path)); err != nil {
			t.Fatalf("retired %s missing: %v", path, err)
		}
	}
	for _, path := range []string{"verification", "x-finder-1", "x-finder-2", "x-cross-1", "x-cross-2"} {
		if _, err := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(err) {
			t.Fatalf("live stale artifact %s: %v", path, err)
		}
	}
}

func TestTwoFinderRerunSupersedesPreviousExecution(t *testing.T) {
	req, commander := completedExecution(t)
	commander.phases = nil
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	capture := readPhaseObject(t, filepath.Join(commander.dir, "run.json"))
	location := jq.StrOf(readcommon.FieldOf(capture, "superseded"))
	if !strings.HasPrefix(location, "x-superseded"+string(filepath.Separator)) {
		t.Fatalf("superseded location = %q", location)
	}
	if _, err := os.Stat(filepath.Join(commander.dir, location, "launcher.json")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("rerun launches = %s", got)
	}
}

func TestRetirementFailureRefusesBeforeLaunch(t *testing.T) {
	req, previous := completedExecution(t)
	writeUnder(t, previous.dir, "x-superseded", "blocked")
	commander := &scripted{}
	req.Commander = commander
	req.FindersRaw = "1"
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "agent-record-failed" || !strings.Contains(ref.Message, "restore") || commander.calls != 0 {
		t.Fatalf("failed retirement launched: calls=%d ref=%v", commander.calls, ref)
	}
	if _, err := os.Stat(filepath.Join(previous.dir, "verification", "verdicts.json")); err != nil {
		t.Fatalf("failure lost prior receipt: %v", err)
	}
}

func assertSupersededReport(t *testing.T, req *core.RunRequest) {
	t.Helper()
	response, ref := reportcore.Run(t.Context(), &reportcore.Request{ReadRequest: req.ReadRequest, Effective: core.Defaults()})
	if ref != nil {
		t.Fatal(ref)
	}
	if response.Data.Approval == nil || response.Data.Approval.Insufficient || len(response.Data.Approval.GatingSigs) != 1 {
		t.Fatalf("report read retired cross standing: %+v", response.Data.Approval)
	}
}

func automaticArtifacts() []string {
	return []string{"launcher.json", "review.json", "verification", "x-finder-1", "x-finder-2", "x-cross-1", "x-cross-2"}
}

// retiredFixture turns the completed fixture into the capture a retirement
// leaves behind: no execution reference, the retirement location recorded, and
// none of the artifacts it moved still live.
func retiredFixture(t *testing.T, dir string, retired bool) {
	t.Helper()
	capture, _, err := config.ReadRunState(dir)
	if err != nil {
		t.Fatal(err)
	}
	capture.Execution = nil
	if retired {
		capture.Superseded = "x-superseded/legacy"
	}
	if err := config.WriteRunState(dir, capture); err != nil {
		t.Fatal(err)
	}
	for _, name := range automaticArtifacts() {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCoordinatorRefusesResumeBeforeRetirement proves a retry that cannot
// launch leaves the completed execution alone: the refusal arrives before the
// retirement moves anything.
func TestCoordinatorRefusesResumeBeforeRetirement(t *testing.T) {
	req, commander := completedExecution(t)
	commander.phases = nil
	req.PassThrough = []string{"--resume"}
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("refusal = %v", ref)
	}
	capture := readPhaseObject(t, filepath.Join(commander.dir, "run.json"))
	if readcommon.FieldOf(readcommon.FieldOf(capture, "execution"), "id") == "" {
		t.Fatalf("refused retry dropped the captured execution: %s", jq.Compact(capture))
	}
	for _, name := range automaticArtifacts() {
		if _, err := os.Stat(filepath.Join(commander.dir, name)); err != nil {
			t.Fatalf("refused retry removed %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(commander.dir, "x-superseded")); !os.IsNotExist(err) {
		t.Fatalf("refused retry published a retirement: %v", err)
	}
}

// TestCoordinatorRecoversARetiredCapture proves a capture that a retirement
// left with no execution reference and no live artifacts starts a replacement
// instead of wedging the bundle.
func TestCoordinatorRecoversARetiredCapture(t *testing.T) {
	req, commander := completedExecution(t)
	retiredFixture(t, commander.dir, true)
	commander.phases = nil
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("retired capture refused: %v", ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("replacement launches = %s", got)
	}
}

// TestCoordinatorRefusesADamagedCapture proves the recovery reads the
// retirement marker: a capture that lost its execution reference without one
// is damaged, not retired, and still refuses.
func TestCoordinatorRefusesADamagedCapture(t *testing.T) {
	req, commander := completedExecution(t)
	retiredFixture(t, commander.dir, false)
	commander.phases = nil
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "agent-record-failed" || !strings.Contains(ref.Message, "restore run.json") {
		t.Fatalf("damaged capture = %v", ref)
	}
	if len(commander.phases) != 0 {
		t.Fatalf("damaged capture launched: %v", commander.phases)
	}
}
