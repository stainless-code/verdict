package agentusage

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/stainless-code/verdict/internal/spend"
)

// OpenCode's `run --format json` event names.
const (
	openCodeStepFinish = "step_finish"
	openCodeTextEvent  = "text"
)

// openCodeCost sums every step_finish event's part.cost: OpenCode prices each
// assistant step itself. A step that carries no cost leaves the run unpriced
// rather than reading as free.
func openCodeCost(kept [][]byte) (Measurement, bool) {
	objs := objects(kept)
	total := new(big.Rat)
	steps := 0
	for _, obj := range objs {
		cost, isStep, ok := stepCost(obj)
		if !isStep {
			continue
		}
		if !ok {
			return Measurement{}, false
		}
		total.Add(total, cost)
		steps++
	}
	if steps == 0 {
		return Measurement{}, false
	}
	return Measurement{USD: spend.FormatRat(total), PricedBy: spend.PricedMeasured}, true
}

// stepCost reads one event as a step_finish: whether it is one, and its
// part.cost when that is a valid amount.
func stepCost(obj map[string]json.RawMessage) (*big.Rat, bool, bool) {
	if kind, ok := str(obj, "type"); !ok || kind != openCodeStepFinish {
		return nil, false, false
	}
	part, ok := child(obj, "part")
	if !ok {
		return nil, true, false
	}
	cost, ok := amount(part, "cost")
	return cost, true, ok
}

// openCodeText joins the text events' deltas — each text event carries the
// part's new text only — in the order the parts first appeared.
func openCodeText(kept [][]byte) (string, bool) {
	var order []string
	parts := map[string]*strings.Builder{}
	for _, obj := range objects(kept) {
		id, text, ok := textDelta(obj)
		if !ok {
			continue
		}
		if _, seen := parts[id]; !seen {
			parts[id] = &strings.Builder{}
			order = append(order, id)
		}
		parts[id].WriteString(text)
	}
	if len(order) == 0 {
		return "", false
	}
	texts := make([]string, 0, len(order))
	for _, id := range order {
		texts = append(texts, parts[id].String())
	}
	return strings.Join(texts, "\n"), true
}

// textDelta reads one text event's part id and new text.
func textDelta(obj map[string]json.RawMessage) (string, string, bool) {
	if kind, ok := str(obj, "type"); !ok || kind != openCodeTextEvent {
		return "", "", false
	}
	part, ok := child(obj, "part")
	if !ok {
		return "", "", false
	}
	id, okID := str(part, "id")
	text, okText := str(part, "text")
	return id, text, okID && okText
}
