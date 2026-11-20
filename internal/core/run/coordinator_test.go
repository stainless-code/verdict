package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type phaseCommander struct {
	t           *testing.T
	dir         string
	phases      []string
	fail        bool
	drift       bool
	repair      bool
	crossRepair bool
	crossBroken bool
	tamper      bool
	distinct    bool
}

func (p *phaseCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	manifest := readPhaseObject(p.t, filepath.Join(p.dir, "launcher.json"))
	if readcommon.FieldOf(manifest, "status") != "in-progress" {
		p.t.Fatal("final manifest was committed before launches finished")
	}
	prompt := cmd.Argv[len(cmd.Argv)-1]
	phase := ""
	for _, name := range []string{"x-finder-1", "x-finder-2", "x-cross-1", "x-cross-2"} {
		if strings.Contains(prompt, name) {
			phase = name
			break
		}
	}
	if phase == "" {
		p.t.Fatalf("unexpected argv: %q", cmd.Argv)
	}
	p.phases = append(p.phases, phase)
	return p.writePhase(phase, cmd), nil
}

func (p *phaseCommander) writePhase(phase string, cmd *core.Command) core.Result {
	p.changeEvidence(phase)
	if p.fail && phase == "x-finder-1" {
		return core.Result{ExitCode: 7}
	}
	name, raw := "review.json", coordinatorReview
	if p.distinct {
		raw = strings.ReplaceAll(raw, "Changed value breaks the caller.", distinctClaim(phase))
	}
	if strings.HasPrefix(phase, "x-cross-") {
		name, raw = "verdicts.json", p.crossReview(phase)
	}
	if p.repair && strings.HasPrefix(phase, "x-finder-") && phaseCalls(p.phases, phase) == 1 {
		raw = `{"schema_version":"1","findings":"broken"}`
	}
	writeUnder(p.t, filepath.Join(p.dir, phase), name, raw)
	if cmd.Dir != filepath.Dir(filepath.Dir(filepath.Dir(p.dir))) {
		p.t.Fatalf("cwd = %q", cmd.Dir)
	}
	return core.Result{}
}

func (p *phaseCommander) crossReview(phase string) string {
	request := readPhaseObject(p.t, filepath.Join(p.dir, phase, "request.json"))
	candidates := jq.Elements(readcommon.FieldOf(request, "candidates"))
	results := []any{}
	for _, candidate := range candidates {
		finding := readcommon.FieldOf(candidate, "finding")
		p.assertCrossCandidate(phase, finding)
		results = append(results, jq.NewObj("sig", readcommon.FieldOf(candidate, "sig"), "verdict", "confirmed",
			"evidence", "The changed value violates the caller contract.", "quote", jq.NewObj("path", "a.go", "side", "RIGHT", "line", 1, "text", "new line 1")))
	}
	if p.crossBroken || (p.crossRepair && len(p.phases) == 3) {
		return `{"results":[]}`
	}
	return jq.PrettyOrdered(jq.NewObj("results", results))
}

func coordinatorFixture(t *testing.T) (*core.RunRequest, *phaseCommander) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	writeUnder(t, dir, "run.json", `{"schema_version":"1"}`)
	writeUnder(t, dir, "PR.json", `{"headRefOid":"head"}`)
	writeUnder(t, dir, "procedure.md", "authoritative procedure")
	commander := &phaseCommander{t: t, dir: dir}
	req := request(root, commander)
	req.RoundsRaw = "1"
	return req, commander
}

func readPhaseObject(t *testing.T, path string) *jq.Obj {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := value.(*jq.Obj)
	if !ok {
		t.Fatal("not an object")
	}
	return obj
}

func TestCoordinatorIsolatesAndCrossesBothOrigins(t *testing.T) {
	req, commander := coordinatorFixture(t)
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatalf("%v manifest=%s", ref, jq.Compact(readPhaseObject(t, filepath.Join(commander.dir, "launcher.json"))))
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("phases = %s", got)
	}
	assertCoordinatorArtifacts(t, commander.dir)
}

func assertCoordinatorArtifacts(t *testing.T, dir string) {
	t.Helper()
	review := readPhaseObject(t, filepath.Join(dir, "review.json"))
	if len(jq.Elements(readcommon.FieldOf(review, "findings"))) != 1 {
		t.Fatal("exact duplicate was not merged")
	}
	plan := readPhaseObject(t, filepath.Join(dir, "verification", "plan.json"))
	checks := jq.Elements(readcommon.FieldOf(plan, "checks"))
	if len(checks) != 2 || readcommon.FieldOf(checks[0], "origin") != "finder-1" || readcommon.FieldOf(checks[0], "checker") != "finder-2" ||
		readcommon.FieldOf(checks[1], "origin") != "finder-2" || readcommon.FieldOf(checks[1], "checker") != "finder-1" {
		t.Fatalf("checks = %s", jq.Compact(checks))
	}
	manifest := readPhaseObject(t, filepath.Join(dir, "launcher.json"))
	if readcommon.FieldOf(manifest, "status") != "complete" {
		t.Fatalf("manifest = %s", jq.Compact(manifest))
	}
	assertFinderProcedures(t, dir)
}

