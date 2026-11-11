package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShellQuote pins the quoting rule: a plain path stays bare so today's
// hints read the same, and every other rune gets POSIX single quotes.
func TestShellQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain path", "/usr/local/bin/verdict", "/usr/local/bin/verdict"},
		{"package name", "@stainless-code/verdict", "@stainless-code/verdict"},
		{"empty", "", ""},
		{"space", "/tmp/my dir/verdict", "'/tmp/my dir/verdict'"},
		{"single quote", "/tmp/it's/verdict", `'/tmp/it'\''s/verdict'`},
		{"dollar", "/tmp/$HOME/verdict", "'/tmp/$HOME/verdict'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shellQuote(tc.in); got != tc.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestUninstallCommandQuotesPaths proves each path hint quotes a spaced dir, so
// the printed command still resolves when copied.
func TestUninstallCommandQuotesPaths(t *testing.T) {
	t.Parallel()
	t.Run("project root with a space", func(t *testing.T) {
		t.Parallel()
		root := spacedProject(t)
		exe := filepath.Join(root, "node_modules", "verdict", "bin", "verdict")
		got := uninstallCommand(&detection{Channel: ChannelProject, exe: exe})
		want := "cd '" + root + "' && npm uninstall @stainless-code/verdict"
		if got != want {
			t.Errorf("uninstallCommand = %q, want %q", got, want)
		}
	})
	t.Run("skills dir with a space", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(t.TempDir(), "home dir")
		exe := filepath.Join(home, ".agents", "skills", "verdict", "bin", "verdict")
		got, ok := skillUninstallCommand(exe)
		if !ok {
			t.Fatalf("skillUninstallCommand(%q) = false, want the skill command", exe)
		}
		want := "verdict skill uninstall --dir '" + filepath.Join(home, ".agents", "skills") + "' --force"
		if got != want {
			t.Errorf("skillUninstallCommand = %q, want %q", got, want)
		}
	})
}

func spacedProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "my project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestSkillUninstallCommandOwnsTheSkillDecision pins the refusal's branch:
// the resolved verdict root reports its command, and any other skills dir
// reports false with no command text to render.
func TestSkillUninstallCommandOwnsTheSkillDecision(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	verdictExe := filepath.Join(home, ".agents", "skills", "verdict", "bin", "verdict")
	cmd, ok := skillUninstallCommand(verdictExe)
	if !ok {
		t.Fatalf("skillUninstallCommand(%q) = false, want the skill command", verdictExe)
	}
	if !strings.HasPrefix(cmd, "verdict skill uninstall --dir ") || !strings.HasSuffix(cmd, " --force") {
		t.Errorf("skillUninstallCommand = %q, want the skill uninstall command", cmd)
	}
	siblingExe := filepath.Join(home, ".agents", "skills", "other", "bin", "verdict")
	if cmd, ok := skillUninstallCommand(siblingExe); ok || cmd != "" {
		t.Errorf("skillUninstallCommand(%q) = %q, %v, want no command for a sibling root", siblingExe, cmd, ok)
	}
}
