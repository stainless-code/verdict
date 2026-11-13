package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestResultSizeBudgetKeepsSmallResults proves a result under the budget is
// byte-for-byte what it always was: the text block and structuredContent are
// the same bytes.
func TestResultSizeBudgetKeepsSmallResults(t *testing.T) {
	t.Parallel()
	small := successResult("report", jq.NewObj(
		"dir", ".verdict/state/5101",
		"note", strings.Repeat("x", maxResultTextBytes-1024),
	))
	if small.IsError {
		t.Fatal("under-limit result is an error")
	}
	if small.Content[0].Text != string(small.StructuredContent) {
		t.Error("under-limit text and structuredContent diverged")
	}
}

// TestResultSizeBudgetTruncatesLargeResults proves an oversized result keeps
// structuredContent whole while the text block becomes an explicit truncation
// summary pointing at the on-disk bundle — no silently clipped JSON.
func TestResultSizeBudgetTruncatesLargeResults(t *testing.T) {
	t.Parallel()
	over := successResult("report", jq.NewObj(
		"dir", ".verdict/state/5101",
		"findings", []any{strings.Repeat("x", maxResultTextBytes)},
	))
	if over.IsError {
		t.Fatal("truncated result must not be an error: structuredContent is complete")
	}
	if len(over.Content[0].Text) >= maxResultTextBytes {
		t.Errorf("text block = %d bytes, want under the %d budget",
			len(over.Content[0].Text), maxResultTextBytes)
	}
	assertTruncationSummary(t, over.Content[0].Text, ".verdict/state/5101", maxResultTextBytes+len(`{"dir":".verdict/state/5101","findings":[""]}`))

	var content struct {
		Reason     string   `json:"truncation_reason"`
		Total      *int     `json:"total"`
		NextOffset *int     `json:"next_offset"`
		Findings   []string `json:"findings"`
		Returned   int      `json:"returned"`
		Truncated  bool     `json:"truncated"`
	}
	if err := json.Unmarshal(over.StructuredContent, &content); err != nil {
		t.Fatalf("structuredContent: %v", err)
	}
	if !content.Truncated || content.Reason != "response_cap" || content.Returned != 0 || content.Total == nil || *content.Total != maxResultTextBytes+len(`{"dir":".verdict/state/5101","findings":[""]}`) || content.NextOffset != nil {
		t.Fatalf("structured receipt: truncated=%v reason=%q returned=%d total=%v next_offset=%v", content.Truncated, content.Reason, content.Returned, content.Total, content.NextOffset)
	}
	if len(content.Findings) != 1 || len(content.Findings[0]) != maxResultTextBytes {
		t.Error("structuredContent lost data")
	}
}

// TestResultSizeBudgetWithoutArtifact proves a verb that reports no bundle dir
// still gets the marker and a next step, just without an artifact path.
func TestResultSizeBudgetWithoutArtifact(t *testing.T) {
	t.Parallel()
	over := successResult("trend", jq.NewObj(
		"rows", []any{strings.Repeat("x", maxResultTextBytes)},
	))
	summary := decodeSummary(t, over.Content[0].Text)
	if _, ok := summary["artifact"]; ok {
		t.Errorf("artifact = %v, want none when the verb reports no dir", summary["artifact"])
	}
	if next, ok := summary["next"].(string); !ok || next == "" {
		t.Errorf("next = %v, want a next step", summary["next"])
	}
}

func assertTruncationSummary(t *testing.T, text, artifact string, full int) {
	t.Helper()
	summary := decodeSummary(t, text)
	want := map[string]any{
		"truncated":         true,
		"truncation_reason": "response_cap",
		"returned":          float64(0),
		"total":             float64(full),
		"next_offset":       nil,
		"tool":              "report",
		"bytes":             float64(full),
		"limit":             float64(maxResultTextBytes),
		"artifact":          artifact,
	}
	for key, value := range want {
		if summary[key] != value {
			t.Errorf("%s = %v, want %v", key, summary[key], value)
		}
	}
	if next, ok := summary["next"].(string); !ok || !strings.Contains(next, artifact) {
		t.Errorf("next = %v, want the on-disk artifact %q", summary["next"], artifact)
	}
}

func decodeSummary(t *testing.T, text string) map[string]any {
	t.Helper()
	var summary map[string]any
	if err := json.Unmarshal([]byte(text), &summary); err != nil {
		t.Fatalf("truncation summary is not JSON: %v", err)
	}
	return summary
}
