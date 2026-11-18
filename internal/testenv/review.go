package testenv

import (
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// FindingsBytes converts a legacy review fixture for checker tests.
func FindingsBytes(raw []byte) []byte {
	return []byte(FindingsReview(string(raw)))
}

// MaybeFindingsReview converts only a review-fixture path's content.
func MaybeFindingsReview(path, content string) string {
	if filepath.Base(path) == "review.json" {
		return FindingsReview(content)
	}
	return content
}

// carriedFindingKeys are the legacy comment keys that read through untouched.
var carriedFindingKeys = []string{
	"path", "side", "rigor", "suggestion", "brief", "category", "sources", "kb",
}

// carriedReviewKeys are the optional top-level arrays a converted document
// keeps when the legacy review carried them.
var carriedReviewKeys = []string{
	"resolved", "triage", "coverage", "chunks", "suppressed",
	"risk", "security_summary", "spec_checks", "receipts",
}

// testSummaryCap mirrors the published summary cap, so a converted summary is
// always inside the contract it is fed to.
const testSummaryCap = 2000

// FindingsReview converts a legacy review fixture into the findings-only
// review shape. It is a test helper only: production code reads findings and
// refuses a document that has none.
func FindingsReview(raw string) string {
	obj := legacyReview(raw)
	if obj == nil {
		return raw
	}
	out := jq.NewObj("schema_version", "1", "findings", convertedFindings(obj))
	if summary := plainSummary(jq.R(mustGet(obj, "body"))); summary != "" {
		out.Set("summary", capRunes(summary))
	}
	for _, key := range carriedReviewKeys {
		if value, has := obj.Get(key); has && value != nil {
			out.Set(key, value)
		}
	}
	return out.MarshalPretty()
}

// legacyReview decodes a document that still needs the conversion, or nil.
// Only a document that carries a legacy body or comments converts: anything
// else reaches the checker byte for byte, so a fixture missing the findings
// array is refused rather than silently given an empty one.
func legacyReview(raw string) *jq.Obj {
	doc, err := jq.DecodeOrdered([]byte(raw))
	if err != nil {
		return nil
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj.Has("findings") {
		return nil
	}
	if !obj.Has("body") && !obj.Has("comments") {
		return nil
	}
	return obj
}

// convertedFindings maps every legacy comment to one finding.
func convertedFindings(obj *jq.Obj) []any {
	findings := make([]any, 0)
	for _, item := range jq.Elements(mustGet(obj, "comments")) {
		if comment, ok := item.(*jq.Obj); ok {
			findings = append(findings, convertedFinding(comment))
		}
	}
	return findings
}

// convertedFinding maps one legacy comment, filling the fields the published
// finding shape requires and the legacy shape never carried.
func convertedFinding(comment *jq.Obj) *jq.Obj {
	finding := jq.NewObj()
	for _, key := range carriedFindingKeys {
		if value, has := comment.Get(key); has {
			finding.Set(key, value)
		}
	}
	finding.Set("line", lineOf(comment))
	if start, ok := jq.IntOf(mustGet(comment, "start_line")); ok {
		finding.Set("start_line", start)
	}
	claim := claimOf(comment)
	finding.Set("claim", claim)
	finding.Set("quote", claim)
	finding.Set("confidence", "high")
	finding.Set("severity", severityOf(comment))
	return finding
}

// claimOf reads the legacy body as the claim, with a stable placeholder so the
// required field is never empty.
func claimOf(comment *jq.Obj) string {
	if claim := jq.R(mustGet(comment, "body")); claim != "" {
		return claim
	}
	return "converted finding"
}

// severityOf reads the legacy severity, defaulting to the middle tier.
func severityOf(comment *jq.Obj) any {
	if severity, has := comment.Get("severity"); has && severity != nil {
		return severity
	}
	return "important"
}

// lineOf reads the legacy anchor, defaulting to the first line.
func lineOf(comment *jq.Obj) int {
	if line, ok := jq.IntOf(mustGet(comment, "line")); ok {
		return line
	}
	return 1
}

// capRunes truncates one summary to the published cap.
func capRunes(summary string) string {
	runes := []rune(summary)
	if len(runes) > testSummaryCap {
		runes = runes[:testSummaryCap]
	}
	return string(runes)
}

func plainSummary(body string) string {
	return strings.TrimSpace(body)
}

func mustGet(obj *jq.Obj, key string) any {
	value, _ := obj.Get(key)
	return value
}
