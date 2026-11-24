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
	stderr      string
	silentPhase string // this phase exits 0 and writes nothing
	calls       map[string]int
	phases      []string
	mu          sync.Mutex
	fail        bool
	failFinders bool // every finder exits 7
	placeholder bool // a failing finder first leaves the directive's empty review
	silent      bool // every finder exits 0 and writes nothing
	drift       bool
	repair      bool
	crossRepair bool
	crossBroken bool
	tamper      bool
	distinct    bool
	empty       bool // finders find nothing, so no cross check is dispatched
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
	if res, failed := p.failure(phase); failed {
		return res
	}
	writeUnder(p.t, filepath.Join(p.dir, phase), p.artifactName(phase), p.artifactBytes(phase, attempt))
	if cmd.Dir != filepath.Dir(filepath.Dir(filepath.Dir(p.dir))) {
		p.t.Errorf("cwd = %q", cmd.Dir)
	}
	return core.Result{}
}

// failure is the scripted result of a phase told to fail or to write nothing.
func (p *phaseCommander) failure(phase string) (core.Result, bool) {
	finder := strings.HasPrefix(phase, "x-finder-")
	switch {
	case p.fail && phase == "x-finder-1", p.failFinders && finder:
		if p.placeholder {
			writeUnder(p.t, filepath.Join(p.dir, phase), reviewFile, `{"schema_version":"1","findings":[]}`)
		}
		return core.Result{ExitCode: 7, Stderr: p.stderr}, true
	case p.silentPhase == phase:
		return core.Result{}, true
	case p.silent && finder:
		return core.Result{Stdout: p.stderr}, true
	case p.failPhase == phase && p.phaseCalls(phase) == 1:
		return core.Result{ExitCode: 7}, true
	}
	return core.Result{}, false
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
	if p.empty {
		return `{"schema_version":"1","summary":"> ✅ no findings Reviewed the diff.","findings":[],` + fixtureReceipts + `}`
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

// missingExecutionCoordinator builds the state coordinate reaches when a
// bundle no longer records an execution: the snapshot is pinned, but run.json
// names no execution or finder count and the launcher carries no execution id.
func missingExecutionCoordinator(t *testing.T, launcher func(t *testing.T, dir string)) *coordinator {
	t.Helper()
	req, commander := coordinatorFixture(t)
	// The documented re-fetch shape: no execution reference and no finder count,
	// so CheckExecution cannot call the run active.
	writeUnder(t, commander.dir, "run.json", `{"schema_version":"1","finders":0}`)
	launcher(t, commander.dir)
	return &coordinator{
		st: &state{req: req}, dir: commander.dir, id: "execution-A",
		snapshot: executionSnapshot{}, failures: []any{},
		response: &core.RunResponse{
			Repo: req.Repo, Pr: req.Pr, At: "2026-01-01T00:00:00Z",
			Data: &core.RunData{Agent: "claude", Bundle: commander.dir},
		},
	}
}

// assertMissingExecutionResponse proves the response never claims an execution
// it cannot count, and names the missing record as a phase failure.
func assertMissingExecutionResponse(t *testing.T, c *coordinator) {
	t.Helper()
	execution := c.response.Data.Execution
	if execution == nil {
		t.Fatal("response carries no execution envelope")
	}
	if execution.Counts != nil {
		t.Fatalf("counts = %+v, want null for an execution the bundle no longer records", execution.Counts)
	}
	if execution.Status != "partial" {
		t.Fatalf("status = %q, want partial: a run without an execution marker is not complete", execution.Status)
	}
	want := core.RunPhaseFailure{Phase: "execution", Code: "agent-record-failed"}
	if !slices.Contains(execution.Failures, want) {
		t.Fatalf("failures = %+v, want %+v", execution.Failures, want)
	}
}

// launcherCase shapes a bundle's launcher record for the missing-execution
// fixtures. The function field comes first because it carries a pointer.
type launcherCase struct {
	launcher func(t *testing.T, dir string)
	name     string
}

// TestCoordinatorReportsMissingExecutionWithoutPanicking drives the response
// tail coordinate always runs, with a bundle that no longer records an
// execution: a launcher killed before its first write leaves a record with no
// execution id, and a re-fetch can reset run.json to no finder count. The old
// code dereferenced the nil summary Attach returns for an inactive execution
// and died with a stack trace instead of refusing.
//
// The test calls executionResponse directly because every fan-in rewrites
// launcher.json from the coordinator's in-memory record, so a stub agent alone
// cannot leave the marker absent by the time coordinate reaches this call.
func TestCoordinatorReportsMissingExecutionWithoutPanicking(t *testing.T) {
	cases := []launcherCase{
		{name: "launcher keeps a complete status but loses the execution id", launcher: func(t *testing.T, dir string) {
			t.Helper()
			writeUnder(t, dir, "launcher.json", `{"schema_version":"2","status":"complete"}`)
		}},
		{name: "launcher is gone", launcher: func(t *testing.T, dir string) {
			t.Helper()
			writeUnder(t, dir, "launcher.json", `{"schema_version":"2","execution_id":"execution-A","status":"complete"}`)
			if err := os.Remove(filepath.Join(dir, "launcher.json")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := missingExecutionCoordinator(t, tc.launcher)
			c.executionResponse()
			assertMissingExecutionResponse(t, c)
		})
	}
}

// TestCoordinatorRefusesMissingExecution proves the tail returns an operator
// refusal rather than a bare null when no earlier phase refused first.
func TestCoordinatorRefusesMissingExecution(t *testing.T) {
	c := missingExecutionCoordinator(t, func(t *testing.T, dir string) {
		t.Helper()
		writeUnder(t, dir, "launcher.json", `{"schema_version":"2","status":"complete"}`)
	})
	c.executionResponse()
	ref := c.executionRefusal()
	if ref == nil || ref.Code != "agent-record-failed" {
		t.Fatalf("refusal = %v, want agent-record-failed", ref)
	}
	if !strings.Contains(ref.Message, "recorded no execution") || !strings.Contains(ref.Message, "re-run the launch") {
		t.Fatalf("message = %q, want the missing execution and its remedy", ref.Message)
	}
	assertMissingExecutionResponse(t, c)
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

const providerError = "API Error: 402 Credit balance is too low sk-live-abcdef0123456789"

// TestCoordinatorSurfacesTheAgentErrorWhenEveryFinderFails pins the refusal an
// operator reads when both finders die: the agent's own code, exit status, and
// redacted output, not a review-insufficient evidence gap.
func TestCoordinatorSurfacesTheAgentErrorWhenEveryFinderFails(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.failFinders, commander.stderr = true, providerError
	req.Environ = []string{"ANTHROPIC_API_KEY=sk-live-abcdef0123456789"}
	response, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "agent-failed" {
		t.Fatalf("refusal = %v, want agent-failed", ref)
	}
	if !strings.Contains(ref.Message, "exited 7") || !strings.Contains(ref.Message, "402 Credit balance is too low") {
		t.Fatalf("message hides the cause: %q", ref.Message)
	}
	if strings.Contains(ref.Message, "sk-live-abcdef0123456789") {
		t.Fatalf("message leaks the key: %q", ref.Message)
	}
	if response == nil || response.Exit != 7 {
		t.Fatalf("response = %+v, want the agent's exit 7 preserved", response)
	}
}

// TestCoordinatorKeepsNoArtifactWhenEveryFinderWritesNothing is the claude
// write-refused case: both finders exit 0 and print the refusal. The code
// must be the one the permission hint keys on, with the agent's words in the
// message.
func TestCoordinatorKeepsNoArtifactWhenEveryFinderWritesNothing(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.silent, commander.stderr = true, "I could not write review.json: permission to use Write was denied"
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "no-review-artifact" {
		t.Fatalf("refusal = %v, want no-review-artifact", ref)
	}
	if !strings.Contains(ref.Message, "permission to use Write was denied") {
		t.Fatalf("message hides the cause: %q", ref.Message)
	}
}

// TestCoordinatorPartialExecutionNamesTheFailedFinder keeps the partial
// verdict (one finder survived, so evidence is merely insufficient) but names
// the failed finder's cause beside it.
func TestCoordinatorPartialExecutionNamesTheFailedFinder(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.fail, commander.stderr = true, providerError
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "review-insufficient" {
		t.Fatalf("refusal = %v", ref)
	}
	if !strings.Contains(ref.Message, "x-finder-1 agent-failed: ") || !strings.Contains(ref.Message, "402 Credit balance") {
		t.Fatalf("message hides the failed finder's cause: %q", ref.Message)
	}
}

// TestCoordinatorPartialFailureNeverLeaksTheAgentExit pins that only an
// agent-failed refusal carries the agent's exit status: a finished finder plus
// a dead sibling is an evidence refusal, and its response must exit with
// verdict's own code.
func TestCoordinatorPartialFailureNeverLeaksTheAgentExit(t *testing.T) {
	for _, phase := range []string{"x-finder-2", "x-cross-2"} {
		t.Run(phase, func(t *testing.T) { checkPartialExit(t, phase) })
	}
}

func checkPartialExit(t *testing.T, phase string) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	commander.failPhase = phase
	// A finder's death is the last response only when no cross check follows
	// it; a dead cross check needs candidates to dispatch.
	commander.empty = phase == "x-finder-2"
	commander.distinct = !commander.empty
	response, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "review-insufficient" {
		t.Fatalf("refusal = %v, want review-insufficient", ref)
	}
	if response == nil || response.Exit != 0 {
		t.Fatalf("response exit = %+v, want 0 for a non-agent-failed refusal", response)
	}
}

// TestCoordinatorPlaceholderDoesNotHideAFailedFinder pins that a dead finder
// counts as failed by its invoke outcome: the directive's empty review decodes,
// so judging by the artifact alone read both dead finders as a survivor.
func TestCoordinatorPlaceholderDoesNotHideAFailedFinder(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.failFinders, commander.placeholder, commander.stderr = true, true, providerError
	response, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "agent-failed" {
		t.Fatalf("refusal = %v, want agent-failed", ref)
	}
	if response == nil || response.Exit != 7 {
		t.Fatalf("response = %+v, want the agent's exit 7", response)
	}
}

// TestCoordinatorNamesEveryFailedFinderAndPrefersTheAgentError pins the
// all-failed refusal when the finders died differently: the agent's failure
// leads over a missing artifact, whichever finder had which, and the message
// names both.
func TestCoordinatorNamesEveryFailedFinderAndPrefersTheAgentError(t *testing.T) {
	req, commander := coordinatorFixture(t)
	commander.fail, commander.silentPhase, commander.stderr = false, "x-finder-1", providerError
	commander.failPhase = "x-finder-2"
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "agent-failed" {
		t.Fatalf("refusal = %v, want agent-failed to lead no-review-artifact", ref)
	}
	for _, want := range []string{"x-finder-1 no-review-artifact: ", "x-finder-2 agent-failed: "} {
		if !strings.Contains(ref.Message, want) {
			t.Fatalf("message omits %q: %q", want, ref.Message)
		}
	}
}

// TestCoordinatorSanitizesTheAgentOutput feeds the failure message the shapes
// an agent prints: escapes around an authorization header and a provider key.
// None may reach the refusal, whole or cut in half by the tail.
func TestCoordinatorSanitizesTheAgentOutput(t *testing.T) {
	leak := "\x1b[31mAuthorization: Bearer eyJhbGciOiJIUzI1NiJ9.secret\x1b[0m key=sk-ant-api03-12345678901234567890"
	for name, output := range map[string]string{
		"whole":       leak,
		"cut":         leak + strings.Repeat("y", messageTailCap-30),
		"url":         "fetch https://user:hunter2pass@host.example/path failed",
		"long-before": strings.Repeat("z", 3*messageWindow) + leak,
	} {
		t.Run(name, func(t *testing.T) { checkSanitized(t, output) })
	}
}

func checkSanitized(t *testing.T, output string) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	commander.failFinders, commander.stderr = true, output
	_, ref := TwoFinders(t.Context(), req)
	if ref == nil || ref.Code != "agent-failed" {
		t.Fatalf("refusal = %v", ref)
	}
	for _, bad := range []string{"eyJ", "api03", "12345678901234567890", "hunter2pass", "\x1b", "Bearer e"} {
		if strings.Contains(ref.Message, bad) {
			t.Fatalf("message leaks %q: %q", bad, ref.Message)
		}
	}
}
