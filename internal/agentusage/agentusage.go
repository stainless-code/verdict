// Package agentusage measures one agent spawn's spend from the agent CLI's own
// structured output: the cost the CLI reports, or the token usage it reports
// priced from the agent catalog's dated price table. It parses the captured
// stdout only, bounded line by line, and trusts nothing that does not match the
// documented shape — a parse that fails measures nothing, and the caller falls
// back to the next source.
package agentusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/big"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/spend"
)

// maxLineBytes bounds one structured-output line. A CLI's result object can
// carry the whole final message, so the bound is generous; a longer line is
// skipped rather than half-read.
const maxLineBytes = 1 << 20

// initialLineBytes is the scanner's starting buffer; it grows to the bound.
const initialLineBytes = 64 << 10

// Measurement is one spawn's spend: the exact USD literal, how it was priced
// (spend.PricedMeasured or spend.PricedTokens), and the model it was priced
// for when the price came from tokens.
type Measurement struct {
	USD      string
	PricedBy string
	Model    string
}

// Measure reads one spawn's spend from its captured stdout in the named
// format. model is the model the launch named (empty when the agent picked
// its default); a token-only format can price only a model the CLI or the
// launch names and the price table carries.
func Measure(format string, out []byte, model string) (Measurement, bool) {
	switch format {
	case agentcatalog.UsageClaudeJSON:
		return claudeCost(out)
	case agentcatalog.UsageCodexJSONL:
		return codexCost(out, model)
	case agentcatalog.UsageOpenCodeJSONL:
		return openCodeCost(out)
	}
	return Measurement{}, false
}

// Text is the agent's own final text out of its structured stdout, so a
// review the agent printed instead of saving is still found. It reads false
// when the stream holds no text in the documented shape.
func Text(format string, out []byte) (string, bool) {
	switch format {
	case agentcatalog.UsageClaudeJSON:
		return claudeText(out)
	case agentcatalog.UsageCodexJSONL:
		return codexText(out)
	case agentcatalog.UsageOpenCodeJSONL:
		return openCodeText(out)
	}
	return "", false
}

// Sum totals measurements exactly. The total is token-priced when any part
// was, since it then rests on the price table.
func Sum(parts []Measurement) (usd, pricedBy string, ok bool) {
	total := new(big.Rat)
	pricedBy = spend.PricedMeasured
	for _, m := range parts {
		rat, valid := new(big.Rat).SetString(m.USD)
		if !valid || rat.Sign() < 0 {
			return "", "", false
		}
		total.Add(total, rat)
		if m.PricedBy == spend.PricedTokens {
			pricedBy = spend.PricedTokens
		}
	}
	return spend.FormatRat(total), pricedBy, len(parts) > 0
}

// objects decodes every line of the stream that is one JSON object, in
// order. Lines that are not — progress noise, a banner — are skipped, and an
// over-long line is dropped whole. complete is false when the stream ends
// mid-object (a watchdog kill or the capture cap cut it): a stream measured
// from its running parts would then undercount, so its callers measure
// nothing.
func objects(out []byte) (objs []map[string]json.RawMessage, complete bool) {
	complete = true
	scan := bufio.NewScanner(bytes.NewReader(out))
	scan.Buffer(make([]byte, 0, initialLineBytes), maxLineBytes)
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 {
			continue
		}
		obj, isObject, parsed := lineObject(line)
		if parsed {
			objs = append(objs, obj)
		}
		// Only the last non-empty line decides: noise before it is fine, a
		// half-written object at the end is not.
		complete = parsed || !isObject
	}
	return objs, complete && scan.Err() == nil
}

// lineObject decodes one line: whether it starts an object, and whether it
// decoded as one whole.
func lineObject(line []byte) (map[string]json.RawMessage, bool, bool) {
	if line[0] != '{' {
		return nil, false, false
	}
	var obj map[string]json.RawMessage
	return obj, true, json.Unmarshal(line, &obj) == nil
}

// whole decodes the entire stream as one JSON object, for a CLI that prints
// its result pretty-printed across lines.
func whole(out []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(bytes.TrimSpace(out), &obj) != nil {
		return nil, false
	}
	return obj, true
}

// str reads one string field.
func str(obj map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := obj[key]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// child reads one object field.
func child(obj map[string]json.RawMessage, key string) (map[string]json.RawMessage, bool) {
	raw, ok := obj[key]
	if !ok {
		return nil, false
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil || out == nil {
		return nil, false
	}
	return out, true
}

// amount reads one field as a finite, nonnegative JSON number, exactly.
func amount(obj map[string]json.RawMessage, key string) (*big.Rat, bool) {
	raw, ok := obj[key]
	// A JSON number only: json.Number would also take a quoted string, and a
	// "0.42" string is not the documented shape.
	raw = bytes.TrimSpace(raw)
	if !ok || len(raw) == 0 || raw[0] == '"' {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var num json.Number
	if dec.Decode(&num) != nil {
		return nil, false
	}
	rat, ok := new(big.Rat).SetString(num.String())
	if !ok || rat.Sign() < 0 {
		return nil, false
	}
	return rat, true
}

// count reads one field as a nonnegative whole number of tokens.
func count(obj map[string]json.RawMessage, key string) (*big.Rat, bool) {
	rat, ok := amount(obj, key)
	if !ok || !rat.IsInt() {
		return nil, false
	}
	return rat, true
}
