package verifycore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

type filterEnvCase struct {
	name        string
	environ     []string
	explicit    []string
	wantKept    []string
	wantDropped []string
	jsonMode    bool
	isWindows   bool
}

func checkFilterChildEnv(t *testing.T, tc *filterEnvCase) {
	t.Helper()
	kept, dropped := verifycore.FilterChildEnv(tc.environ, tc.explicit, tc.jsonMode, tc.isWindows)
	if kept == nil {
		t.Fatal("FilterChildEnv returned nil kept slice; os/exec would inherit parent environment")
	}
	if !slices.Equal(kept, tc.wantKept) {
		t.Errorf("kept = %v, want %v", kept, tc.wantKept)
	}
	if !slices.Equal(dropped, tc.wantDropped) {
		t.Errorf("dropped = %v, want %v", dropped, tc.wantDropped)
	}
}

func TestFilterChildEnvTable(t *testing.T) {
	cases := []filterEnvCase{
		{
			name: "allowlisted kept and unlisted dropped in sorted order",
			environ: []string{
				"PATH=/usr/bin:/bin",
				"HOME=/Users/dev",
				"LANG=en_US.UTF-8",
				"LC_ALL=C",
				"LC_CTYPE=UTF-8",
				"GOPATH=/Users/dev/go",
				"GOCACHE=/tmp/gocache",
				"CARGO_HOME=/Users/dev/.cargo",
				"VIRTUAL_ENV=/Users/dev/.venv",
				"ZETA_CUSTOM=zeta_val_12345",
				"ALPHA_CUSTOM=alpha_val_12345",
				"PYTHONPATH=/tmp/evil",
				"NODE_PATH=/tmp/evil_node",
			},
			wantKept: []string{
				"PATH=/usr/bin:/bin",
				"HOME=/Users/dev",
				"LANG=en_US.UTF-8",
				"LC_ALL=C",
				"LC_CTYPE=UTF-8",
				"GOPATH=/Users/dev/go",
				"GOCACHE=/tmp/gocache",
				"CARGO_HOME=/Users/dev/.cargo",
				"VIRTUAL_ENV=/Users/dev/.venv",
			},
			wantDropped: []string{"ALPHA_CUSTOM", "NODE_PATH", "PYTHONPATH", "ZETA_CUSTOM"},
		},
		{
			name: "secret-shaped names dropped even when matching LC prefix or allowlist",
			environ: []string{
				"PATH=/bin",
				"FOO_TOKEN=tok_secret_value_123",
				"LC_TOKEN=lc_secret_value_456",
				"LC_SECRET=lc_secret_value_789",
				"LC_API_KEY=lc_key_value_000",
				"AWS_SECRET_ACCESS_KEY=aws_secret_value_999",
				"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
				"GITHUB_TOKEN=ghp_1234567890abcdef",
			},
			wantKept: []string{"PATH=/bin"},
			wantDropped: []string{
				"AWS_SECRET_ACCESS_KEY",
				"FOO_TOKEN",
				"GITHUB_TOKEN",
				"LC_API_KEY",
				"LC_SECRET",
				"LC_TOKEN",
				"SSH_AUTH_SOCK",
			},
		},
		{
			name: "explicit opt-in passes secret and custom vars through without warning",
			environ: []string{
				"PATH=/bin",
				"FOO_TOKEN=tok_secret_value_123",
				"LC_TOKEN=lc_secret_value_456",
				"CUSTOM_ENDPOINT=https://internal.example",
				"OTHER_SECRET=dropped_secret_val",
			},
			explicit: []string{"FOO_TOKEN", "LC_TOKEN", "CUSTOM_ENDPOINT"},
			wantKept: []string{
				"PATH=/bin",
				"FOO_TOKEN=tok_secret_value_123",
				"LC_TOKEN=lc_secret_value_456",
				"CUSTOM_ENDPOINT=https://internal.example",
			},
			wantDropped: []string{"OTHER_SECRET"},
		},
		{
			name: "windows runtime vars and case-insensitive matching",
			environ: []string{
				"Path=C:\\Windows\\System32",
				"SystemRoot=C:\\Windows",
				"ComSpec=C:\\Windows\\System32\\cmd.exe",
				"ProgramFiles(x86)=C:\\Program Files (x86)",
				"lc_messages=en_US",
				"lc_token=win_secret_lc",
				"custom_opt=win_custom",
			},
			explicit:  []string{"CUSTOM_OPT"},
			isWindows: true,
			wantKept: []string{
				"Path=C:\\Windows\\System32",
				"SystemRoot=C:\\Windows",
				"ComSpec=C:\\Windows\\System32\\cmd.exe",
				"ProgramFiles(x86)=C:\\Program Files (x86)",
				"lc_messages=en_US",
				"custom_opt=win_custom",
			},
			wantDropped: []string{"lc_token"},
		},
		{
			name:        "all dropped in non-json mode returns non-nil empty slice",
			environ:     []string{"FOO_TOKEN=secret123"},
			jsonMode:    false,
			wantKept:    []string{},
			wantDropped: []string{"FOO_TOKEN"},
		},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			checkFilterChildEnv(t, tc)
		})
	}
}

