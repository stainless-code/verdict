package agentusage

import (
	"encoding/json"
	"slices"

	"github.com/stainless-code/verdict/internal/spend"
)

// claudeResultType is the type field of the result object `claude -p
// --output-format json` prints when the run ends.
const claudeResultType = "result"

// claudeResult finds the result object: the whole stream as one object, else
// the last line that is one. Only an object whose type is "result" counts.
func claudeResult(out []byte) (map[string]json.RawMessage, bool) {
	if obj, ok := whole(out); ok && isClaudeResult(obj) {
		return obj, true
	}
	objs, _ := objects(out)
	for _, obj := range slices.Backward(objs) {
		if isClaudeResult(obj) {
			return obj, true
		}
	}
	return nil, false
}

func isClaudeResult(obj map[string]json.RawMessage) bool {
	kind, ok := str(obj, "type")
	return ok && kind == claudeResultType
}

// claudeCost is the result object's total_cost_usd: the CLI's own cost for
// the whole session.
func claudeCost(out []byte) (Measurement, bool) {
	obj, ok := claudeResult(out)
	if !ok {
		return Measurement{}, false
	}
	usd, ok := amount(obj, "total_cost_usd")
	if !ok {
		return Measurement{}, false
	}
	return Measurement{USD: spend.FormatRat(usd), PricedBy: spend.PricedMeasured}, true
}

// claudeText is the result object's final message.
func claudeText(out []byte) (string, bool) {
	obj, ok := claudeResult(out)
	if !ok {
		return "", false
	}
	return str(obj, "result")
}
