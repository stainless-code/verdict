package spend

import (
	"math/big"
	"os"
	"path/filepath"
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
)

// Priced-by values a spawn receipt records: where its amount came from.
const (
	// PricedMeasured is the agent CLI's own reported cost.
	PricedMeasured = "measured"
	// PricedTokens is the CLI's reported token usage priced from the agent
	// catalog's dated price table.
	PricedTokens = "tokens"
	// PricedAgent is the agent's self-reported cost record.
	PricedAgent = "agent"
	// PricedProjected is the median of past measured spawns of the same agent
	// and model.
	PricedProjected = "projected"
	// PricedNotStarted is a launch whose process never started: zero.
	PricedNotStarted = "not-started"
)

// MedianMeasured is the projected price of one spawn: the median of every
// past spawn receipt under stateDir that the agent's own output priced
// (measured or tokens) for the same agent and model. It reads false when
// there is no such receipt — then the spawn's spend is unknown.
func MedianMeasured(stateDir, agent, model string) (string, bool) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return "", false
	}
	var amounts []*big.Rat
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		amounts = append(amounts, measuredSpawns(filepath.Join(stateDir, e.Name()), agent, model)...)
	}
	if len(amounts) == 0 {
		return "", false
	}
	slices.SortFunc(amounts, func(a, b *big.Rat) int { return a.Cmp(b) })
	mid := len(amounts) / mtdBigTwo
	if len(amounts)%mtdBigTwo == 1 {
		return FormatRat(amounts[mid]), true
	}
	sum := new(big.Rat).Add(amounts[mid-1], amounts[mid])
	return FormatRat(sum.Quo(sum, big.NewRat(mtdBigTwo, 1))), true
}

// measuredSpawns lists one bundle's measured spawn amounts for the agent and
// model, read the way the cost scan reads the ledger.
func measuredSpawns(bundleDir, agent, model string) []*big.Rat {
	raw := openBundleFile(bundleDir, "budget.json")
	if raw == nil {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	var out []*big.Rat
	for _, row := range sliceDoc(doc) {
		if rat, ok := measuredAmount(row, agent, model); ok {
			out = append(out, rat)
		}
	}
	return out
}

// measuredAmount reads one ledger row as a measured spawn of the agent and
// model: a spawn row priced by the agent's own output, with a known amount.
func measuredAmount(row any, agent, model string) (*big.Rat, bool) {
	obj, ok := row.(*jq.Obj)
	if !ok || stringFieldOf(obj, "scope") != ScopeSpawn {
		return nil, false
	}
	pricedBy := stringFieldOf(obj, "priced_by")
	if pricedBy != PricedMeasured && pricedBy != PricedTokens {
		return nil, false
	}
	if stringFieldOf(obj, "agent") != agent || stringFieldOf(obj, "model") != model {
		return nil, false
	}
	projected, _ := obj.Get("projected_usd")
	return ratOfJSONNumber(projected)
}
