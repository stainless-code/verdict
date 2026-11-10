package verify

import (
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestSandboxFlagReachesTheRequest proves --sandbox is parsed and forwarded,
// not merely accepted by the usage text.
func TestSandboxFlagReachesTheRequest(t *testing.T) {
	flags, code, done := parseFlags(&cli.Runner{}, []string{"--sandbox", "--timeout", "10"})
	if done {
		t.Fatalf("parseFlags refused with exit %d", code)
	}
	if !flags.sandbox {
		t.Fatal("--sandbox did not set the parsed flag")
	}
	req := verifyRequest(&cli.Runner{}, Request{Repo: "acme/app", Pr: "7"}, flags)
	if !req.Sandbox {
		t.Error("verify request lost the sandbox opt-in")
	}
}
