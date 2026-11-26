package run

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
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

// TestRulePath pins the rule spelling: relative inside the agent's working
// directory, `//` absolute outside it, and no grant for a path whose bytes the
// rule grammar would read as syntax.
func TestRulePath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		// A drive letter takes the POSIX form Claude Code matches rules in.
		if got := absoluteRulePath(`C:\repo\x`); got != "//c/repo/x" {
			t.Errorf("absoluteRulePath = %q, want //c/repo/x", got)
		}
		return
	}
	for _, tc := range []struct {
		cwd, dir, want string
		ok             bool
	}{
		{cwd: "/repo", dir: "/repo/.verdict/state/42", want: "./.verdict/state/42", ok: true},
		{cwd: "/checkout", dir: "/repo/.verdict/state/local-x", want: "//repo/.verdict/state/local-x", ok: true},
		{cwd: "/repo", dir: "/repo", ok: false},
		{cwd: "/repo", dir: "/my repo/.verdict/state/42", ok: false},
		{cwd: "/repo", dir: "/repo/.verdict/state/[42]", ok: false},
		{cwd: "/repo", dir: "/repo/.verdict/state/a,b", ok: false},
	} {
		got, ok := rulePath(tc.cwd, tc.dir)
		if got != tc.want || ok != tc.ok {
			t.Errorf("rulePath(%q, %q) = %q, %v; want %q, %v", tc.cwd, tc.dir, got, ok, tc.want, tc.ok)
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
