package cli_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// TestSettingsKeySurfacesListEveryAcceptedKey pins the surfaces that tell an
// operator which settings keys exist. The expected set comes from the same list
// the loader uses, so adding a key without updating the hint or either settings
// refusal message fails here.
func TestSettingsKeySurfacesListEveryAcceptedKey(t *testing.T) {
	accepted := core.SettingsTopKeys()
	if len(accepted) == 0 {
		t.Fatal("SettingsTopKeys is empty; the check would pass vacuously")
	}
	assertListsEveryKey(t, "bad-settings hint", core.HintFor("bad-settings"), accepted)
	assertListsEveryKey(t, "unknown-key message", settingsRefusal(t, `{"bogus":1}`).Message, accepted)
	assertListsEveryKey(t, "non-object message", settingsRefusal(t, `[]`).Message, accepted)
}

func settingsRefusal(t *testing.T, body string) *cli.RefusalError {
	t.Helper()
	_, err := cli.ResolvePrefs(&cli.Runner{}, map[string]string{"HOME": "/home/operator"},
		func(string) ([]byte, error) { return []byte(body), nil })
	var ref *cli.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("err = %T; want a typed settings refusal", err)
	}
	if ref.Code != "bad-settings" {
		t.Fatalf("code = %q; want bad-settings", ref.Code)
	}
	return ref
}

func assertListsEveryKey(t *testing.T, surface, message string, accepted []string) {
	t.Helper()
	for _, key := range accepted {
		if !strings.Contains(message, key) {
			t.Errorf("%s %q omits accepted key %q", surface, message, key)
		}
	}
}