func TestVerifyDefaultDenyAndExplicitAllowlist(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "ok"}}
	environ := []string{
		"PATH=/usr/bin",
		"HOME=/Users/dev",
		"FOO_TOKEN=supersecret_foo_token_999",
		"LC_TOKEN=supersecret_lc_token_888",
		"UNLISTED_VAR=unlisted_secret_value_777",
		"ALLOWED_CUSTOM=allowed_custom_value_666",
	}
	req := verifyRequest(c, cmdr, "10", environ, true)
	req.EnvAllowlist = []string{"ALLOWED_CUSTOM"}

	resp, ref := verifycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	wantEnv := []string{
		"PATH=/usr/bin",
		"HOME=/Users/dev",
		"ALLOWED_CUSTOM=allowed_custom_value_666",
		"NO_COLOR=1",
	}
	if got := cmdr.got[0].Env; !slices.Equal(got, wantEnv) {
		t.Errorf("child Env = %v, want %v", got, wantEnv)
	}
	wantWarn := "verify dropped 3 non-allowlisted child env vars (FOO_TOKEN, LC_TOKEN, UNLISTED_VAR); " +
		"pass --env-allowlist or .verdict/verify-env to include"
	if !slices.Equal(resp.Warnings, []string{wantWarn}) {
		t.Errorf("resp.Warnings = %v, want [%q]", resp.Warnings, wantWarn)
	}
	if !slices.Equal(c.warns, []string{wantWarn}) {
		t.Errorf("streamed warns = %v, want [%q]", c.warns, wantWarn)
	}
	assertNoValueLeak(t, strings.Join(resp.Warnings, "\n"),
		"supersecret_foo_token_999", "supersecret_lc_token_888", "unlisted_secret_value_777")
}

func assertNoValueLeak(t *testing.T, text string, forbidden ...string) {
	t.Helper()
	for _, secret := range forbidden {
		if strings.Contains(text, secret) {
			t.Errorf("output leaked secret value %q in:\n%s", secret, text)
		}
	}
}

func writeControlsVerifyEnv(t *testing.T, bundleDir, body string) {
	t.Helper()
	writeFile(t, filepath.Join(bundleDir, "controls", "verify-env"), body)
	sum := sha256.Sum256([]byte(body))
	shaHex := hex.EncodeToString(sum[:])
	manifest := fmt.Sprintf(`{
  "schema_version": "1",
  "policy": "base",
  "base_sha": "abcdef1234567890abcdef1234567890abcdef12",
  "sha": "abcdef1234567890abcdef1234567890abcdef12",
  "files": [
    {
      "path": %q,
      "status": "used",
      "bytes": %d,
      "sha256": %q
    }
  ]
}
`, controls.VerifyEnvPath, len(body), shaHex)
	writeFile(t, filepath.Join(bundleDir, controls.FileName), manifest)
}

func TestVerifyEnvControlsSnapshotTrustedWhileWorktreeIgnored(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	// Untrusted worktree .verdict/verify-env tries to opt in WORKTREE_EVIL_VAR.
	writeFile(t, ".verdict/verify-env", "WORKTREE_EVIL_VAR\n")
	// Base-snapshotted controls/verify-env opts in SNAPSHOT_VAR and FOO_TOKEN.
	writeControlsVerifyEnv(t, ".verdict/state/5201", "# comment line\n\nSNAPSHOT_VAR\r\n  FOO_TOKEN  \n")

	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "ok"}}
	environ := []string{
		"PATH=/bin",
		"SNAPSHOT_VAR=from_base_snapshot",
		"FOO_TOKEN=opted_in_secret_token",
		"WORKTREE_EVIL_VAR=from_untrusted_worktree",
	}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", environ, false))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	wantEnv := []string{
		"PATH=/bin",
		"SNAPSHOT_VAR=from_base_snapshot",
		"FOO_TOKEN=opted_in_secret_token",
	}
	if got := cmdr.got[0].Env; !slices.Equal(got, wantEnv) {
		t.Errorf("child Env = %v, want %v", got, wantEnv)
	}
	wantWarn := "verify dropped 1 non-allowlisted child env vars (WORKTREE_EVIL_VAR); " +
		"pass --env-allowlist or .verdict/verify-env to include"
	if !slices.Equal(resp.Warnings, []string{wantWarn}) {
		t.Errorf("resp.Warnings = %v, want [%q]", resp.Warnings, wantWarn)
	}
}

