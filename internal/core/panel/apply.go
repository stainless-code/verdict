package panelcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	confirmed       = "confirmed"
	refuted         = "refuted"
	insufficient    = "insufficient"
	single          = "single"
	contested       = "contested"
	quoteKey        = "quote"
	quoteUnverified = "unverified"
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
	results := append([]any(nil), storedPanel(prior)...)
	for _, r := range file.Results {
		if r.Sig != id {
			continue
		}
		applied[r.Sig], models[r.Checker] = true, true
		results = mergeResult(results, &r)
	}
	applyVerification(entry, prior, results)
	entry.Set("confidence", confidence(results))
}

// applyVerification writes the merged panel back, keeping the quote standing
// the entry already carries (the evidence pass stamps it) so a panel run never
// invents a passing standing of its own.
func applyVerification(entry *jq.Obj, prior any, results []any) {
	if len(results) == 0 {
		return
	}
	record := jq.NewObj(quoteKey, quoteStanding(prior))
	record.Set("panel", results)
	entry.Set("verification", record)
}

// quoteStanding reads the standing already recorded on the entry, defaulting
// to unverified: a finding no quote check stamped is never a silent pass.
func quoteStanding(prior any) any {
	if old, ok := prior.(*jq.Obj); ok {
		if quote, _ := old.Get(quoteKey); quote != nil {
			return quote
		}
	}
	return quoteUnverified
}

func mergeResult(results []any, r *Result) []any {
	result := jq.NewObj("checker", r.Checker)
	if r.Model != "" {
		result.Set("model", r.Model)
	}
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
		if m["checker"] != r.Checker || (m[fieldVerdict] == refuted && r.Verdict != refuted) {
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

// storedPanel reads model results from the v2 verification object, while
// still accepting a legacy array of results.
func storedPanel(value any) []any {
	if obj, ok := object(value); ok {
		return jq.Elements(obj["panel"])
	}
	return jq.Elements(value)
}

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
	for _, value := range storedPanel(m["verification"]) {
		result, _ := object(value)
		if model, ok := result["checker"].(string); ok && model != "" {
			models[model] = true
		}
	}
}
