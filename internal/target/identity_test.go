package target

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type identityCase struct {
	name     string
	remote   string
	url      string
	provider string
	repo     string
}

// TestRemoteAndBundleAgreeOnIdentity pins that, for every host, the repository
// the origin remote names is the one the same host's bundle URL names, so
// inference compares like with like.
func TestRemoteAndBundleAgreeOnIdentity(t *testing.T) {
	for _, tc := range []identityCase{
		{"github", "git@github.com:acme/app.git", "https://github.com/acme/app/pull/12", "github", "acme/app"},
		{"gitlab nested", "https://gitlab.com/acme/sub/app.git", "https://gitlab.com/acme/sub/app/-/merge_requests/7", "gitlab", "acme/sub/app"},
		{"forgejo", "https://codeberg.org/acme/app.git", "https://codeberg.org/acme/app/pulls/5", "forgejo", "acme/app"},
		{"bitbucket", "git@bitbucket.org:ws/app.git", "https://bitbucket.org/ws/app/pull-requests/9", "bitbucket", "ws/app"},
		{"azure https", "https://acme-org@dev.azure.com/acme-org/proj/_git/app", "https://dev.azure.com/acme-org/proj/_git/app/pullrequest/21", "azure", "proj/app"},
		{"azure ssh", "git@ssh.dev.azure.com:v3/acme-org/proj/app", "https://dev.azure.com/acme-org/proj/_git/app/pullrequest/21", "azure", "proj/app"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkIdentity(t, &tc) })
	}
}

func checkIdentity(t *testing.T, tc *identityCase) {
	t.Helper()
	provider, repo, ref := core.RemoteTarget(tc.remote, nil)
	if ref != nil || provider != tc.provider || repo != tc.repo {
		t.Fatalf("RemoteTarget(%q) = %q %q %v; want %q %q", tc.remote, provider, repo, ref, tc.provider, tc.repo)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"url":"`+tc.url+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readcommon.BundleProject(dir); got != tc.repo {
		t.Errorf("BundleProject(%s) = %q, want %q (the origin's identity)", tc.url, got, tc.repo)
	}
	if got, want := readcommon.BundleHost(dir), core.RemoteHost(tc.remote); got == "" || got != want {
		t.Errorf("BundleHost(%s) = %q, RemoteHost(%q) = %q; want the same web host", tc.url, got, tc.remote, want)
	}
}

func TestResolveInfersAzureFromRecordedBundle(t *testing.T) {
	g := &fakeGit{origin: "https://acme-org@dev.azure.com/acme-org/proj/_git/app", branch: "fix-x"}
	d := checkout(t, map[string]string{
		"21": `{"provider":"azure","head_ref":"fix-x","state":"active","url":"https://dev.azure.com/acme-org/proj/_git/app/pullrequest/21"}`,
	})
	got := resolve(t, g, d, &Request{Verb: "report"})
	if got.Refusal != nil || got.Repo != "proj/app" || got.Pr != "21" {
		t.Fatalf("azure inference: %+v", got)
	}
}

func TestResolveIgnoresBundleFromAnotherHost(t *testing.T) {
	g := &fakeGit{origin: "git@github.com:acme/app.git", branch: "fix-x"}
	d := checkout(t, map[string]string{
		"7": `{"provider":"gitlab","head_ref":"fix-x","url":"https://gitlab.com/acme/app/-/merge_requests/7"}`,
	})
	got := resolve(t, g, d, &Request{Verb: "report"})
	if got.Refusal == nil || got.Refusal.Code != core.CodeTargetUnresolved || got.Pr != "" {
		t.Fatalf("a gitlab.com bundle must not answer for a github.com origin: %+v", got)
	}
}

type providerCase struct {
	name string
	err  string
	rest []string
	want []string
}

func TestWithProvider(t *testing.T) {
	for _, tc := range []providerCase{
		{"absent", "", []string{"--json"}, []string{"--provider", "gitlab", "--json"}},
		{"spaced match", "", []string{"--provider", "gitlab", "--json"}, []string{"--provider", "gitlab", "--json"}},
		{"equals match becomes spaced", "", []string{"--provider=gitlab", "--json"}, []string{"--provider", "gitlab", "--json"}},
		{"second occurrence conflicts", "--provider says github", []string{"--provider", "gitlab", "--provider", "github"}, nil},
		{"equals after spaced conflicts", "--provider says forgejo", []string{"--provider", "gitlab", "--provider=forgejo"}, nil},
		{"after sentinel untouched", "", []string{"--", "--provider=github"}, []string{"--provider", "gitlab", "--", "--provider=github"}},
		{"dangling flag stays for the verb to refuse", "", []string{"--provider"}, []string{"--provider", "gitlab", "--provider"}},
	} {
		t.Run(tc.name, func(t *testing.T) { checkProviderCase(t, &tc) })
	}
}

func checkProviderCase(t *testing.T, tc *providerCase) {
	t.Helper()
	got, ref := withProvider(tc.rest, "gitlab")
	if tc.err != "" {
		if ref == nil || !strings.Contains(ref.Message, tc.err) {
			t.Fatalf("refusal = %+v, want %q", ref, tc.err)
		}
		return
	}
	if ref != nil || !slices.Equal(got, tc.want) {
		t.Fatalf("withProvider(%v) = %v, %v; want %v", tc.rest, got, ref, tc.want)
	}
}
