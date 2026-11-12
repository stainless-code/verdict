package calibratecore

import (
	"fmt"
	"math"
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	judgeNone    = "none"
	judgeHuman   = "human"
	judgeAgent   = "agent"
	rateScale    = 10000
	percentScale = 100
)

func resolutionRate(fixed, judged int) any {
	if judged == 0 {
		return nil
	}
	return math.Round(float64(fixed)/float64(judged)*rateScale) / rateScale
}

func judgeOf(o *outcome) string {
	if o.result == outcomeUnknown {
		return judgeNone
	}
	history, ok := readcommon.FieldOf(o.entry, "history").([]any)
	if !ok {
		return threadJudge(o)
	}
	for _, event := range history {
		if readcommon.FieldOf(event, "event") == "revalidation" || readcommon.FieldOf(event, "actor") == "revalidator" {
			return judgeAgent
		}
	}
	return threadJudge(o)
}

func judgeCounts(outcomes []outcome) (*jq.Obj, string) {
	counts := map[string]int{judgeHuman: 0, judgeAgent: 0, "mixed": 0, judgeNone: 0}
	for i := range outcomes {
		counts[judgeOf(&outcomes[i])]++
	}
	judge := judgeNone
	if counts[judgeHuman] > 0 {
		judge = judgeHuman
	}
	if counts[judgeAgent] > 0 {
		judge = judgeAgent
	}
	if counts[judgeHuman] > 0 && counts[judgeAgent] > 0 {
		judge = "mixed"
	}
	return jq.NewObj(judgeHuman, counts[judgeHuman], judgeAgent, counts[judgeAgent], "mixed", 0, judgeNone, counts[judgeNone]), judge
}

func precisionCell(outcomes []outcome) *jq.Obj {
	p := countPrecision(outcomes)
	return jq.NewObj("fixed", p.fixed, "resolved_unfixed", p.resolvedUnfixed,
		"open", p.open, "unknown", p.unknown, "judged", p.judged,
		"resolution_rate", resolutionRate(p.fixed, p.judged))
}

func extendPrecision(doc *jq.Obj, outcomes []outcome) {
	byJudge, judge := judgeCounts(outcomes)
	doc.Set("judge", judge)
	doc.Set("by_judge", byJudge)
	severity := jq.NewObj()
	for _, tier := range []string{"critical", "important", "informational", "untagged"} {
		selected := severityOutcomes(outcomes, tier)
		severity.Set(tier, precisionCell(selected))
	}
	rules := ruleOutcomes(outcomes)
	byRule := jq.NewObj()
	keys := make([]string, 0, len(rules))
	for rule := range rules {
		keys = append(keys, rule)
	}
	slices.Sort(keys)
	for _, rule := range keys {
		byRule.Set(rule, precisionCell(rules[rule]))
	}
	doc.Set("by_severity", severity)
	doc.Set("by_rule", byRule)
}

func resolutionHuman(outcomes []outcome) string {
	p := countPrecision(outcomes)
	counts, judge := judgeCounts(outcomes)
	rate := "n/a"
	if p.judged > 0 {
		rate = fmt.Sprintf("%.1f%%", float64(p.fixed)/float64(p.judged)*percentScale)
	}
	human, _ := counts.Get(judgeHuman)
	agent, _ := counts.Get(judgeAgent)
	none, _ := counts.Get(judgeNone)
	return fmt.Sprintf("resolution: %d/%d (%s) — judge %s (human %v, agent %v, none %v)",
		p.fixed, p.judged, rate, judge, human, agent, none)
}

func ruleOutcomes(outcomes []outcome) map[string][]outcome {
	rules := map[string][]outcome{}
	for i := range outcomes {
		o := outcomes[i]

		for _, rule := range citedRules(o.kb) {
			rules[rule] = append(rules[rule], o)
		}
	}
	return rules
}

func threadJudge(o *outcome) string {
	if o.result == outcomeFixed || o.result == outcomeResolvedUnfixed {
		return judgeHuman
	}
	return judgeNone
}

func severityOutcomes(outcomes []outcome, tier string) []outcome {
	var selected []outcome
	for i := range outcomes {
		o := &outcomes[i]
		if o.sev == tier || (tier == "untagged" && o.sev == "") {
			selected = append(selected, *o)
		}
	}
	return selected
}

func citedRules(kb any) []string {
	citations, ok := kb.([]any)
	if !ok {
		return nil
	}
	var rules []string
	for _, citation := range citations {
		rule, ok := citation.(string)
		if ok {
			rules = append(rules, rule)
		}
	}
	slices.Sort(rules)
	return slices.Compact(rules)
}
