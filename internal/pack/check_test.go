package pack

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// fixedNow is the clock every check renderer test pins.
func fixedNow() time.Time { return time.Unix(0, 0) }

// runCheckCLI runs one pack argv against a scratch repository root.
func runCheckCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Now: fixedNow}
	code := Run(t.Context(), r, args, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: t.TempDir(),
	})
	return code, out.String(), diag.String()
}

// TestCheckHumanRows proves `pack check <path>` prints the resolved rows: the
// path, the config source, the shipped packs, the offered lenses, and the
// matching rules.
func TestCheckHumanRows(t *testing.T) {
	t.Parallel()
	code, out, diag := runCheckCLI(t, "check", "cmd/verdict/main.go")
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, diag)
	}
	for _, want := range []string{
		"path     cmd/verdict/main.go",
		"config   none",
		"  builtin  verdict/go@1.1.0  source=builtin:go  sha256:",
		"  go  pack=verdict/go  layer=builtin  sha256:",
		"  go-context-lifecycle              severity=critical  pack=verdict/go  layer=builtin  globs=**/*.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if diag != "" {
		t.Errorf("stderr = %q, want empty", diag)
	}
}

// TestCheckJSONEnvelope proves the --json mode emits the typed profile with
// the published keys and the resolved rows.
func TestCheckJSONEnvelope(t *testing.T) {
	t.Parallel()
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true, Now: fixedNow}
	code := Run(t.Context(), r, []string{"check", "app.tsx"}, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: t.TempDir(),
	})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0 (stdout %q stderr %q)", code, out.String(), diag.String())
	}
	env := decodeCheckEnvelope(t, out.String())
	if env.Verb != "pack" || env.Data.Path != "app.tsx" || env.Data.Config != "none" {
		t.Fatalf("envelope = %+v", env)
	}
	if len(env.Data.Packs) != 13 || env.Data.Packs[0].Name != "verdict/builtin" {
		t.Errorf("packs = %+v, want the shipped builtin layer", env.Data.Packs)
	}
	if len(env.Data.Disabled) != 0 {
		t.Errorf("disabled = %v, want none", env.Data.Disabled)
	}
	requireTSResolution(t, &env)
}

// checkEnvelope is the decoded pack check --json envelope.
type checkEnvelope struct {
	Verb string `json:"verb"`
	Data struct {
		Path     string   `json:"path"`
		Config   string   `json:"config"`
		Disabled []string `json:"disabled"`
		Packs    []struct {
			Name  string `json:"name"`
			Layer string `json:"layer"`
		} `json:"packs"`
		Lenses []struct {
			ID   string `json:"id"`
			Pack string `json:"pack"`
		} `json:"lenses"`
		Rules []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
		} `json:"rules"`
	} `json:"data"`
}

// decodeCheckEnvelope decodes one rendered envelope.
func decodeCheckEnvelope(t *testing.T, body string) checkEnvelope {
	t.Helper()
	var env checkEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, body)
	}
	return env
}

// requireTSResolution proves the ts-js lens and its first rule reach the
// TypeScript path in the envelope.
func requireTSResolution(t *testing.T, env *checkEnvelope) {
	t.Helper()
	lens := slices.ContainsFunc(env.Data.Lenses, func(row struct {
		ID   string `json:"id"`
		Pack string `json:"pack"`
	},
	) bool {
		return row.ID == "ts-js" && row.Pack == "verdict/ts-js"
	})
	rule := slices.ContainsFunc(env.Data.Rules, func(row struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
	},
	) bool {
		return row.ID == "ts-js-floating-async" && row.Severity == "critical"
	})
	if !lens || !rule {
		t.Errorf("ts-js resolution missing: lenses=%v rules=%v", env.Data.Lenses, env.Data.Rules)
	}
}

// TestCheckUsageErrors proves a missing path and a flag-shaped path are usage
// errors, not silent list runs or path guesses.
func TestCheckUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"check"}, {"check", "--json"}, {"check", "a", "b"}} {
		code, out, diag := runCheckCLI(t, args...)
		if code != cli.ExitUsage {
			t.Errorf("%v exit = %d, want %d", args, code, cli.ExitUsage)
		}
		if !strings.Contains(diag, "usage: verdict pack") {
			t.Errorf("%v stderr lacks the usage block: %q", args, diag)
		}
		if out != "" {
			t.Errorf("%v wrote stdout: %q", args, out)
		}
	}
}

// TestCheckUnknownFlagNamesItself proves an invalid flag is named back, not
// read as a path.
func TestCheckUnknownFlagNamesItself(t *testing.T) {
	t.Parallel()
	code, _, diag := runCheckCLI(t, "check", "--bogus")
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, cli.ExitUsage)
	}
	if !strings.Contains(diag, `"--bogus"`) {
		t.Errorf("stderr does not name the flag: %q", diag)
	}
}

// TestCheckRefusesUnsafePath proves the CLI surfaces the typed bad-path
// refusal for a path that escapes the repository.
func TestCheckRefusesUnsafePath(t *testing.T) {
	t.Parallel()
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true, Now: fixedNow}
	code := Run(t.Context(), r, []string{"check", "../../etc/passwd"}, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: t.TempDir(),
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout %q)", code, cli.ExitRefused, out.String())
	}
	if !strings.Contains(out.String(), `"bad-path"`) {
		t.Errorf("envelope lacks bad-path:\n%s", out.String())
	}
}
