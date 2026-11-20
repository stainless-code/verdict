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
// claim becomes body and finder confidence gets its store name. The quote
// verification standing is left untouched: the bundle evidence pass stamps
// verified or unverified from the diff bytes before anything is stored.
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

// SpecViolationsOfBundle reads the record receipt's stored spec checks and
// returns the item of every check it marked violated, in stored order. A
// missing or unreadable record has no new evidence to violate.
func SpecViolationsOfBundle(dir string) []string {
	raw, err := os.ReadFile(dir + readcommonRecordSuffix)
	if err != nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return nil
	}
	return SpecViolations(obj)
}

const readcommonRecordSuffix = "/record.json"

// HasSpecViolation reports a violated spec check in a review or record.
func HasSpecViolation(obj *jq.Obj) bool {
	return len(SpecViolations(obj)) > 0
}

// SpecViolations returns the item of every violated spec check in a review or
// record, in stored order: the decision withholds and the gate fails on the
// first, and the tally is the length.
func SpecViolations(obj *jq.Obj) []string {
	var out []string
	for _, item := range SpecChecks(obj) {
		m, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		if status, _ := m.Get("status"); jq.R(status) == statusViolated {
			text, _ := m.Get("item")
			out = append(out, jq.R(text))
		}
	}
	return out
}

// Render returns the minimal deterministic body. A nil approval is the
// pre-decision render the merge reads: the clean lead is written for a review
// that reports no findings, and AlignLead corrects it once the evidence pass
// and the policy have decided.
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

// The three leads the renderer writes. AlignLead swaps CleanLead (or
// AttentionLead when evidence is insufficient) so the body never certifies a
// review the decision refused.
const (
	CleanLead                = "> ✅ No new issues found.\n"
	AttentionLead            = "> ⚠️ Findings require attention.\n"
	EvidenceInsufficientLead = "> ⛔ Evidence checks insufficient — no approval.\n"
)

// AlignLead makes the rendered lead agree with the decision the merge
// computed. Evidence insufficiency is checked first and overrides both
// CleanLead and AttentionLead, prepending the lead when the body carries
// neither; a withheld non-insufficient decision swaps CleanLead for
// AttentionLead. Only the lead line moves; summary text is
// the agent's own words and a finding's claim is never rewritten.
func AlignLead(body string, approval *core.ApprovalReceipt) string {
	if approval == nil {
		return body
	}
	if approval.Insufficient {
		return insufficientLead(body)
	}
	if approval.Approved {
		return body
	}
	swapped, _ := replaceLeadLine(body, CleanLead, AttentionLead)
	return swapped
}

// insufficientLead makes an insufficient body start with the insufficient
// lead. A known lead at the head is swapped; a body whose summary is the
// agent's own quote line gets the lead prepended, because a body may never
// certify a review the decision refused.
func insufficientLead(body string) string {
	if strings.HasPrefix(body, EvidenceInsufficientLead) {
		return body
	}
	for _, lead := range []string{CleanLead, AttentionLead} {
		if swapped, ok := replaceLeadLine(body, lead, EvidenceInsufficientLead); ok &&
			strings.HasPrefix(swapped, EvidenceInsufficientLead) {
			return swapped
		}
	}
	return EvidenceInsufficientLead + body
}

func replaceLeadLine(body, oldLead, newLead string) (string, bool) {
	at := strings.Index(body, oldLead)
	if at < 0 || (at > 0 && body[at-1] != '\n') {
		return body, false
	}
	return body[:at] + newLead + body[at+len(oldLead):], true
}

// renderLead writes the verdict marker and the optional summary paragraph.
// Evidence insufficiency is checked first and always writes
// EvidenceInsufficientLead; the agent's summary is kept verbatim.
func renderLead(b *strings.Builder, review *jq.Obj, approval *core.ApprovalReceipt) {
	summary := Summary(review)
	switch {
	case approval != nil && approval.Insufficient:
		b.WriteString(EvidenceInsufficientLead)
	case !strings.HasPrefix(summary, "> "):
		b.WriteString(undecidedLead(review, approval))
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

// undecidedLead picks between the clean and attention leads for a summary
// that is not its own quote line: only a finding-free review a decision did
// not withhold earns the clean sentence.
func undecidedLead(review *jq.Obj, approval *core.ApprovalReceipt) string {
	if len(Findings(review)) == 0 && (approval == nil || approval.Approved) {
		return CleanLead
	}
	return AttentionLead
}

// renderFinding writes one numbered finding line: severity, anchor, claim.
// An unverified finding is labeled "unverified — not counted" without
// rewriting the agent's claim.
func renderFinding(b *strings.Builder, n int, m *jq.Obj) {
	path, _ := m.Get("path")
	line, _ := m.Get("line")
	severity, _ := m.Get("severity")
	claim, _ := m.Get("claim")
	if _, ok := claim.(string); !ok {
		claim, _ = m.Get("body")
	}
	if isUnverifiedFinding(m) {
		fmt.Fprintf(b, "%d. **%s** `%s:%s` (unverified — not counted) — %s\n",
			n, jq.R(severity), jq.R(path), jq.R(line), jq.R(claim))
		return
	}
	fmt.Fprintf(b, "%d. **%s** `%s:%s` — %s\n", n, jq.R(severity), jq.R(path), jq.R(line), jq.R(claim))
}

func isUnverifiedFinding(m *jq.Obj) bool {
	v, ok := m.Get("verification")
	if !ok {
		return false
	}
	rec, ok := v.(*jq.Obj)
	if !ok || rec == nil {
		return false
	}
	q, _ := rec.Get("quote")
	s, ok := q.(string)
	return ok && s == "unverified"
}
