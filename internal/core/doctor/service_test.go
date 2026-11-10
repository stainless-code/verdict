package doctorcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// commander records every argv a run issues and answers the ones it knows.
type commander struct {
	replies map[string]string
	argvs   [][]string
}

func (c *commander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.argvs = append(c.argvs, append([]string{}, cmd.Argv...))
	if stdout, ok := c.replies[strings.Join(cmd.Argv, " ")]; ok {
		return core.Result{Stdout: stdout}, nil
	}
	return core.Result{ExitCode: 1}, nil
}

func (c *commander) argvLines() []string {
	lines := make([]string, 0, len(c.argvs))
	for _, argv := range c.argvs {
		lines = append(lines, strings.Join(argv, " "))
	}
	return lines
}

// runDoctor executes one preflight over an explicit environment and returns
// the checks it reported.
func runDoctor(t *testing.T, provider string, env map[string]string, cmdr *commander) []Check {
	t.Helper()
	req := &Request{Provider: provider}
	req.Runtime = &core.Runtime{Env: env, Commander: cmdr}
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("doctor refused: %+v", ref)
	}
	return resp.Data.Checks
}

func rowNames(checks []Check) []string {
	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.Name)
	}
	return names
}

func findCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q row in %v", name, rowNames(checks))
	return Check{}
}

// TestHostRowTables pins each host's row list: the host's own CLI rows, the
// shared ps/git rows, its reachability row, and storage — so routing a host to
// another host's probes fails here.
func TestHostRowTables(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		"github":    {"gh-cli", "gh-auth", "token-scopes", "ps", "git", "api-reachable", "storage-writable"},
		"gitlab":    {"glab-cli", "glab-auth", "ps", "git", "api-reachable", "storage-writable"},
		"gitcafe":   {"cafe-cli", "ps", "git", "storage-writable"},
		"forgejo":   {"curl", "forgejo-auth", "ps", "git", "storage-writable"},
		"bitbucket": {"curl", "bitbucket-auth", "ps", "git", "storage-writable"},
		"azure":     {"az", "azure-auth", "ps", "git", "storage-writable"},
	}
	for provider, names := range want {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			got := rowNames(runDoctor(t, provider, nil, &commander{}))
			if strings.Join(got, ",") != strings.Join(names, ",") {
				t.Errorf("%s rows = %v, want %v", provider, got, names)
			}
		})
	}
}

// TestDefaultProviderIsGitHub proves an unset provider keeps today's GitHub
// preflight, so nobody who never passes --provider changes rows.
func TestDefaultProviderIsGitHub(t *testing.T) {
	t.Parallel()
	got := rowNames(runDoctor(t, "", nil, &commander{}))
	want := []string{"gh-cli", "gh-auth", "token-scopes", "ps", "git", "api-reachable", "storage-writable"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default rows = %v, want %v", got, want)
	}
}

// TestHostCLIArgvs pins which CLI each host's preflight shells: gitlab reads
// through glab and github through gh, so routing both to one probe list fails
// here. The `--version` read is dropped from both sides first: it only runs
// when the CLI resolves on PATH, which the test machine does not decide.
func TestHostCLIArgvs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		want     []string
	}{
		{"github", []string{
			"gh auth status",
			"gh api user --include",
			"gh api rate_limit --jq .resources.core.remaining",
		}},
		{"gitlab", []string{
			"glab auth status",
			"glab api user",
		}},
		{"forgejo", nil},
		{"bitbucket", nil},
		{"gitcafe", nil},
		{"azure", nil},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			cmdr := &commander{}
			runDoctor(t, tc.provider, nil, cmdr)
			got := withoutVersion(cmdr.argvLines())
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("%s argv = %v, want %v", tc.provider, got, tc.want)
			}
		})
	}
}

