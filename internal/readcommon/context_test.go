package readcommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestContextCitations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "context.json"), []byte(`[{"source":"docs/auth.md"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	comment := jq.NewObj("sources", []any{"docs/auth.md", "jira:UNKNOWN"})
	review := jq.NewObj("comments", []any{comment})
	var warnings []string
	WarnContextSources(dir, review, func(s string) { warnings = append(warnings, s) })
	if strings.Join(warnings, "\n") != "unknown context source: jira:UNKNOWN" {
		t.Fatalf("warnings = %v", warnings)
	}
	if got := SourcesLine(comment); got != "Sources used: docs/auth.md, jira:UNKNOWN" {
		t.Fatalf("line = %q", got)
	}
}
