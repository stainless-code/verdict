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
	_, err := cli.ResolvePrefs(&cli.Runner{}, map[string]string{"HOME": t.TempDir()},
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

func TestRemovedDiagramSettings(t *testing.T) {
	for _, tc := range []struct{ body, message string }{
		{`{"publish":{"diagrams":{"behavioral":true}}}`, "unknown publish.diagrams key 'behavioral' (want only structural)"},
		{`{"publish":{"diagrams":{"expanded":false}}}`, "unknown publish.diagrams key 'expanded' (want only structural)"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			ref := settingsRefusal(t, tc.body)
			if !strings.Contains(ref.Message, tc.message) {
				t.Fatalf("message = %q; want %q", ref.Message, tc.message)
			}
		})
	}
}

// TestPublishFixPromptSetting proves the new publish boolean travels the same
// path as its siblings: a JSON bool resolves, a non-bool refuses typed
// bad-settings, and an unknown near-miss names the allow-list.
func TestPublishFixPromptSetting(t *testing.T) {
	t.Parallel()
	res, err := cli.ResolvePrefs(&cli.Runner{}, map[string]string{"HOME": t.TempDir()},
		func(string) ([]byte, error) { return []byte(`{"publish":{"fix_prompt":false}}`), nil })
	if err != nil {
		t.Fatalf("publish.fix_prompt=false refused: %v", err)
	}
	if got := res.Effective.PublishFixPrompt; got != "false" {
		t.Fatalf("PublishFixPrompt = %q; want false", got)
	}
	if ref := settingsRefusal(t, `{"publish":{"fix_prompt":"yes"}}`); !strings.Contains(ref.Message,
		"publish.fix_prompt must be a boolean (want true|false)") {
		t.Fatalf("non-bool message = %q", ref.Message)
	}
	if ref := settingsRefusal(t, `{"publish":{"fixprompt":false}}`); !strings.Contains(ref.Message,
		"unknown publish key 'fixprompt'") {
		t.Fatalf("unknown-key message = %q", ref.Message)
	}
}
