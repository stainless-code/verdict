// Package reviewdoc maps the findings-only review document to the internal
// merge shape and renders the minimal deterministic body S3 will replace.
package reviewdoc

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Prepare rewrites one checked review document into the internal merge shape:
// claim becomes body, finder confidence gets its store name, and every
// finding reserves the quote verification seam with unchecked.
func Prepare(review *jq.Obj) {
	for _, item := range Findings(review) {
		if m, ok := item.(*jq.Obj); ok {
			prepareFinding(m)
		}
	}
}

// prepareFinding maps one checked finding to its store keys.
func prepareFinding(m *jq.Obj) {
	if claim, has := m.Get("claim"); has {
		m.Set("body", claim)
	}
	if confidence, has := m.Get("confidence"); has {
		m.Set("finder_confidence", confidence)
	}
	m.Set("verification", jq.NewObj("quote", "unchecked"))
}

// Summary returns the optional human summary. It is never agent-controlled
// body markup: the renderer escapes nothing and renders it as one paragraph.
func Summary(review *jq.Obj) string {
	if review == nil {
		return ""
	}
	value, _ := review.Get("summary")
	if value == nil {
		return ""
	}
	return jq.R(value)
}

// Findings returns the raw findings array.
func Findings(review *jq.Obj) []any {
	return rawArray(review, "findings")
}

// SpecChecks returns the raw spec-check array.
func SpecChecks(review *jq.Obj) []any {
	return rawArray(review, "spec_checks")
}

// Receipts returns the raw receipt array.
func Receipts(review *jq.Obj) []any {
	return rawArray(review, "receipts")
}

// rawArray returns one top-level array, or nothing when it is absent or
// carries another type: the checker owns the refusal for either.
func rawArray(review *jq.Obj, key string) []any {
	if review == nil {
		return nil
	}
	value, _ := review.Get(key)
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}

// SpecChecksTyped decodes the raw spec checks into the stored shape.
func SpecChecksTyped(review *jq.Obj) []core.SpecCheck {
	return decodeSlice[core.SpecCheck](SpecChecks(review))
}

// ReceiptsTyped decodes the raw receipts into the stored shape.
func ReceiptsTyped(review *jq.Obj) []core.Receipt {
	return decodeSlice[core.Receipt](Receipts(review))
}

func decodeSlice[T any](items []any) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		if value, ok := decodeOne[T](item); ok {
			out = append(out, value)
		}
	}
	return out
}

// decodeOne decodes one array entry, reporting whether it took the value.
func decodeOne[T any](item any) (T, bool) {
	var value T
	raw, ok := rawJSON(item)
	if !ok {
		return value, false
	}
	return value, json.Unmarshal(raw, &value) == nil
}

// rawJSON encodes one array entry: an ordered object keeps its byte order.
func rawJSON(item any) ([]byte, bool) {
	if obj, ok := item.(*jq.Obj); ok {
		return []byte(obj.MarshalCompact()), true
	}
	raw, err := json.Marshal(item)
	return raw, err == nil
}

// SpecViolationOfBundle reads the record receipt's stored spec checks.
// A missing or unreadable record has no new evidence to violate.
func SpecViolationOfBundle(dir string) bool {
	raw, err := os.ReadFile(dir + readcommonRecordSuffix)
	if err != nil {
		return false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return false
	}
	return HasSpecViolation(obj)
}

const readcommonRecordSuffix = "/record.json"

// HasSpecViolation reports a violated spec check in a review or record.
func HasSpecViolation(obj *jq.Obj) bool {
	for _, item := range SpecChecks(obj) {
		m, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		if status, _ := m.Get("status"); jq.R(status) == "violated" {
			return true
		}
	}
	return false
}

// Render returns the minimal deterministic body. S3 replaces this renderer;
// S1 keeps the existing approval markers so consumers need no new grammar.
func Render(review *jq.Obj, approval *core.ApprovalReceipt) string {
	var b strings.Builder
	renderLead(&b, review, approval)
	items := Findings(review)
	if len(items) == 0 {
		return b.String()
	}
	b.WriteString("\n")
	for i, item := range items {
		if m, ok := item.(*jq.Obj); ok {
			renderFinding(&b, i+1, m)
		}
	}
	return b.String()
}

// The two leads the renderer writes. AlignLead swaps the clean one for the
// attention one when the authoritative decision withholds approval, so the
// body never certifies a review the decision refused.
const (
	CleanLead     = "> ✅ No new issues found.\n"
	AttentionLead = "> ⚠️ Findings require attention.\n"
)

// AlignLead makes the rendered lead agree with the decision the merge
// computed. The renderer runs before the merge, because its findings are the
// merge input, so it cannot know about stored findings, block clauses or a
// violated spec check: a clean marker must not survive a withheld decision.
// Only the lead line moves; summary text is the agent's own words and a
// finding's claim is never rewritten.
func AlignLead(body string, approval *core.ApprovalReceipt) string {
	if approval == nil || approval.Approved {
		return body
	}
	at := strings.Index(body, CleanLead)
	if at < 0 || (at > 0 && body[at-1] != '\n') {
		return body
	}
	return body[:at] + AttentionLead + body[at+len(CleanLead):]
}

// renderLead writes the verdict marker and the optional summary paragraph.
func renderLead(b *strings.Builder, review *jq.Obj, approval *core.ApprovalReceipt) {
	summary := Summary(review)
	if !strings.HasPrefix(summary, "> ") {
		if len(Findings(review)) == 0 && approval != nil && approval.Approved {
			b.WriteString(CleanLead)
		} else {
			b.WriteString(AttentionLead)
		}
	}
	if summary == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(summary)
	b.WriteString("\n")
}

// renderFinding writes one numbered finding line: severity, anchor, claim.
func renderFinding(b *strings.Builder, n int, m *jq.Obj) {
	path, _ := m.Get("path")
	line, _ := m.Get("line")
	severity, _ := m.Get("severity")
	claim, _ := m.Get("claim")
	if _, ok := claim.(string); !ok {
		claim, _ = m.Get("body")
	}
	fmt.Fprintf(b, "%d. **%s** `%s:%s` — %s\n", n, jq.R(severity), jq.R(path), jq.R(line), jq.R(claim))
}
