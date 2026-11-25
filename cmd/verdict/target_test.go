package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestSplitCommandPairEndsAtFlag pins that a flag before the pair means the
// pair is absent (inference may fill it), instead of being read as a repo.
func TestSplitCommandPairEndsAtFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		wantTarget string
		repo, pr   string
		args       []string
		wantRest   []string
	}{
		{"flag first", "", "", []string{"post", "--dry-run"}, []string{"--dry-run"}},
		{"repo then flag", "acme/app", "", []string{"post", "acme/app", "--dry-run"}, []string{"--dry-run"}},
		{"url then flag", "https://github.com/a/b/pull/1", "", []string{"post", "https://github.com/a/b/pull/1", "--json"}, []string{"--json"}},
		{"full pair", "acme/app", "7", []string{"post", "acme/app", "7", "--json"}, []string{"--json"}},
		{"sentinel", "", "", []string{"post", "--", "x"}, []string{"--", "x"}},
	} {
		_, repo, pr, rest := splitCommand(tc.args)
		if repo != tc.repo || pr != tc.pr || !slices.Equal(rest, tc.wantRest) {
			t.Errorf("%s: split %v = %q %q %q", tc.wantTarget, tc.args, repo, pr, rest)
		}
	}
}

// targetCheckout seeds a git checkout whose origin is acme/app on GitHub, on
// branch fix-x, optionally recording that branch as PR 12's head.
func targetCheckout(t *testing.T, recorded bool) {
	t.Helper()
	hermeticEnv(t)
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "fix-x")
	gitIn(t, root, "remote", "add", "origin", "git@github.com:acme/app.git")
	if recorded {
		dir := filepath.Join(root, ".verdict", "state", "12")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeIn(t, filepath.Join(dir, "PR.json"),
			`{"headRefName":"fix-x","state":"OPEN","url":"https://github.com/acme/app/pull/12"}`)
	}
	t.Chdir(root)
}

type targetEnvelope struct {
	Pr    any `json:"pr"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
	Repo string `json:"repo"`
}

func runEnvelope(t *testing.T, args ...string) (int, targetEnvelope) {
	t.Helper()
	code, out, derr := runCapture(t, args)
	var env targetEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v is not one envelope: %v\nstdout: %s\nstderr: %s", args, err, out, derr)
	}
	return code, env
}

// TestMissingTargetInCheckoutRefuses pins the refusal that replaces the usage
// block when a checkout can be read but names no pull request.
func TestMissingTargetInCheckoutRefuses(t *testing.T) {
	targetCheckout(t, false)
	code, out, derr := runCapture(t, []string{"report"})
	if code != cli.ExitRefused || out != "" ||
		!strings.Contains(derr, `branch "fix-x"`) || !strings.Contains(derr, "verdict report acme/app <pr>") {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, derr)
	}
	code, env := runEnvelope(t, "report", "--json")
	if code != cli.ExitRefused || env.Error == nil || env.Error.Code != "target-unresolved" {
		t.Fatalf("json refusal: exit %d %+v", code, env)
	}
}

// TestInferredTargetReachesTheVerbAndIsEchoed pins that inference supplies the
// pair the verb runs on, and that the human surface says so on stderr.
func TestInferredTargetReachesTheVerbAndIsEchoed(t *testing.T) {
	targetCheckout(t, true)
	_, env := runEnvelope(t, "report", "--json")
	if env.Repo != "acme/app" || env.Pr != float64(12) {
		t.Fatalf("envelope target = %q %v, want acme/app 12", env.Repo, env.Pr)
	}
	_, _, derr := runCapture(t, []string{"report"})
	if !strings.Contains(derr, "target: acme/app#12 (github, inferred from branch \"fix-x\" recorded in .verdict/state/12)") {
		t.Fatalf("stderr lacks the target echo:\n%s", derr)
	}
}

// TestURLTargetReachesTheVerbAndIsEchoed pins the pasted-URL form end to end
// through the front door, including the echo and a typed refusal.
func TestURLTargetReachesTheVerbAndIsEchoed(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	url := "https://github.com/acme/app/pull/12/files#discussion_r9"
	_, env := runEnvelope(t, "report", url, "--json")
	if env.Repo != "acme/app" || env.Pr != float64(12) {
		t.Fatalf("envelope target = %q %v, want acme/app 12", env.Repo, env.Pr)
	}
	_, _, derr := runCapture(t, []string{"report", url})
	if !strings.Contains(derr, "target: acme/app#12 (github, from the URL)") {
		t.Fatalf("stderr lacks the target echo:\n%s", derr)
	}
	for _, args := range [][]string{
		{"fetch", url, "--provider", "gitlab"},
		{"fetch", url, "13"},
		{"report", "https://github.com/acme/../x/pull/1"},
		{"report", "https://ghe.corp.example/a/b/pull/1"},
	} {
		code, _, derr := runCapture(t, args)
		if code != cli.ExitRefused || !strings.Contains(derr, "error: ") {
			t.Fatalf("%v: exit %d stderr %s", args, code, derr)
		}
	}
}

// TestFetchLocalKeepsItsOwnSyntax pins that the local verb never reads a URL
// or an inferred pair: its second positional is a bundle name.
func TestFetchLocalKeepsItsOwnSyntax(t *testing.T) {
	targetCheckout(t, true)
	code, _, derr := runCapture(t, []string{"fetch-local"})
	if code != cli.ExitUsage || derr != usageText() {
		t.Fatalf("bare fetch-local: exit %d stderr %q", code, derr)
	}
}
