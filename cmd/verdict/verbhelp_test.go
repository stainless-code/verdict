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
		args    []string
		want    string
		notWant string
	}{
		{[]string{"post", "--help"}, "  post  <owner/repo> <pr>", "  fetch <owner/repo>"},
		{[]string{"post", "acme/app", "7", "-h"}, "--review-file", "  triage <owner/repo>"},
		{[]string{"learn", "--help"}, "learn --retire <id>", "  init ["},
		{[]string{"doctor", "--help"}, "  doctor [--json]", "usage: verdict"},
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
