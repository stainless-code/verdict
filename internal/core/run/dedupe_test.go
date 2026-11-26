package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/evidence"
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

// TestCoordinatorNeedsTheOtherCheckerToConfirmTheNamedFinding proves finder-2's
// same_as never merges a finding finder-1's check did not confirm: refuted,
// insufficient, or a check that never finished. Both findings stand with the
// verdicts their checkers actually returned.
func TestCoordinatorNeedsTheOtherCheckerToConfirmTheNamedFinding(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ verdict, fail, problem string }{
		"refuted":      {verdict: "refuted", problem: "the named finding was not confirmed by the other checker"},
		"insufficient": {verdict: "insufficient", problem: "the named finding was not confirmed by the other checker"},
		"check failed": {fail: "x-cross-1", problem: "the named finding has no check from the other checker"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req, commander := coordinatorFixture(t)
			commander.distinct, commander.sameAs, commander.secondFinding = true, true, secondDuplicate
			commander.firstCheckerVerdict, commander.failPhase = tc.verdict, tc.fail
			response, ref := TwoFinders(t.Context(), req)
			warnings := runWarnings(response, ref)
			assertUnmerged(t, commander.dir, warnings, tc.problem)
			if tc.verdict != "" {
				assertRecordedVerdict(t, commander.dir, secondDefectSig, "finder-1", tc.verdict)
			}
		})
	}
}

// runWarnings is the warnings a run carried, on its response or its refusal.
func runWarnings(response *core.RunResponse, ref *core.RefusalError) []string {
	if ref != nil {
		return ref.Warnings
	}
	return response.Warnings
}

// assertUnmerged proves both findings still stand, the plan records no merge,
// and a warning names why the same_as was ignored.
func assertUnmerged(t *testing.T, dir string, warnings []string, problem string) {
	t.Helper()
	review := readPhaseObject(t, filepath.Join(dir, "review.json"))
	if n := len(jq.Elements(readcommon.FieldOf(review, "findings"))); n != 2 {
		t.Fatalf("findings = %d, want both kept", n)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.HasSuffix(w, "; ignored: "+problem) }) {
		t.Fatalf("warnings = %q, want %q", warnings, problem)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "verification", "plan.json"))
	if err != nil || strings.Contains(string(raw), `"merged"`) {
		t.Fatalf("plan = %s (%v), want no merge", raw, err)
	}
}

// assertRecordedVerdict proves the aggregate receipt keeps checker's verdict on
// signature, not a confirmation grafted from the other finding.
func assertRecordedVerdict(t *testing.T, dir, signature, checker, verdict string) {
	t.Helper()
	receipt := readPhaseObject(t, filepath.Join(dir, "verification", "verdicts.json"))
	for _, row := range jq.Elements(readcommon.FieldOf(receipt, "results")) {
		if readcommon.FieldOf(row, "sig") == signature && readcommon.FieldOf(row, "checker") == checker {
			if got := readcommon.FieldOf(row, "verdict"); got != verdict {
				t.Fatalf("%s on %s = %v, want %s", checker, signature, got, verdict)
			}
			return
		}
	}
	t.Fatalf("no %s row on %s: %s", checker, signature, jq.Compact(receipt))
}

// sameAsUnion is a frozen union of finder-1's findings and finder-2's, each
// list a comma-joined run of finding objects.
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

// thirdDefect is a second finder-2 finding at the same anchor whose signature
// shares the prefix "87" with secondDefectSig.
const (
	thirdDefect    = `{"path":"a.go","line":1,"side":"RIGHT","severity":"important","claim":"Third finder defect 119.","quote":"new line 1","confidence":"high"}`
	thirdDefectSig = "87d6a7c7bb43b56bc5ee7c57ae56d11dffd2aee3"
)

// sameAsRow is finder-2's cross row on finder-1's finding, naming sameAs.
func sameAsRow(verdict, sameAs string) *jq.Obj {
	return jq.NewObj("sig", firstDefectSig, "checker", "finder-2", "verdict", verdict, "same_as", sameAs)
}

// firstCheckerRow is finder-1's cross row on signature, without a same_as.
func firstCheckerRow(signature, verdict string) *jq.Obj {
	return jq.NewObj("sig", signature, "checker", "finder-1", "verdict", verdict)
}

