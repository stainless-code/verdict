package run

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// coveredReviewDoc is repairReviewDoc with the diff-coverage attestation
// record --require-coverage accepts: the one changed file, whole.
const coveredReviewDoc = `{"schema_version":"1","summary":"> ✅ no findings Reviewed the diff.","findings":[],` +
	`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new"}],"coverage":[{"file":"notes.txt","ranges":"all"}]}`

// msgNoCoverage is record's own coverage-incomplete message for a review with
// no attestation, which run must end with verbatim.
const msgNoCoverage = "coverage-incomplete: no coverage[] attestation in review.json on a non-chunked bundle"

// coverageRequest is a repair request that requires coverage.
func coverageRequest(root string, cmdr *sequenced, rounds string) *core.RunRequest {
	req := repairRequest(root, cmdr, rounds, &collector{})
	req.RequireCoverage = true
	return req
}

// TestRequireCoverageRepairsAMissingAttestation proves --require-coverage
// turns record's coverage refusal into a repair item: round 1 omits
// coverage[], the round-2 prompt names coverage-incomplete, and round 2's
// attested review succeeds. The round-1 directive states the requirement.
func TestRequireCoverageRepairsAMissingAttestation(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	cmdr := perCall(t, root, repairReviewDoc, coveredReviewDoc)
	resp, ref := Run(t.Context(), coverageRequest(root, cmdr, "2"))
	requireRun(t, ref)
	assertSpawns(t, cmdr, 2)
	assertRepairProfile(t, resp, 2, 2, []bool{false, true})
	problems := resp.Data.Repair.Attempts[0].Problems
	if len(problems) != 1 || problems[0].Code != core.ProblemCodeCoverageIncomplete || problems[0].Path != "$.coverage" ||
		problems[0].Message != msgNoCoverage {
		t.Errorf("round-1 problems = %+v, want the one coverage-incomplete item carrying record's message", problems)
	}
	if !strings.Contains(lastPrompt(cmdr, 1), "\nThis run requires the coverage attestation the findings contract describes") {
		t.Errorf("round-1 directive does not state the requirement:\n%s", lastPrompt(cmdr, 1))
	}
	if !strings.Contains(lastPrompt(cmdr, 2), `"code":"coverage-incomplete"`) {
		t.Errorf("round-2 prompt does not name the coverage gap:\n%s", lastPrompt(cmdr, 2))
	}
}

// TestWithoutRequireCoverageAnUnattestedReviewPasses pins the default: the
// same unattested review succeeds in one round and the directive says nothing
// about coverage.
func TestWithoutRequireCoverageAnUnattestedReviewPasses(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	cmdr := perCall(t, root, repairReviewDoc)
	_, ref := Run(t.Context(), repairRequest(root, cmdr, "2", &collector{}))
	requireRun(t, ref)
	assertSpawns(t, cmdr, 1)
	if strings.Contains(lastPrompt(cmdr, 1), "This run requires the coverage attestation") {
		t.Error("the directive states a coverage requirement nobody asked for")
	}
}

// TestRequireCoverageRefusesLikeRecord proves a review that never attests
// ends the way record --require-coverage would: coverage-incomplete with
// record's message, after the rounds (no progress) and in a one-shot alike.
func TestRequireCoverageRefusesLikeRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		rounds string
		want   string
		docs   []string
	}{
		{rounds: "1", docs: []string{repairReviewDoc}, want: msgNoCoverage},
		{
			rounds: "2", docs: []string{repairReviewDoc, repairReviewDoc},
			want: "repair stopped after round 2/2: review.json is byte-identical to round 1 (no progress): " + msgNoCoverage,
		},
	} {
		t.Run("rounds "+tc.rounds, func(t *testing.T) {
			t.Parallel()
			root := repairFixture(t)
			cmdr := perCall(t, root, tc.docs...)
			_, ref := Run(t.Context(), coverageRequest(root, cmdr, tc.rounds))
			if ref == nil || ref.Code != "coverage-incomplete" || ref.Message != tc.want {
				t.Fatalf("ref = %v, want coverage-incomplete: %s", ref, tc.want)
			}
		})
	}
}

// reviewGrantFlag is the claude row's review-write grant for repairFixture's
// bundle, spelled from the repository root the agent starts in.
const reviewGrantFlag = "--allowedTools=Edit(./.verdict/state/42/**)"

