package agentusage

import (
	"encoding/json"

	"github.com/stainless-code/verdict/internal/spend"
)

// claudeResultType is the type field of the result object `claude -p
// --output-format json` prints when the run ends.
const claudeResultType = "result"

// claudeResult is the result object, only as the stream's final line: an
// earlier result-shaped line (an echoed tool output) never counts, and a
// stream that ends on anything else carries no result.
func claudeResult(last []byte) (map[string]json.RawMessage, bool) {
	obj, ok := object(last)
	if !ok {
		return nil, false
	}
	if kind, ok := str(obj, "type"); !ok || kind != claudeResultType {
		return nil, false
	}
	return obj, true
}

// claudeCost is the result object's total_cost_usd: the CLI's own cost for
// the whole session, on the success and the error arm alike.
func claudeCost(last []byte) (Measurement, bool) {
	obj, ok := claudeResult(last)
	if !ok {
		return Measurement{}, false
	}
	usd, ok := amount(obj, "total_cost_usd")
	if !ok {
		return Measurement{}, false
	}
	return Measurement{USD: spend.FormatRat(usd), PricedBy: spend.PricedMeasured}, true
}

// claudeText is the result object's final message; the error arm has none.
func claudeText(last []byte) (string, bool) {
	obj, ok := claudeResult(last)
	if !ok {
		return "", false
	}
	return str(obj, "result")
}
