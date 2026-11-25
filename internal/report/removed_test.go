package report

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/gate"
)

const depHead = testRiskHeadSHA

// TestGatingFlagsAreNotReportFlags pins that report is the advisory read: each
// flag that used to fail its exit is now an ordinary unknown flag (usage exit
// 2, no envelope), because gate is the only gating verb.
func TestGatingFlagsAreNotReportFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--check", "high"},
		{"--completeness"},
		{"--max-risk", "medium"},
		{"--live", "strict"},
	} {
		t.Run(args[0], func(t *testing.T) {
			t.Chdir(t.TempDir())
			var out, diag bytes.Buffer
			r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Cmd: "report"}
			code := Run(context.Background(), r, args, Request{Repo: "acme/app", Pr: "7"})
			if code != cli.ExitUsage {
				t.Fatalf("exit = %d, want %d; stderr=%q", code, cli.ExitUsage, diag.String())
			}
			if out.Len() != 0 {
				t.Errorf("a usage refusal printed on stdout: %q", out.String())
			}
			if want := "unknown flag '" + args[0] + "' for report"; !strings.Contains(diag.String(), want) {
				t.Errorf("stderr = %q, want it to contain %q", diag.String(), want)
			}
		})
	}
}

// depFinding is one open inline finding at severity and category.
func depFinding(severity, category string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
		`"body":"Alias fixture body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
		`"severity":"` + severity + `","category":"` + category + `","sig":"` + strings.Repeat("2", 40) + `",` +
		`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
}

// TestRemovedFlagsGateEquivalents proves gate reproduces what each removed
// report gating flag decided: each row is a pre-removal `report`
// invocation, the gate argv that replaces it, and the exit and gate result the
// report produced over that fixture (read off the last release, kept literal).
// `--coverage warn` and `--live skip` pin the two postures report never graded.
func TestRemovedFlagsGateEquivalents(t *testing.T) {
	post := `{"head":"` + depHead + `","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Isolated.","assessed_head":"` + depHead + `"}}`
	postHigh := strings.Replace(post, `"level":"low"`, `"level":"high"`, 1)
	critical := "[" + depFinding("critical", "security") + "]"
	tests := []struct {
		name       string
		oldReport  string
		findings   string
		post       string
		gateArgs   []string
		wantExit   int
		wantStatus map[string]string
	}{
		{
			name: "check-fails-on-an-open-critical", oldReport: "report --check critical", findings: critical, post: post,
			gateArgs: []string{"--max-severity", "critical", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitRefused, wantStatus: map[string]string{"findings": "fail", "spec": "pass", "evidence": "pass"},
		},
		{
			name: "check-passes-below-the-tier", oldReport: "report --check critical", post: post,
			findings: "[" + depFinding("informational", "style") + "]",
			gateArgs: []string{"--max-severity", "critical", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitOK, wantStatus: map[string]string{"findings": "pass"},
		},
		{
			name: "check-with-category-narrows-the-class", oldReport: "report --check critical --category style", findings: critical, post: post,
			gateArgs: []string{"--max-severity", "critical", "--category", "style", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitOK, wantStatus: map[string]string{"findings": "pass"},
		},
		{
			name: "completeness-passes-on-a-matching-post", oldReport: "report --completeness", findings: "[]", post: post,
			gateArgs: []string{"--require-head", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitOK, wantStatus: map[string]string{"head": "pass"},
		},
		{
			name: "completeness-fails-on-a-stale-post", oldReport: "report --completeness", findings: "[]",
			post:     `{"head":"` + strings.Repeat("c", 40) + `"}`,
			gateArgs: []string{"--require-head", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitRefused, wantStatus: map[string]string{"head": "fail"},
		},
		{
			name: "check-and-completeness-compose", oldReport: "report --check critical --completeness --live skip", findings: critical, post: post,
			gateArgs: []string{"--max-severity", "critical", "--require-head", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitRefused, wantStatus: map[string]string{"findings": "fail", "head": "pass"},
		},
		{
			name: "live-strict-rides-the-check", oldReport: "report --live strict --check important", findings: "[]", post: post,
			gateArgs: []string{"--max-severity", "important", "--coverage", "warn", "--live", "strict"},
			wantExit: cli.ExitOK, wantStatus: map[string]string{"findings": "pass"},
		},
		{
			name: "max-risk-passes-at-the-claim", oldReport: "report --max-risk low", findings: "[]", post: post,
			gateArgs: []string{"--max-risk", "low", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitOK, wantStatus: map[string]string{"risk": "pass"},
		},
		{
			name: "max-risk-fails-over-the-claim", oldReport: "report --max-risk medium", findings: "[]", post: postHigh,
			gateArgs: []string{"--max-risk", "medium", "--coverage", "warn", "--live", "skip"},
			wantExit: cli.ExitRefused, wantStatus: map[string]string{"risk": "fail"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupRiskCLIBundle(t, tc.post)
			findings := filepath.Join(".verdict", "state", "local-risk-cli", "findings.json")
			if err := os.WriteFile(findings, []byte(tc.findings), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, diag bytes.Buffer
			r := cli.NewRunner("gate", "acme/app", "local-risk-cli", &out, &diag, func(string) string { return "" })
			r.Effective = cli.Defaults()
			r.JSONMode = true
			r.RouteHuman()
			code := gate.Run(context.Background(), r, tc.gateArgs, Request{Repo: "acme/app", Pr: "local-risk-cli"})
			if code != tc.wantExit {
				t.Fatalf("%s as gate %v: exit = %d, want %d\nstdout: %s\nstderr: %s",
					tc.oldReport, tc.gateArgs, code, tc.wantExit, out.String(), diag.String())
			}
			var env struct {
				Data struct {
					Dimensions []struct{ ID, Status string } `json:"dimensions"`
					Passed     bool                          `json:"passed"`
				} `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("gate stdout is not one envelope: %v\n%s", err, out.String())
			}
			if env.Data.Passed != (tc.wantExit == cli.ExitOK) {
				t.Errorf("passed = %v for exit %d", env.Data.Passed, code)
			}
			got := map[string]string{}
			for _, d := range env.Data.Dimensions {
				got[d.ID] = d.Status
			}
			for id, want := range tc.wantStatus {
				if got[id] != want {
					t.Errorf("dimension %s = %q, want %q (all: %v)", id, got[id], want, got)
				}
			}
		})
	}
}
