package run

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
)

// resumeFixture is a captured local two-finder bundle: the source, the base
// controls, and the run state record, gate, and report read. The bundle is a
// local one because that is the shape record accepts, and the whole point of
// the resume tests is that the continued execution records cleanly.
func resumeFixture(t *testing.T, slug string) (*core.RunRequest, *phaseCommander) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	req.FindersRaw = ""
	dir := filepath.Join(filepath.Dir(commander.dir), slug)
	if err := os.Rename(commander.dir, dir); err != nil {
		t.Fatal(err)
	}
	commander.dir = dir
	if err := os.Remove(filepath.Join(dir, "PR.json")); err != nil {
		t.Fatal(err)
	}
	req.Pr = slug
	const headSHA, baseSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeUnder(t, dir, "source.json", `{"head_sha":"`+headSHA+`","base_sha":"`+baseSHA+`"}`)
	root := req.RepoRoot
	req.Root = &root
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: root, BaseSHA: baseSHA, AllowHead: true,
	})
	if ref != nil {
		t.Fatalf("resolve controls: %v", ref)
	}
	capture, _, err := config.ReadRunState(dir)
	if err != nil {
		t.Fatal(err)
	}
	capture.BaseSHA = baseSHA
	capture.Inventory = config.InventoryHashes(snap)
	if err := config.WriteRunState(dir, capture); err != nil {
		t.Fatal(err)
	}
	return req, commander
}

// interruptRun runs the fixture once with one phase failing, which is the
// interrupted execution every resume test continues: the run refuses, but it
// leaves a finished manifest and whatever phases did complete.
func interruptRun(t *testing.T, req *core.RunRequest, commander *phaseCommander, phase string) {
	t.Helper()
	commander.failPhase = phase
	if _, ref := Run(t.Context(), req); ref == nil {
		t.Fatalf("%s was expected to interrupt the run", phase)
	}
	commander.failPhase = ""
	commander.reset()
}

// resumeManifest is the launcher record read back as an object.
func resumeManifest(t *testing.T, dir string) *jq.Obj {
	t.Helper()
	return readPhaseObject(t, filepath.Join(dir, "launcher.json"))
}

// resumeBlock is the manifest's resume block.
func resumeBlock(t *testing.T, manifest *jq.Obj) *jq.Obj {
	t.Helper()
	block, ok := readcommon.FieldOf(manifest, "resume").(*jq.Obj)
	if !ok {
		t.Fatalf("manifest carries no resume block: %s", jq.Compact(manifest))
	}
	return block
}

// phaseList reads one []string provenance list off the resume block.
func phaseList(t *testing.T, block *jq.Obj, key string) []string {
	t.Helper()
	out := []string{}
	for _, row := range jq.Elements(readcommon.FieldOf(block, key)) {
		out = append(out, jq.StrOf(row))
	}
	return out
}

// TestResumeRerunsOnlyTheFailedFinder proves the whole resume contract in one
// run: a finder that completed is never spawned again, the failed finder and
// both cross-checks run, the union is re-derived, and the bundle records,
// gates, and reports cleanly.
func TestResumeRerunsOnlyTheFailedFinder(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-finder")
	interruptRun(t, req, commander, "x-finder-2")

	req.Resume = true
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	assertFinderResume(t, response, commander)
	assertResumedBundleRecords(t, req)
}

// assertFinderResume pins the finder-interruption contract: finder-2 relaunched
// with both cross checks and nothing else, the response and the manifest agree
// on which phases ran, and finder-1 was never spawned again.
func assertFinderResume(t *testing.T, response *core.RunResponse, commander *phaseCommander) {
	t.Helper()
	assertPhaseGroups(t, commander.launched(), []string{"x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	if calls := commander.phaseCalls("x-finder-1"); calls != 0 {
		t.Fatalf("finder-1 launched %d times; a completed finder must never be re-invoked", calls)
	}
	resumed := response.Data.Resume
	if resumed == nil {
		t.Fatal("resumed run carried no resume provenance")
	}
	if resumed.PriorStatus != "partial" || resumed.Attempts != 2 ||
		resumed.Fingerprint == "" || resumed.PromptsHash == "" {
		t.Fatalf("resume provenance = %+v", resumed)
	}
	if want := []string{"x-finder-1"}; !slices.Equal(resumed.Reused, want) {
		t.Fatalf("reused = %v, want %v", resumed.Reused, want)
	}
	if want := []string{"x-finder-2", "union", "x-cross-1", "x-cross-2"}; !slices.Equal(resumed.Executed, want) {
		t.Fatalf("executed = %v, want %v", resumed.Executed, want)
	}
	assertRecordedResume(t, commander.dir, resumed.Reused, resumed.Executed)
}

// assertRecordedResume proves the launcher record carries the same reuse
// provenance the envelope reported, so a later reader does not need the
// response.
func assertRecordedResume(t *testing.T, dir string, reused, executed []string) {
	t.Helper()
	block := resumeBlock(t, resumeManifest(t, dir))
	if got := phaseList(t, block, "reused"); !slices.Equal(got, reused) {
		t.Fatalf("recorded reused = %v, want %v", got, reused)
	}
	if got := phaseList(t, block, "executed"); !slices.Equal(got, executed) {
		t.Fatalf("recorded executed = %v, want %v", got, executed)
	}
	if got := jq.StrOf(readcommon.FieldOf(block, "prior_status")); got != "partial" {
		t.Fatalf("recorded prior_status = %q", got)
	}
}

// assertResumedBundleRecords proves the continued execution publishes what the
// next verbs consume: record stores the review, the gate composes every
// dimension from it, and the report reads the approval the recorded cross
// checks earned. Two gate postures are asked because the fixture review carries
// one important finding and no coverage attestation: at critical it passes, and
// at important it fails on exactly that finding, which is the gate reading the
// resumed bundle rather than a receipt it could not decode.
func assertResumedBundleRecords(t *testing.T, req *core.RunRequest) {
	t.Helper()
	if _, ref := recordcore.Run(t.Context(), &recordcore.Request{
		ReadRequest: req.ReadRequest, Effective: core.Defaults(),
	}); ref != nil {
		t.Fatalf("record refused the resumed bundle: %v", ref)
	}
	assertResumedGates(t, req)
}

// assertResumedGates proves the gate reads the continued execution's recorded
// finding: it passes at critical and fails at important on that finding alone.
func assertResumedGates(t *testing.T, req *core.RunRequest) {
	t.Helper()
	critical := runResumedGate(t, req, "critical")
	if critical.Gate == nil || !critical.Gate.Passed {
		t.Fatalf("gate at critical failed on the resumed bundle: %+v", critical.Gate)
	}
	important := runResumedGate(t, req, "important")
	if important.Gate == nil {
		t.Fatalf("gate at important returned no gate: %+v", important.Data)
	}
	if detail := gateDetail(important.Gate, "findings"); !strings.Contains(detail, "1 open finding") {
		t.Fatalf("findings dimension = %q, want the recorded finding", detail)
	}
	if !important.GateFailed {
		t.Fatal("gate at important passed a review that records an open important finding")
	}
	if important.Data.Approval == nil || important.Data.Approval.Insufficient || len(important.Data.Approval.GatingSigs) != 1 {
		t.Fatalf("report approval on the resumed bundle = %+v", important.Data.Approval)
	}
}

// runResumedGate runs the gate's own service call against the resumed bundle at
// one severity tier.
func runResumedGate(t *testing.T, req *core.RunRequest, check string) *reportcore.Response {
	t.Helper()
	response, ref := reportcore.Run(t.Context(), &reportcore.Request{
		ReadRequest: req.ReadRequest, Effective: core.Defaults(),
		Flags: reportcore.Flags{Check: check, Coverage: "warn"},
	})
	if ref != nil {
		t.Fatalf("gate at %s refused the resumed bundle: %v", check, ref)
	}
	return response
}

// gateDetail returns one dimension's detail, or "" when the gate has no such
// dimension.
func gateDetail(gate *core.ReportGate, id string) string {
	for _, dimension := range gate.Dimensions {
		if dimension.ID == id {
			return dimension.Detail
		}
	}
	return ""
}

// TestResumeReusesCompletedCrossChecks proves a cross check that already
// validated is not asked again: only the failed check launches, and the reused
// one is named in the record.
func TestResumeReusesCompletedCrossChecks(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-cross")
	interruptRun(t, req, commander, "x-cross-2")

	req.Resume = true
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	assertPhaseGroups(t, commander.launched(), []string{"x-cross-2"})
	if want := []string{"x-finder-1", "x-finder-2", "x-cross-1"}; !slices.Equal(response.Data.Resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", response.Data.Resume.Reused, want)
	}
	if want := []string{"union", "x-cross-2"}; !slices.Equal(response.Data.Resume.Executed, want) {
		t.Fatalf("executed = %v, want %v", response.Data.Resume.Executed, want)
	}
}

// TestResumeOfCompleteExecutionIsIdempotent proves --resume on a finished
// execution verifies the fingerprint and launches nothing: the bundle answers
// the same handoff without spending another agent call.
func TestResumeOfCompleteExecutionIsIdempotent(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-complete")
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("first run refused: %v", ref)
	}
	commander.reset()

	req.Resume = true
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("re-running a complete execution launched %v", launched)
	}
	resume := response.Data.Resume
	if resume.PriorStatus != "complete" || resume.Attempts != 2 {
		t.Fatalf("resume provenance = %+v", resume)
	}
	if want := []string{"x-finder-1", "x-finder-2", "x-cross-1", "x-cross-2"}; !slices.Equal(resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", resume.Reused, want)
	}
	if want := []string{"union"}; !slices.Equal(resume.Executed, want) {
		t.Fatalf("executed = %v, want %v", resume.Executed, want)
	}
}

