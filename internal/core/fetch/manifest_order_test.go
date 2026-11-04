package fetchcore

import (
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestSortedManifestChunksJQOrder pins the manifest order against jq's
// `sort_by(.index)` total order: null < false < true < numbers < strings. The
// deleted local rank gave true and false one rank, so boolean indexes compared
// equal.
func TestSortedManifestChunksJQOrder(t *testing.T) {
	t.Parallel()
	manifest := map[string]any{"chunks": []any{
		map[string]any{"name": "two", keyIndex: decoded(t, "2")},
		map[string]any{"name": "one-str", keyIndex: decoded(t, `"1"`)},
		map[string]any{"name": "true", keyIndex: decoded(t, "true")},
		map[string]any{"name": "false", keyIndex: decoded(t, "false")},
		map[string]any{"name": "null", keyIndex: decoded(t, "null")},
	}}
	got := make([]string, 0, 5)
	for _, c := range sortedManifestChunks(manifest) {
		got = append(got, jq.Str(c["name"]))
	}
	want := []string{"null", "false", "true", "two", "one-str"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("manifest order = %v; want jq order %v", got, want)
	}
}

// decoded decodes one JSON document.
func decoded(t *testing.T, doc string) any {
	t.Helper()
	v, err := jq.Decode([]byte(doc))
	if err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	return v
}