// TestRepairRoundsCarryTheScopedReviewGrant proves every claude review round
// carries the write grant for its own bundle, once, beside the shell
// restriction, and ahead of the directive.
func TestRepairRoundsCarryTheScopedReviewGrant(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	cmdr := perCall(t, root, invalidDoc, validDoc)
	_, ref := Run(t.Context(), repairRequest(root, cmdr, "2", &collector{}))
	requireRun(t, ref)
	assertSpawns(t, cmdr, 2)
	for _, argv := range cmdr.argv {
		if want := []string{"claude", "-p", discoveryFlag, reviewGrantFlag}; !slices.Equal(argv[:4], want) {
			t.Errorf("round argv = %q, want the prefix %q", argv[:len(argv)-1], want)
		}
	}
}

// TestFinderAndCrossLaunchesGrantOnlyTheirOwnDirectory proves each of a
// two-finder run's four launches carries one grant, scoped to that launch's
// own directory under the bundle: no launch can write another's artifact.
func TestFinderAndCrossLaunchesGrantOnlyTheirOwnDirectory(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, argv := range spawnedArgvs(t, "", false) {
		grants := grantsOf(argv)
		if len(grants) != 1 || !strings.HasPrefix(grants[0], "--allowedTools=Edit(./.verdict/state/42/") ||
			!strings.HasSuffix(grants[0], "/**)") {
			t.Fatalf("argv = %q, want one grant under the bundle", argv[:len(argv)-1])
		}
		seen[grants[0]] = true
	}
	if len(seen) != 4 {
		t.Errorf("grants = %v, want four distinct launch directories", seen)
	}
}

// TestOperatorToolFlagDropsTheReviewGrant proves the operator's own tool
// permission flag after `--` replaces verdict's grant as it replaces the
// restriction: the only --allowedTools in the argv is the operator's.
func TestOperatorToolFlagDropsTheReviewGrant(t *testing.T) {
	t.Parallel()
	for _, argv := range spawnedArgvs(t, "Read,Write", false) {
		if grants := grantsOf(argv); len(grants) != 0 {
			t.Errorf("argv = %q still carries verdict's grant %q", argv[:len(argv)-1], grants)
		}
	}
}

// grantsOf is every joined --allowedTools argument before the directive.
func grantsOf(argv []string) []string {
	var grants []string
	for _, arg := range argv[:len(argv)-1] {
		if strings.HasPrefix(arg, "--allowedTools=") {
			grants = append(grants, arg)
		}
	}
	return grants
}

// TestRulePath pins the rule spelling and every way a grant could widen:
// relative inside the agent's working directory, `//` absolute outside it,
// and no grant for the state root itself, a directory that holds the working
// directory, a symlink that resolves outside the state root, a filesystem
// root, a missing directory, or a path the rule syntax would read as syntax.
func TestRulePath(t *testing.T) {
	t.Parallel()
	root := rulePathFixture(t)
	repo := filepath.Join(root, "repo")
	state := filepath.Join(repo, ".verdict", "state")
	bundle := filepath.Join(state, "42")
	checkout := filepath.Join(root, "checkout")
	for _, tc := range []struct {
		name, cwd, dir, want, why string
	}{
		{name: "inside cwd", cwd: repo, dir: bundle, want: "./.verdict/state/42"},
		{name: "dot-dot cleans to the bundle", cwd: repo, dir: filepath.Join(bundle, "..", "42"), want: "./.verdict/state/42"},
		{name: "the state root", cwd: repo, dir: state, why: "its directory resolves outside .verdict/state"},
		{name: "an ancestor", cwd: repo, dir: repo, why: "its directory resolves outside .verdict/state"},
		{name: "holds cwd", cwd: filepath.Join(bundle, "inner"), dir: bundle, why: "its directory holds the agent's working directory"},
		{name: "filesystem root", cwd: repo, dir: string(filepath.Separator), why: "its directory resolves outside .verdict/state"},
		{name: "missing", cwd: repo, dir: filepath.Join(state, "gone"), why: "its directory cannot be resolved"},
		{name: "space", cwd: repo, dir: filepath.Join(state, "a b"), why: rulePathSyntax},
		{name: "glob", cwd: repo, dir: filepath.Join(state, "x[1]"), why: rulePathSyntax},
		{name: "comma", cwd: repo, dir: filepath.Join(state, "a,b"), why: rulePathSyntax},
	} {
		if got, why := rulePath(tc.cwd, state, tc.dir); got != tc.want || why != tc.why {
			t.Errorf("%s: rulePath = %q, %q; want %q, %q", tc.name, got, why, tc.want, tc.why)
		}
	}
	assertOutsideCwdRulePath(t, checkout, state, bundle)
	assertSymlinkRulePath(t, root, repo, state)
	assertAbsoluteRulePaths(t)
}

