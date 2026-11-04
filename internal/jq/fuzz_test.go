package jq_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// seedFixtures adds every readable file matching the repo-relative globs to
// the fuzz corpus, so the mutator starts from real agent/host JSON.
func seedFixtures(f *testing.F, globs ...string) {
	f.Helper()
	for _, g := range globs {
		paths, err := filepath.Glob(g)
		if err != nil {
			f.Fatal(err)
		}
		for _, p := range paths {
			addFixture(f, p)
		}
	}
}

// addFixture seeds one small readable file.
func addFixture(f *testing.F, path string) {
	f.Helper()
	if b, err := os.ReadFile(path); err == nil && len(b) < 1<<16 {
		f.Add(b)
	}
}

// stdlibDecode decodes data with encoding/json (numbers as [json.Number]): the
// independent oracle for what the document means. ok is false when data is
// not exactly one valid JSON value.
func stdlibDecode(data []byte) (any, bool) {
	if !json.Valid(data) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// plain converts the ordered representation to the stdlib one through its
// compact rendering, then back through encoding/json — so the comparison
// uses only the renderer's bytes and the stdlib's parser.
func plain(t *testing.T, v any) (any, string) {
	t.Helper()
	rendered := renderOrdered(v)
	got, ok := stdlibDecode([]byte(rendered))
	if !ok {
		t.Fatalf("DecodeOrdered value renders as invalid JSON: %q", rendered)
	}
	return got, rendered
}

// renderOrdered renders any DecodeOrdered result compactly.
func renderOrdered(v any) string {
	if o, ok := v.(*jq.Obj); ok {
		return o.MarshalCompact()
	}
	return jq.PrettyOrdered(v)
}

// FuzzDecodeOrdered holds the ordered decoder to three facts that do not
// depend on its own code: it accepts exactly the documents encoding/json
// accepts (no partial acceptance of malformed input), an accepted document
// means the same thing under encoding/json as its re-rendering does, and
// re-rendering is stable.
func FuzzDecodeOrdered(f *testing.F) {
	for _, s := range []string{
		`{}`, `[]`, `null`, `"x"`, `1e999`, `{"b":1,"a":[1,2,{"z":null}],"b":2}`,
		`{"a":1} {"b":2}`, `[1,]`, `{"a"}`, `[1 2]`, "{\"k\":\"\xff\"}", ` `, ``,
		`{"a":{"a":{"a":{}}}}`, `-0.0e-0`, `[1,2`, `{"a":1}}`,
	} {
		f.Add([]byte(s))
	}
	seedFixtures(f, "../../examples/python-client/fixtures/bundle/*/*.json",
		"../../bench/corpus/*/agents/*.json", "../../evals/fixtures/*/*.json", "../../schemas/*.json")
	f.Fuzz(checkDecodeOrdered)
}

// checkDecodeOrdered is the FuzzDecodeOrdered property.
func checkDecodeOrdered(t *testing.T, data []byte) {
	want, valid := stdlibDecode(data)
	got, err := jq.DecodeOrdered(data)
	if (err == nil) != valid {
		t.Fatalf("DecodeOrdered err=%v but json.Valid=%v for %q", err, valid, data)
	}
	if err != nil {
		if got != nil {
			t.Fatalf("refusal returned partial value %#v", got)
		}
		return
	}
	back, rendered := plain(t, got)
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("meaning changed: %q decodes to %#v, re-render %q to %#v", data, want, rendered, back)
	}
	again, err := jq.DecodeOrdered([]byte(rendered))
	if err != nil {
		t.Fatalf("re-render %q does not decode: %v", rendered, err)
	}
	if second := renderOrdered(again); second != rendered {
		t.Fatalf("render unstable: %q then %q", rendered, second)
	}
	assertObjShape(t, got)
}

// assertObjShape checks the Obj bookkeeping invariant: Keys lists each field
// exactly once.
func assertObjShape(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case *jq.Obj:
		assertObjKeys(t, x)
	case []any:
		for _, e := range x {
			assertObjShape(t, e)
		}
	}
}

// assertObjKeys checks one object and recurses into its values.
func assertObjKeys(t *testing.T, x *jq.Obj) {
	t.Helper()
	seen := map[string]bool{}
	for _, k := range x.Keys {
		if seen[k] {
			t.Fatalf("Obj.Keys repeats %q: %v", k, x.Keys)
		}
		seen[k] = true
		assertObjShape(t, x.Fields[k])
	}
	if len(x.Keys) != len(x.Fields) {
		t.Fatalf("Obj has %d keys but %d fields", len(x.Keys), len(x.Fields))
	}
}

// FuzzDecodeJSONLOrdered holds the sequence decoder to the same oracle: a
// stream of objects decodes to exactly the objects encoding/json finds, and
// any malformed or non-object stream refuses without partial results.
func FuzzDecodeJSONLOrdered(f *testing.F) {
	for _, s := range []string{
		"{\"a\":1}\n{\"b\":2}\n", `{"a":1}`, "", "[1]", "{\"a\":1}\n3", "{\"a\":", "{}{}{}", "\n\n",
	} {
		f.Add([]byte(s))
	}
	seedFixtures(f, "../../evals/fixtures/bench-corpus/*.jsonl", "../../internal/mcp/testdata/*.jsonl",
		"../../examples/usage-adapters/*/fixtures/*.jsonl")
	f.Fuzz(checkDecodeJSONL)
}

// checkDecodeJSONL is the FuzzDecodeJSONLOrdered property.
func checkDecodeJSONL(t *testing.T, data []byte) {
	wantN, wantOK := stdlibObjectStream(data)
	got, err := jq.DecodeJSONLOrdered(data)
	if (err == nil) != wantOK {
		t.Fatalf("DecodeJSONLOrdered err=%v, oracle ok=%v for %q", err, wantOK, data)
	}
	if err != nil {
		if got != nil {
			t.Fatalf("refusal returned partial result %v", got)
		}
		return
	}
	if len(got) != wantN {
		t.Fatalf("decoded %d objects, oracle found %d in %q", len(got), wantN, data)
	}
	assertRendersValid(t, got)
}

// assertRendersValid checks every object renders as valid JSON.
func assertRendersValid(t *testing.T, objs []*jq.Obj) {
	t.Helper()
	for _, o := range objs {
		if _, ok := stdlibDecode([]byte(o.MarshalCompact())); !ok {
			t.Fatalf("object renders as invalid JSON: %q", o.MarshalCompact())
		}
	}
}

// stdlibObjectStream counts the values in a JSON stream with encoding/json;
// ok is false for malformed streams and for any non-object value.
func stdlibObjectStream(data []byte) (int, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	n := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, false
		}
		if len(raw) == 0 || raw[0] != '{' {
			return 0, false
		}
		n++
	}
	// More() is false at EOF but also before a stray closer or inside a
	// truncated value; only a clean EOF means the stream really ended.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return 0, false
	}
	return n, true
}
