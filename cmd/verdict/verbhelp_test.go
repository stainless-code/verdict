package main

import (
	"strings"
	"testing"
)

// TestVerbHelpPrintsOnlyThatVerb proves `<verb> --help` answers with the
// verb's own block on stdout (exit 0), not the global usage, and that the
// verbs owning their help and an arg after `--` are left alone.
func TestVerbHelpPrintsOnlyThatVerb(t *testing.T) {
	hermeticEnv(t)
	for _, tc := range []struct {
		want    string
		notWant string
		args    []string
	}{
		{want: "  post  <owner/repo> <pr>", notWant: "  fetch <owner/repo>", args: []string{"post", "--help"}},
		{want: "--review-file", notWant: "  triage <owner/repo>", args: []string{"post", "acme/app", "7", "-h"}},
		{want: "learn --retire <id>", notWant: "  init [", args: []string{"learn", "--help"}},
		{want: "  doctor [--provider github|gitlab|gitcafe|forgejo|bitbucket|azure]", notWant: "usage: verdict", args: []string{"doctor", "--help"}},
	} {
		code, out, derr := runCapture(t, tc.args)
		if code != 0 || derr != "" || !strings.Contains(out, tc.want) || strings.Contains(out, tc.notWant) {
			t.Errorf("%v = exit %d stdout %q stderr %q, want a lone block with %q", tc.args, code, out, derr, tc.want)
		}
	}
	if _, ok := verbHelp([]string{"fetch-local", "a/b", "x", "--", "--help"}); ok {
		t.Error("a --help after -- is a path, not a help request")
	}
	if _, ok := verbHelp([]string{"skill", "--help"}); ok {
		t.Error("skill owns its --help")
	}
	if _, ok := verbHelp([]string{"bogus", "--help"}); ok {
		t.Error("an unknown verb has no block")
	}
}

// TestVerifyHelpNamesGroupValidationFlags proves `verify --help` documents
// the deep group-validation pair the adapter already parses: both flags and
// the receipt they write, so a caller never has to read verify's source.
func TestVerifyHelpNamesGroupValidationFlags(t *testing.T) {
	hermeticEnv(t)
	code, out, derr := runCapture(t, []string{"verify", "--help"})
	if code != 0 || derr != "" {
		t.Fatalf("verify --help = exit %d stderr %q", code, derr)
	}
	for _, want := range []string{"--group <stableID>", "--command <cmd>", "validation-<group>.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify --help misses %q:\n%s", want, out)
		}
	}
}

// TestServeUsageListsEventsRoute pins the route in the usage text.
func TestServeUsageListsEventsRoute(t *testing.T) {
	const want = "    .../findings/{sig}, /v1/events (--no-watch removes it). GET only, no CORS, no writes; Ctrl-C exits 3.\n"
	if !strings.Contains(usageText(), want) {
		t.Fatalf("serve usage route line = %q, want %q", usageText(), want)
	}
}
