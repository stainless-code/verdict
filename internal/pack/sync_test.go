package pack

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// TestSyncWithoutBasePinRefuses proves `pack sync` with no base-owned pin
// refuses typed rather than vending nothing.
func TestSyncWithoutBasePinRefuses(t *testing.T) {
	t.Parallel()
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true, Now: fixedNow}
	code := Run(t.Context(), r, []string{"sync"}, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: t.TempDir(),
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout %q)", code, cli.ExitRefused, out.String())
	}
	if !strings.Contains(out.String(), `"bad-pack"`) {
		t.Errorf("envelope lacks bad-pack:\n%s", out.String())
	}
}

// TestSyncUsageErrors proves `pack sync` takes no extra arguments.
func TestSyncUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"sync", "extra"}, {"sync", "--bogus"}} {
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
