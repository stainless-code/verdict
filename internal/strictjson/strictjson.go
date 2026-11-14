// Package strictjson decodes one JSON document into the generic tree the typed
// readers walk, refusing duplicate keys and trailing data as well as syntax
// errors. A fault is positioned — JSON path, offending key, byte offset — and
// carries no refusal code, so every caller maps it to its own code and the
// tree keeps one decoder instead of one per document.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Fault is the kind of decode failure.
type Fault int

// The zero Fault is a syntax error at the decoder's offset: the case a caller
// reports most often needs no name.
const (
	// duplicateKey is a key a JSON object spells twice.
	duplicateKey Fault = iota + 1
	// trailingData is a second value after the top-level one.
	trailingData
)

// Error is one positioned decode fault: the JSON path of the fault ("$",
// "$.a", "$.a[0]"), the repeated key when one applies, and the decoder's byte
// offset.
type Error struct {
	Path   string
	Key    string
	Offset int64
	Fault  Fault
}

// Error renders the frozen one-line diagnostic.
func (e *Error) Error() string {
	switch e.Fault {
	case duplicateKey:
		return fmt.Sprintf("%s: duplicate key %q", e.Path, e.Key)
	case trailingData:
		return fmt.Sprintf("%s: trailing data after the top object at offset %d", e.Path, e.Offset)
	default:
		return fmt.Sprintf("%s: malformed JSON at offset %d", e.Path, e.Offset)
	}
}

// Decode reads one document into map[string]any, []any, string, [json.Number],
// bool, and nil, refusing a duplicate key at any depth and any value after the
// top-level one.
func Decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, &Error{Path: "$", Offset: dec.InputOffset()}
	}
	val, fault := readTokenValue(dec, tok, "$")
	if fault != nil {
		return nil, fault
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, &Error{Path: "$", Offset: dec.InputOffset(), Fault: trailingData}
	}
	return val, nil
}

// readTokenValue converts one already-read token into the generic tree.
func readTokenValue(dec *json.Decoder, tok json.Token, path string) (any, *Error) {
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		return readObject(dec, path)
	case '[':
		return readArray(dec, path)
	default:
		return nil, &Error{Path: path, Offset: dec.InputOffset()}
	}
}

// readObject reads one JSON object, refusing a key it already holds.
func readObject(dec *json.Decoder, path string) (any, *Error) {
	obj := map[string]any{}
	for dec.More() {
		if fault := readObjectEntry(dec, obj, path); fault != nil {
			return nil, fault
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, &Error{Path: path, Offset: dec.InputOffset()}
	}
	return obj, nil
}

// readObjectEntry reads one key and its value, refusing a key the object
// already spells.
func readObjectEntry(dec *json.Decoder, obj map[string]any, path string) *Error {
	key, fault := readObjectKey(dec, path)
	if fault != nil {
		return fault
	}
	if _, dup := obj[key]; dup {
		return &Error{Path: path, Key: key, Fault: duplicateKey}
	}
	val, fault := readObjectValue(dec, path, key)
	if fault != nil {
		return fault
	}
	obj[key] = val
	return nil
}

// readObjectKey reads one object key.
func readObjectKey(dec *json.Decoder, path string) (string, *Error) {
	keyTok, err := dec.Token()
	if err != nil {
		return "", &Error{Path: path, Offset: dec.InputOffset()}
	}
	key, ok := keyTok.(string)
	if !ok {
		return "", &Error{Path: path, Offset: dec.InputOffset()}
	}
	return key, nil
}

// readObjectValue reads one value under path.key.
func readObjectValue(dec *json.Decoder, path, key string) (any, *Error) {
	valTok, err := dec.Token()
	if err != nil {
		return nil, &Error{Path: path + "." + key, Offset: dec.InputOffset()}
	}
	return readTokenValue(dec, valTok, path+"."+key)
}

// readArray reads one JSON array, threading the index into child paths.
func readArray(dec *json.Decoder, path string) (any, *Error) {
	var out []any
	for i := 0; dec.More(); i++ {
		elemPath := fmt.Sprintf("%s[%d]", path, i)
		valTok, err := dec.Token()
		if err != nil {
			return nil, &Error{Path: elemPath, Offset: dec.InputOffset()}
		}
		val, fault := readTokenValue(dec, valTok, elemPath)
		if fault != nil {
			return nil, fault
		}
		out = append(out, val)
	}
	if _, err := dec.Token(); err != nil {
		return nil, &Error{Path: path, Offset: dec.InputOffset()}
	}
	return out, nil
}

// Kind renders a decoded value's JSON kind for a caller's diagnostic.
func Kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "number"
	}
}
