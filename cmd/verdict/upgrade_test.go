package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestUpgradeFlagErrors pins the usage errors for both verbs.
func TestUpgradeFlagErrors(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("VERDICT_INSTALL_METHOD", "")
	cases := []struct { //nolint:govet // test table shape, not a GC shape
		args []string
		want string
	}{
		{[]string{"upgrade", "--bogus"}, "unknown argument '--bogus' for upgrade"},
		{[]string{"upgrade", "--version"}, "--version needs a value"},
		{[]string{"uninstall", "--version", "1.2.3"}, "unknown argument '--version' for uninstall"},
		{[]string{"uninstall", "stray"}, "unknown argument 'stray' for uninstall"},
	}
	for _, tc := range cases {
		code, out, derr := runCapture(t, tc.args)
		if code != cli.ExitUsage || out != "" || !strings.Contains(derr, tc.want) {
			t.Errorf("%v = %d/%q/%q; want exit 2 and %q", tc.args, code, out, derr, tc.want)
		}
	}
}

// TestUpgradeDryRunPlan pins the adapter's plan/envelope wiring for a
// refused channel.
func TestUpgradeDryRunPlan(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("VERDICT_INSTALL_METHOD", "project-local")
	code, out, derr := runCapture(t, []string{"upgrade", "--dry-run", "--json"})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, derr)
	}
	var env struct { //nolint:govet // decode target mirrors the envelope, not a GC shape
		Schema string         `json:"schema_version"`
		Verb   string         `json:"verb"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, out)
	}
	if env.Verb != "upgrade" || env.Schema != "1" {
		t.Errorf("envelope verb/schema = %q/%q", env.Verb, env.Schema)
	}
	if env.Data["channel"] != "project-local" || env.Data["action"] != "refuse" || env.Data["dry_run"] != true {
		t.Errorf("data = %v", env.Data)
	}
	if !strings.Contains(derr, "upgrade: dry-run: nothing changed") {
		t.Errorf("stderr = %q; want the dry-run line", derr)
	}
}

// TestUninstallRefusalEnvelope pins the typed refusal exit and code.
func TestUninstallRefusalEnvelope(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("VERDICT_INSTALL_METHOD", "npm")
	code, out, _ := runCapture(t, []string{"uninstall", "--json"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, out)
	}
	if env.Error.Code != "uninstall-foreign" {
		t.Errorf("error.code = %q; want uninstall-foreign", env.Error.Code)
	}
}

// TestHelperModeRejectsMissingFlags pins the hidden helper's usage gate.
func TestHelperModeRejectsMissingFlags(t *testing.T) {
	hermeticEnv(t)
	code, out, derr := runCapture(t, []string{"__upgrade-helper"})
	if code != cli.ExitUsage || out != "" || !strings.Contains(derr, "upgrade-helper") {
		t.Fatalf("helper = %d/%q/%q; want exit 2 and an upgrade-helper line", code, out, derr)
	}
}
