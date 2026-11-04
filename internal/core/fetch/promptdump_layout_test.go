package fetchcore

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadTrustedPromptLayouts pins where the prompt dump finds a trusted
// prompt: flat in a skill directory, else under a checkout's skills/verdict,
// the flat copy winning, and a typed miss when neither exists.
func TestReadTrustedPromptLayouts(t *testing.T) {
	const name = "prompts/system.md"
	tests := []struct {
		name  string
		files map[string]string
		want  string
		found bool
	}{
		{name: "flat skill dir", files: map[string]string{name: "flat\n"}, want: "flat\n", found: true},
		{name: "checkout skills/verdict", files: map[string]string{"skills/verdict/" + name: "nested\n"}, want: "nested\n", found: true},
		{name: "flat wins over the checkout copy", files: map[string]string{name: "flat\n", "skills/verdict/" + name: "nested\n"}, want: "flat\n", found: true},
		{name: "neither", files: map[string]string{"prompts/review.md": "other\n"}, found: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(plantFiles(t, tc.files))
			assertTrustedPrompt(t, name, tc.want, tc.found)
		})
	}
}

// assertTrustedPrompt reads one prompt from the current directory and checks
// both whether it was found and what it held.
func assertTrustedPrompt(t *testing.T, name, want string, found bool) {
	t.Helper()
	got, err := readTrustedPrompt(name)
	if found != (err == nil) {
		t.Fatalf("readTrustedPrompt(%q) err = %v, want found=%v", name, err, found)
	}
	if string(got) != want {
		t.Errorf("readTrustedPrompt(%q) = %q, want %q", name, got, want)
	}
}

// plantFiles writes the slash-keyed files into a fresh temp dir and returns it.
func plantFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
