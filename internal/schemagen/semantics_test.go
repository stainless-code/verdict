package schemagen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// publishedSemantics pins the SHA-256 of each schema's canonical JSON as
// published before generation (9f20c140). The generator that replaced the
// hand-written files was a re-serialization: every key, type, required list,
// enum and description had to stay identical. An intentional published-schema
// change updates this map in its own ticket (audit-24 drift rule); a mere
// reformat of schemas/*.schema.json must not change it.
var publishedSemantics = map[string]string{
	"cost.schema.json":     "30031615315e1a60fac58a9b978b2e2ec30142001c92a78dbd25e7e34c46d217",
	"envelope.schema.json": "26cd28cafe01f19259e68a7c7c3154e18ad198fb42517f4c92cb84c20b5c28a4",
	"findings.schema.json": "270f185e1224f490df2ff2c2c7ce69c65433e40c7310a3c40ab64a0fe306d952",
	"post.schema.json":     "c21fdadd3378a6da0a3af63eb49e4026de5729d9c8c26bb99770326454edc766",
	"record.schema.json":   "2f92d7062b5410d48fd8332258685da393a2ea3003d313f55c0d29d6da8d0ecd",
	"review.schema.json":   "b875cd68c44c0ee670c094a26b474478e7b87f4584f6689f70691378703ec237",
}

// TestGeneratedSchemasPreservePublishedSemantics proves the generated files
// parse to the exact documents the hand-written schemas published.
func TestGeneratedSchemasPreservePublishedSemantics(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) != len(publishedSemantics) {
		t.Fatalf("generated %d schemas, semantic baseline has %d", len(files), len(publishedSemantics))
	}
	for _, file := range files {
		checkSemantics(t, file)
	}
}

// checkSemantics compares one generated file with its pinned document hash.
func checkSemantics(t *testing.T, file File) {
	t.Helper()
	want, ok := publishedSemantics[file.Name]
	if !ok {
		t.Errorf("%s has no semantic baseline", file.Name)
		return
	}
	got, err := semanticHash(file.Data)
	if err != nil {
		t.Fatalf("semantic hash %s: %v", file.Name, err)
	}
	if got != want {
		t.Errorf("%s changed published semantics; got %s, want %s (update the baseline only for an intentional schema change)", file.Name, got, want)
	}
}

// semanticHash re-encodes the parsed document with sorted keys and no
// insignificant whitespace, then hashes it.
func semanticHash(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
