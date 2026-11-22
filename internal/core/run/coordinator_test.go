package run

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// phaseCommander scripts the finder and cross phases and logs every launch. The
// log is guarded: the coordinator runs each pair of phases at once, so two
// launches append to it from two goroutines.
type phaseCommander struct {
	t           *testing.T
	dir         string
	cancel      context.CancelFunc
	failPhase   string
	cancelOn    string
	calls       map[string]int
	phases      []string
	mu          sync.Mutex
	fail        bool
	drift       bool
	repair      bool
	crossRepair bool
	crossBroken bool
	tamper      bool
	distinct    bool
	outputDir   bool
}

func (p *phaseCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	manifest := readPhaseObject(p.t, filepath.Join(p.dir, "launcher.json"))
	if readcommon.FieldOf(manifest, "status") != "in-progress" {
		p.t.Error("final manifest was committed before launches finished")
	}
	phase, ok := phaseOf(cmd)
	if !ok {
		p.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{ExitCode: 2}, nil
	}
	attempt := p.record(phase)
	if p.cancelOn == phase && attempt == 1 {
		p.cancel()
		return core.Result{ExitCode: 7}, nil
	}
	return p.writePhase(phase, attempt, cmd), nil
}

// record logs one launch and reports how many times that phase has launched.
func (p *phaseCommander) record(phase string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[phase]++
	p.phases = append(p.phases, phase)
	return p.calls[phase]
}

// launched is a copy of the launch log, so a reader never walks a slice a
// phase may still append to.
func (p *phaseCommander) launched() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.phases)
}

// reset forgets the launch log, so a test can re-run the same fixture and read
// the second run's launches alone.
func (p *phaseCommander) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phases, p.calls = nil, nil
}

func (p *phaseCommander) phaseCalls(phase string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[phase]
}

// phaseOf reads the phase directory out of the launched directive.
func phaseOf(cmd *core.Command) (string, bool) {
	prompt := cmd.Argv[len(cmd.Argv)-1]
	for _, name := range []string{"x-finder-1", "x-finder-2", "x-cross-1", "x-cross-2"} {
		if strings.Contains(prompt, name) {
			return name, true
		}
	}
	return "", false
}

func (p *phaseCommander) writePhase(phase string, attempt int, cmd *core.Command) core.Result {
	p.changeEvidence(phase)
	if p.outputDir {
		p.makeOutputPathADirectory(phase)
	}
	if p.fail && phase == "x-finder-1" {
		return core.Result{ExitCode: 7}
	}
	if p.failPhase == phase && p.phaseCalls(phase) == 1 {
		return core.Result{ExitCode: 7}
	}
	writeUnder(p.t, filepath.Join(p.dir, phase), p.artifactName(phase), p.artifactBytes(phase, attempt))
	if cmd.Dir != filepath.Dir(filepath.Dir(filepath.Dir(p.dir))) {
		p.t.Errorf("cwd = %q", cmd.Dir)
	}
	return core.Result{}
}

// artifactName is the file one phase writes.
func (p *phaseCommander) artifactName(phase string) string {
	if strings.HasPrefix(phase, "x-cross-") {
		return "verdicts.json"
	}
	return reviewFile
}

// artifactBytes renders the bytes one phase leaves, at the attempt it is on.
func (p *phaseCommander) artifactBytes(phase string, attempt int) string {
	if strings.HasPrefix(phase, "x-cross-") {
		return p.crossReview(phase, attempt)
	}
	return p.finderReview(phase, attempt)
}

func (p *phaseCommander) finderReview(phase string, attempt int) string {
	if p.repair && attempt == 1 {
		return `{"schema_version":"1","findings":"broken"}`
	}
	if p.distinct {
		return strings.ReplaceAll(coordinatorReview, "Changed value breaks the caller.", distinctClaim(phase))
	}
	return coordinatorReview
}

// makeOutputPathADirectory gives one phase a `.run.out` the launcher cannot
// write, so that phase buffers a warning naming its own directory: the fan-in
// then has two distinct warnings whose order it must decide.
func (p *phaseCommander) makeOutputPathADirectory(phase string) {
	if !strings.HasPrefix(phase, "x-finder-") || p.phaseCalls(phase) != 1 {
		return
	}
	if err := os.MkdirAll(filepath.Join(p.dir, phase, runOutputFile), 0o700); err != nil {
		p.t.Errorf("plant unwritable output path: %v", err)
	}
}