// TestResumeRefusesDriftedInputs proves every input the fingerprint seals fails
// closed before a single agent starts: the mutated diff, the mutated run state,
// and a recorded prompts_hash that no longer matches the binary.
func TestResumeRefusesDriftedInputs(t *testing.T) {
	t.Parallel()
	cases := []resumeDriftCase{
		{
			name: "mutated full.diff", adjust: mutateFixtureDiff,
			code: codeResumeDrift, want: "full.diff changed",
		},
		{
			name: "mutated run.json", adjust: mutateFixtureRunState,
			code: codeResumeDrift, want: runStateFile + " changed",
		},
		{
			name: "changed prompts_hash", adjust: mutateFixturePromptsHash,
			code: codeResumeDrift, want: "prompts_hash changed",
		},
		{
			name: "changed recorded agent", adjust: mutateFixtureAgent,
			code: codeResumeDrift, want: "the agent differs from the interrupted run's",
		},
		{
			name: "changed controls seal", adjust: mutateFixtureConfigSeal,
			code: "controls-stale", want: "captured inputs changed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertResumeDrift(t, tc) })
	}
}

// resumeDriftCase is one mutated input and the refusal it must earn.
type resumeDriftCase struct {
	adjust func(t *testing.T, dir string)
	name   string
	code   string
	want   string
}

// assertResumeDrift proves one drifted input fails closed before any launch.
func assertResumeDrift(t *testing.T, tc resumeDriftCase) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-drift")
	interruptRun(t, req, commander, "x-finder-2")
	tc.adjust(t, commander.dir)
	req.Resume = true
	_, ref := Run(t.Context(), req)
	if ref == nil {
		t.Fatalf("%s: drifted bundle resumed", tc.name)
	}
	if ref.Code != tc.code {
		t.Fatalf("refusal code = %q, want %q (%s)", ref.Code, tc.code, ref.Message)
	}
	if !strings.Contains(ref.Message, tc.want) {
		t.Fatalf("refusal message %q does not name %q", ref.Message, tc.want)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("drifted bundle launched %v", launched)
	}
}

// mutateFixtureDiff changes the reviewed bytes after the interrupted run, the
// one input every finder read.
func mutateFixtureDiff(t *testing.T, dir string) {
	t.Helper()
	writeUnder(t, dir, "full.diff", fixtureDiff+"\n")
}

// mutateFixtureRunState changes one literal run.json field without changing the
// logical inputs it seals, so the drift check—not the captured-state gate—owns
// the refusal.
func mutateFixtureRunState(t *testing.T, dir string) {
	t.Helper()
	mutateRunStateField(t, dir, `"captured_at": ""`, `"captured_at": "2020-01-01T00:00:00Z"`)
}

// mutateFixturePromptsHash rewrites the recorded prompt digest, the shape a
// binary whose Procedure moved would leave behind.
func mutateFixturePromptsHash(t *testing.T, dir string) {
	t.Helper()
	mutateResumeField(t, dir, "prompts_hash", strings.Repeat("0", 64))
}

// mutateFixtureAgent rewrites the recorded agent name, the shape a resumed run
// with a different --agent would leave behind, so the agent row is proven to be
// part of the comparison rather than just the record.
func mutateFixtureAgent(t *testing.T, dir string) {
	t.Helper()
	mutateResumeField(t, dir, "agent", "other-agent")
}

// mutateFixtureConfigSeal rewrites the captured config hash, which no longer
// matches the inputs seal beside it.
func mutateFixtureConfigSeal(t *testing.T, dir string) {
	t.Helper()
	mutateRunStateField(t, dir, `"config_sha256": ""`, `"config_sha256": "deadbeef"`)
}

