package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// secondDuplicate is finder-2's wording of finder-1's defect at the same
// anchor, one tier more severe.
const secondDuplicate = `{"path":"a.go","line":1,"side":"RIGHT","severity":"critical","claim":"Second finder defect.","quote":"new line 1","confidence":"high"}`

// The two signatures are sig.Finding("a.go", "", claim) for the two claims,
// spelled out so a change to the identity shows up here.
const (
	firstDefectSig  = "6ff46dc17b1a1093d66fa2cd1ccf9138c4b45b20"
	secondDefectSig = "877884a36da944aea07e210c74c066b8878b9e49"
)

func TestCoordinatorMergesDuplicateNamedBySameAs(t *testing.T) {
	t.Parallel()
	req, commander := coordinatorFixture(t)
	commander.distinct, commander.sameAs, commander.secondFinding = true, true, secondDuplicate
	response, ref := TwoFinders(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	review := readPhaseObject(t, filepath.Join(commander.dir, "review.json"))
	if got := compactRows(jq.Elements(readcommon.FieldOf(review, "findings"))); got != `[{"path":"a.go","line":1,"side":"RIGHT","severity":"critical","claim":"First finder defect.","quote":"new line 1","confidence":"high"}]` {
		t.Fatalf("findings = %s", got)
	}
	assertMergedPlan(t, commander.dir)
	assertMergedReceipt(t, commander.dir)
	if counts := response.Data.Execution.Counts; counts == nil || counts.Confirmed != 2 {
		t.Fatalf("counts = %+v", counts)
	}
	if slices.ContainsFunc(response.Warnings, func(w string) bool { return strings.Contains(w, "same_as") }) {
		t.Fatalf("warnings = %q", response.Warnings)
	}
}

// assertMergedPlan proves the survivor carries both directed checks and both
// origins, and the plan names the folded signature and its checker.
func assertMergedPlan(t *testing.T, dir string) {
	t.Helper()
	plan := readPhaseObject(t, filepath.Join(dir, "verification", "plan.json"))
	if got := compactRows(jq.Elements(readcommon.FieldOf(plan, "checks"))); got != `[{"sig":"`+firstDefectSig+`","origin":"finder-1","checker":"finder-2"},{"sig":"`+firstDefectSig+`","origin":"finder-2","checker":"finder-1"}]` {
		t.Fatalf("checks = %s", got)
	}
	sources := jq.Elements(readcommon.FieldOf(plan, "sources"))
	if len(sources) != 1 || len(jq.Elements(readcommon.FieldOf(sources[0], "origins"))) != 2 {
		t.Fatalf("sources = %s", compactRows(sources))
	}
	merged := readcommon.FieldOf(sources[0], "merged")
	if readcommon.FieldOf(merged, "sig") != secondDefectSig || readcommon.FieldOf(merged, "checker") != "finder-2" {
		t.Fatalf("sources = %s", compactRows(sources))
	}
}

// assertMergedReceipt proves the receipt holds both checks on the survivor
// without the same_as field, and the bound execution reads it as confirmed by
// both finders.
func assertMergedReceipt(t *testing.T, dir string) {
	t.Helper()
	receipt := readPhaseObject(t, filepath.Join(dir, "verification", "verdicts.json"))
	results := jq.Elements(readcommon.FieldOf(receipt, "results"))
	if len(results) != 2 || strings.Contains(compactRows(results), "same_as") ||
		readcommon.FieldOf(results[0], "sig") != firstDefectSig || readcommon.FieldOf(results[1], "sig") != firstDefectSig {
		t.Fatalf("results = %s", compactRows(results))
	}
	execution := panelcore.CheckExecution(dir, nil)
	if execution.Problem() != "" || !execution.Eligible[firstDefectSig] ||
		!slices.Equal(execution.Origins[firstDefectSig], []string{"finder-1", "finder-2"}) {
		t.Fatalf("execution = %q eligible=%v origins=%v", execution.Problem(), execution.Eligible, execution.Origins)
	}
}

func TestCoordinatorKeepsDistinctSameLineFindingsWithoutSameAs(t *testing.T) {
	t.Parallel()
	req, commander := coordinatorFixture(t)
	commander.distinct, commander.secondFinding = true, secondDuplicate
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	review := readPhaseObject(t, filepath.Join(commander.dir, "review.json"))
	if n := len(jq.Elements(readcommon.FieldOf(review, "findings"))); n != 2 {
		t.Fatalf("findings = %d, want both distinct bugs", n)
	}
}

func TestCoordinatorIgnoresSameAsAwayFromTheCandidate(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ second, problem string }{
		"two lines away": {
			second:  `{"path":"a.go","line":3,"side":"RIGHT","severity":"important","claim":"Second finder defect.","quote":"old line 3","confidence":"high"}`,
			problem: "it names a finding at line 3, not overlapping or adjacent to line 1",
		},
		"another file": {
			second:  `{"path":"old.go","line":1,"side":"RIGHT","severity":"important","claim":"Second finder defect.","quote":"new line 1","confidence":"high"}`,
			problem: "it names a finding in old.go, not a.go",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertSameAsIgnored(t, tc.second, tc.problem)
		})
	}
}

