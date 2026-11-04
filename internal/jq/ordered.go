package jq

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"strings"
)

// opDecodeJSONSeq names the JSON-sequence decode operation in sealed errors.
const opDecodeJSONSeq = "decode json sequence"

// Obj is a JSON object that preserves key order across read-modify-write,
// mirroring jq's `+`-merge order (existing keys keep position, new keys
// append). Nested values decode recursively: objects become *Obj, arrays
// stay []any, numbers stay [json.Number]. This is the KB-store representation;
// the findings stores always pass through jq's key-sorting pretty printer,
// so plain maps suffice there.
type Obj struct {
	Fields map[string]any
	Keys   []string
}

// NewObj builds an Obj from ordered pairs.
func NewObj(pairs ...any) *Obj {
	o := &Obj{Fields: map[string]any{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			continue
		}
		o.Set(k, pairs[i+1])
	}
	return o
}

// Get returns the field value.
func (o *Obj) Get(key string) (any, bool) {
	v, ok := o.Fields[key]
	return v, ok
}

// Set updates a field in place or appends it, like jq's `+`.
func (o *Obj) Set(key string, v any) {
	if _, ok := o.Fields[key]; !ok {
		o.Keys = append(o.Keys, key)
	}
	o.Fields[key] = v
}

// Delete removes a field, like jq's `del`.
func (o *Obj) Delete(key string) {
	if _, ok := o.Fields[key]; !ok {
		return
	}
	delete(o.Fields, key)
	keys := o.Keys[:0]
	for _, k := range o.Keys {
		if k != key {
			keys = append(keys, k)
		}
	}
	o.Keys = keys
}

// MarshalCompact renders the object with keys in stored order, jq -c style.
func (o *Obj) MarshalCompact() string {
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(Compact(k))
		sb.WriteByte(':')
		sb.WriteString(valueCompact(o.Fields[k]))
	}
	sb.WriteByte('}')
	return sb.String()
}

// MarshalPretty renders the object jq '.' style: 2-space indent, stored key
// order, no HTML escaping, no trailing newline.
func (o *Obj) MarshalPretty() string {
	return valuePretty(o, "", "  ")
}

// valuePretty renders v with stored order for *Obj and sorted order for
// plain maps (the latter only arises from hand-built values, never decoded
// stores). [Number] renders as its raw literal (see Compact).
func valuePretty(v any, prefix, indent string) string {
	if n, ok := v.(Number); ok {
		return n.String()
	}
	var sb strings.Builder
	writePretty(&sb, v, 0, prefix, indent)
	return sb.String()
}

// writePretty writes v at depth with the Go encoder's indentation layout:
// `{\n<pad>"k": v,\n...\n<pad>}` and the array analog, matching jq byte
// for byte (`, ` never appears; separators are `": "` and `",\n<pad>"`).
func writePretty(sb *strings.Builder, v any, depth int, prefix, indent string) {
	pad := strings.Repeat(indent, depth)
	switch t := v.(type) {
	case *Obj:
		writePrettyObj(sb, t, depth, pad, pad+indent, prefix, indent)
	case map[string]any:
		writePrettyMap(sb, t, depth, pad, pad+indent, prefix, indent)
	case []any:
		writePrettyArr(sb, t, depth, pad, pad+indent, prefix, indent)
	default:
		sb.WriteString(Compact(v))
	}
}

// writePrettyMap writes a plain map with jq's codepoint-sorted key order.
func writePrettyMap(sb *strings.Builder, t map[string]any, depth int, pad, childPad, prefix, indent string) {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sortStrings(keys)
	if len(keys) == 0 {
		sb.WriteString("{}")
		return
	}
	sb.WriteString("{\n")
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(childPad)
		sb.WriteString(Compact(k))
		sb.WriteString(": ")
		writePretty(sb, t[k], depth+1, prefix, indent)
	}
	sb.WriteString("\n" + pad + "}")
}

// writePrettyObj writes an object at depth with Go-encoder indentation.
func writePrettyObj(sb *strings.Builder, t *Obj, depth int, pad, childPad, prefix, indent string) {
	if len(t.Keys) == 0 {
		sb.WriteString("{}")
		return
	}
	sb.WriteString("{\n")
	for i, k := range t.Keys {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(childPad)
		sb.WriteString(Compact(k))
		sb.WriteString(": ")
		writePretty(sb, t.Fields[k], depth+1, prefix, indent)
	}
	sb.WriteString("\n" + pad + "}")
}

// writePrettyArr writes an array at depth with Go-encoder indentation.
func writePrettyArr(sb *strings.Builder, t []any, depth int, pad, childPad, prefix, indent string) {
	if len(t) == 0 {
		sb.WriteString("[]")
		return
	}
	sb.WriteString("[\n")
	for i, e := range t {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(childPad)
		writePretty(sb, e, depth+1, prefix, indent)
	}
	sb.WriteString("\n" + pad + "]")
}