// mutateRunStateField rewrites one literal run.json field, keeping the file
// valid JSON so the refusal under test is the drift check rather than a parse
// failure.
func mutateRunStateField(t *testing.T, dir, old, replacement string) {
	t.Helper()
	path := filepath.Join(dir, runStateFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), old) {
		t.Fatalf("%s does not carry %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), old, replacement, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mutateResumeField rewrites one field inside the manifest's resume block, the
// way a binary whose embedded prompts moved would leave it.
func mutateResumeField(t *testing.T, dir, key, value string) {
	t.Helper()
	path := filepath.Join(dir, launcherFile)
	manifest := resumeManifest(t, dir)
	block := resumeBlock(t, manifest)
	block.Set(key, value)
	if err := os.WriteFile(path, []byte(jq.PrettyOrdered(manifest)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// resumeDamageCase is one way a completed finder artifact can be spoiled.
type resumeDamageCase struct {
	damage func(t *testing.T, path string)
	name   string
	want   string
}

// TestResumeRefusesDamagedBoundArtifact proves a completed finder whose bound
// artifact disappeared or changed refuses the resume instead of silently
// dropping that finder's coverage or reusing bytes nobody can vouch for.
func TestResumeRefusesDamagedBoundArtifact(t *testing.T) {
	t.Parallel()
	cases := []resumeDamageCase{
		{damage: removeBoundReview, name: "missing", want: "is missing"},
		{damage: corruptBoundReview, name: "corrupt", want: "changed since the interrupted execution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertDamagedArtifactRefusal(t, tc) })
	}
}

// assertDamagedArtifactRefusal proves one spoiled artifact fails closed instead
// of silently dropping that finder's coverage.
func assertDamagedArtifactRefusal(t *testing.T, tc resumeDamageCase) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-damaged")
	interruptRun(t, req, commander, "x-finder-2")
	tc.damage(t, filepath.Join(commander.dir, "x-finder-1", reviewFile))
	req.Resume = true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeResumeDrift {
		t.Fatalf("refusal = %v", ref)
	}
	if !strings.Contains(ref.Message, tc.want) {
		t.Fatalf("refusal message %q does not name %q", ref.Message, tc.want)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("damaged artifact launched %v", launched)
	}
}

// removeBoundReview deletes the artifact the interrupted manifest bound.
func removeBoundReview(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// corruptBoundReview replaces the artifact with bytes that no longer match the
// digest the interrupted manifest bound.
func corruptBoundReview(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResumeWithoutPriorExecutionRefuses proves --resume on a bundle nobody ran
// is a typed refusal that launches nothing, so a wrapper's retry cannot start
// an execution it did not mean to.
func TestResumeWithoutPriorExecutionRefuses(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-fresh")
	req.Resume = true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeCannotResume {
		t.Fatalf("refusal = %v", ref)
	}
	if !strings.Contains(ref.Message, "no launcher record") {
		t.Fatalf("refusal message = %q", ref.Message)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("refusal launched %v", launched)
	}
}

// TestResumeRefusesOneFinderShape proves --resume cannot be pointed at the
// single-finder launch, which has no phases to continue: the one process is the
// whole run.
func TestResumeRefusesOneFinderShape(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-single")
	req.Resume = true
	req.FindersRaw = "1"
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeCannotResume {
		t.Fatalf("refusal = %v", ref)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("one-finder resume launched %v", launched)
	}
}

// TestResumeRefusesRenderShapes proves a render cannot claim to resume: a
// --print render or one stage would leave the recorded execution half finished
// while appearing to continue it.
func TestResumeRefusesRenderShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		apply func(*core.RunRequest)
		name  string
	}{
		{apply: renderPrint, name: "print"},
		{apply: renderStage, name: "stage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertRenderResumeRefusal(t, tc.apply) })
	}
}

// assertRenderResumeRefusal proves one render shape refuses before launching.
func assertRenderResumeRefusal(t *testing.T, apply func(*core.RunRequest)) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-render")
	interruptRun(t, req, commander, "x-finder-2")
	req.Resume = true
	apply(req)
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeBadFlags {
		t.Fatalf("refusal = %v", ref)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("render resume launched %v", launched)
	}
}

// renderPrint asks for the whole pipeline's command instead of a launch.
func renderPrint(req *core.RunRequest) { req.Print = true }

// renderStage asks for one stage's command instead of a launch.
func renderStage(req *core.RunRequest) { req.StageRaw = "1" }

// readFixtureFile reads one bundle file as the bytes on disk.
func readFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// assertFileUntouched proves the file still holds the bytes it held before the
// refused invocation, so a refusal the operator can fix retried nothing away.
func assertFileUntouched(t *testing.T, path string, before []byte) {
	t.Helper()
	if after := readFixtureFile(t, path); !bytes.Equal(after, before) {
		t.Fatalf("%s changed under a refusal:\nbefore: %s\nafter:  %s", path, before, after)
	}
}

// executionIDOf reads the execution reference the bundle's run.json carries.
func executionIDOf(t *testing.T, dir string) string {
	t.Helper()
	captured, _, err := config.ReadRunState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured.Execution == nil {
		t.Fatalf("%s carries no execution reference", runStateFile)
	}
	return captured.Execution.ID
}

// assertResumeRefused runs one resume and proves it refused with the given code
// and detail, launched nothing, and left the launcher record byte-identical.
func assertResumeRefused(t *testing.T, req *core.RunRequest, commander *phaseCommander, path string,
	before []byte, code, want string,
) {
	t.Helper()
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != code {
		t.Fatalf("resume refusal = %v, want %s", ref, code)
	}
	if want != "" && !strings.Contains(ref.Message, want) {
		t.Fatalf("refusal message %q does not name %q", ref.Message, want)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("refused resume launched %v", launched)
	}
	assertFileUntouched(t, path, before)
}

// TestResumeSpendRefusalLeavesTheRecordUntouched proves a launch the spend
// guard refuses hands the interrupted execution back exactly as it was: the
// record the next attempt reads still names the same execution, attempt, bound
// artifacts, and failures, and no agent started.
func TestResumeSpendRefusalLeavesTheRecordUntouched(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-spend")
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	req.SpendPlan = []spend.Phase{{
		ExecutionID: executionIDOf(t, commander.dir), LaunchID: "finder-1-discovery-1",
		Finder: "finder-1", Phase: "discovery", USD: new(2.0),
	}}
	req.Env["REVIEW_BUDGET_USD"] = "1"
	req.Resume = true
	assertResumeRefused(t, req, commander, path, before, "budget-exceeded", "")
}

// TestResumeRefusesDamagedBoundCrossCheck proves a cross check the interrupted
// execution sealed but cannot hand over refuses before anything launches or is
// rewritten, so a second --resume meets the same intact record and refuses the
// same way instead of resuming on a half-repaired bundle.
func TestResumeRefusesDamagedBoundCrossCheck(t *testing.T) {
	t.Parallel()
	cases := []resumeDamageCase{
		{damage: removeBoundReview, name: "missing", want: "is missing"},
		{damage: corruptBoundReview, name: "corrupt", want: "changed since the interrupted execution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertDamagedCrossRefusal(t, tc) })
	}
}

// assertDamagedCrossRefusal spoils one bound cross artifact and proves both
// attempts to resume refuse the same way: the second one meets the record the
// first one left, because a refusal publishes nothing.
func assertDamagedCrossRefusal(t *testing.T, tc resumeDamageCase) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-cross-damaged")
	interruptRun(t, req, commander, "x-cross-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	tc.damage(t, filepath.Join(commander.dir, "x-cross-1", "verdicts.json"))
	req.Resume = true
	assertResumeRefused(t, req, commander, path, before, codeResumeDrift, tc.want)
	assertResumeRefused(t, req, commander, path, before, codeResumeDrift, tc.want)
}

// TestResumeContinuesAcrossInterruptedAttempts proves an execution survives two
// interruptions: the first attempt fails finder 2, the second (continuing it)
// fails cross 1, and the third reuses both finders and the completed cross
// check while re-running only what is still missing. Each refused attempt
// publishes the bindings it earned and the failures it has not resolved, which
// is what the next attempt reads.
func TestResumeContinuesAcrossInterruptedAttempts(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-chain")
	interruptRun(t, req, commander, "x-finder-2")
	assertResumeAttempt(t, commander.dir, resumeAttempt{
		attempts: 1, bound: "x-finder-1/review.json",
		failed: "x-finder-2", status: "partial",
	})
	assertChainSecondAttempt(t, req, commander)
	commander.reset()

	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("third attempt refused: %v launches=%v", ref, commander.launched())
	}
	assertChainThirdAttempt(t, response, commander)
}

// assertChainSecondAttempt continues the interrupted execution once more and
// fails a cross check, proving the attempt reused the finder the first one
// finished and left its own bindings and failure behind.
func assertChainSecondAttempt(t *testing.T, req *core.RunRequest, commander *phaseCommander) {
	t.Helper()
	req.Resume = true
	commander.failPhase = "x-cross-1"
	if _, ref := Run(t.Context(), req); ref == nil {
		t.Fatal("second attempt was expected to interrupt on x-cross-1")
	}
	commander.failPhase = ""
	assertResumeAttempt(t, commander.dir, resumeAttempt{
		attempts: 2, bound: "x-finder-2/review.json",
		failed: "x-cross-1", status: "partial",
	})
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
}

// assertChainThirdAttempt proves the third attempt finished the execution by
// launching the one check that still failed, without spending the agent again.
func assertChainThirdAttempt(t *testing.T, response *core.RunResponse, commander *phaseCommander) {
	t.Helper()
	assertPhaseGroups(t, commander.launched(), []string{"x-cross-1"})
	resume := response.Data.Resume
	if resume.Attempts != 3 || resume.PriorStatus != "partial" {
		t.Fatalf("resume provenance = %+v", resume)
	}
	if want := []string{"x-finder-1", "x-finder-2", "x-cross-2"}; !slices.Equal(resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", resume.Reused, want)
	}
	if want := []string{"union", "x-cross-1"}; !slices.Equal(resume.Executed, want) {
		t.Fatalf("executed = %v, want %v", resume.Executed, want)
	}
	manifest := resumeManifest(t, commander.dir)
	if got := jq.StrOf(readcommon.FieldOf(manifest, keyStatus)); got != "complete" {
		t.Fatalf("status = %q, want complete", got)
	}
	if failures := jq.Elements(readcommon.FieldOf(manifest, keyFailures)); len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
}

// resumeAttempt is what one interrupted attempt must leave behind.
type resumeAttempt struct {
	bound    string
	failed   string
	status   string
	attempts int
}

// assertResumeAttempt proves one interrupted attempt left a record the next
// resume can act on: it names the phase that attempt failed, and it seals the
// artifact the completed phase beside it wrote.
func assertResumeAttempt(t *testing.T, dir string, want resumeAttempt) {
	t.Helper()
	manifest := resumeManifest(t, dir)
	if got := jq.StrOf(readcommon.FieldOf(manifest, "status")); got != want.status {
		t.Fatalf("attempt %d status = %q, want %q", want.attempts, got, want.status)
	}
	block := resumeBlock(t, manifest)
	if got := int(readcommon.NumOr(readcommon.FieldOf(block, "attempts"))); got != want.attempts {
		t.Fatalf("attempts = %d, want %d", got, want.attempts)
	}
	if digest := boundDigest(manifest, want.bound); digest == "" {
		t.Fatalf("attempt %d bound no digest for %s: %s", want.attempts, want.bound, jq.Compact(manifest))
	}
	if rows := jq.Elements(readcommon.FieldOf(manifest, "failures")); len(rows) != 1 ||
		jq.StrOf(readcommon.FieldOf(rows[0], "phase")) != want.failed {
		t.Fatalf("attempt %d failures = %v, want %s", want.attempts, rows, want.failed)
	}
}

// TestResumeInProgressRunKeepsItsFinders proves a run killed between its finder
// barrier and its cross checks resumes from the record it published mid-run,
// and that a second kill keeps it: the two finished finders stay sealed and are
// never spawned again, only the cross checks launch, and the response names the
// interrupted status the operator's retry continued.
func TestResumeInProgressRunKeepsItsFinders(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-killed")
	killAttempt(t, req, commander, "x-cross-1")
	assertInProgressBindings(t, commander.dir, 1)

	req.Resume = true
	killAttempt(t, req, commander, "x-cross-2")
	assertPhaseGroups(t, commander.launched(), []string{"x-cross-1", "x-cross-2"})
	assertInProgressBindings(t, commander.dir, 2)

	commander.reset()
	commander.cancelOn = ""
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	assertPhaseGroups(t, commander.launched(), []string{"x-cross-1", "x-cross-2"})
	if response.Data.Resume.PriorStatus != statusInProgress || response.Data.Resume.Attempts != 3 {
		t.Fatalf("resume provenance = %+v", response.Data.Resume)
	}
	if want := []string{"x-finder-1", "x-finder-2"}; !slices.Equal(response.Data.Resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", response.Data.Resume.Reused, want)
	}
}

// killAttempt runs the execution once and cancels it from inside one phase's
// launch, which is the interruption a killed terminal leaves: the phase is
// abandoned mid-write and nothing after the barrier publishes.
func killAttempt(t *testing.T, req *core.RunRequest, commander *phaseCommander, phase string) {
	t.Helper()
	commander.reset()
	ctx, cancel := context.WithCancel(t.Context())
	commander.cancel, commander.cancelOn = cancel, phase
	if _, ref := Run(ctx, req); ref == nil || ref.Code != codeCanceled {
		t.Fatalf("attempt killed on %s refused with %v", phase, ref)
	}
}

// TestCanceledExecutionIsResumable proves a run canceled after it published
// its in-progress record says it can be resumed, so the re-entry line carries
// --resume and the finished finder is reused rather than relaunched.
func TestCanceledExecutionIsResumable(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-canceled")
	ctx, cancel := context.WithCancel(t.Context())
	commander.cancel, commander.cancelOn = cancel, "x-finder-2"
	response, ref := Run(ctx, req)
	if ref == nil || ref.Code != codeCanceled {
		t.Fatalf("canceled attempt refusal = %v", ref)
	}
	if response == nil || !response.Resumable {
		t.Fatalf("response = %+v, want resumable", response)
	}
}

// cancelOnWarn cancels the run at its first replayed warning: the coordinator
// replays the finders' buffered warnings after both have ended and before it
// reads the cancel, so the cancel lands after every finder finished its phase.
type cancelOnWarn struct {
	cancel context.CancelFunc
}

func (p *cancelOnWarn) Human(string) {}
func (p *cancelOnWarn) Diag(string)  {}
func (p *cancelOnWarn) Warn(string)  { p.cancel() }

// TestCanceledDiscoveryKeepsTheFinishedFinder proves a discovery canceled
// after finder-1 finished publishes finder-1's binding beside finder-2's
// failure, so the resume reuses finder-1 instead of paying for it twice and
// relaunches only finder-2.
func TestCanceledDiscoveryKeepsTheFinishedFinder(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-cancel-merge")
	commander.outputDir, commander.failPhase = true, "x-finder-2"
	ctx, cancel := context.WithCancel(t.Context())
	req.Progress = &cancelOnWarn{cancel: cancel}
	if _, ref := Run(ctx, req); ref == nil || ref.Code != codeCanceled {
		t.Fatalf("canceled attempt refusal = %v", ref)
	}
	manifest := resumeManifest(t, commander.dir)
	if boundDigest(manifest, "x-finder-1/review.json") == "" {
		t.Fatalf("canceled attempt bound no digest for the finished finder: %s", jq.Compact(manifest))
	}
	req.Progress = nil
	commander.outputDir, commander.failPhase = false, ""
	commander.reset()
	req.Resume = true
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("resume refusal = %v", ref)
	}
	if got := commander.phaseCalls("x-finder-1"); got != 0 {
		t.Fatalf("resume relaunched the finished finder %d time(s): %v", got, commander.launched())
	}
	if got := commander.phaseCalls("x-finder-2"); got != 1 {
		t.Fatalf("resume launched the failed finder %d time(s), want 1: %v", got, commander.launched())
	}
}

// assertInProgressBindings proves one killed attempt left the two finished
// finders sealed in a record that is still in progress, so the next attempt can
// reuse them instead of spending the agent again.
func assertInProgressBindings(t *testing.T, dir string, attempts int) {
	t.Helper()
	manifest := resumeManifest(t, dir)
	if got := jq.StrOf(readcommon.FieldOf(manifest, "status")); got != statusInProgress {
		t.Fatalf("killed attempt %d status = %q, want %q", attempts, got, statusInProgress)
	}
	block := resumeBlock(t, manifest)
	if got := int(readcommon.NumOr(readcommon.FieldOf(block, "attempts"))); got != attempts {
		t.Fatalf("killed attempt records attempts = %d, want %d", got, attempts)
	}
	for _, name := range []string{"x-finder-1/review.json", "x-finder-2/review.json"} {
		if boundDigest(manifest, name) == "" {
			t.Fatalf("killed attempt %d bound no digest for %s: %s", attempts, name, jq.Compact(manifest))
		}
	}
}

// TestResumeKilledMidFinderKeepsThePriorRecord proves a resumed attempt that
// dies while its re-run finder is still working hands the next attempt the
// interruption's own record: the finder the retry could not vouch for holds no
// binding to reuse, and the finder beside it is still sealed, so the third
// attempt does not pay for work the first one already finished.
func TestResumeKilledMidFinderKeepsThePriorRecord(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-killed-finder")
	interruptRun(t, req, commander, "x-finder-2")
	req.Resume = true
	ctx, cancel := context.WithCancel(t.Context())
	commander.cancel, commander.cancelOn = cancel, "x-finder-2"
	if _, ref := Run(ctx, req); ref == nil || ref.Code != codeCanceled {
		t.Fatalf("killed attempt refusal = %v", ref)
	}
	manifest := resumeManifest(t, commander.dir)
	if digest := boundDigest(manifest, "x-finder-1/review.json"); digest == "" {
		t.Fatalf("killed attempt dropped finder-1's binding: %s", jq.Compact(manifest))
	}
	if digest := boundDigest(manifest, "x-finder-2/review.json"); digest != "" {
		t.Fatalf("killed attempt kept a binding for the finder it re-ran: %s", jq.Compact(manifest))
	}
	commander.reset()
	commander.cancelOn = ""

	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	if want := []string{"x-finder-1"}; !slices.Equal(response.Data.Resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", response.Data.Resume.Reused, want)
	}
}

// TestResumeRefusesSymlinkedPhasePath proves a phase directory that is really
// a link refuses the resume before anything is read through it or written past
// it: the artifacts beside the link stay where they are, nothing starts, and
// the record the next attempt reads is the one the interrupted run left.
func TestResumeRefusesSymlinkedPhasePath(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"x-finder-1", "x-cross-2"} {
		t.Run(name, func(t *testing.T) { assertSymlinkedPhaseRefusal(t, name) })
	}
}

// assertSymlinkedPhaseRefusal swaps one phase directory for a link to a
// directory outside the bundle and proves the resume refuses it untouched.
func assertSymlinkedPhaseRefusal(t *testing.T, name string) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-link")
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	outside := t.TempDir()
	target := filepath.Join(outside, name)
	if err := os.Rename(filepath.Join(commander.dir, name), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(commander.dir, name)); err != nil {
		t.Fatal(err)
	}
	planted := readFixtureFile(t, filepath.Join(target, phaseFile(name)))

	req.Resume = true
	// The pre-launch bundle walk refuses the link before the resume plan
	// reads it: the agent's write grant covers the whole bundle.
	assertResumeRefused(t, req, commander, path, before, readcommon.CodeArtifactUnsafe, "is a symlink inside the bundle")
	if after := readFixtureFile(t, filepath.Join(target, phaseFile(name))); !bytes.Equal(after, planted) {
		t.Fatalf("resume wrote through the link into %s", target)
	}
}

// phaseFile names the artifact one phase directory holds.
func phaseFile(name string) string {
	if strings.HasPrefix(name, "x-cross-") {
		return "verdicts.json"
	}
	return reviewFile
}

// rewriteManifest edits the launcher record in place, so a test can plant the
// record shape it wants the next resume to read.
func rewriteManifest(t *testing.T, dir string, mutate func(*jq.Obj)) {
	t.Helper()
	manifest := resumeManifest(t, dir)
	mutate(manifest)
	writeUnder(t, dir, launcherFile, jq.PrettyOrdered(manifest)+"\n")
}

// rebindArtifact points the record's binding for one artifact at the bytes now
// on disk, which is how a test plants a record that sealed the wrong output.
func rebindArtifact(t *testing.T, dir, name string) {
	t.Helper()
	digest := shaHex(readFixtureFile(t, filepath.Join(dir, name)))
	rewriteManifest(t, dir, func(manifest *jq.Obj) {
		artifacts, ok := readcommon.FieldOf(manifest, "artifacts").(*jq.Obj)
		if !ok {
			t.Fatalf("manifest binds no artifacts: %s", jq.Compact(manifest))
		}
		artifacts.Set(portableKey(name), digest)
	})
}

// TestResumeRefusesRecoveredContextDrift proves the handed context is sealed:
// adding a context.txt after the interruption, or dropping the one the bundle
// carried, refuses instead of reviewing a spec the first attempt never read.
func TestResumeRefusesRecoveredContextDrift(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prepare func(t *testing.T, dir string)
		adjust  func(t *testing.T, dir string)
		name    string
	}{
		{
			name: "added",
			prepare: func(t *testing.T, _ string) {
				t.Helper()
			},
			adjust: func(t *testing.T, dir string) {
				t.Helper()
				writeUnder(t, dir, "context.txt", "a spec the interrupted run never read")
			},
		},
		{
			name: "removed",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				writeUnder(t, dir, "context.txt", "a spec the interrupted run read")
			},
			adjust: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "context.txt")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, commander := resumeFixture(t, "local-resume-context")
			tc.prepare(t, commander.dir)
			interruptRun(t, req, commander, "x-finder-2")
			path := filepath.Join(commander.dir, launcherFile)
			before := readFixtureFile(t, path)
			tc.adjust(t, commander.dir)
			req.Resume = true
			assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "context.txt changed")
		})
	}
}

