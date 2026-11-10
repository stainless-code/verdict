package mcpconfig

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateSnippets = flag.Bool("update", false, "regenerate testdata snippet goldens")

type snippetCase struct {
	name     string
	agent    string
	readOnly bool
	operator bool
}

var snippetGoldenCases = []snippetCase{
	{"claude", AgentClaude, false, false},
	{"cursor", AgentCursor, false, false},
	{"codex", AgentCodex, false, false},
	{"gemini", AgentGemini, false, false},
	{"qwen", AgentQwen, false, false},
	{"windsurf", AgentWindsurf, false, false},
	{"opencode", AgentOpencode, false, false},
	{"goose", AgentGoose, false, false},
	{"copilot", AgentCopilot, false, false},
	{"claude-read-only", AgentClaude, true, false},
	{"claude-operator", AgentClaude, false, true},
	{"codex-read-only", AgentCodex, true, false},
	{"codex-operator", AgentCodex, false, true},
}

// TestSnippetGoldens pins every host's printed entry plus the posture variants.
func TestSnippetGoldens(t *testing.T) {
	t.Parallel()
	for _, tc := range snippetGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pinSnippetGolden(t, tc)
		})
	}
}

func pinSnippetGolden(t *testing.T, tc snippetCase) {
	t.Helper()
	req := &Request{
		Agent: tc.agent, Cwd: t.TempDir(),
		Env:      map[string]string{"HOME": t.TempDir()},
		ReadOnly: tc.readOnly, Operator: tc.operator,
	}
	res, ref := Run(req)
	if ref != nil {
		t.Fatalf("%s: %v", tc.name, ref)
	}
	path := filepath.Join("testdata", "snippets", tc.name+".golden")
	if *updateSnippets {
		writeSnippetGolden(t, path, res.Snippet)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (re-run with -update to create it)", tc.name, err)
	}
	if string(raw) != res.Snippet {
		t.Errorf("%s snippet drifted:\ngot\n%s\nwant\n%s", tc.name, res.Snippet, raw)
	}
}

func writeSnippetGolden(t *testing.T, path, snippet string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(snippet), 0o644); err != nil {
		t.Fatal(err)
	}
}
