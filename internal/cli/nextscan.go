package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

const (
	// nextScanCap bounds the JSON prefix buffered for envelope scanning so huge
	// outputs (such as --show-prompts) never cause unbounded buffering.
	nextScanCap = 256 << 10
	// nextLineCap bounds one partial human line while scanning for `next: `.
	nextLineCap = 8 << 10
	// nextPrefix is the frozen human-mode next-step line prefix.
	nextPrefix = "next: "
)

// NextScanner wraps an [io.Writer], forwarding every byte unchanged while
// observing the printed `next` hint: the envelope's top-level `next` field in
// JSON mode, or the last `next: <hint>` line in human mode.
type NextScanner struct {
	inner      io.Writer
	lastLine   string
	jsonBuf    bytes.Buffer
	lineBuf    bytes.Buffer
	lineOver   bool
	jsonCapped bool
}

// NewNextScanner returns a scanning writer wrapping inner.
func NewNextScanner(inner io.Writer) *NextScanner {
	return &NextScanner{inner: inner}
}

// InstallNextScanner wraps r.Out (and r.Human when it shares r.Out) with a
// [NextScanner] and returns the scanner plus a restore function.
func InstallNextScanner(r *Runner) (scan *NextScanner, restore func()) {
	origOut, origHuman := r.Out, r.Human
	scan = NewNextScanner(origOut)
	r.Out = scan
	if origHuman == origOut {
		r.Human = scan
	}
	return scan, func() {
		r.Out = origOut
		r.Human = origHuman
	}
}

// Write forwards p to the underlying writer and inspects it for `next` hints.
func (s *NextScanner) Write(p []byte) (int, error) {
	s.observeJSON(p)
	s.observeLines(p)
	n, err := s.inner.Write(p)
	if err != nil {
		return n, jq.Sealed("write output", err)
	}
	return n, nil
}

// Next returns the captured hint for the active output mode: the JSON
// envelope's `next` key when jsonMode is true, else the last human `next: `
// line.
func (s *NextScanner) Next(jsonMode bool) string {
	if jsonMode {
		return parseEnvelopeNext(s.jsonBuf.Bytes())
	}
	if s.lineBuf.Len() > 0 && !s.lineOver {
		s.checkHumanLine(s.lineBuf.String())
		s.lineBuf.Reset()
	}
	return s.lastLine
}

func (s *NextScanner) observeJSON(p []byte) {
	if s.jsonCapped {
		return
	}
	rem := nextScanCap - s.jsonBuf.Len()
	if rem <= 0 {
		s.jsonCapped = true
		return
	}
	if len(p) > rem {
		s.jsonBuf.Write(p[:rem])
		s.jsonCapped = true
		return
	}
	s.jsonBuf.Write(p)
}

func (s *NextScanner) observeLines(p []byte) {
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			s.appendLineFragment(p)
			return
		}
		s.appendLineFragment(p[:idx])
		if !s.lineOver {
			s.checkHumanLine(s.lineBuf.String())
		}
		s.lineBuf.Reset()
		s.lineOver = false
		p = p[idx+1:]
	}
}

func (s *NextScanner) appendLineFragment(frag []byte) {
	if s.lineOver {
		return
	}
	if s.lineBuf.Len()+len(frag) > nextLineCap {
		s.lineOver = true
		return
	}
	s.lineBuf.Write(frag)
}

func (s *NextScanner) checkHumanLine(line string) {
	cleaned := strings.TrimSpace(stripANSI(line))
	if rest, ok := strings.CutPrefix(cleaned, nextPrefix); ok {
		s.lastLine = strings.TrimSpace(rest)
	}
}

// parseEnvelopeNext extracts the top-level `"next"` string from a JSON envelope
// even when the buffered prefix was truncated after nextScanCap bytes.
func parseEnvelopeNext(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ""
	}
	var env struct {
		Next string `json:"next"`
	}
	if err := json.Unmarshal(trimmed, &env); err == nil {
		return env.Next
	}
	return scanTopLevelNext(trimmed)
}

// scanTopLevelNext walks top-level tokens of a truncated JSON object prefix
// until it reads `"next": "<value>"`.
func scanTopLevelNext(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return ""
	}
	for dec.More() {
		val, done := nextTopLevelField(dec)
		if done {
			return val
		}
	}
	return ""
}

func nextTopLevelField(dec *json.Decoder) (string, bool) {
	key, ok := readStringToken(dec)
	if !ok {
		return "", true
	}
	if key == "next" {
		val, _ := readStringToken(dec)
		return val, true
	}
	var skip json.RawMessage
	if err := dec.Decode(&skip); err != nil {
		return "", true
	}
	return "", false
}

func readStringToken(dec *json.Decoder) (string, bool) {
	tok, err := dec.Token()
	if err != nil {
		return "", false
	}
	s, ok := tok.(string)
	return s, ok
}

// stripANSI removes CSI SGR escape sequences (`\x1b[...m`) so forced-color
// human output still matches the `next: ` line prefix.
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i = skipCSISequence(s, i+2)
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func skipCSISequence(s string, i int) int {
	for i < len(s) {
		c := s[i]
		i++
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return i
		}
	}
	return i
}
