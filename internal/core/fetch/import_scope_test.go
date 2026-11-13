package fetchcore

import (
	"os"
	"path/filepath"
	"testing"

	learncore "github.com/stainless-code/verdict/internal/core/learn"
	"github.com/stainless-code/verdict/internal/kb"
)

// TestImportCommaGlob refuses scopes the KB cannot represent faithfully.
func TestImportCommaGlob(t *testing.T) {
	dir := commaGlobPack(t)
	root := t.TempDir()
	req := &learncore.Request{Pack: dir, ID: "safe-rule", Rigor: "pointed", JSONMode: true}
	req.RepoRoot = root
	_, ref := learncore.Run(t.Context(), req)
	file := filepath.Join(root, kb.File)
	if ref == nil {
		t.Fatalf("comma glob imported; recalled for src/a: %d", commaGlobRecallCount(t, file))
	}
	if ref.Code != "bad-scope" || ref.Message != "pack rule glob contains a comma; KB scopes cannot represent it without changing its meaning" {
		t.Fatalf("comma glob refusal: %v", ref)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("refused import wrote KB: %v", err)
	}
}

func commaGlobPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"pack.json":          `{"schema_version":"1","name":"test/scope","version":"1.0.0","severity":{"must":"important"},"rules":[{"id":"safe-rule","severity":"must","globs":["src/a,b.go"],"category":"safety","description":"Check errors","examples":["return err"]}]}`,
		"rules/safe-rule.md": "Check errors",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func commaGlobRecallCount(t *testing.T, file string) int {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	rules, ok := recallRules(nil, raw)
	if !ok {
		t.Fatal("invalid imported KB")
	}
	return len(matchRules(rules, []string{"src/a"}))
}
