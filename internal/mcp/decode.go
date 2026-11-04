package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

const jsonNull = "null"

type paramError struct {
	msg string
}

func (e *paramError) Error() string { return e.msg }

func paramErrorf(format string, args ...any) error {
	return &paramError{msg: fmt.Sprintf(format, args...)}
}

type args struct {
	obj map[string]json.RawMessage
	err error
}

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

func decodeArgs(t *tool, raw json.RawMessage) (any, error) {
	return t.decode(raw)
}

func (a *args) fail(err error) {
	if a.err == nil {
		a.err = err
	}
}

func (a *args) take(key string) (json.RawMessage, bool) {
	raw, ok := a.obj[key]
	if ok {
		delete(a.obj, key)
	}
	return raw, ok
}

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

func (a *args) done() error {
	if a.err != nil {
		return a.err
	}
	if unknown := firstKey(a.obj); unknown != "" {
		return paramErrorf("unknown argument %q", unknown)
	}
	return nil
}

func firstKey(obj map[string]json.RawMessage) string {
	keys := slices.Collect(maps.Keys(obj))
	if len(keys) == 0 {
		return ""
	}
	return slices.Min(keys)
}