// TestResumeRefusesChangedLaunchDefinition proves the resolved launch
// definition is sealed: the same agent name pointed at another argument shape
// is a different reviewer, so the resume refuses before spending it.
func TestResumeRefusesChangedLaunchDefinition(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-launch-def")
	req.Agent = "local"
	defs := []config.PersonalAgent{{
		Name: "local", Binary: "local-agent", Args: []string{"exec"}, PromptFlag: "-t",
	}}
	interruptWith(t, req, commander, "x-finder-2", defs)
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	changed := []config.PersonalAgent{{
		Name: "local", Binary: "local-agent", Args: []string{"run"}, PromptFlag: "-t",
	}}
	req.Resume = true
	_, ref := WithAgents(t.Context(), req, changed)
	if ref == nil || ref.Code != codeResumeDrift {
		t.Fatalf("refusal = %v", ref)
	}
	if !strings.Contains(ref.Message, "launch definition") {
		t.Fatalf("refusal message = %q", ref.Message)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("changed definition launched %v", launched)
	}
	assertFileUntouched(t, path, before)
}

// TestLaunchDefDigestTracksEveryField proves the digest covers the binary, its
// argv prefix, its probe argv, the row's documented discovery restriction and
// review flags, and the restriction, review flags, and write grant one attempt
// actually applied: a row or an attempt that differs in any of them is a
// different launch definition.
func TestLaunchDefDigestTracksEveryField(t *testing.T) {
	t.Parallel()
	base := agentcatalog.Launch{
		Binary: "agent", Args: []string{"exec"}, ProbeArgs: []string{"--version"},
		DiscoveryArgs: []string{"--exclude-tools", "bash"}, ReviewArgs: []string{"--setting-sources=user"},
	}
	restriction := []string{"--exclude-tools", "bash"}
	review := []string{"--setting-sources=user"}
	grant := []string{"--allowedTools=Edit(./.verdict/state/42/**)"}
	digest := launchDefDigest(&base, restriction, review, grant)
	if digest == "" || digest != launchDefDigest(&base, restriction, review, grant) {
		t.Fatalf("digest is not stable: %q", digest)
	}
	assertRowChangesMove(t, &base, digest, restriction, review, grant)
	for what, dropped := range map[string]string{
		"discovery restriction": launchDefDigest(&base, nil, review, grant),
		"review flags":          launchDefDigest(&base, restriction, nil, grant),
		"review write grant":    launchDefDigest(&base, restriction, review, nil),
	} {
		if dropped == digest {
			t.Errorf("digest ignored an attempt that dropped the %s", what)
		}
	}
}

