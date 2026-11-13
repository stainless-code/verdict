package main

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestVerbMissingValue pins one text for a verb flag given without its value:
// every parser reports it through cli.MissingValue, so it never reads as an
// unknown flag and never drifts per verb.
func TestVerbMissingValue(t *testing.T) {
	hermeticEnv(t)
	cases := []struct {
		name string
		want string
		args []string
	}{
		{"skill show format", "--format needs a value (want md or json)", []string{"skill", "--format"}},
		{"skill install dir", "--dir needs a value", []string{"skill", "install", "--dir"}},
		{"skill install agent", "--agent needs a value", []string{"skill", "install", "--agent"}},
		{"upgrade version", "--version needs a value", []string{"upgrade", "--version"}},
		{"mcp config agent", "--agent needs a value (want claude, cursor, codex, gemini, qwen, windsurf, opencode, goose, or copilot)", []string{"mcp", "config", "--agent"}},
		{"fetch base-sha", "--base-sha needs a value (want <sha>)", []string{"fetch", "acme/app", "7", "--base-sha"}},
		{"fetch since", "--since needs a value (want <sha>)", []string{"fetch", "acme/app", "7", "--since"}},
		{"fetch provider", "--provider needs a value (want <name>)", []string{"fetch", "acme/app", "7", "--provider"}},
		{"fetch-local range", "--range needs a value (want A[..|...]B)", []string{"fetch-local", "acme/app", "x", "--range"}},
		{"fetch-local base", "--base needs a value (want <ref>)", []string{"fetch-local", "acme/app", "x", "--base"}},
		{"adopt from", "--from needs a value (want <local-slug>)", []string{"adopt", "acme/app", "7", "--from"}},
		{"fix sig", "--sig needs a value (want <sig>)", []string{"fix", "acme/app", "local-x", "--sig"}},
		{"post approve", "--approve needs a value (want true|false)", []string{"post", "acme/app", "7", "--approve"}},
		{"post review-file", "--review-file needs a value (want <path>)", []string{"post", "acme/app", "7", "--review-file"}},
		{"post summary", "--summary needs a value (want comment|off)", []string{"post", "acme/app", "7", "--summary"}},
		{"triage sig", "--sig needs a value (want <sig>)", []string{"triage", "acme/app", "7", "--sig"}},
		{"triage status", "--status needs a value (want <status>)", []string{"triage", "acme/app", "7", "--status"}},
		{"triage rationale", "--rationale needs a value (want <text>)", []string{"triage", "acme/app", "7", "--rationale"}},
		{"report status", "--status needs a value (want all|open|addressed|deferred|wont-fix|false-positive)", []string{"report", "acme/app", "7", "--status"}},
		{"report kind", "--kind needs a value (want all|inline|section)", []string{"report", "acme/app", "7", "--kind"}},
		{"report output", "--output needs a value (want <path>)", []string{"report", "acme/app", "7", "--output"}},
		{"report sarif", "--sarif needs a value (want <path>)", []string{"report", "acme/app", "7", "--sarif"}},
		{"report check", "--check needs a value (want critical|important|informational)", []string{"report", "acme/app", "7", "--check"}},
		{"report live", "--live needs a value (want auto|strict|skip)", []string{"report", "acme/app", "7", "--live"}},
		{"next status", "--status needs a value (want <status>)", []string{"next", "acme/app", "7", "--status"}},
		{"show sig", "--sig needs a value (want <sig>)", []string{"show", "acme/app", "7", "--sig"}},
		{"check-tree expect", "--expect needs a value (want <prefix,...>)", []string{"check-tree", "--expect"}},
		{"verify timeout", "--timeout needs a value (want <secs>)", []string{"verify", "acme/app", "7", "--timeout"}},
		{"learn rule", "--rule needs a value (want <text>)", []string{"learn", "--rule"}},
		{"learn scope", "--scope needs a value (want <globs>)", []string{"learn", "--scope"}},
		{"learn id", "--id needs a value (want <id>)", []string{"learn", "--id"}},
		{"learn provenance", "--provenance needs a value (want <s>)", []string{"learn", "--provenance"}},
		{"learn rigor", "--rigor needs a value (want <level>)", []string{"learn", "--rigor"}},
		{"learn retire", "--retire needs a value (want <id>)", []string{"learn", "--retire"}},
		{"learn restore", "--restore needs a value (want <id>)", []string{"learn", "--restore"}},
		{"learn reason", "--reason needs a value (want <text>)", []string{"learn", "--reason"}},
		{"init preset", "--preset needs a value (want chill|standard|strict)", []string{"init", "--preset"}},
		{"threads provider", "--provider needs a value (want <name>)", []string{"threads", "acme/app", "7", "--provider"}},
		{"calibrate judge", "--judge needs a value (want SIG:VERDICT[:HELPFUL])", []string{"calibrate", "acme/app", "7", "--judge"}},
		{"calibrate judge-file", "--judge-file needs a value (want PATH)", []string{"calibrate", "acme/app", "7", "--judge-file"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkUsageMessage(t, tc.args, tc.want)
		})
	}
}

func checkUsageMessage(t *testing.T, args []string, want string) {
	t.Helper()
	code, out, derr := runCapture(t, args)
	if code != cli.ExitUsage {
		t.Errorf("%v exit = %d; want %d (stderr: %s)", args, code, cli.ExitUsage, derr)
	}
	if out != "" {
		t.Errorf("%v wrote stdout %q; a usage error stays on stderr", args, out)
	}
	if got := strings.TrimSpace(derr); got != want {
		t.Errorf("%v stderr = %q; want %q", args, got, want)
	}
}
