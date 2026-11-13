package panelcore

import (
	"fmt"
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Apply enriches live findings and preserves refutations across later confirmations.
func Apply(entries []any, file *File) (*core.VerificationSummary, []string) {
	if file == nil {
		return nil, nil
	}
	var warnings []string
	applied := make(map[string]bool)
	models := make(map[string]bool)
	for _, item := range entries {
		entry, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		id, _ := entry.Get("sig")
		status, _ := entry.Get("status")
		if status != "open" && status != "deferred" {
			continue
		}
		prior, _ := entry.Get("verification")
		results := append([]any(nil), jq.Elements(prior)...)
		for _, r := range file.Results {
			if r.Sig != id {
				continue
			}
			applied[r.Sig] = true
			models[r.Model] = true
			result := jq.NewObj("model", r.Model, "verdict", r.Verdict, "evidence", r.Evidence)
			if r.Provider != "" {
				result.Set("provider", r.Provider)
			}
			if r.At != "" {
				result.Set("at", r.At)
			}
			replaced := false
			for i, old := range results {
				m, ok := object(old)
				if !ok || m["model"] != r.Model {
					continue
				}
				if m["verdict"] == "refuted" && r.Verdict != "refuted" {
					continue
				}
				results[i] = result
				replaced = true
				break
			}
			if !replaced {
				results = append(results, result)
			}
		}
		if len(results) > 0 {
			entry.Set("verification", results)
		}
		entry.Set("confidence", confidence(results))
	}
	for _, r := range file.Results {
		if !applied[r.Sig] {
			warnings = append(warnings, fmt.Sprintf("verification/verdicts.json: ignored result for non-inline or terminal finding %s", r.Sig))
		}
	}
	summary := Summarize(entries)
	if summary == nil {
		summary = &core.VerificationSummary{}
	}
	summary.Verifiers = len(models)
	return summary, warnings
}
func confidence(results []any) string {
	state := "single"
	for _, r := range results {
		m, _ := object(r)
		if m["verdict"] == "refuted" {
			return "contested"
		}
		if m["verdict"] == "confirmed" {
			state = "confirmed"
		}
	}
	return state
}

// Summarize counts the stored confidence on live findings, returning nil for legacy stores.
func Summarize(entries []any) *core.VerificationSummary {
	var out *core.VerificationSummary
	for _, item := range entries {
		m, ok := object(item)
		if !ok || !slices.Contains([]any{"open", "deferred"}, m["status"]) {
			continue
		}
		c, ok := m["confidence"].(string)
		if !ok {
			continue
		}
		if out == nil {
			out = &core.VerificationSummary{}
		}
		switch c {
		case "confirmed":
			out.Confirmed++
		case "contested":
			out.Contested++
		case "single":
			out.Single++
		}
	}
	return out
}
