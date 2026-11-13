package trendcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestRuleAudit pins the published reaction or rule contract.
func TestRuleAudit(t *testing.T) {
	st := &state{}
	finding := func(status string, ids ...any) any { return jq.NewObj("status", status, "kb", ids) }
	rows := []row{{findings: []any{finding("open", "Z"), finding("addressed", "A", "A"), finding("false-positive", "A"), finding("deferred", "A")}}}
	got := ruleAudit(st, rows).MarshalCompact()
	want := `{"A":{"cited":3,"accepted":1,"false_hits":1,"judged":2,"acceptance_rate":0.5,"false_hit_rate":0.5},"Z":{"cited":1,"accepted":0,"false_hits":0,"judged":0,"acceptance_rate":null,"false_hit_rate":null}}`
	if got != want {
		t.Fatalf("rules = %s; want %s", got, want)
	}
	rows[0].findings[0], rows[0].findings[3] = rows[0].findings[3], rows[0].findings[0]
	if got := ruleAudit(&state{}, rows).MarshalCompact(); got != want {
		t.Fatalf("reversed rules = %s", got)
	}
}