// confirmedSecond is finder-1's confirmation of finder-2's finding, the check a
// same_as naming that finding needs before it can merge.
func confirmedSecond() *jq.Obj { return firstCheckerRow(secondDefectSig, "confirmed") }

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
			rows := []any{sameAsRow("confirmed", tc.sameAs), confirmedSecond()}
			pairs, notes := sameAsUnion(t, sameAsFirst, tc.second).sameAsPairs(rows)
			if len(notes) != 0 || len(pairs) != 1 || pairs[0].own != secondDefectSig {
				t.Fatalf("pairs = %+v notes = %q", pairs, notes)
			}
		})
	}
}

// sameAsCase is one same_as the guard must refuse: finder-2's findings, the
// rows both checkers returned, and the reason the warning names.
type sameAsCase struct {
	second, problem string
	rows            []any
}

func TestSameAsProblems(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]sameAsCase{
		"refuted": {
			second: secondDuplicate, rows: []any{sameAsRow("refuted", secondDefectSig), confirmedSecond()},
			problem: "only a confirmed check can name a duplicate",
		},
		"unknown sig": {
			second: secondDuplicate, rows: []any{sameAsRow("confirmed", "ffff"), confirmedSecond()},
			problem: "it names no single finding in the union",
		},
		"ambiguous prefix": {
			second: secondDuplicate + "," + thirdDefect,
			rows: []any{
				sameAsRow("confirmed", "87"), confirmedSecond(),
				firstCheckerRow(thirdDefectSig, "confirmed"),
			},
			problem: "it names no single finding in the union",
		},
		"not the checker's own": {
			second: secondDuplicate, rows: []any{sameAsRow("confirmed", firstDefectSig), confirmedSecond()},
			problem: "it names a finding the checker did not report alone",
		},
		"other side": {
			second:  `{"path":"a.go","line":1,"side":"LEFT","severity":"important","claim":"Second finder defect.","quote":"old line 1","confidence":"high"}`,
			rows:    []any{sameAsRow("confirmed", secondDefectSig), confirmedSecond()},
			problem: "it names a finding on the other side of the diff",
		},
		"named finding refuted by the other checker": {
			second: secondDuplicate,
			rows: []any{
				sameAsRow("confirmed", secondDefectSig),
				firstCheckerRow(secondDefectSig, "refuted"),
			},
			problem: "the named finding was not confirmed by the other checker",
		},
		"named finding insufficient": {
			second: secondDuplicate,
			rows: []any{
				sameAsRow("confirmed", secondDefectSig),
				firstCheckerRow(secondDefectSig, "insufficient"),
			},
			problem: "the named finding was not confirmed by the other checker",
		},
		"named finding never checked": {
			second: secondDuplicate, rows: []any{sameAsRow("confirmed", secondDefectSig)},
			problem: "the named finding has no check from the other checker",
		},
		"zero start_line spans nothing": {
			second:  `{"path":"a.go","start_line":0,"line":5,"side":"RIGHT","severity":"important","claim":"Second finder defect.","quote":"old line 5","confidence":"high"}`,
			rows:    []any{sameAsRow("confirmed", secondDefectSig), confirmedSecond()},
			problem: "it names a finding at line 5, not overlapping or adjacent to line 1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertSameAsProblem(t, tc)
		})
	}
}

func assertSameAsProblem(t *testing.T, tc sameAsCase) {
	t.Helper()
	pairs, notes := sameAsUnion(t, sameAsFirst, tc.second).sameAsPairs(tc.rows)
	if len(pairs) != 0 || len(notes) != 1 || !strings.HasSuffix(notes[0], "; ignored: "+tc.problem) {
		t.Fatalf("pairs = %+v notes = %q", pairs, notes)
	}
}

// TestSameAsRefusesAnExactSignatureDuplicate proves a finding both finders
// already reported under one signature cannot fold a third: it carries both
// origins, so the checker confirming it is checking its own claim too.
func TestSameAsRefusesAnExactSignatureDuplicate(t *testing.T) {
	t.Parallel()
	u := sameAsUnion(t, sameAsFirst, sameAsFirst+","+secondDuplicate)
	rows := []any{sameAsRow("confirmed", secondDefectSig), firstCheckerRow(firstDefectSig, "confirmed"), confirmedSecond()}
	assertSingleNote(t, u, rows, "the checked finding is not the other finder's alone")
}

