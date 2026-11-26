package run

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// coverageParagraph is the directive's up-front statement that this run
// enforces the attestation the findings contract asks for, so a first attempt
// usually carries it instead of spending a repair round. The shape stays in
// the contract.
const coverageParagraph = "This run requires the coverage attestation the findings contract describes, " +
	"checked the way record --require-coverage checks it: a gap is sent back for repair, and a review " +
	"that still lacks it is refused coverage-incomplete."

// coverageFix is the repair item's fix text, the coverage-incomplete hint's
// shape.
const coverageFix = `stamp top-level "coverage": [{"file": "...", "ranges": "1-240"}] for every changed ` +
	`file, attesting every changed line you read ("ranges": "all" for a whole file)`

// withCoverage folds record --require-coverage's gate into one round's check:
// the gap becomes an error the repair round names, so the agent fixes it
// before record would refuse the review. A malformed attestation is the
// evaluator's bad-review-input, whose problems the shape check already
// reports; they are added only when the shape check let them through.
func withCoverage(dir string, review []byte, check recordcore.CheckResult) recordcore.CheckResult {
	gate := recordcore.CoverageGate(dir, review)
	switch {
	case gate == nil:
		return check
	case gate.Code != coverage.CodeIncomplete && check.ShapeErrors() > 0:
		return check
	case gate.Code != coverage.CodeIncomplete && len(gate.Problems) > 0:
		check.Problems = append(check.Problems, gate.Problems...)
		check.Errors += len(gate.Problems)
		return check
	}
	check.Problems = append(check.Problems, core.Problem{
		Path: "$." + coverage.Field, Code: core.ProblemCodeCoverageIncomplete, Level: core.ProblemLevelError,
		Message: gate.Message, Fix: coverageFix,
	})
	check.Errors++
	return check
}

// coverageRefusal is the refusal a run ends with when the coverage gap is
// the only error left: coverage-incomplete with record's own message, so the
// run refuses exactly as record --require-coverage would. note, when set,
// leads the message (the no-progress stop). Any other error left keeps its
// own refusal.
func coverageRefusal(check recordcore.CheckResult, note string) *core.RefusalError {
	if check.Evidence != nil && check.Evidence.Errors > 0 {
		return nil
	}
	msg := onlyCoverageError(check.Problems)
	if msg == "" {
		return nil
	}
	if note != "" {
		msg = note + ": " + msg
	}
	// Both halves are verdict's own text: the note is composed here and the
	// message is record's, already built with Msgf.
	return &core.RefusalError{Code: coverage.CodeIncomplete, Message: core.Msgf("%s", core.Trusted(msg))}
}

// onlyCoverageError is the coverage item's message when it is the only
// error among problems, "" otherwise.
func onlyCoverageError(problems []core.Problem) string {
	msg := ""
	for i := range problems {
		switch {
		case problems[i].Level != core.ProblemLevelError:
		case problems[i].Code != core.ProblemCodeCoverageIncomplete:
			return ""
		default:
			msg = problems[i].Message
		}
	}
	return msg
}

// withoutCoverage is check with the coverage items taken out, for choosing
// the refusal when other errors remain: a shape error or an evidence gap
// keeps its own code, and the coverage item never turns an evidence refusal
// into a shape one.
func withoutCoverage(check recordcore.CheckResult) recordcore.CheckResult {
	kept := make([]core.Problem, 0, len(check.Problems))
	for i := range check.Problems {
		if check.Problems[i].Code == core.ProblemCodeCoverageIncomplete {
			check.Errors--
			continue
		}
		kept = append(kept, check.Problems[i])
	}
	check.Problems = kept
	return check
}
