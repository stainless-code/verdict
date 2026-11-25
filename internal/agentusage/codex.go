package agentusage

import (
	"encoding/json"
	"math/big"
	"slices"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/spend"
)

// Codex's `exec --json` event names.
const (
	codexTurnCompleted = "turn.completed"
	codexItemCompleted = "item.completed"
	codexAgentMessage  = "agent_message"
)

// tokensPerPrice is the token count a catalog price is quoted for.
const tokensPerPrice = 1_000_000

// codexCost prices the last turn.completed event's usage. Codex fills that
// usage from the thread's running total, so the last one is the run's whole
// usage and summing turns would count earlier turns twice. The stream names
// no model, so only a model the launch named and the price table carries can
// be priced.
func codexCost(out []byte, model string) (Measurement, bool) {
	objs, complete := objects(out)
	usage, ok := lastCodexUsage(objs)
	if !ok || !complete || model == "" {
		return Measurement{}, false
	}
	price, ok := agentcatalog.PriceFor(model)
	if !ok {
		return Measurement{}, false
	}
	tokens, ok := codexTokens(usage)
	if !ok {
		return Measurement{}, false
	}
	// Cached input is a subset of input, and reasoning output a subset of
	// output, as the API bills them.
	usd := new(big.Rat)
	usd.Add(usd, perMillion(new(big.Rat).Sub(tokens.input, tokens.cached), price.InputUSD))
	usd.Add(usd, perMillion(tokens.cached, price.CachedInputUSD))
	usd.Add(usd, perMillion(tokens.output, price.OutputUSD))
	return Measurement{USD: spend.FormatRat(usd), PricedBy: spend.PricedTokens, Model: model}, true
}

// codexUsage is one validated usage object's token counts.
type codexUsage struct{ input, cached, output *big.Rat }

// codexTokens validates a usage object the table can price: whole,
// nonnegative counts, cached within input, no prompt-cache writes, and not
// the all-zero report Codex emits when no token update arrived.
func codexTokens(usage map[string]json.RawMessage) (codexUsage, bool) {
	input, okIn := count(usage, "input_tokens")
	cached, okCached := count(usage, "cached_input_tokens")
	output, okOut := count(usage, "output_tokens")
	if !okIn || !okCached || !okOut || cached.Cmp(input) > 0 {
		return codexUsage{}, false
	}
	if writes, ok := count(usage, "cache_write_input_tokens"); ok && writes.Sign() > 0 {
		return codexUsage{}, false
	}
	if input.Sign() == 0 && output.Sign() == 0 {
		return codexUsage{}, false
	}
	return codexUsage{input: input, cached: cached, output: output}, true
}

// perMillion is tokens times a per-million-token price literal.
func perMillion(tokens *big.Rat, priceUSD string) *big.Rat {
	rate, ok := new(big.Rat).SetString(priceUSD)
	if !ok {
		return new(big.Rat)
	}
	cost := new(big.Rat).Mul(tokens, rate)
	return cost.Quo(cost, big.NewRat(tokensPerPrice, 1))
}

// lastCodexUsage is the usage object of the stream's last turn.completed.
func lastCodexUsage(objs []map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	for _, obj := range slices.Backward(objs) {
		if kind, ok := str(obj, "type"); ok && kind == codexTurnCompleted {
			return child(obj, "usage")
		}
	}
	return nil, false
}

// codexText is the last agent_message item's text.
func codexText(out []byte) (string, bool) {
	objs, _ := objects(out)
	for _, obj := range slices.Backward(objs) {
		if text, ok := agentMessage(obj); ok {
			return text, true
		}
	}
	return "", false
}

// agentMessage reads one item.completed event's agent_message text.
func agentMessage(obj map[string]json.RawMessage) (string, bool) {
	if kind, ok := str(obj, "type"); !ok || kind != codexItemCompleted {
		return "", false
	}
	item, ok := child(obj, "item")
	if !ok {
		return "", false
	}
	if kind, ok := str(item, "type"); !ok || kind != codexAgentMessage {
		return "", false
	}
	return str(item, "text")
}
