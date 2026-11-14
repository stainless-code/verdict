package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

// generatedSchema returns one generated schema decoded for the in-memory
// checker, so the contract under test is the published document.
func generatedSchema(t *testing.T, name string) any {
	t.Helper()
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, file := range files {
		if file.Name != name {
			continue
		}
		schema, err := schemacheck.Decode(file.Data)
		if err != nil {
			t.Fatalf("decode generated %s: %v", name, err)
		}
		return schema
	}
	t.Fatalf("%s not generated", name)
	return nil
}

// TestIntegerFieldsTakeLiteralSpellingsOnly pins the repository's one integer
// rule where it used to split: an integer is written as an integer literal, so
// 8.0 and 1e1 are not integers in any published schema. The config reader and
// the pack manifest decode into Go integers and refuse those spellings; the
// schema check must refuse them too, or `validate.sh` passes a document the
// binary then rejects with bad-config or bad-pack.
func TestIntegerFieldsTakeLiteralSpellingsOnly(t *testing.T) {
	t.Run("config index.files", func(t *testing.T) {
		assertSpelledInteger(t, generatedSchema(t, "config.schema.json"),
			`{"schema_version":"1","index":{"files":%s}}`,
			func(doc string) *core.RefusalError { _, ref := config.Parse([]byte(doc)); return ref })
	})
	t.Run("pack max_files", func(t *testing.T) {
		assertSpelledInteger(t, generatedSchema(t, "pack.schema.json"),
			`{"schema_version":"1","name":"acme/pack","version":"0.1.0","lenses":[{"id":"perf","max_files":%s}]}`,
			loadPackRefusal)
	})
}

// assertSpelledInteger proves one integer field takes the literal spelling and
// refuses the zero-fraction and exponent spellings, on both sides: the schema
// check and the runtime parser. The literal row is the control — it shows the
// refusals are about the spelling and not about the field.
func assertSpelledInteger(t *testing.T, schema any, format string, parse func(string) *core.RefusalError) {
	t.Helper()
	for _, tc := range []struct {
		spelling string
		accepted bool
	}{
		{spelling: "8", accepted: true},
		{spelling: "8.0"},
		{spelling: "1e1"},
	} {
		doc := fmt.Sprintf(format, tc.spelling)
		value, err := schemacheck.Decode([]byte(doc))
		if err != nil {
			t.Fatalf("candidate is not JSON: %v", err)
		}
		schemaAccepts := len(schemacheck.Validate(schema, schema, value)) == 0
		if schemaAccepts != tc.accepted {
			t.Errorf("schema accepts %s = %t; want %t", doc, schemaAccepts, tc.accepted)
		}
		parserAccepts := parse(doc) == nil
		if parserAccepts != tc.accepted {
			t.Errorf("parser accepts %s = %t; want %t", doc, parserAccepts, tc.accepted)
		}
	}
}

// loadPackRefusal writes one pack manifest into a throwaway pack directory and
// returns what the real loader answers, so the runtime side is the loader a
// user's `pack list` runs.
func loadPackRefusal(doc string) *core.RefusalError {
	dir, err := os.MkdirTemp("", "pack-int-")
	if err != nil {
		return &core.RefusalError{Code: "temp", Message: err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.MkdirAll(filepath.Join(dir, "lenses"), 0o755); err != nil {
		return &core.RefusalError{Code: "temp", Message: err.Error()}
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(doc), 0o644); err != nil {
		return &core.RefusalError{Code: "temp", Message: err.Error()}
	}
	if err := os.WriteFile(filepath.Join(dir, "lenses", "perf.md"), []byte("body\n"), 0o644); err != nil {
		return &core.RefusalError{Code: "temp", Message: err.Error()}
	}
	_, ref := pack.LoadDir(dir)
	return ref
}
