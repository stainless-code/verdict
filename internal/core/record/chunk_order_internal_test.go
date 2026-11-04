package recordcore

import (
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// mustJQValue decodes one JSON document into jq's value model.
func mustJQValue(t *testing.T, doc string) any {
	t.Helper()
	v, err := jq.Decode([]byte(doc))
	if err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	return v
}

// TestSortChunksJQOrder pins the manifest order against jq's `sort_by(.index)`
// total order: null < false < true < numbers < strings. The deleted local rank
// gave true and false one rank, so boolean indexes compared equal.
func TestSortChunksJQOrder(t *testing.T) {
	t.Parallel()
	docs := []struct {
		name  string
		index string
	}{
		{name: "two", index: "2"},
		{name: "one-str", index: `"1"`},
		{name: "true", index: "true"},
		{name: "false", index: "false"},
		{name: "null", index: "null"},
	}
	chunks := make([]chunk, 0, len(docs))
	for _, d := range docs {
		chunks = append(chunks, chunk{name: d.name, index: mustJQValue(t, d.index)})
	}
	sortChunks(chunks)
	got := make([]string, 0, len(chunks))
	for _, c := range chunks {
		got = append(got, c.name)
	}
	want := []string{"null", "false", "true", "two", "one-str"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chunk order = %v; want jq order %v", got, want)
	}
}
