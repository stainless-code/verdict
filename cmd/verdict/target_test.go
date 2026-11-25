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

// TestRepoOnlyVerbsStopAtAFlag pins that a flag before the repo means the repo
// is absent for trend and risk, so the flag survives and origin fills the repo.
func TestRepoOnlyVerbsStopAtAFlag(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"trend", "risk"} {
		_, repo, pr, rest := splitCommand([]string{verb, "--overlap", "--json"})
		if repo != "" || pr != "" || !slices.Equal(rest, []string{"--overlap", "--json"}) {
			t.Errorf("%s flag-first split = %q %q %q; want no repo and both flags kept", verb, repo, pr, rest)
		}
		_, repo, _, rest = splitCommand([]string{verb, "acme/app", "--json"})
		if repo != "acme/app" || !slices.Equal(rest, []string{"--json"}) {
			t.Errorf("%s repo-first split = %q %q", verb, repo, rest)
		}
	}
}

// TestRepoOnlyVerbFlagFirstInfersOrigin runs the verbs through the front door
// in a checkout: the flag is kept and the echo names the origin's repository.
func TestRepoOnlyVerbFlagFirstInfersOrigin(t *testing.T) {
	targetCheckout(t, false)
	_, env := runEnvelope(t, "trend", "--json")
	if env.Repo != "acme/app" {
		t.Fatalf("trend envelope repo = %q, want acme/app (origin)", env.Repo)
	}
	code, _, derr := runCapture(t, []string{"risk", "--status", "open"})
	if strings.Contains(derr, usageText()) || !strings.Contains(derr, "target: acme/app (github, repository from the origin remote)") {
		t.Fatalf("risk --status open: exit %d\nstderr: %s", code, derr)
	}
}

// TestThreadsResolveKeepsItsSubverb pins that the subverb sitting where the
// repo would is put back after the pair behind it is resolved.
func TestThreadsResolveKeepsItsSubverb(t *testing.T) {
	targetCheckout(t, true)
	for _, tc := range []struct {
		name string
		pr   string
		rest []string
		want []string
	}{
		{"URL form", "https://github.com/acme/app/pull/12", []string{"--dry-run"}, []string{"12", "--dry-run"}},
		{"inferred form", "", []string{"--dry-run"}, []string{"12", "--dry-run"}},
		{"explicit form", "acme/app", []string{"7", "--thread", "x"}, []string{"7", "--thread", "x"}},
	} {
		got := resolveTarget("threads", "resolve", tc.pr, tc.rest, nil, os.Getenv)
		wantPr := "acme/app"
		if got.Refusal != nil || got.Repo != "resolve" || got.Pr != wantPr || !slices.Equal(got.Rest, tc.want) {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	got := resolveTarget("threads", "resolve", "https://github.com/acme/app/pull/12", nil, nil, os.Getenv)
	if slices.Contains(got.Rest, "--provider") {
		t.Fatalf("threads resolve takes no --provider flag: %+v", got)
	}
}

func TestThreadsResolveOutsideACheckoutIsUsage(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	got := resolveTarget("threads", "resolve", "", nil, nil, os.Getenv)
	if got.Refusal != nil || got.Repo != "resolve" || got.Pr != "" {
		t.Fatalf("nothing to read must leave the verb's own usage error: %+v", got)
	}
}

// TestNoJSONShapesTheTargetRefusal pins that --no-json (a global force) is
// applied before the target refusal renders, so a JSON default in the
// environment does not turn it into an envelope.
func TestNoJSONShapesTheTargetRefusal(t *testing.T) {
	targetCheckout(t, false)
	t.Setenv("REVIEW_OUTPUT_FORMAT", "json")
	code, out, derr := runCapture(t, []string{"report", "--no-json"})
	if code != cli.ExitRefused || out != "" || !strings.Contains(derr, "error: ") {
		t.Fatalf("exit %d\nstdout: %q\nstderr: %s", code, out, derr)
	}
}

// TestNoJSONKeepsTheTargetEcho pins the echo under the same override.
func TestNoJSONKeepsTheTargetEcho(t *testing.T) {
	targetCheckout(t, true)
	t.Setenv("REVIEW_OUTPUT_FORMAT", "json")
	_, _, derr := runCapture(t, []string{"report", "--no-json"})
	if !strings.Contains(derr, "target: acme/app#12") {
		t.Fatalf("the echo was suppressed by the env's JSON default:\n%s", derr)
	}
}