// assertRowChangesMove fails unless each change to the row moves the digest.
func assertRowChangesMove(t *testing.T, base *agentcatalog.Launch, digest string, restriction, review, grant []string) {
	t.Helper()
	for _, change := range []func(*agentcatalog.Launch){
		func(def *agentcatalog.Launch) { def.Binary = "other-agent" },
		func(def *agentcatalog.Launch) { def.Args = []string{"run"} },
		func(def *agentcatalog.Launch) { def.ProbeArgs = nil },
		func(def *agentcatalog.Launch) { def.DiscoveryArgs = []string{"--exclude-tools", "bash,powershell"} },
		func(def *agentcatalog.Launch) { def.ReviewArgs = nil },
	} {
		changed := *base
		change(&changed)
		if launchDefDigest(&changed, restriction, review, grant) == digest {
			t.Errorf("digest ignored a launch definition change: %+v", changed)
		}
	}
}

// TestResumeDetectsReviewFlagsDrift proves the review flags are sealed: a
// resume of an execution whose attempts carried no review flags — recorded
// under the digest such an attempt seals — refuses before any agent starts,
// rather than spending the remaining phases with configuration the record
// never sealed.
func TestResumeDetectsReviewFlagsDrift(t *testing.T) {
	t.Parallel()
	const slug = "local-resume-review-flags"
	req, commander := resumeFixture(t, slug)
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	def, _ := lookupAgent(nameClaude)
	restriction := discoveryRestriction(&def, false)
	grant := []string{"--allowedTools=Edit(./.verdict/state/" + slug + "/**)"}
	sealed := launchDefDigest(&def, restriction, reviewArgs(&def), grant)
	raw := readFixtureFile(t, path)
	if !strings.Contains(string(raw), sealed) {
		t.Fatalf("launcher.json does not carry the digest this test computes (%s)", sealed)
	}
	bare := def
	bare.ReviewArgs = nil
	writeUnder(t, commander.dir, launcherFile,
		strings.ReplaceAll(string(raw), sealed, launchDefDigest(&bare, restriction, nil, grant)))
	before := readFixtureFile(t, path)
	req.Resume = true
	assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "launch definition")
}

