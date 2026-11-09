package recordcore

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Messages for the coverage surface. The percentage and the gap list are
// always computed, never composed from finding text.
const (
	msgCoverageWarn   = "coverage: %d%% of " + coverage.Basis + " attested (%d/%d; %d file(s) with gaps)"
	msgCoverageLine   = "> ⚠️ coverage: %d%% of changed lines attested (unattested: %s)"
	msgCoverageAbsent = "coverage-incomplete: no coverage[] attestation in review.json on a non-chunked bundle"
	msgCoverageGap    = "coverage-incomplete: %d%% of " + msgCoverageBasis + " attested (%d/%d); unattested: %s"
	msgCoverageBasis  = coverage.Basis
	msgCoverageMore   = " +%d more"
	// lineGapCap is how many files the body line names before it summarizes.
	lineGapCap = 3
	// msgGapCap is how many files a refusal names before it summarizes.
	msgGapCap = 5
)

// evaluateCoverage runs the shared evaluator for this merge and applies the
// opt-in gate. The refusal is raised here, before any store write, so a refused
// run leaves the prior findings.json untouched.
func (st *state) evaluateCoverage() error {
	sum, warns, ref := coverage.Evaluate(st.dir, st.review, readcommon.Generated)
	for _, w := range warns {
		st.warn(w)
	}
	if sum.Chunked && (st.requireCoverage || coverage.Present(st.review)) {
		st.warn(coverage.ChunkWarning)
	}
	if ref != nil {
		// The evaluator's refusal is the contract: its code and hint are what the
		// caller matches. Sealing it here would erase both.
		return ref //nolint:wrapcheck // typed refusal passes through unchanged
	}
	st.coverage = &sum
	if sum.Chunked || !st.requireCoverage {
		return nil
	}
	return st.coverageGate(&sum)
}

// coverageGate is the opt-in requirement itself, kept apart from the evaluator
// bookkeeping so each reads as the rule it enforces.
func (st *state) coverageGate(sum *coverage.Summary) error {
	if sum.Total == 0 {
		// Every changed file is exempt (generated or vendored) or there is
		// nothing to measure: nothing to attest, so nothing to require.
		return nil
	}
	if !sum.Present {
		return &core.RefusalError{Code: coverage.CodeIncomplete, Message: msgCoverageAbsent}
	}
	if !sum.Complete() {
		return &core.RefusalError{Code: coverage.CodeIncomplete, Message: coverageGapMessage(sum)}
	}
	return nil
}

// coverageGapMessage names the unattested ranges, capped, so the operator can
// act without running another verb.
func coverageGapMessage(sum *coverage.Summary) string {
	percent := 0
	if sum.Percent != nil {
		percent = *sum.Percent
	}
	return fmt.Sprintf(msgCoverageGap, percent, sum.Attested, sum.Total, gapList(sum, msgGapCap))
}

// gapList renders "src/a.ts:1-2, src/b.ts:9" from the summary's gaps, naming at
// most limit files and summarizing the rest.
func gapList(sum *coverage.Summary, limit int) string {
	var parts []string
	for i, g := range sum.Unattested {
		if i == limit {
			break
		}
		parts = append(parts, g.File+":"+g.Ranges)
	}
	out := strings.Join(parts, ", ")
	if rest := len(sum.Unattested) - limit; rest > 0 {
		out += fmt.Sprintf(msgCoverageMore, rest)
	}
	return out
}

// applyCoverageLine inserts the one machine-built coverage line before the
// footer. An absent attestation and a complete one add nothing, so a legacy
// review stays byte-identical and a full one stays clean.
func (st *state) applyCoverageLine(body string) string {
	sum := st.coverage
	if sum == nil || sum.Chunked || !sum.Present || sum.Complete() || !sum.Measured() {
		return body
	}
	percent := 0
	if sum.Percent != nil {
		percent = *sum.Percent
	}
	line := fmt.Sprintf(msgCoverageLine, percent, gapList(sum, lineGapCap))
	return body + "\n\n" + line + "\n"
}

// reportCoverage warns the human summary once per merge: the same numbers the
// body line and the stored receipt carry.
func (st *state) reportCoverage() {
	sum := st.coverage
	if sum == nil || sum.Chunked || !sum.Present || sum.Complete() || !sum.Measured() {
		return
	}
	percent := 0
	if sum.Percent != nil {
		percent = *sum.Percent
	}
	st.warn(fmt.Sprintf(msgCoverageWarn, percent, sum.Attested, sum.Total, len(sum.Unattested)))
}
