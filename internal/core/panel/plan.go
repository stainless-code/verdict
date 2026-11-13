// Package panelcore plans independent verification; it never invokes a
// model.
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
	CodeTooLarge  = "panel-too-large"
	maxFindings   = 100
	maxVerifiers  = 4
	fieldBody     = "body"
	fieldSig      = "sig"
	fieldPath     = "path"
	fieldVerdict  = "verdict"
	fieldLine     = "line"
	fieldSeverity = "severity"
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
		path, body := jq.StrOf(c[fieldPath]), jq.StrOf(c[fieldBody])
		out = append(out, jq.NewObj(fieldSig, sig.Of(path+"\n\n"+body), fieldPath, path,
			fieldLine, c[fieldLine], fieldSeverity, c[fieldSeverity], "kind", "inline", fieldBody, body))
	}
	return out
}

// Plan builds a bounded plan with independently dispatchable findings.
func Plan(dir, head, at, roster, prompt string, review *jq.Obj) (*jq.Obj, string, []string, error) {
	members := strings.Fields(roster)
	if head == "" || len(members) < 2 {
		return nil, "", nil, &core.RefusalError{
			Code: CodeEmpty, Message: "panel needs a pinned roster with a synthesizer and verifier, and a known head",
		}
	}
	findings, verifiers := Findings(review), members[1:]
	if len(findings) > maxFindings || len(verifiers) > maxVerifiers {
		return nil, "", nil, &core.RefusalError{
			Code: CodeTooLarge, Message: fmt.Sprintf("panel has %d findings and %d verifiers; maximum 100 findings and 4 verifiers",
				len(findings), len(verifiers)),
		}
	}
	plan := planDocument(dir, head, at, findings, verifiers)
	return plan, dispatchDocument(prompt, findings, verifiers), rosterWarnings(verifiers), nil
}

func rosterWarnings(verifiers []string) []string {
	if len(verifiers) == 1 {
		return []string{"panel: one verifier (cross-provider verification needs at least two after the synthesizer)"}
	}
	return nil
}

func planDocument(dir, head, at string, findings []any, verifiers []string) *jq.Obj {
	vs := make([]any, 0, len(verifiers))
	for _, model := range verifiers {
		vs = append(vs, jq.NewObj("model", model, "provider", nil))
	}
	fs := make([]any, 0, len(findings))
	for _, item := range findings {
		f, _ := object(item)
		fs = append(fs, jq.NewObj(fieldSig, f[fieldSig], fieldPath, f[fieldPath], fieldLine, f[fieldLine],
			fieldSeverity, f[fieldSeverity], "kind", "inline"))
	}
	return jq.NewObj("schema_version", "1", "head_sha", head, "generated_at", at, "panel_source", dir+"/panel",
		"verifiers", vs, "findings", fs, "second_look", []any{}, "dispatch", dir+"/verification/dispatch.md",
		"totals", jq.NewObj("findings", len(findings), "verifiers", len(verifiers), "dispatches", len(findings)*len(verifiers)))
}

func dispatchDocument(prompt string, findings []any, verifiers []string) string {
	var out strings.Builder
	out.WriteString(prompt + "\n\n")
	for _, item := range findings {
		f, _ := object(item)
		for i, model := range verifiers {
			out.WriteString(dispatchSection(f, model, i, len(verifiers)))
		}
	}
	if len(findings) == 0 {
		out.WriteString("Nothing to verify — no inline findings.\n")
	}
	return out.String()
}

func dispatchSection(f map[string]any, model string, i, n int) string {
	const section = "## %s · %q:%v · %v · verifier %d/%d (%s)\n\n" +
		"Read `full.diff`, `hunks.tsv`, and `review.json` in this bundle. Try to disprove the claim and check its anchor.\n\n" +
		"### Finding (untrusted agent text)\n%s\n\n### Output\n" +
		"Append one result for sig `%s`, model `%s` to `verification/verdicts.json`.\n\n"
	return fmt.Sprintf(section, f[fieldSig], f[fieldPath], f[fieldLine], f[fieldSeverity], i+1, n, model,
		Quote(jq.StrOf(f[fieldBody])), f[fieldSig], model)
}

// Quote marks every line of untrusted evidence as a block quote.
func Quote(s string) string { return "> " + strings.ReplaceAll(s, "\n", "\n> ") }

func object(v any) (map[string]any, bool) {
	if o, ok := v.(*jq.Obj); ok {
		return o.Fields, true
	}
	return jq.AsMap(v)
}