// assertSameAsIgnored runs finder-2's same_as against a finding the anchors
// keep apart: both findings stand, the plan records no merge, and a warning
// names the reason.
func assertSameAsIgnored(t *testing.T, second, problem string) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	commander.distinct, commander.sameAs, commander.secondFinding = true, true, second
	response, ref := TwoFinders(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	review := readPhaseObject(t, filepath.Join(commander.dir, "review.json"))
	if n := len(jq.Elements(readcommon.FieldOf(review, "findings"))); n != 2 {
		t.Fatalf("findings = %d, want the far pair kept apart", n)
	}
	want := "finder-2's cross check named finding " + firstDefectSig[:12] + " same_as "
	if !slices.ContainsFunc(response.Warnings, func(w string) bool {
		return strings.HasPrefix(w, want) && strings.HasSuffix(w, "; ignored: "+problem)
	}) {
		t.Fatalf("warnings = %q, want %q … %q", response.Warnings, want, problem)
	}
	raw, err := os.ReadFile(filepath.Join(commander.dir, "verification", "plan.json"))
	if err != nil || strings.Contains(string(raw), `"merged"`) {
		t.Fatalf("plan = %s (%v), want no merge", raw, err)
	}
}

// sameAsUnion is a frozen union of one finding from each finder.
func sameAsUnion(t *testing.T, first, second string) *unionIndex {
	t.Helper()
	review, plan, err := discoveryUnion("test", []byte(fixtureDiff), []*jq.Obj{
		unionInput(t, `{"findings":[`+first+`]}`), unionInput(t, `{"findings":[`+second+`]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return indexUnion(review, plan)
}

// sameAsFirst is finder-1's finding the checker confirms in the unit tests.
var sameAsFirst = strings.ReplaceAll(coordinatorFinding, "Changed value breaks the caller.", "First finder defect.")

// sameAsRow is finder-2's cross row on finder-1's finding, naming sameAs.
func sameAsRow(verdict, sameAs string) *jq.Obj {
	return jq.NewObj("sig", firstDefectSig, "checker", "finder-2", "verdict", verdict, "same_as", sameAs)
}

func TestSameAsAcceptsAnchoredPairs(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ second, sameAs string }{
		"adjacent range": {
			second: `{"path":"a.go","start_line":2,"line":4,"side":"RIGHT","severity":"important","claim":"Second finder defect.","quote":"old line 4","confidence":"high"}`,
			sameAs: secondDefectSig,
		},
		"short sig": {second: secondDuplicate, sameAs: secondDefectSig[:8]},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pairs, notes := sameAsUnion(t, sameAsFirst, tc.second).sameAsPairs([]any{sameAsRow("confirmed", tc.sameAs)})
			if len(notes) != 0 || len(pairs) != 1 || pairs[0].own != secondDefectSig {
				t.Fatalf("pairs = %+v notes = %q", pairs, notes)
			}
		})
	}
}

func TestSameAsProblems(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ second, verdict, sameAs, problem string }{
		"refuted": {
			second: secondDuplicate, verdict: "refuted", sameAs: secondDefectSig,
			problem: "only a confirmed check can name a duplicate",
		},
		"unknown sig": {
			second: secondDuplicate, verdict: "confirmed", sameAs: "ffff",
			problem: "it names no single finding in the union",
		},
		"not the checker's own": {
			second: secondDuplicate, verdict: "confirmed", sameAs: firstDefectSig,
			problem: "it names a finding the checker did not report alone",
		},
		"other side": {
			second:  `{"path":"a.go","line":1,"side":"LEFT","severity":"important","claim":"Second finder defect.","quote":"old line 1","confidence":"high"}`,
			verdict: "confirmed", sameAs: secondDefectSig,
			problem: "it names a finding on the other side of the diff",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pairs, notes := sameAsUnion(t, sameAsFirst, tc.second).sameAsPairs([]any{sameAsRow(tc.verdict, tc.sameAs)})
			if len(pairs) != 0 || len(notes) != 1 || !strings.HasSuffix(notes[0], "; ignored: "+tc.problem) {
				t.Fatalf("pairs = %+v notes = %q", pairs, notes)
			}
		})
	}
}

func TestSameAsMergesAFindingOnce(t *testing.T) {
	t.Parallel()
	u := sameAsUnion(t, sameAsFirst, secondDuplicate)
	row := sameAsRow("confirmed", secondDefectSig)
	reverse := jq.NewObj("sig", secondDefectSig, "checker", "finder-1", "verdict", "confirmed", "same_as", firstDefectSig)
	pairs, notes := u.sameAsPairs([]any{row, reverse})
	if len(pairs) != 1 || len(notes) != 1 || !strings.HasSuffix(notes[0], "; ignored: a finding merges at most once") {
		t.Fatalf("pairs = %+v notes = %q", pairs, notes)
	}
}

func TestSameAsKeepsTheBetterEvidencedQuoteAndHigherSeverity(t *testing.T) {
	t.Parallel()
	u := sameAsUnion(t, sameAsFirst, secondDuplicate)
	pair := []sameAsPair{{checked: firstDefectSig, own: secondDefectSig, checker: "finder-2"}}
	onlySecond := func(row *jq.Obj) bool { return readcommon.FieldOf(row, "claim") == "Second finder defect." }
	merged := u.mergePairs(pair, onlySecond)
	if merged[0].kept != secondDefectSig || merged[0].dropped != firstDefectSig {
		t.Fatalf("kept %s, want the verified quote's finding", merged[0].kept)
	}
	both := func(*jq.Obj) bool { return true }
	merged = u.mergePairs(pair, both)
	if merged[0].kept != firstDefectSig || readcommon.FieldOf(merged[0].row, "severity") != "critical" {
		t.Fatalf("merged = %s %s, want the confirmed candidate at critical", merged[0].kept, jq.Compact(merged[0].row))
	}
	if core.TierRank(readcommon.FieldOf(u.rows[firstDefectSig], "severity")) != core.TierRank("important") {
		t.Fatal("merge rewrote the frozen union row")
	}
}

// TestResumeReachesTheSameMerge proves the merge is a function of the frozen
// union and the verified cross rows: a resume that reuses the check naming
// same_as and relaunches the other folds the same pair, and a resume of the
// finished execution rewrites the merged review byte for byte.
func TestResumeReachesTheSameMerge(t *testing.T) {
	t.Parallel()
	req, commander := resumeFixture(t, "local-resume-same-as")
	commander.distinct, commander.sameAs, commander.secondFinding = true, true, secondDuplicate
	interruptRun(t, req, commander, "x-cross-1")

	req.Resume = true
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("resume refused: %v launches=%v", ref, commander.launched())
	}
	assertPhaseGroups(t, commander.launched(), []string{"x-cross-1"})
	reviewPath := filepath.Join(commander.dir, "review.json")
	merged, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(jq.Elements(readcommon.FieldOf(readPhaseObject(t, reviewPath), "findings"))); n != 1 {
		t.Fatalf("findings = %d, want the pair merged", n)
	}
	if problem := panelcore.CheckExecution(commander.dir, nil).Problem(); problem != "" {
		t.Fatalf("execution = %q", problem)
	}
	commander.reset()
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("second resume refused: %v", ref)
	}
	if again, err := os.ReadFile(reviewPath); err != nil || !bytes.Equal(again, merged) {
		t.Fatalf("review = %s (%v), want %s", again, err, merged)
	}
}
