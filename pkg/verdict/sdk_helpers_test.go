package verdict_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
	"github.com/stainless-code/verdict/pkg/verdict"
)

// TestMain scrubs the git environment the launcher exported.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// sdkNow is the deterministic clock every SDK test injects.
var sdkNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// sdkAt is sdkNow rendered in the envelope's UTC form.
const sdkAt = "2026-11-02T12:00:00Z"

// Heads and sigs shared by the SDK fixtures (40-hex findings sigs).
const (
	sdkHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sdkBaseB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	sdkSigCrit = "c111111111111111111111111111111111111111"
	sdkSigImp  = "b222222222222222222222222222222222222222"
	sdkSigAddr = "d444444444444444444444444444444444444444"
)

// sdkFinding renders one inline findings entry on notes.txt line 2.
func sdkFinding(sig, status, severity, rigor string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"` + rigor + `","severity":"` + severity + `",` +
		`"sig":"` + sig + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",` +
		`"status":"` + status + `","rationale":"","history":[]}`
}

// sdkStore is the canonical mixed findings store.
func sdkStore() string {
	return "[" + sdkFinding(sdkSigCrit, "open", "critical", "walked") + "," +
		sdkFinding(sdkSigImp, "open", "important", "pointed") + "," +
		sdkFinding(sdkSigAddr, "addressed", "important", "walked") + "]"
}

// sdkDiff is the one-hunk fixture diff every bundle carries.
const sdkDiff = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
`

// writePRBundle writes a GitHub-shaped bundle shell
// (.verdict/state/<pr>/PR.json plus the one-hunk diff).
func writePRBundle(t *testing.T, pr string) {
	t.Helper()
	writeFile(".verdict/state/"+pr+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+sdkHeadA+`","baseRefOid":"`+sdkBaseB+`",`+
			`"author":{"login":"teammate"}}`)
	writeFile(".verdict/state/"+pr+"/full.diff", sdkDiff)
}

// writeFindings writes a findings store into a bundle.
func writeFindings(t *testing.T, pr, store string) {
	t.Helper()
	writeFile(".verdict/state/"+pr+"/findings.json", store)
}

// sdkInvocation builds the repo/PR positionals every SDK fixture uses.
func sdkInvocation(pr string) verdict.Invocation {
	return verdict.Invocation{Repo: "acme/app", PR: pr}
}

// newSDKClient builds a client on the deterministic clock with the given
// stream observer and command runner.
func newSDKClient(progress verdict.Progress, cmdr verdict.Commander) *verdict.Client {
	c := verdict.New(verdict.Options{
		Now:       func() time.Time { return sdkNow },
		Progress:  progress,
		Commander: cmdr,
	})

	verdict.SetRepoRootForTest(c, func(context.Context) (string, error) { return ".", nil })
	return c
}

// recordedProgress keeps every observed line so tests can assert what the
// SDK streamed instead of only what it returned.
type recordedProgress struct {
	human []string
	warn  []string
	diag  []string
}

// Human implements verdict.Progress.
func (p *recordedProgress) Human(line string) { p.human = append(p.human, line) }

// Warn implements verdict.Progress.
func (p *recordedProgress) Warn(msg string) { p.warn = append(p.warn, msg) }

// Diag implements verdict.Progress.
func (p *recordedProgress) Diag(line string) { p.diag = append(p.diag, line) }

// joinedHuman renders the recorded human lines for substring assertions.
func (p *recordedProgress) joinedHuman() string { return strings.Join(p.human, "\n") }

// cannedRule answers one spawned command whose argv contains every token.
type cannedRule struct {
	tokens []string
	result verdict.CommandResult
}

// fakeCommander scripts the public Commander seam: the first rule that
// matches every token answers, and an unmatched spawn fails the test.
type fakeCommander struct {
	t     *testing.T
	rules []cannedRule
	calls [][]string
}

