package core

import (
	"context"
	"strings"
	"testing"
)

type originCommander struct {
	remote string
	calls  int
	exit   int
}

func (c *originCommander) Run(_ context.Context, cmd *Command) (Result, error) {
	c.calls++
	if strings.Join(cmd.Argv, " ") != "git remote get-url origin" {
		panic("wrong origin command")
	}
	return Result{Stdout: c.remote, ExitCode: c.exit}, nil
}

func TestProviderRemoteForms(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"https://github.com/a/b.git", "github"},
		{"git@gitlab.com:a/b", "gitlab"},
		{"ssh://git@codeberg.org:2222/a/b.git", "forgejo"},
		{"https://user:secret@bitbucket.org:443/a/b", "bitbucket"},
		{"https://dev.azure.com/org/project/_git/repo", "azure"},
		{"git@ssh.dev.azure.com:v3/org/project/repo", "azure"},
		{"https://gitcafe.com/a/b", "gitcafe"},
		{"https://unknown.invalid/a/b", ""},
		{"https://user:secret@unknown.invalid/a/b", ""},
		{"https:/user:secret@github.com/a/b", ""},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			cmdr := &originCommander{remote: tc.remote}
			got, source, ref := ResolveProvider(t.Context(), cmdr, "", "", nil)
			if got != tc.want {
				t.Fatalf("provider = %q, want %q", got, tc.want)
			}
			assertProviderSelection(t, tc.remote, tc.want, source, ref)
		})
	}
}

func TestProviderSelection(t *testing.T) {
	cmdr := &originCommander{remote: "https://unknown.invalid/a/b"}
	got, source, ref := ResolveProvider(t.Context(), cmdr, "", "gitlab", nil)
	if got != "gitlab" || source != "flag" || ref != nil || cmdr.calls != 0 {
		t.Fatalf("explicit override: %q %q %+v calls=%d", got, source, ref, cmdr.calls)
	}
	cmdr.exit = 2
	got, source, ref = ResolveProvider(t.Context(), cmdr, "", "", nil)
	if got != "github" || source != "default" || ref != nil {
		t.Fatalf("no origin: %q %q %+v", got, source, ref)
	}
}

func TestProviderConfiguredHosts(t *testing.T) {
	env := map[string]string{"GITLAB_HOST": "git.internal", "FORGEJO_HOST": "https://forge.internal"}
	if got := providerFromRemote("git@git.internal:a/b", env); got != "gitlab" {
		t.Fatalf("GitLab = %q", got)
	}
	if got := providerFromRemote("https://forge.internal/a/b", env); got != "forgejo" {
		t.Fatalf("Forgejo = %q", got)
	}
	env["FORGEJO_HOST"] = "https://git.internal"
	if got := providerFromRemote("https://user:secret@git.internal/a/b", env); got != "" {
		t.Fatalf("ambiguous = %q", got)
	}
}

func assertProviderSelection(t *testing.T, remote, want, source string, ref *RefusalError) {
	t.Helper()
	if want != "" {
		assertRemoteSource(t, source, ref)
		return
	}
	assertUnknownSource(t, remote, ref)
}

func assertRemoteSource(t *testing.T, source string, ref *RefusalError) {
	t.Helper()
	if ref != nil || source != "remote" {
		t.Fatalf("source = %q, refusal = %+v", source, ref)
	}
}

func assertUnknownSource(t *testing.T, remote string, ref *RefusalError) {
	t.Helper()
	if ref == nil || ref.Code != "provider-unknown" {
		t.Fatalf("refusal = %+v", ref)
	}
	if strings.Contains(ref.Message, remote) || strings.Contains(ref.Message, "secret") {
		t.Fatal("remote leaked")
	}
}