// TestResumeDetectsDiscoveryRestrictionDrift proves the read-only discovery
// shape is sealed: continuing an interrupted execution with --allow-shell, so
// the remaining phases would spend a tool set the record never sealed, fails
// closed before any agent starts.
func TestResumeDetectsDiscoveryRestrictionDrift(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-discovery")
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	req.AllowShell = true
	req.Resume = true
	assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "launch definition")
}

// interruptWith runs one attempt through WithAgents, so a test can interrupt a
// launch whose agent row came from the prefs layer.
func interruptWith(t *testing.T, req *core.RunRequest, commander *phaseCommander, phase string,
	defs []config.PersonalAgent,
) {
	t.Helper()
	commander.failPhase = phase
	if _, ref := WithAgents(t.Context(), req, defs); ref == nil {
		t.Fatalf("%s was expected to interrupt the run", phase)
	}
	commander.failPhase = ""
	commander.reset()
}

// TestResumeRefusesInvalidRecordedAttempts proves a record whose attempt count
// no execution could have written (zero, negative, fractional, or non-numeric)
// refuses rather than being truncated or incremented.
func TestResumeRefusesInvalidRecordedAttempts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value any
		name  string
	}{
		{name: "zero", value: 0},
		{name: "negative", value: -2},
		{name: "fractional", value: 1.5},
		{name: "string", value: "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertInvalidAttempts(t, tc.value)
		})
	}
}

// assertInvalidAttempts plants one impossible attempt count and proves the
// resume refuses it before launching anything.
func assertInvalidAttempts(t *testing.T, attempts any) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-attempts")
	interruptRun(t, req, commander, "x-finder-2")
	rewriteManifest(t, commander.dir, func(manifest *jq.Obj) {
		resumeBlock(t, manifest).Set("attempts", attempts)
	})
	req.Resume = true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeCannotResume || !strings.Contains(ref.Message, "at least one") {
		t.Fatalf("attempts %v refusal = %v", attempts, ref)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("attempts %v launched %v", attempts, launched)
	}
}