// rulePathFixture is a resolved temp root holding repo/.verdict/state with a
// bundle (and a directory inside it), three bundles named with rule syntax,
// another checkout, and a directory outside the repository.
func rulePathFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "repo", ".verdict", "state")
	for _, dir := range []string{
		filepath.Join(state, "42", "inner"), filepath.Join(state, "a b"), filepath.Join(state, "x[1]"),
		filepath.Join(state, "a,b"), filepath.Join(root, "checkout"), filepath.Join(root, "outside"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// assertOutsideCwdRulePath pins the `//` grant a launch from another checkout
// gets for the bundle.
func assertOutsideCwdRulePath(t *testing.T, checkout, state, bundle string) {
	t.Helper()
	got, why := rulePath(checkout, state, bundle)
	if why != "" || !strings.HasPrefix(got, "//") || !strings.HasSuffix(got, "/repo/.verdict/state/42") {
		t.Errorf("outside cwd: rulePath = %q, %q; want the bundle's absolute // path", got, why)
	}
}

// assertSymlinkRulePath proves a symlink under the state root that resolves
// outside it gets no grant. A platform that cannot create the link has
// nothing to grant through it.
func assertSymlinkRulePath(t *testing.T, root, repo, state string) {
	t.Helper()
	link := filepath.Join(state, "link")
	if err := os.Symlink(filepath.Join(root, "outside"), link); err != nil {
		return
	}
	if got, why := rulePath(repo, state, link); got != "" || why != "its directory resolves outside .verdict/state" {
		t.Errorf("symlink: rulePath = %q, %q; want refused as outside the state root", got, why)
	}
}

// rulePathSyntax is the reason a path with a rule metacharacter gets.
const rulePathSyntax = "its path holds a character the permission-rule syntax reads as syntax"

// assertAbsoluteRulePaths pins the `//` spelling: a POSIX path keeps its
// slashes, a drive letter takes the /c form, and a root or UNC share has none.
func assertAbsoluteRulePaths(t *testing.T) {
	t.Helper()
	cases := map[string]string{"/": "", "/repo/x": "//repo/x"}
	if runtime.GOOS == "windows" {
		cases = map[string]string{`C:\`: "", `C:\repo\x`: "//c/repo/x", `\\server\share\x`: "", `\\server\share`: ""}
	}
	for in, want := range cases {
		if got := absoluteRulePath(in); got != want {
			t.Errorf("absoluteRulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRequireCoverageKeepsTheEvidenceRefusal proves the coverage item never
// changes which refusal another gap earns: a review missing both a hunk
// receipt and coverage still ends review-insufficient, as without the flag.
func TestRequireCoverageKeepsTheEvidenceRefusal(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	cmdr := perCall(t, root, unreceiptedDoc, unreceiptedDoc)
	_, ref := Run(t.Context(), coverageRequest(root, cmdr, "2"))
	if ref == nil || ref.Code != core.CodeReviewInsufficient {
		t.Fatalf("ref = %v, want review-insufficient", ref)
	}
}

// coverageContract is the findings contract's attestation line, the shape
// record and gate check.
const coverageContract = "Attest what you read — the default `gate` needs it: top-level `coverage[]` holds " +
	"`{\"file\":\"<path>\",\"ranges\":\"all\"}` per changed file, `[]` if none."

// TestFinderProcedureAsksForTheAttestation proves every finder is asked for
// the coverage attestation the default gate requires, and a chunked bundle's
// finder for its chunks[] attestation too.
func TestFinderProcedureAsksForTheAttestation(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	plain, ref := finderProcedure(dir, filepath.Join(dir, "x-finder-1"), 0)
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(string(plain), coverageContract) {
		t.Errorf("finder procedure never asks for coverage[]:\n%s", plain)
	}
	writeUnder(t, dir, filepath.Join("chunks", "manifest.json"), `{"chunks":[]}`)
	chunked, ref := finderProcedure(dir, filepath.Join(dir, "x-finder-1"), 0)
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(string(chunked), "Attest the chunks you read in top-level `chunks[]`, naming every manifest chunk") {
		t.Errorf("chunked finder procedure never asks for chunks[]:\n%s", chunked)
	}
}

// TestTwoFindersRequireCoverageRefusesAnUnattestedUnion proves the default
// topology enforces --require-coverage on the union: neither finder attests,
// so the run refuses coverage-incomplete — never review-insufficient — before
// any cross check spends.
func TestTwoFindersRequireCoverageRefusesAnUnattestedUnion(t *testing.T) {
	t.Parallel()
	req, phases := coordinatorFixture(t)
	req.FindersRaw, req.RequireCoverage = "2", true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "coverage-incomplete" {
		t.Fatalf("ref = %v, want coverage-incomplete", ref)
	}
	if n := phases.phaseCalls("x-cross-1") + phases.phaseCalls("x-cross-2"); n != 0 {
		t.Errorf("cross checks launched %d time(s), want none after the union failed coverage", n)
	}
}

// TestTwoFindersRequireCoverageAcceptsTheUnion proves one finder's attestation
// carries the union: the finder that attests nothing is not a failed phase,
// so the execution completes rather than reading partial.
func TestTwoFindersRequireCoverageAcceptsTheUnion(t *testing.T) {
	t.Parallel()
	req, phases := coordinatorFixture(t)
	req.FindersRaw, req.RequireCoverage = "2", true
	phases.attests = map[string]bool{"x-finder-1": true}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("run refused: %v", ref)
	}
	manifest := readPhaseObject(t, filepath.Join(phases.dir, launcherFile))
	if status := readcommon.FieldOf(manifest, "status"); status != "complete" {
		t.Errorf("status = %v, want complete: one finder's coverage gap is no failure", status)
	}
}

// TestResumeSealsRequireCoverage proves --require-coverage is sealed in the
// launcher record: resuming with the flag as it was continues, and flipping it
// either way is a different run, refused resume-drift before any agent starts.
func TestResumeSealsRequireCoverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		first, second bool
		drift         bool
	}{
		{name: "both on", first: true, second: true},
		{name: "on then off", first: true, drift: true},
		{name: "off then on", second: true, drift: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkResumeSeal(t, tc.first, tc.second, tc.drift)
		})
	}
}

// checkResumeSeal interrupts one execution launched with first, resumes it
// with second, and checks the resume drifts exactly when drift says.
func checkResumeSeal(t *testing.T, first, second, drift bool) {
	t.Helper()
	req, commander := resumeFixture(t, "local-resume-coverage")
	commander.attests = map[string]bool{"x-finder-1": true, "x-finder-2": true}
	req.RequireCoverage = first
	interruptRun(t, req, commander, "x-finder-2")
	path := filepath.Join(commander.dir, launcherFile)
	before := readFixtureFile(t, path)
	req.RequireCoverage, req.Resume = second, true
	if drift {
		assertResumeRefused(t, req, commander, path, before, codeResumeDrift, "--require-coverage")
		return
	}
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("resume with the same flag refused: %v", ref)
	}
}

// TestRunRefusesBundleSymlink proves a launch never writes through a symlinked
// managed path: an untrusted checkout whose .verdict/state/<bundle> points
// elsewhere would aim the agent's write grant there, so run refuses it the
// way record and post do, before any agent starts.
func TestRunRefusesBundleSymlink(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	bundle := bundleDir(root)
	moved := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(bundle, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, bundle); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cmdr := perCall(t, root, coveredReviewDoc)
	_, ref := Run(t.Context(), repairRequest(root, cmdr, "1", &collector{}))
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("ref = %v, want bad-config for the symlinked bundle", ref)
	}
	assertSpawns(t, cmdr, 0)
}

