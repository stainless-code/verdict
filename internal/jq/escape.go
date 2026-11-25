package jq

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// UTF-8 shapes of the runes [EscapeControls] rewrites. A lead byte (>= 0xC0)
// never appears as a continuation byte, so a lead byte followed by the right
// continuation bytes cannot match inside another rune, valid or not.
const (
	c1Lead       = 0xC2 // U+0080–U+009F are C2 80–C2 9F
	c1First      = 0x80
	c1Last       = 0x9F
	threeLead    = 0xE2 // U+2000–U+2FFF
	threeBase    = 0x2000
	contMask     = 0xC0
	contTag      = 0x80
	contBits     = 0x3F
	contShift    = 6
	bidiEmbedLo  = 0x202A // LRE, RLE, PDF, LRO, RLO
	bidiEmbedHi  = 0x202E
	bidiIsolateL = 0x2066 // LRI, RLI, FSI, PDI
	bidiIsolateH = 0x2069
	twoBytes     = 2
	threeBytes   = 3
	escapeDigits = 4
	hexBase      = 16
)

// EscapeControls rewrites the C1 controls (U+0080–U+009F) and the bidi
// controls (U+202A–U+202E, U+2066–U+2069) in marshaled JSON as lowercase
// `\uXXXX`, the way encoding/json already writes U+2028. encoding/json leaves
// those runes raw, so a consumer that prints an envelope string to a terminal
// would execute a CSI (U+009B) or see reordered text. The decoded string is
// unchanged. Outside strings valid JSON never contains these runes, so the
// byte-level pass is safe on any marshaled document; a document with nothing
// to rewrite is returned as-is, without allocating. Invalid UTF-8 is already
// replaced by the encoder before this runs, and a malformed sequence here is
// never rewritten.
func EscapeControls(b []byte) []byte {
	var out []byte
	prev := 0
	for i := 0; i+1 < len(b); i++ {
		r, width := controlAt(b, i)
		if width == 0 {
			continue
		}
		if out == nil {
			out = make([]byte, 0, len(b)+escapeDigits)
		}
		out = append(out, b[prev:i]...)
		out = appendEscape(out, r)
		prev = i + width
		i = prev - 1
	}
	if out == nil {
		return b
	}
	return append(out, b[prev:]...)
}

// controlAt decodes a rune [EscapeControls] rewrites at i, returning its byte
// width, or 0 when the bytes there are anything else.
func controlAt(b []byte, i int) (rune, int) {
	switch b[i] {
	case c1Lead:
		if b[i+1] >= c1First && b[i+1] <= c1Last {
			return rune(b[i+1]), twoBytes
		}
	case threeLead:
		if r, ok := bidiAt(b, i); ok {
			return r, threeBytes
		}
	}
	return 0, 0
}

// bidiAt decodes the three-byte rune at i and reports whether it is a bidi
// control. Both trailing bytes must be continuation bytes, so a malformed
// sequence never decodes to one.
func bidiAt(b []byte, i int) (rune, bool) {
	if i+twoBytes >= len(b) || b[i+1]&contMask != contTag || b[i+twoBytes]&contMask != contTag {
		return 0, false
	}
	r := rune(threeBase) | rune(b[i+1]&contBits)<<contShift | rune(b[i+twoBytes]&contBits)
	embed := r >= bidiEmbedLo && r <= bidiEmbedHi
	isolate := r >= bidiIsolateL && r <= bidiIsolateH
	return r, embed || isolate
}

// appendEscape appends r as lowercase `\uXXXX`.
func appendEscape(out []byte, r rune) []byte {
	digits := strconv.FormatInt(int64(r), hexBase)
	out = append(out, '\\', 'u')
	for pad := len(digits); pad < escapeDigits; pad++ {
		out = append(out, '0')
	}
	return append(out, digits...)
}

// Marshal is [json.Marshal] with [EscapeControls] applied: the one encoder for
// JSON a consumer reads.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	return EscapeControls(raw), err
}

// MarshalIndent is [json.MarshalIndent] with [EscapeControls] applied to the
// encoded value only: prefix and indent sit outside strings, so they reach the
// output as given, as encoding/json writes them.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	raw, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, prefix, indent); err != nil {
		return nil, Sealed("indent JSON", err)
	}
	return out.Bytes(), nil
}