// TestResumeRefusesOversizedReads proves both reads are capped: a record grown
// past the record cap refuses before it is parsed, and a sealed artifact past
// the artifact cap refuses instead of being loaded whole.
func TestResumeRefusesOversizedReads(t *testing.T) {
	t.Parallel()
	t.Run("record", func(t *testing.T) {
		req, commander := resumeFixture(t, "local-resume-big-record")
		interruptRun(t, req, commander, "x-finder-2")
		record := readFixtureFile(t, filepath.Join(commander.dir, launcherFile))
		writeUnder(t, commander.dir, launcherFile, string(record)+strings.Repeat(" ", int(maxResumeRecordBytes)))
		req.Resume = true
		_, ref := Run(t.Context(), req)
		if ref == nil || ref.Code != codeCannotResume {
			t.Fatalf("refusal = %v", ref)
		}
		if !strings.Contains(ref.Message, "record cap") {
			t.Fatalf("refusal message = %q", ref.Message)
		}
		if launched := commander.launched(); len(launched) != 0 {
			t.Fatalf("oversized record launched %v", launched)
		}
	})
	t.Run("artifact", func(t *testing.T) {
		req, commander := resumeFixture(t, "local-resume-big-artifact")
		interruptRun(t, req, commander, "x-finder-2")
		writeUnder(t, commander.dir, filepath.Join("x-finder-1", reviewFile),
			strings.Repeat("x", int(resumeArtifactCap)+1))
		rebindArtifact(t, commander.dir, filepath.Join("x-finder-1", reviewFile))
		path := filepath.Join(commander.dir, launcherFile)
		before := readFixtureFile(t, path)
		req.Resume = true
		assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "read cap")
	})
}

// TestResumeComparesRecordedBudgetByValue proves the recorded review depth is
// compared as a decision, not as JSON text: reordered keys are the same depth,
// and a different tier is a different one.
func TestResumeComparesRecordedBudgetByValue(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-budget")
	req.BudgetRaw = "standard"
	interruptRun(t, req, commander, "x-finder-2")
	rewriteManifest(t, commander.dir, func(manifest *jq.Obj) {
		recorded, ok := readcommon.FieldOf(manifest, "budget").(*jq.Obj)
		if !ok {
			t.Fatalf("manifest records no budget: %s", jq.Compact(manifest))
		}
		reordered := jq.NewObj()
		for _, key := range slices.Backward(recorded.Keys) {
			value, _ := recorded.Get(key)
			reordered.Set(key, value)
		}
		manifest.Set("budget", reordered)
	})
	req.Resume = true
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("reordered budget refused: %v", ref)
	}

	req, commander = resumeFixture(t, "local-resume-budget-changed")
	req.BudgetRaw = "standard"
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	req.BudgetRaw, req.Resume = "deep", true
	assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "review budget differs")
}

// TestResumeKeepsSealedCrossCheckAcrossKills proves a sealed cross check
// survives the discovery barrier of a later attempt and a kill during the cross
// barrier: the record keeps publishing its binding, so the next attempt reuses
// it instead of spending that check again.
func TestResumeKeepsSealedCrossCheckAcrossKills(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-cross-sealed")
	interruptRun(t, req, commander, "x-cross-2")
	if boundDigest(resumeManifest(t, commander.dir), "x-cross-1/verdicts.json") == "" {
		t.Fatal("interrupted attempt bound no digest for x-cross-1")
	}
	req.Resume = true
	killAttempt(t, req, commander, "x-cross-2")
	if boundDigest(resumeManifest(t, commander.dir), "x-cross-1/verdicts.json") == "" {
		t.Fatalf("killed attempt dropped the sealed cross check: %s",
			jq.Compact(resumeManifest(t, commander.dir)))
	}
	commander.reset()
	commander.cancelOn = ""
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	assertPhaseGroups(t, commander.launched(), []string{"x-cross-2"})
	if want := []string{"x-finder-1", "x-finder-2", "x-cross-1"}; !slices.Equal(response.Data.Resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", response.Data.Resume.Reused, want)
	}
}

// TestResumeSkipsSpendWhenNothingLaunches proves the spend guard follows the
// plan: a finished execution resumes under a budget that could not pay for a
// launch, while a resume that still has a phase to run refuses and leaves the
// record alone.
func TestResumeSkipsSpendWhenNothingLaunches(t *testing.T) {
	t.Parallel()
	t.Run("complete", assertCompleteResumeSkipsSpend)
	t.Run("partial", assertPartialResumeAsksSpend)
}

// assertCompleteResumeSkipsSpend resumes a finished execution under a budget
// that could not pay for a launch, and proves it still answers.
func assertCompleteResumeSkipsSpend(t *testing.T) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-spend-complete")
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("initial run refused: %v", ref)
	}
	commander.reset()
	tightBudget(t, req, commander.dir)
	req.Resume = true
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("completed resume refused: %v", ref)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("completed resume launched %v", launched)
	}
	if want := []string{"union"}; !slices.Equal(response.Data.Resume.Executed, want) {
		t.Fatalf("executed = %v, want %v", response.Data.Resume.Executed, want)
	}
}

// assertPartialResumeAsksSpend proves a resume that still has a phase to run
// asks the guard first and leaves the record alone when it refuses.
func assertPartialResumeAsksSpend(t *testing.T) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-spend-partial")
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	tightBudget(t, req, commander.dir)
	req.Resume = true
	assertResumeRefused(t, req, commander, path, before, "budget-exceeded", "")
}

// tightBudget makes the invocation unaffordable for any launch: one discovery
// round priced above a one-dollar cap.
func tightBudget(t *testing.T, req *core.RunRequest, dir string) {
	t.Helper()
	req.SpendPlan = []spend.Phase{{
		ExecutionID: executionIDOf(t, dir), LaunchID: "finder-1-discovery-1",
		Finder: "finder-1", Phase: "discovery", USD: new(2.0),
	}}
	req.Env["REVIEW_BUDGET_USD"] = "1"
}

// TestResumeValidatesCrossRowsBeforePublishing proves a sealed cross check
// whose rows no longer fit the batch refuses during the read-only plan: no
// agent starts, and neither the record nor the plan the run would publish is
// written.
func TestResumeValidatesCrossRowsBeforePublishing(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-cross-rows")
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("initial run refused: %v", ref)
	}
	commander.reset()
	writeUnder(t, commander.dir, filepath.Join("x-cross-1", "verdicts.json"), `{"results":[]}`)
	rebindArtifact(t, commander.dir, filepath.Join("x-cross-1", "verdicts.json"))
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	planPath := filepath.Join(commander.dir, verificationDir, "plan.json")
	planBefore := readFixtureFile(t, planPath)
	req.Resume = true
	assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "cross-check validation")
	assertFileUntouched(t, planPath, planBefore)
}

// TestResumeSealedBytesRejectsChangedArtifact proves the plan's seal is checked
// again before a phase is consumed: bytes that differ from the ones the plan
// verified refuse instead of being read a second time.
func TestResumeSealedBytesRejectsChangedArtifact(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-seal")
	interruptRun(t, req, commander, "x-finder-2")
	artifact := filepath.Join("x-finder-1", reviewFile)
	sealed := readFixtureFile(t, filepath.Join(commander.dir, artifact))
	state := &state{req: req}
	c := &coordinator{st: state, dir: commander.dir}
	if ref := c.sealedBytes(sealed, artifact); ref != nil {
		t.Fatalf("sealed bytes refused: %v", ref)
	}
	writeUnder(t, commander.dir, artifact, strings.Replace(string(sealed), "Changed value", "Changed codepath", 1))
	ref := c.sealedBytes(sealed, artifact)
	if ref == nil || ref.Code != codeResumeDrift || !strings.Contains(ref.Message, "after the resume verified it") {
		t.Fatalf("changed bytes refusal = %v", ref)
	}
}

