package recordcore_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// specStatusReview renders the evidence fixture's review with fragment as its
// document tail, so each case carries its own spec_checks rows.
func specStatusReview(fragment string) string {
	return evidenceReview(nil, receiptFragment+fragment)
}

// specChecks renders a spec_checks array with trailing comma, ready to be a
// review document's tail.
func specChecks(rows ...string) string {
	return `"spec_checks":[` + strings.Join(rows, ",") + `],`
}

// metRow is the first fixture item, met by the quoted NEW line.
const metRow = `{"item":"- the record must verify quotes","status":"met",` +
	`"evidence":{"path":"notes.txt","line":2,"quote":"new line"}}`

// unmetRow is the second fixture item, met by nothing the change carries.
const unmetRow = `{"item":"- the record must receipt every hunk","status":"unmet",` +
	`"note":"nothing receipts hunks yet"}`

// specStatusBundle writes the fixture bundle with one spec source and the
// review under test.
func specStatusBundle(t *testing.T, review string) string {
	t.Helper()
	return evidenceBundle(t, review, map[string]string{
		"context.txt":  specContext("SPEC.md"),
		"context.json": specManifest("SPEC.md", "spec"),
	})
}

// checkSpec writes the fixture bundle with review and runs the shared bundle
// check record and the merge both call, so a problem assertion cannot disagree
// with what record would refuse.
func checkSpec(t *testing.T, review string) recordcore.CheckResult {
	t.Helper()
	dir := specStatusBundle(t, review)
	res, ref := recordcore.CheckBundle(dir, []byte(review))
	if ref != nil {
		t.Fatalf("CheckBundle refusal: %v", ref)
	}
	return res
}

// specCheckProblems returns every problem whose path names one spec_checks row:
// the shape checker's (an unwritten note, an unexplained n/a) and the evidence
// pass's (a requirement nothing implements, evidence on a row that may not cite
// any), because one list is what the operator reads.
func specCheckProblems(res recordcore.CheckResult) []recordcore.Problem {
	all := res.Problems
	if res.Evidence != nil {
		all = append(slices.Clone(all), res.Evidence.Problems...)
	}
	var out []recordcore.Problem
	for _, p := range all {
		if strings.HasPrefix(p.Path, "$.spec_checks") {
			out = append(out, p)
		}
	}
	return out
}

// assertSpecProblem proves the problem list carries code at level on path, and
// returns the problem so a caller can assert its wording.
func assertSpecProblem(t *testing.T, res recordcore.CheckResult, path, code, level string) recordcore.Problem {
	t.Helper()
	for _, p := range specCheckProblems(res) {
		if p.Path == path && p.Code == code && p.Level == level {
			return p
		}
	}
	t.Fatalf("problems = %+v, want %s %s at %s", specCheckProblems(res), level, code, path)
	return recordcore.Problem{}
}

// TestRecordUnmetSpecCheckWithholdsApproval proves a requirement the change
// does not deliver is a recorded outcome, not a refusal: the row verifies, the
// receipt is not insufficient, and approval withholds naming the item.
func TestRecordUnmetSpecCheckWithholdsApproval(t *testing.T) {
	dir := specStatusBundle(t, specStatusReview(specChecks(metRow, unmetRow)))
	resp, ref := recordEvidence(t, false)
	if ref != nil {
		t.Fatalf("record refusal: %v", ref)
	}
	ev := evidenceOf(t, resp)
	if ev.Insufficient {
		t.Errorf("evidence = %+v, want sufficient: an unmet row verified", ev)
	}
	if want := (core.EvidenceSpec{Total: 2, Covered: 2, Min: 80, Met: true}); ev.Spec != want {
		t.Errorf("spec = %+v, want %+v", ev.Spec, want)
	}
	approval := storedRecord(t, dir).Approval
	if approval == nil || approval.Approved {
		t.Fatalf("approval = %+v, want withheld", approval)
	}
	const wantReason = "unmet spec check: - the record must receipt every hunk"
	if approval.Reason != wantReason {
		t.Errorf("reason = %q, want %q", approval.Reason, wantReason)
	}
}

// TestUnmetSpecCheckNamesTheItemAndCarriesNoNote proves the published problem
// names the item the review failed to deliver, the level is an error, and the
// agent's own note prose never reaches it: the note is rendered into a log.
func TestUnmetSpecCheckNamesTheItemAndCarriesNoNote(t *testing.T) {
	res := checkSpec(t, specStatusReview(specChecks(metRow, unmetRow)))
	p := assertSpecProblem(t, res, "$.spec_checks[1].status", core.ProblemCodeSpecCheckUnmet, core.ProblemLevelError)
	if !strings.Contains(p.Message, "- the record must receipt every hunk") {
		t.Errorf("message = %q, want the item text", p.Message)
	}
	if strings.Contains(p.Message, "nothing receipts hunks yet") {
		t.Errorf("message = %q, want the note kept out of it", p.Message)
	}
}

