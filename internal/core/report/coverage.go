package reportcore

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Messages for the coverage read mode.
const (
	msgCoverageChunked = "coverage: chunked bundle (%d chunks) — use the chunks[] attestation"
	msgCoverageNoStore = "coverage: %d%% — no review.json yet"
	msgCoverageHuman   = "coverage: %d%% (%d/%d changed lines, %d files%s)"
	msgCoverageExempt  = ", %d generated exempt"
	msgCoverageUnread  = "unattested:"
	msgCoverageNone    = "unattested: (none)"
	msgCoverageNA      = "coverage: n/a — nothing changed to measure"
	msgCoverageBadJSON = "review.json is not readable JSON: %v"
)

// reviewSuffix names the agent's review artifact inside a bundle.
const reviewSuffix = "/review.json"

// RunNextCoverage executes `next --coverage`: a pure bundle read that reports
// how much of the changed diff the agent attested. It never loads
// findings.json, so it works before record and cannot be blocked by a missing
// store, and it never touches the network.
func RunNextCoverage(req *core.NextRequest) (*core.NextResponse, *core.RefusalError) {
	resp := &core.NextResponse{
		Repo:     req.Repo,
		Pr:       req.Pr,
		At:       req.TimeUTC(),
		Warnings: append([]string{}, req.Warnings...),
	}
	dir := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	review, ref := loadCoverageReview(dir)
	if ref != nil {
		return resp, ref
	}
	sum, warns, ref := coverage.Evaluate(dir, review, readcommon.Generated)
	resp.Warnings = append(resp.Warnings, warns...)
	if ref != nil {
		return resp, ref
	}
	resp.Coverage = coverageReport(&sum)
	resp.Human = coverageHuman(&sum, review == nil)
	return resp, nil
}

// loadCoverageReview reads review.json, treating an absent file as the normal
// pre-review state and refusing a malformed one instead of guessing.
func loadCoverageReview(dir string) (*jq.Obj, *core.RefusalError) {
	raw, err := os.ReadFile(dir + reviewSuffix)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &core.RefusalError{Code: codeReportFailed, Message: fmt.Sprintf("could not read %s%s: %v", dir, reviewSuffix, err)}
	}
	doc, derr := jq.DecodeOrdered(raw)
	if derr != nil {
		return nil, &core.RefusalError{Code: codeReportFailed, Message: fmt.Sprintf(msgCoverageBadJSON, derr)}
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return nil, &core.RefusalError{Code: codeReportFailed, Message: fmt.Sprintf(msgCoverageBadJSON, "not a JSON object")}
	}
	return obj, nil
}

// coverageReport converts the evaluator summary into the wire payload.
func coverageReport(sum *coverage.Summary) *core.CoverageReport {
	out := &core.CoverageReport{
		Percent:  sum.Percent,
		Attested: sum.Attested,
		Total:    sum.Total,
		Files:    sum.Files,
		Exempt:   sum.Exempt,
		Chunked:  sum.Chunked,
	}
	for _, g := range sum.Unattested {
		out.Unattested = append(out.Unattested, core.CoverageGap{File: g.File, Ranges: g.Ranges})
	}
	if out.Unattested == nil {
		out.Unattested = []core.CoverageGap{}
	}
	return out
}

// coverageHuman renders the two blocks: the summary line, then the gaps. A
// chunked bundle names its chunk count and stops — chunks[] is its attestation.
func coverageHuman(sum *coverage.Summary, noReview bool) string {
	var b strings.Builder
	switch {
	case sum.Chunked:
		return fmt.Sprintf(msgCoverageChunked, sum.Chunks)
	case sum.Percent == nil:
		return msgCoverageNA
	case noReview:
		fmt.Fprintf(&b, msgCoverageNoStore, *sum.Percent)
	default:
		exempt := ""
		if sum.Exempt > 0 {
			exempt = fmt.Sprintf(msgCoverageExempt, sum.Exempt)
		}
		fmt.Fprintf(&b, msgCoverageHuman, *sum.Percent, sum.Attested, sum.Total, sum.Files, exempt)
	}
	if len(sum.Unattested) == 0 {
		b.WriteString("\n" + msgCoverageNone)
		return b.String()
	}
	b.WriteString("\n" + msgCoverageUnread)
	for _, g := range sum.Unattested {
		b.WriteString("\n- " + g.File + ":" + g.Ranges)
	}
	return b.String()
}
