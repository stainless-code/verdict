package calibratecore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestCountPrecisionByCategory proves the live join buckets outcomes by the
// finding's category: published labels keep their bucket, null reads
// untagged, and a zero judged denominator renders null instead of zero.
func TestCountPrecisionByCategory(t *testing.T) {
	t.Parallel()
	outcomes := []outcome{
		{result: outcomeFixed, cat: "security", sev: "critical"},
		{result: statusOpen, cat: "security", sev: "critical"},
		{result: outcomeUnknown, cat: "untagged", sev: "untagged"},
	}
	p := countPrecision(outcomes)
	sec, ok := p.byCategory["security"]
	if !ok {
		t.Fatalf("by_category = %v, want a security bucket", p.byCategory)
	}
	if sec.fixed != 1 || sec.open != 1 || sec.judged() != 2 {
		t.Errorf("security cell = %+v, want fixed=1 open=1 judged=2", sec)
	}
	unt, ok := p.byCategory["untagged"]
	if !ok {
		t.Fatalf("by_category = %v, want an untagged bucket", p.byCategory)
	}
	if unt.unknown != 1 || unt.judged() != 0 {
		t.Errorf("untagged cell = %+v, want unknown=1 judged=0", unt)
	}
	if len(p.byCategory) != 2 {
		t.Errorf("by_category has %d buckets, want only the observed two", len(p.byCategory))
	}
}

// TestLiveWireByCategory proves the wire renders each cell with judged and a
// null zero-denominator rate.
func TestLiveWireByCategory(t *testing.T) {
	t.Parallel()
	outcomes := []outcome{
		{result: outcomeFixed, cat: "security", sev: "critical"},
		{result: statusOpen, cat: "security", sev: "critical"},
		{result: outcomeUnknown, cat: "untagged", sev: "untagged"},
	}
	live := &liveResult{reviewID: "1", head0: "aaaaaaa", live: map[string]any{}, outcomes: outcomes, dir: t.TempDir()}
	precision, _ := live.Wire().Get("precision")
	prec, ok := precision.(*jq.Obj)
	if !ok || prec == nil {
		t.Fatalf("precision = %#v, want an ordered object", precision)
	}
	byCat, _ := prec.Get("by_category")
	cats, ok := byCat.(*jq.Obj)
	if !ok || cats == nil {
		t.Fatalf("by_category = %#v, want an ordered object", byCat)
	}
	checkFixedRateCell(t, cats, "security", 0.5, 2)
	checkNullRateCell(t, cats)
}

// checkFixedRateCell proves one judged bucket renders its fixed rate.
func checkFixedRateCell(t *testing.T, cats *jq.Obj, name string, rate float64, judged int) {
	t.Helper()
	doc, _ := cats.Get(name)
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		t.Fatalf("%s cell = %#v, want an ordered object", name, doc)
	}
	if got, _ := obj.Get("resolution_rate"); got != rate {
		t.Errorf("%s resolution_rate = %v, want %v", name, got, rate)
	}
	if got, _ := obj.Get("judged"); got != judged {
		t.Errorf("%s judged = %v, want %d", name, got, judged)
	}
}

// TestOutcomeCategory proves the audit row carries the label, or null when
// the finding is untagged.
func TestOutcomeCategory(t *testing.T) {
	t.Parallel()
	outcomesWithLabel := []outcome{{result: statusOpen, cat: "security", sev: "critical", status: "open"}}
	doc := outcomeDoc(&outcomesWithLabel[0])
	if cat, _ := doc.Get("category"); cat != "security" {
		t.Errorf("outcome category = %#v, want the label", cat)
	}
	outcomesBare := []outcome{{result: outcomeUnknown, cat: "untagged", sev: "untagged", status: "open"}}
	if cat, _ := outcomeDoc(&outcomesBare[0]).Get("category"); cat != nil {
		t.Errorf("untagged outcome category = %#v, want null", cat)
	}
}

// checkNullRateCell proves the zero-denominator bucket renders a null rate.
func checkNullRateCell(t *testing.T, cats *jq.Obj) {
	t.Helper()
	untDoc, _ := cats.Get("untagged")
	untObj, ok := untDoc.(*jq.Obj)
	if !ok || untObj == nil {
		t.Fatalf("untagged cell = %#v, want an ordered object", untDoc)
	}
	if rate, _ := untObj.Get("resolution_rate"); rate != nil {
		t.Errorf("untagged resolution_rate = %v, want null on a zero denominator", rate)
	}
}
