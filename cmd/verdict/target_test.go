package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/target"
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

// assertTargetRefusal checks the human shape of the usage-class refusal: the
// typed error naming the verb's args, then the verb's own usage line.
func assertTargetRefusal(t *testing.T, derr, verb string) {
	t.Helper()
	if !strings.Contains(derr, "error: ") || !strings.Contains(derr, "verdict "+verb+" ") ||
		!strings.Contains(derr, "\nusage: verdict "+verb+" <owner/repo> <pr> ") {
		t.Fatalf("stderr is not the %s target refusal plus its usage line:\n%s", verb, derr)
	}
}

func checkoutWithoutBundle(t *testing.T) {
	t.Helper()
	targetCheckout(t, false)
}

func outsideACheckout(t *testing.T) {
	t.Helper()
	hermeticEnv(t)
	t.Chdir(t.TempDir())
}

// TestMissingTargetRefusesAsUsageClass pins the one behavior of every pair
// verb that names no resolvable target, inside a checkout that records no
// bundle for its branch and outside any checkout alike: exit 2, the typed
// refusal plus the verb's usage line, and under --json the envelope.
func TestMissingTargetRefusesAsUsageClass(t *testing.T) {
	for name, setup := range map[string]func(*testing.T){
		"checkout without a recorded bundle": checkoutWithoutBundle,
		"outside a checkout":                 outsideACheckout,
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			checkMissingTarget(t)
		})
	}
}

func checkMissingTarget(t *testing.T) {
	t.Helper()
	code, out, derr := runCapture(t, []string{"report"})
	if code != cli.ExitUsage || out != "" {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, derr)
	}
	assertTargetRefusal(t, derr, "report")
	code, env := runEnvelope(t, "report", "--json")
	if code != cli.ExitUsage || env.Error == nil || env.Error.Code != "target-unresolved" {
		t.Fatalf("json refusal: exit %d %+v", code, env)
	}
}