// TestReviewGrantYieldsToOperatorPermissions pins when the operator's `--`
// tail owns the grant's question: a permission mode, a permission bypass, an
// allow list, or a whole tool set drops it; an unrelated flag, or the same
// spelling as another option's operand, keeps it.
func TestReviewGrantYieldsToOperatorPermissions(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	state, bundle := filepath.Join(root, ".verdict", "state"), bundleDir(root)
	claude := launchRow(t, "claude")
	for _, tc := range []struct {
		pass  []string
		grant bool
	}{
		{pass: []string{"--permission-mode", "plan"}},
		{pass: []string{"--dangerously-skip-permissions"}},
		{pass: []string{"--allowedTools=Read"}},
		{pass: []string{"--tools", "Read"}},
		{pass: []string{"--model", "opus"}, grant: true},
		{pass: []string{"--append-system-prompt", "--permission-mode"}, grant: true},
	} {
		grant, why := reviewGrant(claude, tc.pass, root, state, bundle)
		if (len(grant) == 1) != tc.grant || why != "" {
			t.Errorf("pass %q: grant = %q (%q), want granted %v", tc.pass, grant, why, tc.grant)
		}
	}
}

// TestRunWarnsWhenTheGrantIsDropped proves a launch whose bundle path the
// rule syntax cannot carry says so instead of failing silently later.
func TestRunWarnsWhenTheGrantIsDropped(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	odd := filepath.Join(root, ".verdict", "state", "local-feat(api)")
	if err := os.Rename(bundleDir(root), odd); err != nil {
		t.Fatal(err)
	}
	req := repairRequest(root, &sequenced{}, "1", &collector{})
	req.Pr, req.Print = "local-feat(api)", true
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("print refused: %v", ref)
	}
	want := "claude launches without its review write grant: " + rulePathSyntax
	if !slices.ContainsFunc(resp.Warnings, func(w string) bool { return strings.HasPrefix(w, want) }) {
		t.Errorf("warnings = %q, want one naming the dropped grant", resp.Warnings)
	}
	if slices.ContainsFunc(resp.Data.Argv, func(a string) bool { return strings.HasPrefix(a, "--allowedTools=") }) {
		t.Errorf("argv = %q carries a grant for an unspellable path", resp.Data.Argv)
	}
}