func assertFinderProcedures(t *testing.T, dir string) {
	t.Helper()
	for _, phase := range []string{"x-finder-1", "x-finder-2"} {
		raw, err := os.ReadFile(filepath.Join(dir, phase, "procedure.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), filepath.Join(dir, phase, "review.json")) || strings.Contains(string(raw), "<bundle>") {
			t.Fatalf("procedure output is not isolated: %s", raw)
		}
	}
}

func TestCoordinatorFailedFinderIsPartial(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.fail = true
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "review-insufficient" {
		t.Fatalf("refusal = %v", ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1" {
		t.Fatalf("phases = %s", got)
	}
	manifest := readPhaseObject(t, filepath.Join(commander.dir, "launcher.json"))
	if readcommon.FieldOf(manifest, "status") != "partial" {
		t.Fatal("failed finder completed execution")
	}
}

func TestCoordinatorDriftStopsBeforeNextPhase(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.drift = true
	commander.repair = true
	req.RoundsRaw = "2"
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "stale-review-artifact" || len(commander.phases) != 1 {
		t.Fatalf("refusal=%v phases=%v", ref, commander.phases)
	}
	manifest := readPhaseObject(t, filepath.Join(commander.dir, "launcher.json"))
	if readcommon.FieldOf(manifest, "status") != "in-progress" {
		t.Fatal("drift committed final manifest")
	}
}

func TestCoordinatorRepairsFinderIndependently(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.RoundsRaw = "2"
	commander.repair = true
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-1,x-finder-2,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("phases = %s", got)
	}
}

func TestCoordinatorRepairsCrossAtMostOnce(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.RoundsRaw = "5"
	commander.crossRepair = true
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-1,x-cross-2" {
		t.Fatalf("phases = %s", got)
	}
}

func phaseCalls(phases []string, phase string) int {
	count := 0
	for _, name := range phases {
		if name == phase {
			count++
		}
	}
	return count
}

func TestCoordinatorExhaustsCrossRepairWithoutExtraLaunches(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.RoundsRaw = "5"
	commander.crossBroken = true
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "review-insufficient" {
		t.Fatalf("refusal = %v", ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-1,x-cross-2,x-cross-2" {
		t.Fatalf("phases = %s", got)
	}
}

func TestCoordinatorDetectsChangedCompletedFinder(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.tamper = true
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "stale-review-artifact" || len(commander.phases) != 2 {
		t.Fatalf("refusal=%v phases=%v", ref, commander.phases)
	}
}

func TestCoordinatorManifestBindsPrivateRepairArtifacts(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.RoundsRaw = "2"
	commander.repair = true
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	manifest := readPhaseObject(t, filepath.Join(commander.dir, "launcher.json"))
	artifacts := readcommon.FieldOf(manifest, "artifacts")
	key := filepath.Join("x-finder-1", ".run.rounds", "01.review.json")
	if got := readcommon.FieldOf(artifacts, key); got != "770bb888faf96d0ccaea1b97d2e1e7693b2dc270b53d5413f68d25d85e31c088" {
		t.Fatalf("repair artifact digest = %v", got)
	}
}

func distinctClaim(phase string) string {
	if phase == "x-finder-1" {
		return "First finder defect."
	}

	return "Second finder defect."
}

func oppositeClaim(phase string) string {
	if phase == "x-cross-1" {
		return "Second finder defect."
	}

	return "First finder defect."
}

func TestCoordinatorCrossDispatchesOnlyOppositeCandidates(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.distinct = true
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if got := strings.Join(commander.phases, ","); got != "x-finder-1,x-finder-2,x-cross-1,x-cross-2" {
		t.Fatalf("phases = %s", got)
	}
}

func (p *phaseCommander) changeEvidence(phase string) {
	if p.tamper && phase == "x-finder-2" {
		writeUnder(p.t, filepath.Join(p.dir, "x-finder-1"), "review.json", `{"changed":true}`)
	}
	if p.drift {
		writeUnder(p.t, p.dir, "full.diff", fixtureDiff+"\n")
	}
}

func (p *phaseCommander) assertCrossCandidate(phase string, finding any) {
	expected := "Changed value breaks the caller."
	if p.distinct {
		expected = oppositeClaim(phase)
	}
	if readcommon.FieldOf(finding, "claim") != expected {
		p.t.Fatal("selected claim missing from cross batch")
	}
}

func TestCoordinatorPublishesExecutionSummary(t *testing.T) {
	req, _ := coordinatorFixture(t)
	response, ref := TwoFinders(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	execution := response.Data.Execution
	if execution == nil || execution.Counts == nil || execution.ID == "" || execution.Status != "complete" || execution.Counts.Completed != 2 || execution.Counts.Confirmed != 2 {
		t.Fatalf("execution = %+v", execution)
	}
}

func TestCoordinatorPublishesPartialFailures(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.fail = true
	response, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "review-insufficient" {
		t.Fatalf("refusal = %v", ref)
	}
	execution := response.Data.Execution
	if execution == nil || execution.Counts == nil || execution.Status != "partial" || execution.Counts.Completed != 1 || len(execution.Failures) != 1 {
		t.Fatalf("execution = %+v", execution)
	}
	if execution.Failures[0].Phase != "x-finder-1" || execution.Failures[0].Code != "agent-failed" {
		t.Fatalf("failures = %+v", execution.Failures)
	}
}
