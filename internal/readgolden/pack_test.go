package readgolden_test

import (
	"testing"
)

// packLayers plants a built-in override fixture: an org pack the repo pack
// replaces, with one rule per layer in the fenced KB section.
func packLayers(t *testing.T, dir, home string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		"org-packs/pack.json": `{"schema_version":"1","name":"acme/org","version":"2.0.0",` +
			`"severity":{"security":"critical"},"lenses":[{"id":"security"}],` +
			`"rules":[{"id":"no-secrets","severity":"security","globs":["**/*.go"]}]}`,
		"org-packs/lenses/security.md":  "security body\n",
		"org-packs/rules/no-secrets.md": "never log a secret\n",
		".verdict/packs/repo/pack.json": `{"schema_version":"1","name":"acme/repo","version":"0.1.0",` +
			`"replace":["acme/org"],"severity":{"style":"important"},` +
			`"lenses":[{"id":"security"},{"id":"style"}],` +
			`"rules":[{"id":"repo-style","severity":"style","globs":["**/*.go"],"tools":["gofmt"]}]}`,
		".verdict/packs/repo/lenses/security.md":  "repo security body\n",
		".verdict/packs/repo/lenses/style.md":     "repo style body\n",
		".verdict/packs/repo/rules/repo-style.md": "run gofmt\n",
	})
	writeFiles(t, home, map[string]string{
		".config/verdict/settings.json": `{"packs":["org-packs"]}`,
	})
}

// packConflict plants a repo pack that overrides the built-in lens without a
// replace declaration, beside a stray file the repo layer warns about, so the
// refusal's carried warning has a human surface to pin.
func packConflict(t *testing.T, dir, _ string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		".verdict/packs/README.md": "docs\n",
		".verdict/packs/repo/pack.json": `{"schema_version":"1","name":"acme/repo","version":"0.1.0",` +
			`"lenses":[{"id":"perf"}]}`,
		".verdict/packs/repo/lenses/perf.md": "repo perf\n",
	})
}

// TestPackGolden pins the pack list read surface: the three-layer JSON and
// human rows, and the undeclared-override refusal.
func TestPackGolden(t *testing.T) {
	t.Parallel()
	goldenRead(t, "pack/layers-json", packLayers, nil, nil, "pack", "list", "--json")
	goldenRead(t, "pack/layers-human", packLayers, nil, nil, "pack", "list")
	goldenRead(t, "pack/conflict-json", packConflict, nil, nil, "pack", "list", "--json")
	goldenRead(t, "pack/conflict-human", packConflict, nil, nil, "pack", "list")
}