// TestRepairRefusalCountsWhatItLists proves a refusal left with a shape error
// and the coverage gap counts the errors it lists: the coverage item leaves
// both, so "N error(s)" and the problem list agree.
func TestRepairRefusalCountsWhatItLists(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	cmdr := perCall(t, root, invalidDoc, invalidDoc)
	_, ref := Run(t.Context(), coverageRequest(root, cmdr, "2"))
	if ref == nil {
		t.Fatal("run succeeded, want a refusal")
	}
	errs := 0
	for _, p := range ref.Problems {
		if p.Code == core.ProblemCodeCoverageIncomplete {
			t.Errorf("refusal lists the coverage item beside a shape error: %+v", p)
		}
		if p.Level == core.ProblemLevelError {
			errs++
		}
	}
	if !strings.Contains(ref.Message, fmt.Sprintf("with %d error(s)", errs)) {
		t.Errorf("message %q does not count the %d error(s) it lists", ref.Message, errs)
	}
}

// TestStageRenderStatesTheCoverageRequirement proves a --print --stage render
// under --require-coverage carries the same requirement paragraph a launch
// does, and every directive's review document names coverage.
func TestStageRenderStatesTheCoverageRequirement(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	recipeControl(t, root, repoRecipe)
	req := request(root, &scripted{})
	req.Print, req.Recipe, req.StageRaw, req.RequireCoverage = true, "quick", "1", true
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("print refused: %v", ref)
	}
	prompt := resp.Data.Argv[len(resp.Data.Argv)-1]
	if !strings.Contains(prompt, coverageParagraph) || !strings.Contains(prompt, `"coverage": []`) {
		t.Errorf("stage render lacks the coverage requirement:\n%s", prompt)
	}
}

