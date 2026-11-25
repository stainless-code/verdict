//go:build darwin

package verifycore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/testkit"
)

// TestSandboxExecWarnsAboutOwnSecrets drives `verify --sandbox` on macOS with
// a seeded parent environment: a secret-named variable makes the run warn
// through the normal channel (so --json carries it), naming the variable and
// never its value; without one there is no such warning.
func TestSandboxExecWarnsAboutOwnSecrets(t *testing.T) {
	requireSandboxHost(t)
	const value = "ghp_0123456789abcdefSECRET"
	for _, tc := range []struct {
		name     string
		environ  []string
		wantWarn bool
	}{
		{"secret-present", []string{"PATH=/bin", "GH_TOKEN=" + value}, true},
		{"secret-absent", []string{"PATH=/bin", "EDITOR=vim"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			testkit.WriteFile(t, ".git/HEAD", "ref: refs/heads/main\n")
			testkit.WriteFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
			home := sandboxHomeFixture(t)
			cmdr := &sandboxCommander{results: []core.Result{{}, {Stdout: "ok"}}}
			req := sandboxRequest(append(tc.environ, "HOME="+home), cmdr)
			resp, ref := verifycore.Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			checkOwnSecretsWarning(t, resp.Warnings, value, tc.wantWarn)
		})
	}
}

func checkOwnSecretsWarning(t *testing.T, warnings []string, value string, want bool) {
	t.Helper()
	joined := strings.Join(warnings, "\n")
	if strings.Contains(joined, value) {
		t.Fatalf("warnings leak the secret value: %q", warnings)
	}
	got := strings.Contains(joined, "kern.procargs2") && strings.Contains(joined, "carries GH_TOKEN —")
	if got != want {
		t.Fatalf("own-secrets warning present = %t, want %t: %q", got, want, warnings)
	}
}