// Run implements verdict.Commander.
func (f *fakeCommander) Run(_ context.Context, cmd *verdict.Command) (verdict.CommandResult, error) {
	f.t.Helper()
	f.calls = append(f.calls, cmd.Argv)
	joined := strings.Join(cmd.Argv, " ")
	for _, rule := range f.rules {
		if containsAll(joined, rule.tokens) {
			return rule.result, nil
		}
	}
	f.t.Fatalf("unexpected command: %q", joined)
	return verdict.CommandResult{}, nil
}

// containsAll reports whether s contains every token.
func containsAll(s string, tokens []string) bool {
	for _, token := range tokens {
		if !strings.Contains(s, token) {
			return false
		}
	}
	return true
}

// spawned reports whether any recorded argv contains token.
func (f *fakeCommander) spawned(token string) bool {
	for _, argv := range f.calls {
		if strings.Contains(strings.Join(argv, " "), token) {
			return true
		}
	}
	return false
}

// decodeData decodes a response's raw data profile into a generic map.
func decodeData(t *testing.T, data json.RawMessage) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("data is not one JSON object: %v\n%s", err, data)
	}
	return doc
}

// asMap reads one object out of a decoded profile.
func asMap(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want an object", what, v)
	}
	return m
}

// asSlice reads one array out of a decoded profile.
func asSlice(t *testing.T, v any, what string) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("%s = %v, want an array", what, v)
	}
	return a
}

// assertNumbers asserts decoded numeric fields, so a dropped or renamed
// profile key reads red.
func assertNumbers(t *testing.T, doc map[string]any, want map[string]float64) {
	t.Helper()
	for key, value := range want {
		got, ok := doc[key].(float64)
		if !ok || got != value {
			t.Errorf("data.%s = %v, want %v", key, doc[key], value)
		}
	}
}

// assertFields asserts decoded fields of any type.
func assertFields(t *testing.T, doc map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if doc[key] != value {
			t.Errorf("data.%s = %v, want %v", key, doc[key], value)
		}
	}
}

// requireNoError fails the test when a call returned an error.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// requireRefusal fails unless err is the typed public refusal carrying code
// and hint.
func requireRefusal(t *testing.T, err error, code, hint string) {
	t.Helper()
	ref, ok := errors.AsType[*verdict.RefusalError](err)
	if !ok {
		t.Fatalf("err = %v, want *verdict.RefusalError", err)
	}
	if ref.Code != code {
		t.Errorf("code = %q, want %q", ref.Code, code)
	}
	if ref.Hint != hint {
		t.Errorf("hint = %q, want %q", ref.Hint, hint)
	}
	if ref.Message == "" {
		t.Error("message is empty, want the refusal text")
	}
}

// readText reads one fixture file as a string.
func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// splitLines returns the non-empty lines of a store file.
func splitLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// gitRun runs one git command in dir with the harness's pinned config.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "sdk",
		"GIT_AUTHOR_EMAIL":    "sdk@test",
		"GIT_COMMITTER_NAME":  "sdk",
		"GIT_COMMITTER_EMAIL": "sdk@test",
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// sdkGitRepo initializes dir as a git checkout holding the named files in
// one seed commit with the working tree clean.
func sdkGitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	gitRun(t, dir, "init", "-q")
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "seed")
}

// contains reports whether s contains sub, read as prose at the call sites.
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// calibrateStore renders the probe fixture: one cited addressed finding
// (real), one uncited open finding (unknown), and one cited
// false-positive (false).
func calibrateStore() string {
	return "[" + calibrateFinding(sdkSigCrit, "addressed", "critical",
		"Fixed body here with enough substance.", `["r-a1"]`) + "," +
		calibrateFinding(sdkSigImp, "open", "important",
			"Open body here with enough substance.", `[]`) + "," +
		calibrateFinding(sdkSigAddr, "false-positive", "important",
			"Unseen body here with enough substance.", `["r-a2"]`) + "]"
}

// calibrateFinding renders one inline entry with a body and kb cites.
func calibrateFinding(sig, status, severity, body, kb string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"` + body + `","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"` + severity + `",` +
		`"sig":"` + sig + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",` +
		`"status":"` + status + `","rationale":"","history":[],"kb":` + kb + `}`
}
