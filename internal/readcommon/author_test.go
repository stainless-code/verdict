package readcommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestAuthorReadsThroughTheCap proves the fingerprint reader honors the
// artifact cap instead of slurping whatever size a bundle carries, and that an
// unreadable claim is no claim at all.
func TestAuthorReadsThroughTheCap(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Author(dir); ok {
		t.Error("absent author.json read as a fingerprint")
	}
	writeAuthorBody(t, dir, `{"schema_version":"1","agent":"codex","model":null,"confidence":"medium",`+
		`"evidence":[{"field":"head_ref","signal":"codex/"},{"field":"body"},{"signal":"codex/"},"loose"]}`)
	assertAuthorCodex(t, dir)
	writeAuthorBody(t, dir, `{"agent":"codex","padding":"`+strings.Repeat("x", int(jq.MaxAuthorBytes))+`"}`)
	if file, ok := Author(dir); ok {
		t.Errorf("over-cap author.json read as %+v, want no claim", file)
	}
}

// TestAuthorRejectsAnUnknownClaim proves a document naming an agent or a
// confidence the schema does not publish reads as no claim, so no reader grows
// a fourth agent or a third confidence from a hand-edited file.
func TestAuthorRejectsAnUnknownClaim(t *testing.T) {
	cases := []struct{ name, body string }{
		{
			name: "unpublished agent",
			body: `{"schema_version":"1","agent":"gemini","model":null,"confidence":"high","evidence":[]}`,
		},
		{
			name: "unpublished confidence",
			body: `{"schema_version":"1","agent":"claude","model":null,"confidence":"certain","evidence":[]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAuthorBody(t, dir, tc.body)
			if file, ok := Author(dir); ok {
				t.Errorf("author = %+v, want no claim", file)
			}
		})
	}
}

func writeAuthorBody(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, AuthorSuffix[1:]), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertAuthorCodex(t *testing.T, dir string) {
	t.Helper()
	file, ok := Author(dir)
	if !ok || file.Agent != "codex" || file.Model != nil || file.Confidence != "medium" {
		t.Fatalf("author = %+v (ok %v), want codex/medium", file, ok)
	}
	if len(file.Evidence) != 1 || file.Evidence[0].Field != "head_ref" || file.Evidence[0].Signal != "codex/" {
		t.Errorf("evidence = %+v", file.Evidence)
	}
}