// TestUnmetSpecCheckNeedsNote proves the status that declares a requirement
// missing must say what is missing: without a note the review refuses.
func TestUnmetSpecCheckNeedsNote(t *testing.T) {
	noNote := `{"item":"- the record must receipt every hunk","status":"unmet"}`
	res := checkSpec(t, specStatusReview(specChecks(metRow, noNote)))
	assertSpecProblem(t, res, "$.spec_checks[1].note", core.ProblemCodeSpecNoteMissing, core.ProblemLevelError)
	if ref := recordcore.ReviewRefusal(res, false); ref == nil || ref.Code != recordcore.CodeBadReviewInput {
		t.Fatalf("refusal = %+v, want bad-review-input", ref)
	}
}

// TestNASpecCheckWithoutNoteWarnsUntilStrict proves the unexplained `n/a` row
// records by default and refuses once input is strict: rows already in the wild
// carry that shape, so it may not break a re-record.
func TestNASpecCheckWithoutNoteWarnsUntilStrict(t *testing.T) {
	na := `{"item":"- the record must receipt every hunk","status":"n/a"}`
	res := checkSpec(t, specStatusReview(specChecks(metRow, na)))
	assertSpecProblem(t, res, "$.spec_checks[1].note", core.ProblemCodeSpecNAUnjustified, core.ProblemLevelWarning)
	if ref := recordcore.ReviewRefusal(res, false); ref != nil {
		t.Fatalf("default refusal: %v, want the unexplained n/a recorded", ref)
	}
	strict := recordcore.ReviewRefusal(res, true)
	if strict == nil || strict.Code != recordcore.CodeBadReviewInput {
		t.Fatalf("strict refusal = %+v, want bad-review-input", strict)
	}
	if !slices.ContainsFunc(strict.Problems, func(p core.Problem) bool {
		return p.Code == core.ProblemCodeSpecNAUnjustified && p.Level == core.ProblemLevelError
	}) {
		t.Errorf("strict problems = %+v, want the n/a warning promoted", strict.Problems)
	}
}

// TestNASpecCheckWithNoteRecords proves a justified `n/a` row is silent: the
// note is the whole rule, and a row that carries one has nothing to answer for.
func TestNASpecCheckWithNoteRecords(t *testing.T) {
	na := `{"item":"- the record must receipt every hunk","status":"n/a",` +
		`"note":"this change adds no hunk receipts"}`
	res := checkSpec(t, specStatusReview(specChecks(metRow, na)))
	if got := specCheckProblems(res); len(got) != 0 {
		t.Fatalf("problems = %+v, want none", got)
	}
	if _, ref := recordEvidence(t, false); ref != nil {
		t.Fatalf("record refusal: %v", ref)
	}
}

// TestUnmetSpecCheckRefusesEvidence proves an unmet row may not cite a diff
// line: nothing in the change implements it, so a quote there is a claim about
// code that does not exist.
func TestUnmetSpecCheckRefusesEvidence(t *testing.T) {
	withEvidence := `{"item":"- the record must receipt every hunk","status":"unmet",` +
		`"note":"nothing receipts hunks yet",` +
		`"evidence":{"path":"notes.txt","line":2,"quote":"new line"}}`
	res := checkSpec(t, specStatusReview(specChecks(metRow, withEvidence)))
	p := assertSpecProblem(t, res, "$.spec_checks[1].evidence.path", core.ProblemCodeSpecEvidenceMismatch, core.ProblemLevelError)
	if !strings.Contains(p.Message, "has no diff line to quote") {
		t.Errorf("message = %q, want the reason the evidence is refused", p.Message)
	}
}

// TestSpecNoteBounds proves the note is one line with a published ceiling: a
// non-string refuses as a type error, and a note past the cap is out of range.
func TestSpecNoteBounds(t *testing.T) {
	cases := []struct {
		name  string
		note  string
		code  string
		level string
	}{
		{"non-string", `false`, core.ProblemCodeWrongType, core.ProblemLevelError},
		{"blank", `"   "`, core.ProblemCodeOutOfRange, core.ProblemLevelError},
		{"too long", `"` + strings.Repeat("x", 501) + `"`, core.ProblemCodeOutOfRange, core.ProblemLevelError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := `{"item":"- the record must receipt every hunk","status":"unmet","note":` + c.note + `}`
			res := checkSpec(t, specStatusReview(specChecks(metRow, row)))
			assertSpecProblem(t, res, "$.spec_checks[1].note", c.code, c.level)
		})
	}
}

// TestSpecAbsentBundleOwesNoChecks proves the checklist is demanded only when
// the bundle names a spec source: a bundle whose context is plain reference
// material records a review with no spec_checks at all.
func TestSpecAbsentBundleOwesNoChecks(t *testing.T) {
	review := evidenceReview(nil, receiptFragment+specChecks())
	dir := evidenceBundle(t, review, map[string]string{
		"context.txt":  specContext("NOTES.md"),
		"context.json": specManifest("NOTES.md", "context"),
	})
	resp, ref := recordEvidence(t, false)
	if ref != nil {
		t.Fatalf("record refusal: %v", ref)
	}
	if ev := evidenceOf(t, resp); ev.Spec != (core.EvidenceSpec{Met: true}) {
		t.Errorf("spec = %+v, want no checklist", ev.Spec)
	}
	if approval := storedRecord(t, dir).Approval; approval == nil || !approval.Approved {
		t.Errorf("approval = %+v, want approved: nothing asked for a checklist", approval)
	}
}