// TestSnapshotArtifactKeysArePortable proves the launcher record binds bundle
// paths the way every reader spells them: with forward slashes, whatever the
// platform's separators are, so a Windows bundle's lookup finds its artifact.
func TestSnapshotArtifactKeysArePortable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUnder(t, dir, filepath.Join("x-finder-1", reviewFile), "{}")
	snapshot := executionSnapshot{}
	if ref := snapshot.bindPhase(dir, "x-finder-1"); ref != nil {
		t.Fatal(ref)
	}
	if snapshot["x-finder-1/"+reviewFile] == "" {
		t.Fatalf("bound keys are not portable: %v", snapshot)
	}
	manifest := jq.NewObj("artifacts", jq.NewObj("x-finder-1/"+reviewFile, "abc"))
	if got := boundDigest(manifest, filepath.Join("x-finder-1", reviewFile)); got != "abc" {
		t.Fatalf("portable lookup = %q, want abc", got)
	}
	legacy := jq.NewObj("artifacts", jq.NewObj(`x-finder-1\`+reviewFile, "def"))
	if got := boundDigest(legacy, filepath.Join("x-finder-1", reviewFile)); got != "def" {
		t.Fatalf("legacy backslash lookup = %q, want def", got)
	}
}

// TestResumeRelaunchedFinderDoesNotReuseStaleReview proves an unsealed
// review.json left behind by an earlier attempt is cleared before the finder is
// relaunched: if the relaunched process exits without writing, the old finding
// must never enter the union.
func TestResumeRelaunchedFinderDoesNotReuseStaleReview(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-stale-finder")
	interruptRun(t, req, commander, "x-finder-2")
	stalePath := filepath.Join("x-finder-2", reviewFile)
	writeUnder(t, commander.dir, stalePath,
		`{"schema_version":"1","findings":[{"path":"a.go","line":2,"severity":"high","claim":"STALE-OLD-FINDING","quote":"value = 2","confidence":0.9}],"receipts":[{"path":"a.go","line":2,"quote":"value = 2"}]}`)
	rewriteManifest(t, commander.dir, func(manifest *jq.Obj) {
		if artifacts, ok := readcommon.FieldOf(manifest, "artifacts").(*jq.Obj); ok {
			artifacts.Delete("x-finder-2/" + reviewFile)
		}
	})
	commander.failPhase = "x-finder-2"
	req.Resume = true
	if _, ref := Run(t.Context(), req); ref == nil {
		t.Fatal("failed relaunched finder unexpectedly succeeded")
	}
	aggregate := string(readFixtureFile(t, filepath.Join(commander.dir, reviewFile)))
	if strings.Contains(aggregate, "STALE-OLD-FINDING") {
		t.Fatalf("relaunched failed finder contributed stale review to union: %s", aggregate)
	}
}

// TestResumeSeedSealedRejectsChangedCrossVerdicts proves seedSealed checks
// each reused cross check against the bytes planResume sealed before binding.
func TestResumeSeedSealedRejectsChangedCrossVerdicts(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-seed-sealed")
	interruptRun(t, req, commander, "x-cross-2")
	artifact := filepath.Join("x-cross-1", "verdicts.json")
	sealed := readFixtureFile(t, filepath.Join(commander.dir, artifact))
	c := &coordinator{
		st:       &state{req: req},
		dir:      commander.dir,
		snapshot: executionSnapshot{},
		resume: &resumePlan{
			reuseCross: []bool{true, false},
			crossBytes: [][]byte{sealed, nil},
		},
	}
	writeUnder(t, commander.dir, artifact, `{"results":[]}`)
	ref := c.seedSealed()
	if ref == nil || ref.Code != codeResumeDrift || !strings.Contains(ref.Message, "after the resume verified it") {
		t.Fatalf("seedSealed changed cross refusal = %v", ref)
	}
	if got := c.snapshot["x-cross-1/verdicts.json"]; got != "" {
		t.Fatalf("seedSealed bound mutated cross artifact: %q", got)
	}
}

// TestResumeDetectsThreadsFeedDrift proves threads.json is sealed in the resume
// fingerprint: unchanged feeds reuse finders, while adding, editing, or
// removing threads.json refuses resume-drift before launching.
func TestResumeDetectsThreadsFeedDrift(t *testing.T) {
	t.Parallel()
	t.Run("added", func(t *testing.T) {
		req, commander := resumeFixture(t, "local-resume-threads-added")
		interruptRun(t, req, commander, "x-finder-2")
		writeUnder(t, commander.dir, "threads.json", `{"schema_version":"1","threads":[]}`)
		path := filepath.Join(commander.dir, launcherFile)
		before := readFixtureFile(t, path)
		req.Resume = true
		assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "threads.json")
	})
	t.Run("changed", func(t *testing.T) {
		req, commander := resumeFixture(t, "local-resume-threads-changed")
		writeUnder(t, commander.dir, "threads.json", `{"schema_version":"1","threads":[]}`)
		interruptRun(t, req, commander, "x-finder-2")
		writeUnder(t, commander.dir, "threads.json", `{"schema_version":"1","threads":[{"id":"t1"}]}`)
		path := filepath.Join(commander.dir, launcherFile)
		before := readFixtureFile(t, path)
		req.Resume = true
		assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "threads.json")
	})
	t.Run("unchanged", func(t *testing.T) {
		req, commander := resumeFixture(t, "local-resume-threads-same")
		writeUnder(t, commander.dir, "threads.json", `{"schema_version":"1","threads":[]}`)
		interruptRun(t, req, commander, "x-finder-2")
		req.Resume = true
		response, ref := Run(t.Context(), req)
		if ref != nil {
			t.Fatalf("unchanged threads.json refused: %v", ref)
		}
		if want := []string{"x-finder-1"}; !slices.Equal(response.Data.Resume.Reused, want) {
			t.Fatalf("reused = %v, want %v", response.Data.Resume.Reused, want)
		}
	})
}

// TestResumeDetectsPassThroughDrift proves pass-through arguments after `--`
// are sealed by digest in the resume fingerprint without writing raw values to
// launcher.json.
func TestResumeDetectsPassThroughDrift(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-passthrough")
	req.PassThrough = []string{"--model", "alpha-secret-token"}
	interruptRun(t, req, commander, "x-finder-2")

	recordPath := filepath.Join(commander.dir, launcherFile)
	recordText := string(readFixtureFile(t, recordPath))
	if strings.Contains(recordText, "alpha-secret-token") {
		t.Fatalf("launcher.json leaked raw pass-through argument: %s", recordText)
	}

	before := readFixtureFile(t, recordPath)
	req.PassThrough = []string{"--model", "beta-other"}
	req.Resume = true
	assertResumeRefused(t, req, commander, recordPath, before, codeResumeDrift, "pass-through")

	req.PassThrough = []string{"--model", "alpha-secret-token"}
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("identical pass-through refused: %v", ref)
	}
	if want := []string{"x-finder-1"}; !slices.Equal(response.Data.Resume.Reused, want) {
		t.Fatalf("reused = %v, want %v", response.Data.Resume.Reused, want)
	}
}
