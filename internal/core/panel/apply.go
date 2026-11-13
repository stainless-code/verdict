package panelcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	confirmed    = "confirmed"
	refuted      = "refuted"
	insufficient = "insufficient"
	single       = "single"
	contested    = "contested"
)

// Apply enriches live findings while keeping refutations on record.
func Apply(entries []any, file *File) (*core.PanelSummary, []string) {
	if file == nil {
		return nil, nil
	}
	applied, models := make(map[string]bool), make(map[string]bool)
	for _, item := range entries {
		applyEntry(item, file, applied, models)
	}
	summary := Summarize(entries)
	if summary == nil {
		summary = &core.PanelSummary{}
	}
	summary.Verifiers = len(models)
	return summary, ignoredResults(file, applied)
}

func applyEntry(item any, file *File, applied, models map[string]bool) {
	entry, ok := item.(*jq.Obj)
	if !ok || !live(entry.Fields) {
		return
	}
	id, _ := entry.Get(fieldSig)
	prior, _ := entry.Get("verification")
	results := append([]any(nil), jq.Elements(prior)...)
	for _, r := range file.Results {
		if r.Sig != id {
			continue
		}
		applied[r.Sig], models[r.Model] = true, true
		results = mergeResult(results, &r)
	}
	if len(results) > 0 {
		entry.Set("verification", results)
	}
	entry.Set("confidence", confidence(results))
}

func mergeResult(results []any, r *Result) []any {
	result := jq.NewObj("model", r.Model)
	if r.Provider != "" {
		result.Set("provider", r.Provider)
	}
	result.Set(fieldVerdict, r.Verdict)
	result.Set("evidence", r.Evidence)
	if r.At != "" {
		result.Set("at", r.At)
	}
	for i, old := range results {
		m, _ := object(old)
		if m["model"] != r.Model || (m[fieldVerdict] == refuted && r.Verdict != refuted) {
			continue
		}
		results[i] = result
		return results
	}
	return append(results, result)
}

func ignoredResults(file *File, applied map[string]bool) []string {
	var warnings []string
	for _, r := range file.Results {
		if !applied[r.Sig] {
			warnings = append(warnings, "verification/verdicts.json: ignored result for non-inline or terminal finding "+r.Sig)
		}
	}
	return warnings
}

func confidence(results []any) string {
	state := single
	for _, r := range results {
		m, _ := object(r)
		if m[fieldVerdict] == refuted {
			return contested
		}
		if m[fieldVerdict] == confirmed {
			state = confirmed
		}
	}
	return state
}

func live(m map[string]any) bool { return m["status"] == "open" || m["status"] == "deferred" }

// Summarize returns live confidence counts, or nil for a legacy store.
func Summarize(entries []any) *core.PanelSummary {
	out := &core.PanelSummary{}
	for _, item := range entries {
		m, _ := object(item)
		if live(m) {
			countConfidence(out, m["confidence"])
		}
	}
	if out.Confirmed+out.Contested+out.Single == 0 {
		return nil
	}
	out.Verifiers = len(verifierModels(entries))
	return out
}

func countConfidence(out *core.PanelSummary, value any) {
	switch value {
	case confirmed:
		out.Confirmed++
	case contested:
		out.Contested++
	case single:
		out.Single++
	}
}

func verifierModels(entries []any) map[string]bool {
	models := make(map[string]bool)
	for _, item := range entries {
		m, _ := object(item)
		if !live(m) {
			continue
		}
		collectModels(m, models)
	}
	return models
}

func collectModels(m map[string]any, models map[string]bool) {
	for _, value := range jq.Elements(m["verification"]) {
		result, _ := object(value)
		if model, ok := result["model"].(string); ok && model != "" {
			models[model] = true
		}
	}
}
