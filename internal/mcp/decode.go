package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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

// args is one decoded arguments object. Field decoders consume keys as they
// go, so the leftovers name the first unknown argument exactly as the schema
// rejects it. The first failure wins: later fields are not decoded.
type args struct {
	obj map[string]json.RawMessage
	err error
}

// newArgs validates the tools/call arguments envelope. A missing or null
// arguments member decodes as the empty object.
func newArgs(raw json.RawMessage) (*args, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		raw = []byte("{}")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, paramErrorf("arguments must be an object: %v", err)
	}
	return &args{obj: obj}, nil
}

// decodeRequest builds one typed request from the schema's required list and
// the request type's explicit field decoder. The required list is the same
// value the schema advertises, so the two cannot drift.
func decodeRequest[Req any](raw json.RawMessage, required []string, decode func(*Req, *args)) (*Req, error) {
	a, err := newArgs(raw)
	if err != nil {
		return nil, err
	}
	a.require(required)
	req := new(Req)
	decode(req, a)
	return req, a.done()
}

// decodeArgs turns one tools/call arguments object into the tool's concrete
// core request pointer. The caller handles a hand-built tool with no decoder.
func decodeArgs(t *tool, raw json.RawMessage) (any, error) {
	return t.decode(raw)
}

// fail records the first decoding failure.
func (a *args) fail(err error) {
	if a.err == nil {
		a.err = err
	}
}

// take returns one argument and marks it consumed.
func (a *args) take(key string) (json.RawMessage, bool) {
	raw, ok := a.obj[key]
	if ok {
		delete(a.obj, key)
	}
	return raw, ok
}

// require proves every declared required argument is present and non-empty,
// in declaration order, before any field decodes.
func (a *args) require(required []string) {
	if a.err != nil {
		return
	}
	for _, name := range required {
		if !a.satisfied(name) {
			return
		}
	}
}

// satisfied reports whether one required argument is present and non-empty,
// recording the first violation.
func (a *args) satisfied(name string) bool {
	raw, ok := a.obj[name]
	if !ok || string(raw) == jsonNull {
		a.fail(paramErrorf("missing required argument %q", name))
		return false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && value == "" {
		a.fail(paramErrorf("missing required argument %q", name))
		return false
	}
	return true
}

// str decodes one string argument.
func (a *args) str(key string, dst *string) {
	if a.err != nil {
		return
	}
	raw, ok := a.take(key)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		a.fail(paramErrorf("argument %q: %v", key, err))
	}
}

// boolean decodes one bool argument.
func (a *args) boolean(key string, dst *bool) {
	if a.err != nil {
		return
	}
	raw, ok := a.take(key)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		a.fail(paramErrorf("argument %q: %v", key, err))
	}
}

// strings decodes one string-array argument.
func (a *args) strings(key string, dst *[]string) {
	if a.err != nil {
		return
	}
	raw, ok := a.take(key)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		a.fail(paramErrorf("argument %q: %v", key, err))
	}
}

// flags decodes the nested flags object. A wrong shape keeps the transport's
// `argument "flags" must be an object` wording; unknown nested keys are
// reported with their parent path, unlike type errors.
func (a *args) flags(decode func(*args)) {
	if a.err != nil {
		return
	}
	raw, ok := a.take(argFlags)
	if !ok {
		return
	}
	sub, err := newArgs(raw)
	if err != nil {
		a.fail(paramErrorf("argument %q must be an object", argFlags))
		return
	}
	decode(sub)
	if sub.err != nil {
		a.fail(sub.err)
		return
	}
	if unknown := firstKey(sub.obj); unknown != "" {
		a.fail(paramErrorf("unknown argument %q", argFlags+"."+unknown))
	}
}

// done reports the recorded failure, else the lexicographically first
// unconsumed key as an unknown argument.
func (a *args) done() error {
	if a.err != nil {
		return a.err
	}
	if unknown := firstKey(a.obj); unknown != "" {
		return paramErrorf("unknown argument %q", unknown)
	}
	return nil
}

// firstKey returns the lexicographically first map key ("" when empty), so
// unknown-argument errors name the same key on every run.
func firstKey(obj map[string]json.RawMessage) string {
	keys := slices.Collect(maps.Keys(obj))
	if len(keys) == 0 {
		return ""
	}
	return slices.Min(keys)
}