func TestVerifyWorktreeVerifyEnvIgnoredWithoutControlsManifest(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	writeFile(t, ".verdict/verify-env", "WORKTREE_ONLY_VAR\n")

	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "ok"}}
	environ := []string{"PATH=/bin", "WORKTREE_ONLY_VAR=should_not_pass"}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", environ, false))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if got := cmdr.got[0].Env; !slices.Equal(got, []string{"PATH=/bin"}) {
		t.Errorf("child Env = %v, want [PATH=/bin]", got)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "WORKTREE_ONLY_VAR") {
		t.Errorf("resp.Warnings = %v, want WORKTREE_ONLY_VAR dropped warning", resp.Warnings)
	}
}

func TestVerifyInvalidEnvAllowlistRefusesWithoutLeakingValue(t *testing.T) {
	for _, bad := range []string{"", "123BAD", "FOO_TOKEN=supersecret_inline_999", "BAD-NAME"} {
		t.Run(bad, func(t *testing.T) {
			checkInvalidEnvAllowlist(t, bad)
		})
	}
}

func checkInvalidEnvAllowlist(t *testing.T, bad string) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf ok"}`)
	c, cmdr := &collector{}, &scriptCommander{}
	req := verifyRequest(c, cmdr, "10", []string{"PATH=/bin"}, true)
	req.EnvAllowlist = []string{bad}
	_, ref := verifycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("ref = %v, want bad-flags", ref)
	}
	assertNoValueLeak(t, ref.Message, "supersecret_inline_999")
	if bad != "" {
		assertNoValueLeak(t, ref.Message, bad)
	}
	if len(cmdr.got) != 0 {
		t.Errorf("invalid allowlist spawned %d commands", len(cmdr.got))
	}
}

func TestVerifyInvalidVerifyEnvFileRefusesWithoutLeakingLine(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	writeControlsVerifyEnv(t, ".verdict/state/5201", "VALID_VAR\nFOO_TOKEN=supersecret_file_val_999\n")

	c, cmdr := &collector{}, &scriptCommander{}
	_, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("ref = %v, want bad-flags", ref)
	}
	if !strings.Contains(ref.Message, ".verdict/verify-env line 2") {
		t.Errorf("ref.Message = %q, want line number", ref.Message)
	}
	assertNoValueLeak(t, ref.Message, "supersecret_file_val_999")
	if len(cmdr.got) != 0 {
		t.Errorf("invalid .verdict/verify-env spawned %d commands", len(cmdr.got))
	}
}

func TestGroupValidationFiltersChildEnv(t *testing.T) {
	root := groupBundle(t)
	cmdr := &groupCommander{head: groupHead, out: "ok\n"}
	req := groupRequest(root, "group-a1", "go test ./...", cmdr)
	req.Environ = []string{
		"PATH=/usr/bin",
		"FOO_TOKEN=group_secret_token_123",
		"GROUP_OPT_IN=group_opted_in_456",
	}
	req.EnvAllowlist = []string{"GROUP_OPT_IN"}

	resp, ref := verifycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	if len(cmdr.runs) != 3 {
		t.Fatalf("runs = %d, want 3", len(cmdr.runs))
	}
	wantEnv := []string{"PATH=/usr/bin", "GROUP_OPT_IN=group_opted_in_456"}
	if got := cmdr.runs[1].Env; !slices.Equal(got, wantEnv) {
		t.Errorf("group command Env = %v, want %v", got, wantEnv)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "FOO_TOKEN") {
		t.Errorf("resp.Warnings = %v, want FOO_TOKEN dropped warning", resp.Warnings)
	}
	assertNoValueLeak(t, strings.Join(resp.Warnings, "\n"), "group_secret_token_123")
}
