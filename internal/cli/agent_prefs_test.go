package cli_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestPrefsAgentNamesAreNotCredentialKeys pins the same boundary as the
// personal parser: an agent name is data, but a field inside its definition
// still refuses a credential key.
func TestPrefsAgentNamesAreNotCredentialKeys(t *testing.T) {
	for _, name := range []string{"token", "patchbot"} {
		t.Run(name, func(t *testing.T) { assertPrefsAgentNameAccepted(t, name) })
	}
}

func assertPrefsAgentNameAccepted(t *testing.T, name string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"agents": map[string]any{
		name: map[string]any{"binary": "local-agent"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.ResolvePrefs(&cli.Runner{}, map[string]string{"HOME": t.TempDir()},
		func(string) ([]byte, error) { return body, nil })
	if err != nil {
		t.Fatalf("ResolvePrefs refused agent name %q: %v", name, err)
	}
}

func TestPrefsCredentialFieldInsideAgentStillRefuses(t *testing.T) {
	body, err := json.Marshal(map[string]any{"agents": map[string]any{
		"local": map[string]any{"binary": "local-agent", "token": "abc"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.ResolvePrefs(&cli.Runner{}, map[string]string{"HOME": t.TempDir()},
		func(string) ([]byte, error) { return body, nil })
	var ref *cli.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("err = %T; want a typed settings refusal", err)
	}
	if ref.Code != "bad-settings-credentials" {
		t.Errorf("code = %q; want bad-settings-credentials", ref.Code)
	}
}
