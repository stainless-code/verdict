package verifycore

import (
	"fmt"
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

// TestExposedSecretsWarningNames pins how names are rendered: a duplicate
// is listed once, a name outside the env-name shape (a newline, an escape)
// is counted and never echoed, a name embedding a token shape is redacted,
// and the list stops at maxNamedSecrets with the rest counted.
func TestExposedSecretsWarningNames(t *testing.T) {
	const value = "value-long-enough-1234"
	token := "ghp_" + strings.Repeat("A1b2C3d4E5", 4)[:36]
	many := make([]string, 0, maxNamedSecrets+2)
	for i := range maxNamedSecrets + 2 {
		many = append(many, fmt.Sprintf("K%02d_TOKEN=%s", i, value))
	}
	for _, tc := range []struct {
		name, want, absent string
		environ            []string
	}{
		{"duplicate-listed-once", "carries GH_TOKEN —", "GH_TOKEN, GH_TOKEN", []string{"GH_TOKEN=" + value, "GH_TOKEN=" + value}},
		{
			"malformed-counted-not-echoed", "carries GH_TOKEN, and 1 malformed name(s) —", "EVIL",
			[]string{"GH_TOKEN=" + value, "EVIL\nINJECTED_TOKEN=" + value},
		},
		{"token-shaped-name-redacted", "_TOKEN —", token, []string{token + "_TOKEN=" + value}},
		{"capped", "K09_TOKEN, and 2 more —", "K10_TOKEN", many},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := exposedSecretsWarning(sandboxMechanismSandboxExec, tc.environ)
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.absent) || strings.Contains(got, value) {
				t.Fatalf("warning = %q, want %q and no %q or value", got, tc.want, tc.absent)
			}
		})
	}
}