// TestRunRefusesASymlinkedReviewArtifact is the round-2 repro: a change that
// checks in .verdict/state/42/review.json as a link to ../../../src/main.go
// would aim claude's scoped write at that source file. run refuses before any
// agent starts, and the source file is untouched.
func TestRunRefusesASymlinkedReviewArtifact(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	source := filepath.Join(root, "src", "main.go")
	writeUnder(t, root, filepath.Join("src", "main.go"), "package main\n")
	if err := os.Symlink(filepath.Join("..", "..", "..", "src", "main.go"), filepath.Join(bundleDir(root), reviewFile)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cmdr := perCall(t, root, coveredReviewDoc)
	_, ref := Run(t.Context(), repairRequest(root, cmdr, "1", &collector{}))
	if ref == nil || ref.Code != "artifact-path-unsafe" {
		t.Fatalf("ref = %v, want artifact-path-unsafe", ref)
	}
	assertSpawns(t, cmdr, 0)
	if got, err := os.ReadFile(source); err != nil || string(got) != "package main\n" {
		t.Errorf("source = %q (%v), want it untouched", got, err)
	}
}

// TestRunNeverReadsAReviewThroughALink proves the post-exit check does not
// follow a link the launch left behind: a review.json that is a symlink to a
// well-formed review elsewhere is not an artifact, so the run refuses instead
// of grading bytes from outside the bundle.
func TestRunNeverReadsAReviewThroughALink(t *testing.T) {
	t.Parallel()
	root := repairFixture(t)
	outside := filepath.Join(t.TempDir(), "review.json")
	cmdr := &sequenced{write: func(int) {
		if err := os.WriteFile(outside, []byte(coveredReviewDoc), 0o600); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(outside, filepath.Join(bundleDir(root), reviewFile)); err != nil {
			t.Error(err)
		}
	}}
	if _, ref := Run(t.Context(), repairRequest(root, cmdr, "1", &collector{})); ref == nil {
		t.Fatal("run graded a review read through a symlink")
	}
}

// TestRunRefusesAnUnsafeBundleEntry proves the pre-launch walk covers the whole
// bundle, since the agent's write grant does: a symlinked cost.json or
// validation.json, and a review.json hard-linked to a source file, each refuse
// artifact-path-unsafe before any agent starts, and the source file keeps its
// bytes.
func TestRunRefusesAnUnsafeBundleEntry(t *testing.T) {
	t.Parallel()
	for name, plant := range map[string]func(t *testing.T, bundle, source string) error{
		"symlinked cost.json": func(_ *testing.T, bundle, source string) error {
			return os.Symlink(source, filepath.Join(bundle, "cost.json"))
		},
		"symlinked validation.json": func(_ *testing.T, bundle, source string) error {
			return os.Symlink(source, filepath.Join(bundle, "validation.json"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertUnsafeBundleRefused(t, plant)
		})
	}
}

// assertUnsafeBundleRefused plants one entry in a fresh bundle, then proves the
// launch refuses before any spawn and the source file is untouched.
func assertUnsafeBundleRefused(t *testing.T, plant func(t *testing.T, bundle, source string) error) {
	t.Helper()
	root := repairFixture(t)
	writeUnder(t, root, filepath.Join("src", "main.go"), "package main\n")
	source := filepath.Join(root, "src", "main.go")
	if err := plant(t, bundleDir(root), source); err != nil {
		t.Fatalf("plant: %v", err)
	}
	cmdr := perCall(t, root, coveredReviewDoc)
	if _, ref := Run(t.Context(), repairRequest(root, cmdr, "1", &collector{})); ref == nil || ref.Code != "artifact-path-unsafe" {
		t.Fatalf("ref = %v, want artifact-path-unsafe", ref)
	}
	assertSpawns(t, cmdr, 0)
	if got, err := os.ReadFile(source); err != nil || string(got) != "package main\n" {
		t.Errorf("source = %q (%v), want it untouched", got, err)
	}
}

// TestArtifactReadsNeverFollowALink proves the readers after exit open the
// artifact without following a link, so a link swapped in after the Lstat
// check is refused rather than read.
func TestArtifactReadsNeverFollowALink(t *testing.T) {
	t.Parallel()
	bundle := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, reviewFile), []byte(coveredReviewDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, reviewFile), filepath.Join(bundle, reviewFile)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A symlinked phase directory: the leaf is a regular file, the link is
	// one component up.
	if err := os.Symlink(outside, filepath.Join(bundle, "x-cross-1")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	for _, path := range []string{filepath.Join(bundle, reviewFile), filepath.Join(bundle, "x-cross-1", reviewFile)} {
		assertNoReadThroughLink(t, bundle, path)
	}
}

// assertNoReadThroughLink proves both artifact readers refuse path.
func assertNoReadThroughLink(t *testing.T, bundle, path string) {
	t.Helper()
	args := &launchArgs{abs: path, artifact: reviewFile, target: launchTarget{evidence: bundle}}
	if raw, ref := readRoundReview(args, &core.Result{}); ref == nil {
		t.Errorf("readRoundReview read %d bytes of %s through a link", len(raw), path)
	}
	if raw, ok := crashReviewBytes(bundle, path); ok {
		t.Errorf("crashReviewBytes read %d bytes of %s through a link", len(raw), path)
	}
}
