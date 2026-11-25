package run

import (
	"encoding/json"
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
	t.Parallel()
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
	t.Parallel()
	req, commander := completedExecution(t)
	commander.reset()
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
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
}

func TestRetirementFailureRefusesBeforeLaunch(t *testing.T) {
	t.Parallel()
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

func TestCoordinatorRefusesResumeBeforeRetirement(t *testing.T) {
	t.Parallel()
	req, commander := completedExecution(t)
	commander.reset()
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

func TestCoordinatorRecoversARetiredCapture(t *testing.T) {
	t.Parallel()
	req, commander := completedExecution(t)
	retiredFixture(t, commander.dir, true)
	commander.reset()
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("retired capture refused: %v", ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
}

// TestRetiredCaptureFailsClosedOnAnUninspectablePath proves the recovery treats
// only a confirmed absence as a retired artifact: where the lookup itself fails
// - here every artifact path resolves inside a plain file - the capture stays
// damaged instead of reading as retired and starting over on top of evidence.
func TestRetiredCaptureFailsClosedOnAnUninspectablePath(t *testing.T) {
	t.Parallel()
	bundle := filepath.Join(t.TempDir(), "local-damaged")
	if err := os.WriteFile(bundle, []byte("not a bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if retiredCapture(&config.RunState{Superseded: "x-superseded/legacy"}, bundle) {
		t.Fatal("capture with uninspectable artifact paths read as retired")
	}
}

func TestCoordinatorRefusesADamagedCapture(t *testing.T) {
	t.Parallel()
	req, commander := completedExecution(t)
	retiredFixture(t, commander.dir, false)
	commander.reset()
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "agent-record-failed" || !strings.Contains(ref.Message, "restore run.json") {
		t.Fatalf("damaged capture = %v", ref)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("damaged capture launched: %v", launched)
	}
}

// TestSupersedeRetiresOrphanedPhaseWithNoActiveExecution proves a phase
// directory no execution marker accounts for is retired, not refused: the old
// code let phaseDirectory fail with "cannot create fresh phase directory",
// which names the symptom and leaves the operator to delete artifacts by hand.
func TestSupersedeRetiresOrphanedPhaseWithNoActiveExecution(t *testing.T) {
	t.Parallel()
	req, commander := coordinatorFixture(t)
	req.FindersRaw = "2"
	// A partial run nothing marks: no launcher record, and a capture that names
	// no finders, exactly what a re-fetch over a partial run leaves behind.
	writeUnder(t, commander.dir, "run.json", `{"schema_version":"1","finders":0}`)
	writeUnder(t, filepath.Join(commander.dir, "x-finder-2"), "review.json", `{"changed":true}`)
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatalf("orphaned phase refused the re-run: %v", ref)
	}
	location := retiredLocation(t, commander.dir)
	retired := readPhaseBytes(t, filepath.Join(commander.dir, location, "x-finder-2", "review.json"))
	if string(retired) != `{"changed":true}` {
		t.Fatalf("retired finder artifact = %q", retired)
	}
	if fresh := readPhaseBytes(t, filepath.Join(commander.dir, "x-finder-2", "review.json")); string(fresh) == `{"changed":true}` {
		t.Fatal("the re-run reused the orphaned phase directory")
	}
	assertCoordinatorArtifacts(t, commander.dir)
}

// TestSupersedeRetiresAnInterruptedPriorRunAfterRefetch proves the shape the
// damaged-capture refusal used to trap: an in-progress execution whose launcher
// record and both finder phases survived, and a re-fetch that reset run.json to
// no finders and no execution reference. The artifacts themselves record the
// execution, so they are retired and the replacement run proceeds.
func TestSupersedeRetiresAnInterruptedPriorRunAfterRefetch(t *testing.T) {
	t.Parallel()
	req, commander := completedExecution(t)
	commander.reset()
	writeUnder(t, commander.dir, "launcher.json",
		`{"schema_version":"2","execution_id":"prior","status":"in-progress","finders":2}`)
	refetchedCapture(t, commander.dir)
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("interrupted prior run refused the re-run: %v", ref)
	}
	location := retiredLocation(t, commander.dir)
	for _, name := range []string{"launcher.json", filepath.Join("x-finder-1", "review.json"), filepath.Join("x-finder-2", "review.json")} {
		if raw := readPhaseBytes(t, filepath.Join(commander.dir, location, name)); len(raw) == 0 {
			t.Fatalf("retired %s is empty", name)
		}
	}
	if raw := readPhaseBytes(t, filepath.Join(commander.dir, location, "launcher.json")); !strings.Contains(string(raw), `"prior"`) {
		t.Fatalf("retired launcher record = %s, want the interrupted execution", raw)
	}
	capture := readPhaseObject(t, filepath.Join(commander.dir, "run.json"))
	if readcommon.FieldOf(capture, "superseded") != location {
		t.Fatalf("capture superseded = %s, want %s", jq.Compact(capture), location)
	}
	assertCoordinatorArtifacts(t, commander.dir)
}

// refetchedCapture rewrites run.json the way fetch's persistRunBundle does over
// an existing bundle: a brand-new capture with no execution reference and no
// finder count, while every artifact of the interrupted execution stays put.
func refetchedCapture(t *testing.T, dir string) {
	t.Helper()
	capture, _, err := config.ReadRunState(dir)
	if err != nil {
		t.Fatal(err)
	}
	capture.Execution, capture.Finders, capture.Superseded = nil, 0, ""
	if err := config.WriteRunState(dir, capture); err != nil {
		t.Fatal(err)
	}
}

// retiredLocation names the one retirement a bundle holds, failing when the
// bundle published none or several.
func retiredLocation(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, supersededDir))
	if err != nil {
		t.Fatalf("no retirement published: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, filepath.Join(supersededDir, entry.Name()))
	}
	if len(names) != 1 {
		t.Fatalf("retirements = %q, want exactly one", names)
	}
	return names[0]
}

func readPhaseBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// TestNextHeadReviewIgnoresEarlierTwoFinderPost is the stale-receipt false
// fail end to end: a two-finder review posted at the first head, a retirement
// and a re-fetch for the next commit, then a review of the new head. The
// earlier post.json still carries its finder summary, but it is bound to the
// earlier head, so neither a hand-recorded review nor a `run --finders 1`
// review of the new head inherits a cross requirement from it.
func TestNextHeadReviewIgnoresEarlierTwoFinderPost(t *testing.T) {
	t.Parallel()
	for _, launch := range []bool{false, true} {
		name := map[bool]string{false: "manual", true: "one-finder"}[launch]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reviewNextHead(t, launch)
		})
	}
}

