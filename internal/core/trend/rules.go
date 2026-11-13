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
	for _, r := range rows {
		for _, finding := range r.findings {
			ids, _ := readcommon.FieldOf(finding, "kb").([]any)
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
				c := cells[id]
				c.cited++
				switch readcommon.StatusOf(finding) {
				case statusAddressed:
					c.accepted++
				case statusFalsePositive:
					c.falseHits++
				}
			}
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
		judged := c.accepted + c.falseHits
		var acceptance, falseRate any
		if judged > 0 {
			acceptance = float64(c.accepted) / float64(judged)
			falseRate = float64(c.falseHits) / float64(judged)
		}
		out.Set(id, jq.NewObj("cited", c.cited, "accepted", c.accepted, "false_hits", c.falseHits, "judged", judged, "acceptance_rate", acceptance, "false_hit_rate", falseRate))
		st.human(fmt.Sprintf("rule %s: accepted=%d false-hits=%d judged=%d cited=%d", id, c.accepted, c.falseHits, judged, c.cited))
	}
	return out
}
