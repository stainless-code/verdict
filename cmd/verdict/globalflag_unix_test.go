//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestMisplacedGlobalFlagMakesNoProviderCall leaves gh untouched.
func TestMisplacedGlobalFlagMakesNoProviderCall(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "gh-calls.log")
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\necho 'stub-gh: network disabled' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(t.TempDir())

	got := runOne(t, "fetch", "--color", "always")
	if got.code != cli.ExitUsage {
		t.Fatalf("fetch --color always exit = %d; want %d (stderr: %s)", got.code, cli.ExitUsage, got.err)
	}
	if calls := readCalls(t, log); calls != "" {
		t.Errorf("a misplaced global flag reached the provider: %s", calls)
	}

	if got := runOne(t, "fetch", "acme/app", "1"); got.code != 1 {
		t.Fatalf("control fetch exit = %d; want the stub's refusal (stderr: %s)", got.code, got.err)
	}
	if calls := readCalls(t, log); !strings.Contains(calls, "pr view 1 --repo acme/app") {
		t.Errorf("control call log = %q; want the stub's pr view call", calls)
	}
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