// reviewNextHead reviews the new head by hand or through `run --finders 1`,
// records it, and requires a sufficient review whose evidence passes.
func reviewNextHead(t *testing.T, launch bool) {
	t.Helper()
	req, dir := nextHeadBundle(t)
	if launch {
		singleFinderRun(t, req, dir)
		requireOneFinderCapture(t, dir)
	} else {
		writeUnder(t, dir, "review.json", coordinatorReview)
	}
	recordSuperseded(t, req)
	response, ref := reportcore.Run(t.Context(), &reportcore.Request{
		ReadRequest: req.ReadRequest, Effective: core.Defaults(),
		Flags: reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatal(ref)
	}
	if approval := response.Data.Approval; approval == nil || approval.Insufficient {
		t.Fatalf("earlier head's post made this review insufficient: %+v", approval)
	}
	if dim := gateDimension(response.Gate, "evidence"); dim.Status != "pass" {
		t.Fatalf("evidence = %s %q, want pass", dim.Status, dim.Detail)
	}
}

func requireOneFinderCapture(t *testing.T, dir string) {
	t.Helper()
	if capture := readPhaseObject(t, filepath.Join(dir, "run.json")); jq.R(readcommon.FieldOf(capture, "finders")) != "1" {
		t.Fatalf("one-finder launch left run.json %s, want finders: 1", jq.Compact(capture))
	}
}

func gateDimension(gate *core.ReportGate, id string) core.GateDimension {
	for _, dim := range gate.Dimensions {
		if dim.ID == id {
			return dim
		}
	}
	return core.GateDimension{ID: id, Status: "absent"}
}

// nextHeadBundle leaves a bundle at a new head whose only record of the
// earlier two-finder review is the post.json receipt bound to the earlier one.
func nextHeadBundle(t *testing.T) (*core.RunRequest, string) {
	t.Helper()
	req, previous := completedExecution(t)
	dir := previous.dir
	raw, err := json.Marshal(&core.Post{
		SchemaVersion: "1", Review: map[string]any{}, PostedAt: "2026-11-02T12:00:00Z",
		Head:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verification: &core.PanelSummary{Finders: &core.FinderSummary{Completed: 2, Confirmed: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeUnder(t, dir, "post.json", string(raw))
	singleFinderRun(t, req, dir) // the next commit's review retires the execution
	writeUnder(t, dir, "source.json", `{"head_sha":"cccccccccccccccccccccccccccccccccccccccc","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	refetchedCapture(t, dir)
	return req, dir
}

func singleFinderRun(t *testing.T, req *core.RunRequest, dir string) {
	t.Helper()
	req.Commander = &scripted{write: func() { writeUnder(t, dir, "review.json", coordinatorReview) }}
	req.FindersRaw = "1"
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
}