// Clone returns a shallow copy: key order and field values shared.
func (o *Obj) Clone() *Obj {
	c := &Obj{Keys: append([]string(nil), o.Keys...), Fields: maps.Clone(o.Fields)}
	return c
}

// Has reports whether the field exists.
func (o *Obj) Has(key string) bool {
	_, ok := o.Fields[key]
	return ok
}

// DecodeOrdered parses one JSON document with objects preserved as *Obj in
// document key order and numbers as [json.Number]. The stream must hold
// exactly one value.
func DecodeOrdered(data []byte) (any, error) {
	vals, err := decodeSequence(data)
	if err != nil {
		return nil, err
	}
	if len(vals) != 1 {
		return nil, Sealed("decode json", errTrailingData)
	}
	return vals[0], nil
}

// valueCompact renders a possibly order-preserving value compactly.
// [Number] renders as its raw literal (see Compact).
func valueCompact(v any) string {
	if n, ok := v.(Number); ok {
		return n.String()
	}
	if o, ok := v.(*Obj); ok {
		return o.MarshalCompact()
	}
	if a, ok := v.([]any); ok {
		return arrayCompact(a)
	}
	return Compact(v)
}

// arrayCompact renders an array compactly.
func arrayCompact(a []any) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, e := range a {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(valueCompact(e))
	}
	sb.WriteByte(']')
	return sb.String()
}

// DecodeJSONLOrdered parses a JSON sequence (kb.jsonl stores) into
// order-preserving objects. Non-object values decode to their plain forms
// and are carried as-is.
func DecodeJSONLOrdered(data []byte) ([]*Obj, error) {
	vals, err := decodeSequence(data)
	if err != nil {
		return nil, err
	}
	out := make([]*Obj, 0, len(vals))
	for _, v := range vals {
		o, ok := v.(*Obj)
		if !ok {
			return nil, Sealed(opDecodeJSONSeq, errWantObject)
		}
		out = append(out, o)
	}
	return out, nil
}

// decodeSequence decodes every JSON value in data in order.
func decodeSequence(data []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out []any
	for dec.More() {
		tok, err := nextToken(dec)
		if err != nil {
			return nil, err
		}
		v, err := decodeTokenValue(dec, tok)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	// More() is also false in front of a stray closer (`}` or `]`), so only a
	// clean EOF means the stream is done: anything else is trailing garbage.
	_, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return out, nil
	}
	if err == nil {
		err = errTrailingData
	}
	return nil, Sealed(opDecodeJSONSeq, err)
}

// nextToken reads one token, sealing decoder errors.
func nextToken(dec *json.Decoder) (json.Token, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, Sealed(opDecodeJSONSeq, err)
	}
	return tok, nil
}

// closeValue consumes a value's closing delimiter.
func closeValue(dec *json.Decoder) error {
	if _, err := dec.Token(); err != nil {
		return Sealed(opDecodeJSONSeq, err)
	}
	return nil
}

// decodeTokenValue decodes one value whose opening token was already read.
func decodeTokenValue(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec)
		case '[':
			return decodeArray(dec)
		default:
			return nil, Sealed(opDecodeJSONSeq, errTrailingData)
		}
	case string, bool, nil, json.Number:
		return t, nil
	default:
		f, ok := tok.(float64)
		if ok {
			return f, nil
		}
		return nil, Sealed(opDecodeJSONSeq, errTrailingData)
	}
}

// decodeObject decodes the remainder of an object after `{`.
func decodeObject(dec *json.Decoder) (*Obj, error) {
	o := &Obj{Fields: map[string]any{}}
	for dec.More() {
		key, err := decodeKey(dec)
		if err != nil {
			return nil, err
		}
		if err := decodeField(dec, o, key); err != nil {
			return nil, err
		}
	}
	if err := closeValue(dec); err != nil {
		return nil, err
	}
	return o, nil
}

// decodeKey reads one object key.
func decodeKey(dec *json.Decoder) (string, error) {
	keyTok, err := nextToken(dec)
	if err != nil {
		return "", err
	}
	key, ok := keyTok.(string)
	if !ok {
		return "", Sealed(opDecodeJSONSeq, errWantObject)
	}
	return key, nil
}

// decodeField decodes one object field value.
func decodeField(dec *json.Decoder, o *Obj, key string) error {
	valTok, err := nextToken(dec)
	if err != nil {
		return err
	}
	v, err := decodeTokenValue(dec, valTok)
	if err != nil {
		return err
	}
	o.Set(key, v)
	return nil
}

// decodeArray decodes the remainder of an array after `[`.
func decodeArray(dec *json.Decoder) ([]any, error) {
	var out []any
	for dec.More() {
		tok, err := nextToken(dec)
		if err != nil {
			return nil, err
		}
		v, err := decodeTokenValue(dec, tok)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := closeValue(dec); err != nil {
		return nil, err
	}
	return out, nil
}
