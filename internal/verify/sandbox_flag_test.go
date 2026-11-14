package verify

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestSandboxFlagReachesTheRequest(t *testing.T) {
	flags, code, done := parseFlags(&cli.Runner{}, []string{"--sandbox", "--timeout", "10"})
	if done {
		t.Fatalf("parseFlags refused with exit %d", code)
	}
	if !flags.sandbox {
		t.Fatal("--sandbox did not set the parsed flag")
	}
	req := verifyRequest(&cli.Runner{}, Request{Repo: "acme/app", Pr: "7"}, &flags)
	if !req.Sandbox {
		t.Error("verify request lost the sandbox opt-in")
	}
}

func TestEnvAllowlistFlagReachesTheRequest(t *testing.T) {
	flags, code, done := parseFlags(&cli.Runner{}, []string{
		"--env-allowlist", "FOO_TOKEN,BAR_URL",
		"--env-allowlist", "BAZ_DIR",
	})
	if done {
		t.Fatalf("parseFlags refused with exit %d", code)
	}
	want := []string{"FOO_TOKEN", "BAR_URL", "BAZ_DIR"}
	if !slices.Equal(flags.envAllowlist, want) {
		t.Fatalf("flags.envAllowlist = %v, want %v", flags.envAllowlist, want)
	}
	req := verifyRequest(&cli.Runner{}, Request{Repo: "acme/app", Pr: "7"}, &flags)
	if !slices.Equal(req.EnvAllowlist, want) {
		t.Errorf("req.EnvAllowlist = %v, want %v", req.EnvAllowlist, want)
	}
}
