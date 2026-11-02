package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// jsonNull is the JSON null literal the argument decoder recognizes.
const jsonNull = "null"

// paramError is a tools/call argument validation failure: the caller maps
// it to JSON-RPC -32602 (never a typed refusal).
type paramError struct {
	msg string
}

// Error implements error.
func (e *paramError) Error() string { return e.msg }

// paramErrorf builds one validation error.
func paramErrorf(format string, args ...any) error {
	return &paramError{msg: fmt.Sprintf(format, args...)}
}

// decodeArgs turns one tools/call arguments object into the tool's concrete
// core request pointer. Unknown keys and wrong types are invalid params,
// never a typed refusal.
func decodeArgs(t *tool, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		raw = []byte("{}")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, paramErrorf("arguments must be an object: %v", err)
	}
	if err := checkRequired(t, obj); err != nil {
		return nil, err
	}
	req := reflect.New(t.request)
	if err := decodeStruct(req.Elem(), obj, t); err != nil {
		return nil, err
	}
	if unknown := firstKey(obj); unknown != "" {
		return nil, paramErrorf("unknown argument %q", unknown)
	}
	return req.Interface(), nil
}

// firstKey returns one map key in deterministic order ("" when empty).
func firstKey(obj map[string]json.RawMessage) string {
	for key := range obj {
		return key
	}
	return ""
}

// checkRequired proves every declared required argument is present and
// non-empty.
func checkRequired(t *tool, obj map[string]json.RawMessage) error {
	for _, name := range t.required {
		raw, ok := obj[name]
		if !ok || string(raw) == jsonNull {
			return paramErrorf("missing required argument %q", name)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil && value == "" {
			return paramErrorf("missing required argument %q", name)
		}
	}
	return nil
}

// decodeStruct fills one request struct from its arguments object, deleting
// consumed keys so the caller can reject unknown ones.
//
//nolint:revive // one branch per JSON shape keeps validation auditable
func decodeStruct(dst reflect.Value, obj map[string]json.RawMessage, t *tool) error {
	fields := visibleFields(dst.Type(), t.omit)
	for i := range fields {
		f := &fields[i]
		key := argName(&f.sf)
		raw, ok := obj[key]
		if !ok {
			continue
		}
		delete(obj, key)
		fv := dst.FieldByIndex(f.index)
		if f.nested {
			if err := decodeNested(fv, raw, key, t); err != nil {
				return err
			}
			continue
		}
		if err := json.Unmarshal(raw, fv.Addr().Interface()); err != nil {
			return paramErrorf("argument %q: %v", key, err)
		}
	}
	return nil
}

// decodeNested validates and decodes one nested Flags object.
func decodeNested(dst reflect.Value, raw json.RawMessage, key string, t *tool) error {
	var sub map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sub); err != nil {
		return paramErrorf("argument %q must be an object", key)
	}
	if err := decodeStruct(dst, sub, t); err != nil {
		return err
	}
	if unknown := firstKey(sub); unknown != "" {
		return paramErrorf("unknown argument %q", key+"."+unknown)
	}
	return nil
}
