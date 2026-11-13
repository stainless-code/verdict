package trendcore

import (
	"fmt"
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type ruleCell struct{ cited, accepted, falseHits int }

func ruleAudit(st *state, rows []row) *jq.Obj {
	cells := map[string]*ruleCell{}
	for i := range rows {
		for _, finding := range rows[i].findings {
			tallyRuleFinding(cells, finding)
		}
	}
	keys := make([]string, 0, len(cells))
	for k := range cells {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := jq.NewObj()
	for _, id := range keys {
		c := cells[id]
		out.Set(id, c.wire())
		st.human(fmt.Sprintf("rule %s: accepted=%d false-hits=%d judged=%d cited=%d",
			id, c.accepted, c.falseHits, c.accepted+c.falseHits, c.cited))
	}
	return out
}

func (c *ruleCell) wire() *jq.Obj {
	judged := c.accepted + c.falseHits
	var acceptance, falseRate any
	if judged > 0 {
		acceptance = float64(c.accepted) / float64(judged)
		falseRate = float64(c.falseHits) / float64(judged)
	}
	return jq.NewObj("cited", c.cited, "accepted", c.accepted, "false_hits", c.falseHits,
		"judged", judged, "acceptance_rate", acceptance, "false_hit_rate", falseRate)
}

func tallyRuleFinding(cells map[string]*ruleCell, finding any) {
	ids, ok := readcommon.FieldOf(finding, "kb").([]any)
	if !ok {
		return
	}
	seen := map[string]bool{}
	for _, raw := range ids {
		id, ok := raw.(string)
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if cells[id] == nil {
			cells[id] = &ruleCell{}
		}
		cells[id].countFinding(finding)
	}
}

func (c *ruleCell) countFinding(finding any) {
	c.cited++
	switch readcommon.StatusOf(finding) {
	case statusAddressed:
		c.accepted++
	case statusFalsePositive:
		c.falseHits++
	}
}
