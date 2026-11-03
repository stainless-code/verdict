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
	"cost.schema.json": "30031615315e1a60fac58a9b978b2e2ec30142001c92a78dbd25e7e34c46d217",
	// 08: additive fetchEstimate $defs entry (measured-usage basis fields).
	// BL04: additive showData/showFix/verifyHint $defs entries (show --json
	// data.fix). Neither change touched an existing property, required key,
	// or type.
	"envelope.schema.json": "25f0255f901143af705b7d6d8179b8154f0e76dd0ba63f8fd1da3e36d0bc4b6b",
	"findings.schema.json": "2ee11aaeb826ef09afcca6a675e7c5780ef05576c654e6f833545441921c2a26",
	"post.schema.json":     "f706cc28f621eb668c8d621b03013f65f706a352d0ddc4da8b64068f98a3f100",
	"record.schema.json":   "6e14c660471d31aa6a88e0cff787860a6ecdd0302cdc527eb511dde8542767fc",
	"review.schema.json":   "75471bb0d98ac1ddbc07be485ef00d7a2777501cb0756d50fbc100b2cb054423",
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
