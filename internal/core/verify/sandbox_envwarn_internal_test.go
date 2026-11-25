package verifycore

import (
	"strings"
	"testing"
)

// TestExposedSecretsWarning pins the decision on every host: only a mechanism
// that leaves other processes' environments readable warns, only for
// variables the redactor treats as secrets (secret-shaped name, value of at
// least 8 bytes), and the warning names them sorted and never carries a value.
func TestExposedSecretsWarning(t *testing.T) {
	const ghValue, awsValue = "ghp_0123456789abcdef", "wJalrXUtnFEMI/K7MDENG"
	environ := []string{
		"PATH=/bin", "HOME=/Users/dev", "GH_TOKEN=" + ghValue,
		"AWS_SECRET_ACCESS_KEY=" + awsValue, "API_TOKEN=short", "EDITOR=vim",
	}
	for _, tc := range []struct {
		name, mechanism, wantNames string
		environ                    []string
	}{
		{"sandbox-exec-with-secrets", sandboxMechanismSandboxExec, "AWS_SECRET_ACCESS_KEY, GH_TOKEN", environ},
		{"network-only-with-secrets", sandboxMechanismUnshareNet, "AWS_SECRET_ACCESS_KEY, GH_TOKEN", environ},
		{"bwrap-hides-processes", sandboxMechanismBwrap, "", environ},
		{"not-sandboxed", "", "", environ},
		{"no-secrets", sandboxMechanismSandboxExec, "", []string{"PATH=/bin", "API_TOKEN=short"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkExposedSecretsWarning(t, exposedSecretsWarning(tc.mechanism, tc.environ), tc.wantNames, ghValue, awsValue)
		})
	}
}

// checkExposedSecretsWarning requires no warning when wantNames is empty,
// else one naming exactly wantNames and none of the values or non-secrets.
func checkExposedSecretsWarning(t *testing.T, got, wantNames string, values ...string) {
	t.Helper()
	if wantNames == "" {
		if got != "" {
			t.Fatalf("warning = %q, want none", got)
		}
		return
	}
	if !strings.Contains(got, "verdict's own environment carries "+wantNames+" —") {
		t.Errorf("warning = %q, want it to name exactly %s", got, wantNames)
	}
	for _, leak := range append(values, "API_TOKEN") {
		if strings.Contains(got, leak) {
			t.Errorf("warning = %q leaks %q", got, leak)
		}
	}
}
