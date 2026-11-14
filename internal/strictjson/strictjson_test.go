package strictjson

import (
	"encoding/json"
	"testing"
)

// TestDecodeTree pins the generic tree the callers walk: objects, arrays,
// strings, booleans, and null.
//
//nolint:revive // the decoded tree's assertions read as one block
func TestDecodeTree(t *testing.T) {
	tree, err := Decode([]byte(`{"a":[1,{"b":"c"},true],"d":null}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	root, ok := tree.(map[string]any)
	if !ok {
		t.Fatalf("root = %T, want map[string]any", tree)
	}
	items, ok := root["a"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("a = %#v, want three items", root["a"])
	}
	item, ok := items[1].(map[string]any)
	if !ok || item["b"] != "c" {
		t.Errorf("a[1] = %#v, want an object with b = \"c\"", items[1])
	}
	if got := items[2]; got != true {
		t.Errorf("a[2] = %#v, want true", got)
	}
	if got := root["d"]; got != nil {
		t.Errorf("d = %#v, want nil", got)
	}
}

// TestDecodeKeepsNumbersExact proves a JSON number survives as its literal
// text, so a reader decides whether a value is an integer.
func TestDecodeKeepsNumbersExact(t *testing.T) {
	items := numbersOf(t, []byte(`{"n":[1,1.50]}`), "n")
	want := []string{"1", "1.50"}
	for i, num := range items {
		if got := num.String(); got != want[i] {
			t.Errorf("n[%d] = %s, want %s", i, got, want[i])
		}
	}
}

// numbersOf decodes one document and returns the numbers of one array key.
func numbersOf(t *testing.T, raw []byte, key string) []json.Number {
	t.Helper()
	tree, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	root, ok := tree.(map[string]any)
	if !ok {
		t.Fatalf("root = %T, want map[string]any", tree)
	}
	items, ok := root[key].([]any)
	if !ok {
		t.Fatalf("%s = %#v, want an array", key, root[key])
	}
	nums := make([]json.Number, 0, len(items))
	for _, item := range items {
		num, ok := item.(json.Number)
		if !ok {
			t.Fatalf("%s item = %#v, want a number", key, item)
		}
		nums = append(nums, num)
	}
	return nums
}

// TestDecodeRefusals pins each positioned fault: a duplicate key names its
// path and the repeated key, trailing data and malformed input name the
// offset, and an array element carries its index in the path.
//
//nolint:revive // the refusal matrix is one table
func TestDecodeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		give string
		want string
	}{
		{"duplicate top key", `{"a":1,"a":2}`, `$: duplicate key "a"`},
		{"duplicate nested key", `{"recipes":{"quick":{"v":1,"v":2}}}`, `$.recipes.quick: duplicate key "v"`},
		{"duplicate array key", `{"a":[{"b":1,"b":2}]}`, `$.a[0]: duplicate key "b"`},
		{"trailing data", `{"a":1} {}`, `$: trailing data after the top object at offset 9`},
		{"malformed", `{"a":`, `$.a: malformed JSON at offset 5`},
		{"malformed value", `{"a":{"b":[}}`, `$.a.b: malformed JSON at offset 11`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.give))
			if err == nil {
				t.Fatalf("Decode(%s) accepted the document", tc.give)
			}
			if err.Error() != tc.want {
				t.Errorf("Decode(%s) = %q, want %q", tc.give, err.Error(), tc.want)
			}
		})
	}
}

// TestDecodeAcceptsScalar proves a top-level scalar is one document, not a
// fault: only the typed readers refuse it.
func TestDecodeAcceptsScalar(t *testing.T) {
	tree, err := Decode([]byte(`7`))
	if err != nil {
		t.Fatalf("Decode(7) = %v, want the scalar value", err)
	}
	if num, ok := tree.(json.Number); !ok || num.String() != "7" {
		t.Errorf("Decode(7) = %#v, want the number 7", tree)
	}
}
