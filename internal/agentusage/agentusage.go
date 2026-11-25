// Package agentusage measures one agent spawn's spend from the agent CLI's own
// structured output: the cost the CLI reports, or the token usage it reports
// priced from the agent catalog's dated price table. A Tap reads the spawn's
// stdout as it streams — never stderr, never the launcher's tail-capped
// capture — keeping only the lines its format needs, bounded line by line and
// in total. A parse that does not match the documented shape, an over-long
// line, an overflowing stream, or a stream cut mid-object measures nothing,
// and the caller falls back to the next source.
package agentusage

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sync"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/spend"
)

const (
	// maxLineBytes bounds one structured-output line. A result object can
	// carry the whole final message, so the bound is generous; a longer line
	// marks the stream unmeasurable rather than half-read.
	maxLineBytes = 4 << 20
	// maxKeptBytes bounds every line one tap keeps; past it the stream is
	// unmeasurable, never measured from the lines that fit.
	maxKeptBytes = 32 << 20
)

// Measurement is one spawn's spend: the exact USD literal, how it was priced
// (spend.PricedMeasured or spend.PricedTokens), and the model it was priced
// for when the price came from tokens.
type Measurement struct {
	USD      string
	PricedBy string
	Model    string
}

// Tap collects one spawn's stdout for its format while the process runs. It
// is an [io.Writer] that never fails, safe for the one writer goroutine a
// process's stdout has.
type Tap struct {
	format   string
	last     []byte
	pending  []byte
	lines    [][]byte
	kept     int
	mu       sync.Mutex
	overflow bool
	skipping bool
	wrote    bool
}

// NewTap is a tap for one format.
func NewTap(format string) *Tap {
	return &Tap{format: format}
}

// Capture is a finished tap over one complete capture, for a caller that
// already holds the stdout bytes.
func Capture(format string, out []byte) *Tap {
	t := NewTap(format)
	t.Feed(out)
	return t
}

// Feed is Write for a caller that holds the bytes: a tap never fails.
func (t *Tap) Feed(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.feed(p)
}

// Wrote reports whether the process wrote any stdout at all.
func (t *Tap) Wrote() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wrote
}

// Write splits the stream into lines, keeping what the format reads.
func (t *Tap) Write(p []byte) (int, error) {
	t.Feed(p)
	return len(p), nil
}

func (t *Tap) feed(p []byte) {
	if len(p) > 0 {
		t.wrote = true
	}
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			t.buffer(rest)
			break
		}
		t.buffer(rest[:i])
		t.endLine()
		rest = rest[i+1:]
	}
}

// buffer appends to the current line, giving up on a line past the bound.
func (t *Tap) buffer(p []byte) {
	if t.skipping {
		return
	}
	if len(t.pending)+len(p) > maxLineBytes {
		t.overflow, t.skipping, t.pending = true, true, nil
		return
	}
	t.pending = append(t.pending, p...)
}

// endLine settles one complete line.
func (t *Tap) endLine() {
	line := bytes.TrimSpace(t.pending)
	skipped := t.skipping
	t.pending, t.skipping = nil, false
	if skipped || len(line) == 0 {
		return
	}
	t.last = append(t.last[:0], line...)
	// A half-written object anywhere — not only at the end — means the
	// stream lost bytes, so nothing read from it can be trusted.
	if line[0] == '{' && !json.Valid(line) {
		t.overflow = true
		return
	}
	if !keeps(t.format, line) {
		return
	}
	t.kept += len(line)
	if t.kept > maxKeptBytes {
		t.overflow = true
		return
	}
	t.lines = append(t.lines, bytes.Clone(line))
}

// settled is the tap's view once the stream ended: the kept lines and the
// final non-empty line, a trailing unterminated line included. ok is false
// when the stream cannot be measured: an over-long line, an overflow, or an
// object line anywhere that never finished.
func (t *Tap) settled() (kept [][]byte, last []byte, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(bytes.TrimSpace(t.pending)) > 0 || t.skipping {
		t.endLine()
	}
	return t.lines, t.last, !t.overflow
}

// keeps reports whether one line is one the format reads. The check is a
// cheap substring screen; the parser validates every kept line in full.
func keeps(format string, line []byte) bool {
	if line[0] != '{' {
		return false
	}
	switch format {
	case agentcatalog.UsageCodexJSONL:
		return bytes.Contains(line, []byte(codexTurnCompleted)) || bytes.Contains(line, []byte(codexAgentMessage))
	case agentcatalog.UsageOpenCodeJSONL:
		return bytes.Contains(line, []byte(openCodeStepFinish)) || bytes.Contains(line, []byte(`"text"`))
	}
	// Claude's result is read from the final line alone.
	return false
}

// Measure reads one spawn's spend from its tap. model is the model the launch
// runs (empty when unknown); a token-only format prices only a model the
// price table carries.
func Measure(t *Tap, model string) (Measurement, bool) {
	kept, last, ok := t.settled()
	if !ok {
		return Measurement{}, false
	}
	switch t.format {
	case agentcatalog.UsageClaudeJSON:
		return claudeCost(last)
	case agentcatalog.UsageCodexJSONL:
		return codexCost(kept, model)
	case agentcatalog.UsageOpenCodeJSONL:
		return openCodeCost(kept)
	}
	return Measurement{}, false
}

// Text is the agent's own text out of its structured stdout, so a review the
// agent printed instead of saving is still found. It reads false when the
// stream holds no text in the documented shape.
func Text(t *Tap) (string, bool) {
	kept, last, ok := t.settled()
	if !ok {
		return "", false
	}
	switch t.format {
	case agentcatalog.UsageClaudeJSON:
		return claudeText(last)
	case agentcatalog.UsageCodexJSONL:
		return codexText(kept)
	case agentcatalog.UsageOpenCodeJSONL:
		return openCodeText(kept)
	}
	return "", false
}

// Sum totals measurements exactly. The total is token-priced when any part
// was, since it then rests on the price table.
func Sum(parts []Measurement) (usd, pricedBy string, ok bool) {
	total := new(big.Rat)
	pricedBy = spend.PricedMeasured
	for _, m := range parts {
		rat, valid := spend.ParseAmount(m.USD)
		if !valid {
			return "", "", false
		}
		total.Add(total, rat)
		if m.PricedBy == spend.PricedTokens {
			pricedBy = spend.PricedTokens
		}
	}
	return spend.FormatRat(total), pricedBy, len(parts) > 0
}

// objects decodes kept lines that are each one JSON object, in order.
func objects(lines [][]byte) []map[string]json.RawMessage {
	objs := make([]map[string]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		if obj, ok := object(line); ok {
			objs = append(objs, obj)
		}
	}
	return objs
}

// object decodes one line as a JSON object.
func object(line []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(line, &obj) != nil || obj == nil {
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
	return object(raw)
}

// amount reads one field as a JSON number — never a quoted string — under
// the cost scan's own literal bounds: finite, nonnegative, at most 64
// characters, an exponent of at most 12 in magnitude.
func amount(obj map[string]json.RawMessage, key string) (*big.Rat, bool) {
	raw, ok := obj[key]
	if !ok {
		return nil, false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == '"' {
		return nil, false
	}
	var num json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&num) != nil {
		return nil, false
	}
	return spend.ParseAmount(num.String())
}

// count reads one field as a nonnegative whole number of tokens.
func count(obj map[string]json.RawMessage, key string) (*big.Rat, bool) {
	rat, ok := amount(obj, key)
	if !ok || !rat.IsInt() {
		return nil, false
	}
	return rat, true
}