// TestCheckoutRefusalNamesTheBranch keeps the branch and the exact args in the
// message a checkout produces.
func TestCheckoutRefusalNamesTheBranch(t *testing.T) {
	targetCheckout(t, false)
	_, _, derr := runCapture(t, []string{"report"})
	if !strings.Contains(derr, `branch "fix-x"`) || !strings.Contains(derr, "verdict report acme/app <pr>") {
		t.Fatalf("stderr:\n%s", derr)
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

func TestThreadsResolveOutsideACheckoutRefuses(t *testing.T) {
	hermeticEnv(t)
	t.Chdir(t.TempDir())
	got := resolveTarget("threads", "resolve", "", nil, nil, os.Getenv)
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "verdict threads resolve <owner/repo> <pr>") {
		t.Fatalf("nothing to read must refuse naming the subverb's args: %+v", got)
	}
}

func TestUsageLineNamesTheVerbsOwnEntry(t *testing.T) {
	t.Parallel()
	for verb, want := range map[string]string{
		"fetch":           "usage: verdict fetch <owner/repo> <pr> ",
		"threads resolve": "usage: verdict threads resolve <owner/repo> <pr> ",
		"nosuchverb":      "usage: verdict nosuchverb <owner/repo> <pr> [opts]",
	} {
		if got := usageLine(verb); !strings.HasPrefix(got, want) || strings.Contains(got, "\n") {
			t.Errorf("usageLine(%q) = %q, want one line starting %q", verb, got, want)
		}
	}
}

// TestNoJSONShapesTheTargetRefusal pins that --no-json (a global force) is
// applied before the target refusal renders, so a JSON default in the
// environment does not turn it into an envelope.
func TestNoJSONShapesTheTargetRefusal(t *testing.T) {
	targetCheckout(t, false)
	t.Setenv("REVIEW_OUTPUT_FORMAT", "json")
	code, out, derr := runCapture(t, []string{"report", "--no-json"})
	if code != cli.ExitUsage || out != "" || !strings.Contains(derr, "error: ") {
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

// TestTalksToHost pins which verbs may ask the host for a branch's pull
// request: only those that already read it, and drain only with --post.
func TestTalksToHost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		verb string
		rest []string
		want bool
	}{
		{"fetch", nil, true},
		{"pr-wait", nil, true},
		{"ready", nil, true},
		{"threads", nil, true},
		{"threads resolve", []string{"--dry-run"}, true},
		{"drain", nil, false},
		{"drain", []string{"--agent", "claude"}, false},
		{"drain", []string{"--post"}, true},
		{"drain", []string{"--", "--post"}, false},
		{"report", nil, false},
		{"show", nil, false},
		{"next", nil, false},
		{"risk", nil, false},
		{"trend", nil, false},
		{"gate", nil, false},
		{"handoff", nil, false},
		{"journal", nil, false},
		{"calibrate", nil, false},
	} {
		if got := talksToHost(tc.verb, tc.rest); got != tc.want {
			t.Errorf("talksToHost(%q, %v) = %v, want %v", tc.verb, tc.rest, got, tc.want)
		}
	}
}

// TestHostFlagCheckersCoverEveryHostVerb pins the flag checkers to the verbs
// that may ask the host, so none starts asking without a pre-lookup check.
func TestHostFlagCheckersCoverEveryHostVerb(t *testing.T) {
	t.Parallel()
	want := []string{"drain"}
	for verb := range hostVerbs {
		want = append(want, verb)
	}
	got := make([]string, 0, len(hostFlagCheckers))
	for verb := range hostFlagCheckers {
		got = append(got, verb)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("hostFlagCheckers verbs = %v, host-talking verbs = %v", got, want)
	}
}

// TestPrArgFollowsTheVerbsUsageLine pins the number placeholder a refusal
// names to the one the verb's own usage entry spells.
func TestPrArgFollowsTheVerbsUsageLine(t *testing.T) {
	t.Parallel()
	for verb, want := range map[string]string{
		"fetch":      "<pr>",
		"run":        "<pr|local-name>",
		"fix":        "<local-slug|pr>",
		"journal":    "<pr|local-name>",
		"nosuchverb": "<pr>",
	} {
		if got := prArgFor(verb); got != want {
			t.Errorf("prArgFor(%q) = %q, want %q", verb, got, want)
		}
	}
}

func TestMissingTargetNamesTheVerbsOwnNumberArgument(t *testing.T) {
	outsideACheckout(t)
	for verb, want := range map[string]string{"run": "<pr|local-name>", "fix": "<local-slug|pr>", "report": "<pr>"} {
		_, _, derr := runCapture(t, []string{verb})
		if !strings.Contains(derr, "verdict "+verb+" <owner/repo> "+want) {
			t.Errorf("%s: stderr lacks %q:\n%s", verb, want, derr)
		}
	}
}

// TestURLProviderEqualsFlagPassesTheFlagCheck pins the documented URL spelling
// `--provider=<name>`: the verb's own flag check runs before the URL's
// provider is resolved, so it must see the spaced form every verb accepts.
func TestURLProviderEqualsFlagPassesTheFlagCheck(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("PATH", t.TempDir())
	t.Chdir(t.TempDir())
	_, _, derr := runCapture(t, []string{"fetch", "https://github.com/acme/app/pull/12", "--provider=github"})
	if strings.Contains(derr, "unknown flag") {
		t.Fatalf("the documented --provider=github spelling was refused as a flag error:\n%s", derr)
	}
}

// TestFlagRefusalFollowsTheFinalOutputMode pins that a host verb's flag
// refusal renders after the global flags apply: the last of --json and
// --no-json decides, so no envelope reaches stdout here.
func TestFlagRefusalFollowsTheFinalOutputMode(t *testing.T) {
	targetCheckout(t, false)
	code, out, derr := runCapture(t, []string{"pr-wait", "acme/app", "12", "--json", "--no-json", "--frobnicate"})
	if code == 0 || out != "" || !strings.Contains(derr, "frobnicate") {
		t.Fatalf("exit %d\nstdout: %q\nstderr: %s", code, out, derr)
	}
	code, out, _ = runCapture(t, []string{"pr-wait", "acme/app", "12", "--no-json", "--json", "--frobnicate"})
	if code == 0 || !strings.Contains(out, `"code": "bad-flags"`) {
		t.Fatalf("a later --json wins and gets the envelope: exit %d stdout %q", code, out)
	}
}

// TestCancelSurvivesUnreadableSettings pins that a canceled target resolution
// exits 3 with the cancel refusal even when the settings file is corrupt.
func TestCancelSurvivesUnreadableSettings(t *testing.T) {
	hermeticEnv(t)
	writeCorruptSettings(t)
	outFile, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	r := newRunner("fetch", "", "", outFile, errFile, os.Getenv)
	tgt := &target.Resolved{Refusal: &core.RefusalError{Code: "canceled", Message: "canceled before target resolution completed"}}
	code, done := gateInvocation(r, "fetch", tgt, &cli.GlobalForces{}, envMap(os.Getenv), os.ReadFile, errFile)
	raw, rerr := os.ReadFile(errFile.Name())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !done || code != cli.ExitBroke || strings.Contains(string(raw), "settings") || !strings.Contains(string(raw), "canceled") {
		t.Fatalf("code %d done %v stderr %q; want exit 3 naming the cancel, not the settings", code, done, raw)
	}
}