func assertSingleNote(t *testing.T, u *unionIndex, rows []any, problem string) {
	t.Helper()
	pairs, notes := u.sameAsPairs(rows)
	if len(pairs) != 0 || len(notes) != 1 || !strings.HasSuffix(notes[0], "; ignored: "+problem) {
		t.Fatalf("pairs = %+v notes = %q", pairs, notes)
	}
}

// TestSameAsMutualNamingIsAgreement proves both checkers naming the same pair,
// the expected happy path, merges once and warns about nothing.
func TestSameAsMutualNamingIsAgreement(t *testing.T) {
	t.Parallel()
	u := sameAsUnion(t, sameAsFirst, secondDuplicate)
	reverse := jq.NewObj("sig", secondDefectSig, "checker", "finder-1", "verdict", "confirmed", "same_as", firstDefectSig)
	pairs, notes := u.sameAsPairs([]any{sameAsRow("confirmed", secondDefectSig), reverse})
	if len(pairs) != 1 || len(notes) != 0 {
		t.Fatalf("pairs = %+v notes = %q", pairs, notes)
	}
}

// TestSameAsMergesAFindingOnce proves a finding already folded with one partner
// refuses a third, and the refusal does not depend on the order an agent wrote
// its rows.
func TestSameAsMergesAFindingOnce(t *testing.T) {
	t.Parallel()
	u := sameAsUnion(t, sameAsFirst, secondDuplicate+","+thirdDefect)
	third := jq.NewObj("sig", thirdDefectSig, "checker", "finder-1", "verdict", "confirmed", "same_as", firstDefectSig)
	for _, rows := range [][]any{
		{sameAsRow("confirmed", secondDefectSig), confirmedSecond(), third},
		{third, confirmedSecond(), sameAsRow("confirmed", secondDefectSig)},
	} {
		pairs, notes := u.sameAsPairs(rows)
		if len(pairs) != 1 || pairs[0].own != secondDefectSig || len(notes) != 1 ||
			!strings.HasSuffix(notes[0], "; ignored: a finding merges at most once") {
			t.Fatalf("pairs = %+v notes = %q", pairs, notes)
		}
	}
}

func TestLineSpanRejectsNonPositiveAnchors(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		`{"line":0}`:                  "0 0 false",
		`{"start_line":0,"line":0}`:   "0 0 false",
		`{"start_line":3}`:            "0 0 false",
		`{"start_line":0,"line":500}`: "500 500 true",
		`{"start_line":-4,"line":9}`:  "9 9 true",
		`{"start_line":2,"line":4}`:   "2 4 true",
	} {
		start, end, ok := lineSpan(unionInput(t, raw))
		if got := fmt.Sprintf("%d %d %v", start, end, ok); got != want {
			t.Fatalf("lineSpan(%s) = %s, want %s", raw, got, want)
		}
	}
}

// TestFindingQuoteVerifiedReadsStartLine proves a multi-line finding's quote
// near its start_line verifies, exactly as record's evidence pass reads it.
func TestFindingQuoteVerifiedReadsStartLine(t *testing.T) {
	t.Parallel()
	idx := evidence.IndexDiff([]byte(fixtureDiff))
	row := unionInput(t, `{"path":"a.go","start_line":2,"line":12,"side":"RIGHT","quote":"old line 2"}`)
	if !findingQuoteVerified(idx, row) {
		t.Fatal("a quote at start_line read as unverified")
	}
}

func TestRekeySpecChecksFollowsTheFold(t *testing.T) {
	t.Parallel()
	u := sameAsUnion(t, sameAsFirst, secondDuplicate)
	specs := []any{
		unionInput(t, `{"item":"full","status":"violated","finding_sig":"`+secondDefectSig+`"}`),
		unionInput(t, `{"item":"short","status":"violated","finding_sig":"`+secondDefectSig[:8]+`"}`),
		unionInput(t, `{"item":"empty","status":"met","finding_sig":""}`),
		unionInput(t, `{"item":"absent","status":"met"}`),
	}
	got := compactRows(rekeySpecChecks(specs, u, map[string]string{secondDefectSig: firstDefectSig}))
	want := `[{"item":"full","status":"violated","finding_sig":"` + firstDefectSig + `"},` +
		`{"item":"short","status":"violated","finding_sig":"` + firstDefectSig + `"},` +
		`{"item":"empty","status":"met","finding_sig":""},{"item":"absent","status":"met"}]`
	if got != want {
		t.Fatalf("specs = %s\nwant    %s", got, want)
	}
}