// withoutVersion drops the CLI-version reads, whose occurrence depends on the
// binary resolving on PATH rather than on the host's probe list.
func withoutVersion(lines []string) []string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasSuffix(line, " --version") {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// TestTokenAuthRows proves the credential rows report presence by name only:
// each row's detail is the frozen sentence, and a set secret never reaches it.
//
//nolint:revive // the credential matrix reads as one block
func TestTokenAuthRows(t *testing.T) {
	t.Parallel()
	const secret = "s3cret-token-value"
	cases := []struct {
		name     string
		provider string
		env      map[string]string
		row      string
		severity string
		detail   string
		ok       bool
	}{
		{
			name: "forgejo unset", provider: "forgejo", env: nil, row: "forgejo-auth",
			severity: severityFail,
			detail:   "missing FORGEJO_HOST, FORGEJO_TOKEN — forgejo fetch needs both (an app token with repo read)",
		},
		{
			name: "forgejo set", provider: "forgejo",
			env: map[string]string{"FORGEJO_HOST": "https://forge.example", "FORGEJO_TOKEN": secret},
			row: "forgejo-auth", ok: true, severity: severityFail, detail: "FORGEJO_HOST and FORGEJO_TOKEN set",
		},
		{
			name: "bitbucket missing password", provider: "bitbucket",
			env: map[string]string{"BITBUCKET_USERNAME": "me"},
			row: "bitbucket-auth", severity: severityFail,
			detail: "set BITBUCKET_TOKEN or BITBUCKET_PASSWORD — bitbucket needs one of the two",
		},
		{
			name: "bitbucket token", provider: "bitbucket",
			env: map[string]string{"BITBUCKET_TOKEN": secret},
			row: "bitbucket-auth", ok: true, severity: severityFail, detail: "BITBUCKET_TOKEN set",
		},
		{
			name: "bitbucket password", provider: "bitbucket",
			env: map[string]string{"BITBUCKET_USERNAME": "me", "BITBUCKET_PASSWORD": secret},
			row: "bitbucket-auth", ok: true, severity: severityFail,
			detail: "BITBUCKET_USERNAME and BITBUCKET_PASSWORD set",
		},
		{
			name: "azure no org", provider: "azure", env: nil, row: "azure-auth",
			severity: severityFail,
			detail:   "missing AZURE_DEVOPS_ORG=https://dev.azure.com/{org} — azure fetch reads it first",
		},
		{
			name: "azure login instead of PAT", provider: "azure",
			env: map[string]string{"AZURE_DEVOPS_ORG": "https://dev.azure.com/acme"},
			row: "azure-auth", severity: severityWarn,
			detail: "AZURE_DEVOPS_EXT_PAT unset — azure fetch then needs an az login with token access",
		},
		{
			name: "azure PAT", provider: "azure",
			env: map[string]string{"AZURE_DEVOPS_ORG": "https://dev.azure.com/acme", "AZURE_DEVOPS_EXT_PAT": secret},
			row: "azure-auth", ok: true, severity: severityFail, detail: "AZURE_DEVOPS_ORG and AZURE_DEVOPS_EXT_PAT set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := findCheck(t, runDoctor(t, tc.provider, tc.env, &commander{}), tc.row)
			if got.OK != tc.ok || got.Severity != tc.severity {
				t.Errorf("%s = %v/%v, want ok=%v severity=%q", tc.row, got.OK, got.Severity, tc.ok, tc.severity)
			}
			if got.Detail != tc.detail {
				t.Errorf("%s detail = %q, want %q", tc.row, got.Detail, tc.detail)
			}
			if strings.Contains(got.Detail, secret) {
				t.Errorf("%s detail echoes the credential value: %q", tc.row, got.Detail)
			}
		})
	}
}

// TestUnknownProviderRefuses proves the enum refusal is typed and names the
// accepted hosts, matching fetch --provider. No row runs first: an unknown
// host has no probes to run.
func TestUnknownProviderRefuses(t *testing.T) {
	t.Parallel()
	cmdr := &commander{}
	req := &Request{Provider: "gitea"}
	req.Runtime = &core.Runtime{Commander: cmdr}
	_, ref := Run(context.Background(), req)
	if ref == nil {
		t.Fatal("unknown provider was accepted")
	}
	if ref.Code != "bad-provider" {
		t.Errorf("code = %q, want bad-provider", ref.Code)
	}
	want := "unknown --provider 'gitea' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
	if len(cmdr.argvs) != 0 {
		t.Errorf("unknown provider issued %v", cmdr.argvLines())
	}
}

// TestGitLabRowsFollowTheGLabReads proves a configured glab reports the rows
// its reads decide: auth from `glab auth status` and reachability from
// `glab api user`, with no gh row anywhere in the preflight.
//
//nolint:revive // the row audit reads as one block
func TestGitLabRowsFollowTheGLabReads(t *testing.T) {
	t.Parallel()
	cmdr := &commander{replies: map[string]string{
		"glab --version":   "glab 1.40.0\n",
		"glab auth status": "Logged in to gitlab.com\n",
		"glab api user":    `{"username":"reviewer"}`,
	}}
	checks := runDoctor(t, "gitlab", nil, cmdr)
	auth := findCheck(t, checks, "glab-auth")
	if !auth.OK || auth.Detail != "authenticated" {
		t.Errorf("glab-auth = %+v, want ok authenticated", auth)
	}
	api := findCheck(t, checks, "api-reachable")
	if !api.OK || api.Detail != "GitLab API reachable" {
		t.Errorf("api-reachable = %+v, want the glab read to reach it", api)
	}
	for _, row := range checks {
		if row.Name == "gh-cli" || row.Name == "gh-auth" {
			t.Errorf("gitlab preflight carries the gh row %q", row.Name)
		}
	}
}
