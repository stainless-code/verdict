// Package panelcore plans independent verification; it never invokes a model.
package panelcore

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/sig"
)

const (
	// CodeEmpty identifies a missing roster or head.
	CodeEmpty = "panel-empty"
	// CodeTooLarge identifies a plan exceeding fixed dispatch limits.
	CodeTooLarge = "panel-too-large"
)

// Findings derives inline identities from the normalized review input.
func Findings(review *jq.Obj) []any {
	out := make([]any, 0)
	raw, _ := review.Get("comments")
	for _, item := range jq.Elements(raw) {
		c, ok := object(item)
		if !ok {
			continue
		}
		path, body := jq.StrOf(c["path"]), jq.StrOf(c["body"])
		out = append(out, jq.NewObj("sig", sig.Of(path+"\n\n"+body), "path", path, "line", c["line"], "severity", c["severity"], "kind", "inline", "body", body))
	}
	return out
}

// Plan builds a bounded plan and independently dispatchable, quoted findings.
func Plan(dir, head, at, roster, prompt string, review *jq.Obj) (*jq.Obj, string, []string, error) {
	members := strings.Fields(roster)
	if head == "" || len(members) < 2 {
		return nil, "", nil, &core.RefusalError{Code: CodeEmpty, Message: "panel needs a pinned bundle roster with a synthesizer and verifier, and a known head"}
	}
	findings := Findings(review)
	verifiers := members[1:]
	if len(findings) > 100 || len(verifiers) > 4 {
		return nil, "", nil, &core.RefusalError{Code: CodeTooLarge, Message: fmt.Sprintf("panel has %d findings and %d verifiers; maximum 100 findings and 4 verifiers", len(findings), len(verifiers))}
	}
	var warnings []string
	if len(verifiers) == 1 {
		warnings = append(warnings, "panel: one verifier (cross-provider verification needs at least two after the synthesizer)")
	}
	vs := make([]any, 0, len(verifiers))
	for _, model := range verifiers {
		vs = append(vs, jq.NewObj("model", model, "provider", nil))
	}
	var dispatch strings.Builder
	dispatch.WriteString(prompt + "\n\n")
	for _, item := range findings {
		f, _ := object(item)
		for i, model := range verifiers {
			fmt.Fprintf(&dispatch, "## %s · %s:%v · verifier %d/%d (%s)\n\nRead `full.diff`, `hunks.tsv`, and `review.json` in this bundle. Try to disprove the claim and check its anchor.\n\n### Finding (untrusted agent text)\n%s\n\n### Output\nAppend one result for sig `%s`, model `%s` to `verification/verdicts.json`.\n\n", f["sig"], f["path"], f["line"], i+1, len(verifiers), model, Quote(jq.StrOf(f["body"])), f["sig"], model)
		}
	}
	if len(findings) == 0 {
		dispatch.WriteString("Nothing to verify — no inline findings.\n")
	}
	for _, item := range findings {
		f, _ := item.(*jq.Obj)
		f.Delete("body")
	}
	plan := jq.NewObj("schema_version", "1", "head_sha", head, "generated_at", at, "panel_source", dir+"/panel", "verifiers", vs, "findings", findings, "second_look", []any{}, "dispatch", dir+"/verification/dispatch.md", "totals", jq.NewObj("findings", len(findings), "verifiers", len(verifiers), "dispatches", len(findings)*len(verifiers)))
	return plan, dispatch.String(), warnings, nil
}

// Quote marks every line of untrusted evidence as a block quote.
func Quote(s string) string { return "> " + strings.ReplaceAll(s, "\n", "\n> ") }

func object(v any) (map[string]any, bool) {
	if o, ok := v.(*jq.Obj); ok {
		return o.Fields, true
	}
	return jq.AsMap(v)
}