func TestSameAsShapeAcceptsOnlyStrings(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]bool{
		`{"sig":"s"}`:                 true,
		`{"sig":"s","same_as":null}`:  true,
		`{"sig":"s","same_as":""}`:    true,
		`{"sig":"s","same_as":"abc"}`: true,
		`{"sig":"s","same_as":7}`:     false,
		`{"sig":"s","same_as":["a"]}`: false,
	} {
		rows := []any{unionInput(t, raw)}
		batch := []any{jq.NewObj("sig", "s")}
		problems := assignCrossRows(rows, "finder-2", batch)
		if got := len(problems) == 0; got != want {
			t.Fatalf("assignCrossRows(%s) problems = %v, want valid %v", raw, problems, want)
		}
		if !want && problems[0].Path != "$.results[0].same_as" {
			t.Fatalf("assignCrossRows(%s) problem path = %q, want $.results[0].same_as", raw, problems[0].Path)
		}
	}
}

func TestFoldStrongerKeepsCategoryAndRigor(t *testing.T) {
	t.Parallel()
	row := unionInput(t, `{"severity":"important","rigor":"pointed"}`)
	dropped := unionInput(t, `{"severity":"critical","category":"security","rigor":"reproduced","reproduction":{"command":"go test"}}`)
	foldStronger(row, dropped)
	if got := jq.NewObj("row", row).MarshalCompact(); got != `{"row":{"severity":"critical","rigor":"reproduced","category":"security","reproduction":{"command":"go test"}}}` {
		t.Fatalf("row = %s", got)
	}
	weaker := unionInput(t, `{"severity":"informational","category":"style","rigor":"said"}`)
	foldStronger(row, weaker)
	if got := jq.NewObj("row", row).MarshalCompact(); got != `{"row":{"severity":"critical","rigor":"reproduced","category":"security","reproduction":{"command":"go test"}}}` {
		t.Fatalf("row after a weaker fold = %s", got)
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
	assertMergedBundleGates(t, req)
}

// assertMergedBundleGates proves record, gate, and approval read the merged
// bundle as one finding: the gate at critical fails on exactly one open
// finding, and approval gates on the survivor alone.
func assertMergedBundleGates(t *testing.T, req *core.RunRequest) {
	t.Helper()
	if _, ref := recordcore.Run(t.Context(), &recordcore.Request{
		ReadRequest: req.ReadRequest, Effective: core.Defaults(),
	}); ref != nil {
		t.Fatalf("record refused the merged bundle: %v", ref)
	}
	critical := runResumedGate(t, req, "critical")
	if critical.Gate == nil || !critical.GateFailed {
		t.Fatalf("gate at critical passed a merged critical finding: %+v", critical.Gate)
	}
	if detail := gateDetail(critical.Gate, "findings"); !strings.Contains(detail, "1 open finding") {
		t.Fatalf("findings dimension = %q, want the one merged finding", detail)
	}
	approval := critical.Data.Approval
	if approval == nil || approval.Insufficient || !slices.Equal(approval.GatingSigs, []string{firstDefectSig}) {
		t.Fatalf("approval = %+v, want gating on %s alone", approval, firstDefectSig)
	}
}

// TestOwnFindingsCarryTheWholeAnchor proves the request hands the checker every
// anchor field the guard compares, so it can tell an adjacent range or the
// other side apart before it names a same_as.
func TestOwnFindingsCarryTheWholeAnchor(t *testing.T) {
	t.Parallel()
	second := `{"path":"a.go","start_line":2,"line":4,"side":"LEFT","severity":"important","claim":"Second finder defect.","quote":"old line 4","confidence":"high"}`
	review, plan, err := discoveryUnion("test", []byte(fixtureDiff), []*jq.Obj{
		unionInput(t, `{"findings":[`+sameAsFirst+`]}`), unionInput(t, `{"findings":[`+second+`]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := compactRows(ownFindings(plan, review, "finder-2")); got != `[{"sig":"`+secondDefectSig+`","path":"a.go","start_line":2,"line":4,"side":"LEFT","claim":"Second finder defect."}]` {
		t.Fatalf("own_findings = %s", got)
	}
}