func (p *phaseCommander) crossReview(phase string, attempt int) string {
	request := readPhaseObject(p.t, filepath.Join(p.dir, phase, "request.json"))
	candidates := jq.Elements(readcommon.FieldOf(request, "candidates"))
	results := []any{}
	for _, candidate := range candidates {
		finding := readcommon.FieldOf(candidate, "finding")
		p.assertCrossCandidate(phase, finding)
		results = append(results, jq.NewObj("sig", readcommon.FieldOf(candidate, "sig"), "verdict", "confirmed",
			"evidence", "The changed value violates the caller contract.", "quote", jq.NewObj("path", "a.go", "side", "RIGHT", "line", 1, "text", "new line 1")))
	}
	if p.crossBroken || (p.crossRepair && phase == "x-cross-1" && attempt == 1) {
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
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	assertCoordinatorArtifacts(t, commander.dir)
}

// assertPhaseGroups proves the launch log is the given groups in that group
// order, each group in any order: the finder pair and the cross pair are
// barriers, while the two launches inside a barrier overlap.
func assertPhaseGroups(t *testing.T, phases []string, groups ...[]string) {
	t.Helper()
	at := 0
	for _, group := range groups {
		at = matchPhaseGroup(t, phases, at, group)
	}
	if at != len(phases) {
		t.Fatalf("launches = %v, want %v", phases, groups)
	}
}

// matchPhaseGroup consumes one barrier's launches: every position must be an
// unconsumed member of the group, and the group may finish in any order.
func matchPhaseGroup(t *testing.T, phases []string, at int, group []string) int {
	t.Helper()
	remaining := slices.Clone(group)
	for range group {
		if at >= len(phases) {
			t.Fatalf("launches = %v, want one of %v next", phases, group)
		}
		found := slices.Index(remaining, phases[at])
		if found < 0 {
			t.Fatalf("launch %d is %q, want one of %v (log %v)", at, phases[at], group, phases)
		}
		remaining = slices.Delete(remaining, found, found+1)
		at++
	}
	return at
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
	// The survived finder still owes its opposite check: only x-cross-1 has a
	// candidate batch, so the failed finder's own check is never dispatched.
	assertPhaseGroups(t, commander.launched(), []string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1"})
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
	if ref == nil || ref.Code != "stale-review-artifact" {
		t.Fatalf("refusal = %v", ref)
	}
	for _, phase := range commander.launched() {
		if strings.HasPrefix(phase, "x-cross-") {
			t.Fatalf("drift crossed: %v", commander.launched())
		}
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
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-1", "x-finder-2", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
}

func TestCoordinatorRepairsCrossAtMostOnce(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.RoundsRaw = "5"
	commander.crossRepair = true
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-1", "x-cross-2"})
}

func TestCoordinatorExhaustsCrossRepairWithoutExtraLaunches(t *testing.T) {
	req, commander := coordinatorFixture(t)
	req.RoundsRaw = "5"
	commander.crossBroken = true
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "review-insufficient" {
		t.Fatalf("refusal = %v", ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-1", "x-cross-2", "x-cross-2"})
}

// TestCoordinatorDetectsChangedCompletedFinder proves a bound finder artifact
// that changes underfoot refuses the run. The change lands during a cross
// launch, which is where a parallel discovery can see it: both finders' phases
// are bound by then, so the checkpoint that refuses is the running check's own.
func TestCoordinatorDetectsChangedCompletedFinder(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.tamper = true
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "stale-review-artifact" {
		t.Fatalf("refusal = %v", ref)
	}
	if _, err := os.Stat(filepath.Join(commander.dir, "verification", "verdicts.json")); !os.IsNotExist(err) {
		t.Fatalf("tampered execution published verdicts: %v", err)
	}
	launched := commander.launched()
	if slices.Contains(launched, "x-finder-1") == false || slices.Contains(launched, "x-finder-2") == false {
		t.Fatalf("detection skipped a finder: %v", launched)
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
	// The record binds bundle paths with forward slashes on every platform, and
	// the key is a published string, so the assertion spells it out.
	key := "x-finder-1/.run.rounds/01.review.json"
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
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
}

func (p *phaseCommander) changeEvidence(phase string) {
	if p.tamper && strings.HasPrefix(phase, "x-cross-") && p.phaseCalls(phase) == 1 {
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
		p.t.Error("selected claim missing from cross batch")
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

func TestCoordinatorRefusesSessionResumePerAgent(t *testing.T) {
	cases := []struct {
		name        string
		agent       string
		passThrough []string
		refused     bool
	}{
		{"codex resume subcommand", "codex", []string{"resume", "--last"}, true},
		{"codex config override", "codex", []string{"-c", `model_reasoning_effort="high"`}, false},
		{"codex sandbox short flag", "codex", []string{"-s", "workspace-write"}, false},
		{"claude continue flag", "claude", []string{"-c"}, true},
		{"claude resume flag", "claude", []string{"--resume", "execution-A"}, true},
		{"opencode continue flag", "opencode", []string{"-c"}, true},
		{"opencode fork session", "opencode", []string{"--fork"}, true},
		{"opencode named session", "opencode", []string{"-s", "sess-1"}, true},
		{"pi continue flag", "pi", []string{"-c"}, true},
		{"pi fork session", "pi", []string{"--fork", "sess-1"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertResumePassThrough(t, tc.agent, tc.passThrough, tc.refused)
		})
	}
}

func assertResumePassThrough(t *testing.T, agent string, passThrough []string, refused bool) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	req.Agent, req.PassThrough = agent, passThrough
	_, ref := TwoFinders(t.Context(), req)
	if refused {
		if ref == nil || ref.Code != "bad-flags" || len(commander.launched()) != 0 {
			t.Fatalf("refusal = %v, launches = %v", ref, commander.launched())
		}
		return
	}
	if ref != nil {
		t.Fatalf("pass-through refused: %v", ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
}

func TestCoordinatorHandsOffTheAggregateBundle(t *testing.T) {
	req, commander := completedExecution(t)
	commander.reset()
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if want := "record the review with verdict record acme/app local-supersede"; response.Next != want {
		t.Fatalf("next = %q, want %q", response.Next, want)
	}
}
