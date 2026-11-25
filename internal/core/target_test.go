package core

import (
	"strings"
	"testing"
)

func TestParseTargetURL(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"GITLAB_HOST":      "https://git.corp.example",
		"FORGEJO_HOST":     "https://forge.corp.example:3000",
		"AZURE_DEVOPS_ORG": "https://dev.azure.com/acme-org",
	}
	for _, tc := range []struct {
		name, raw string
		want      Target
	}{
		{"github", "https://github.com/acme/app/pull/12", Target{"github", "github.com", "acme/app", "12"}},
		{"github files tail", "https://github.com/acme/app/pull/12/files", Target{"github", "github.com", "acme/app", "12"}},
		{"github trailing slash", "https://github.com/acme/app/pull/12/", Target{"github", "github.com", "acme/app", "12"}},
		{"github discussion fragment", "https://github.com/acme/app/pull/12#discussion_r99", Target{"github", "github.com", "acme/app", "12"}},
		{"github query", "https://github.com/acme/app/pull/12/files?diff=split&w=1", Target{"github", "github.com", "acme/app", "12"}},
		{"github host case", "https://GitHub.com/acme/app/pull/12", Target{"github", "github.com", "acme/app", "12"}},
		{"github http", "http://github.com/acme/app/pull/3", Target{"github", "github.com", "acme/app", "3"}},
		{"gitlab", "https://gitlab.com/acme/app/-/merge_requests/7", Target{"gitlab", "gitlab.com", "acme/app", "7"}},
		{"gitlab nested", "https://gitlab.com/acme/sub/deep/app/-/merge_requests/7/diffs", Target{"gitlab", "gitlab.com", "acme/sub/deep/app", "7"}},
		{"gitlab legacy", "https://gitlab.com/acme/app/merge_requests/7", Target{"gitlab", "gitlab.com", "acme/app", "7"}},
		{"gitlab self-hosted", "https://git.corp.example/g/app/-/merge_requests/4", Target{"gitlab", "git.corp.example", "g/app", "4"}},
		{"codeberg", "https://codeberg.org/acme/app/pulls/5/commits", Target{"forgejo", "codeberg.org", "acme/app", "5"}},
		{"forgejo self-hosted port", "https://forge.corp.example:3000/acme/app/pulls/5", Target{"forgejo", "forge.corp.example:3000", "acme/app", "5"}},
		{"bitbucket", "https://bitbucket.org/ws/app/pull-requests/9/diff", Target{"bitbucket", "bitbucket.org", "ws/app", "9"}},
		{"azure", "https://dev.azure.com/acme-org/My%20Proj/_git/app/pullrequest/21", Target{"azure", "dev.azure.com", "My Proj/app", "21"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ref := ParseTargetURL(tc.raw, env)
			if ref != nil || got != tc.want {
				t.Fatalf("ParseTargetURL(%q) = %+v, %v; want %+v", tc.raw, got, ref, tc.want)
			}
		})
	}
}

func TestParseTargetURLRefusals(t *testing.T) {
	t.Parallel()
	env := map[string]string{"GITLAB_HOST": "git.corp.example", "AZURE_DEVOPS_ORG": "https://dev.azure.com/acme-org"}
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"extra segments", "https://github.com/acme/app/pull/12/files/extra/more", "continues past"},
		{"unknown view", "https://github.com/acme/app/pull/12/secrets", "continues past"},
		{"issue route", "https://github.com/acme/app/issues/12", "not a pull request route"},
		{"repo root", "https://github.com/acme/app", "not a pull request route"},
		{"no path", "https://github.com", "names no repository"},
		{"parent segment", "https://github.com/acme/../app/pull/12", "empty, dot-relative"},
		{"dot segment", "https://github.com/./app/pull/12", "empty, dot-relative"},
		{"encoded parent segment", "https://github.com/acme/%2e%2e/pull/12", "dot-relative"},
		{"encoded slash", "https://github.com/acme%2Fapp/x/pull/12", "separator"},
		{"encoded number", "https://github.com/acme/app/pull/%31%32", "percent-encoded"},
		{"empty segment", "https://github.com/acme//pull/12", "empty, dot-relative"},
		{"flag-shaped owner", "https://github.com/-evil/app/pull/12", "starting with '-'"},
		{"non numeric", "https://github.com/acme/app/pull/abc", "not a positive integer"},
		{"zero", "https://github.com/acme/app/pull/0", "not a positive integer"},
		{"leading zero", "https://github.com/acme/app/pull/012", "not a positive integer"},
		{"negative", "https://github.com/acme/app/pull/-3", "starting with '-'"},
		{"huge", "https://github.com/acme/app/pull/12345678901", "not a positive integer"},
		{"credentials", "https://user:tok@github.com/acme/app/pull/12", "credentials"},
		{"ftp scheme", "ftp://github.com/acme/app/pull/12", "http or https"},
		{"ssh scheme", "ssh://git@github.com/acme/app/pull/12", "http or https"},
		{"whitespace", "https://github.com/acme/app/pull/12 ", "whitespace"},
		{"backslash", `https://github.com/acme\app/pull/12`, "backslash"},
		{"control byte", "https://github.com/acme/app/pull/12\x00", "control byte"},
		{"unknown host", "https://ghe.corp.example/acme/app/pull/12", "not a known code host"},
		{"lookalike host", "https://github.com.evil.example/acme/app/pull/12", "not a known code host"},
		{"known host odd port", "https://github.com:8443/acme/app/pull/12", "not a known code host"},
		{"gitcafe", "https://gitcafe.com/acme/app/pulls/1", "gitcafe"},
		{"gitlab one segment", "https://gitlab.com/app/-/merge_requests/7", "<group>/<project>"},
		{"gitlab dash in path", "https://gitlab.com/a/-/b/-/merge_requests/7", "<group>/<project>"},
		{"gitlab github route", "https://gitlab.com/acme/app/pull/7", "not a merge request route"},
		{"github gitlab route", "https://github.com/acme/app/-/merge_requests/7", "not a pull request route"},
		{"azure wrong org", "https://dev.azure.com/other/p/_git/app/pullrequest/1", "AZURE_DEVOPS_ORG"},
		{"azure bad route", "https://dev.azure.com/acme-org/p/_git/app/pullrequests/1", "Azure DevOps"},
		{"not a url", "https://", "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ref := ParseTargetURL(tc.raw, env)
			if ref == nil || ref.Code != CodeBadTargetURL || !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("ParseTargetURL(%q) = %+v; want %s containing %q", tc.raw, ref, CodeBadTargetURL, tc.want)
			}
		})
	}
}

func TestParseTargetURLNeverEchoesCredentials(t *testing.T) {
	t.Parallel()
	_, ref := ParseTargetURL("https://user:hunter2@github.com/acme/app/pull/12", nil)
	if ref == nil || strings.Contains(ref.Message, "hunter2") {
		t.Fatalf("refusal = %+v; want one that omits the credential", ref)
	}
}

func TestParseTargetURLAzureWithoutOrgEnv(t *testing.T) {
	t.Parallel()
	_, ref := ParseTargetURL("https://dev.azure.com/acme-org/p/_git/app/pullrequest/1", nil)
	if ref == nil || !strings.Contains(ref.Message, "AZURE_DEVOPS_ORG") {
		t.Fatalf("refusal = %+v; want the AZURE_DEVOPS_ORG hint", ref)
	}
}

func TestParseTargetURLConflictingHostMapping(t *testing.T) {
	t.Parallel()
	env := map[string]string{"FORGEJO_HOST": "https://gitlab.com"}
	_, ref := ParseTargetURL("https://gitlab.com/acme/app/-/merge_requests/7", env)
	if ref == nil || !strings.Contains(ref.Message, "two providers") {
		t.Fatalf("refusal = %+v; want the two-providers refusal", ref)
	}
}

func TestRemoteTarget(t *testing.T) {
	t.Parallel()
	env := map[string]string{"GITLAB_HOST": "git.corp.example"}
	for _, tc := range []struct {
		raw, provider, repo string
	}{
		{"git@github.com:acme/app.git", "github", "acme/app"},
		{"git@github.com:acme/app", "github", "acme/app"},
		{"https://github.com/acme/app.git", "github", "acme/app"},
		{"https://github.com/acme/app/", "github", "acme/app"},
		{"https://x-access-token:tok@github.com/acme/app.git", "github", "acme/app"},
		{"ssh://git@github.com:22/acme/app.git", "github", "acme/app"},
		{"ssh://git@gitlab.com:2222/acme/sub/app.git", "gitlab", "acme/sub/app"},
		{"git@git.corp.example:g/sub/app.git", "gitlab", "g/sub/app"},
		{"https://codeberg.org/acme/app.git", "forgejo", "acme/app"},
		{"git@bitbucket.org:ws/app.git", "bitbucket", "ws/app"},
		{"https://acme-org@dev.azure.com/acme-org/proj/_git/app", "azure", "proj/app"},
		{"git@ssh.dev.azure.com:v3/acme-org/proj/app", "azure", "proj/app"},
	} {
		provider, repo, ref := RemoteTarget(tc.raw, env)
		if ref != nil || provider != tc.provider || repo != tc.repo {
			t.Errorf("RemoteTarget(%q) = %q, %q, %v; want %q, %q", tc.raw, provider, repo, ref, tc.provider, tc.repo)
		}
	}
}

func TestRemoteTargetRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, code string }{
		{"https://ghe.corp.example/acme/app.git", "provider-unknown"},
		{"git@github.com:acme.git", CodeBadTargetURL},
		{"git@github.com:acme/app/extra.git", CodeBadTargetURL},
		{"https://github.com/acme/%2e%2e", CodeBadTargetURL},
		{"https://github.com/-acme/app", CodeBadTargetURL},
		{"git@gitlab.com:solo.git", CodeBadTargetURL},
		{"file:///srv/git/app.git", "provider-unknown"},
		{"", "provider-unknown"},
	} {
		_, _, ref := RemoteTarget(tc.raw, nil)
		if ref == nil || ref.Code != tc.code {
			t.Errorf("RemoteTarget(%q) = %+v; want %s", tc.raw, ref, tc.code)
		}
		if ref != nil && tc.raw != "" && strings.Contains(ref.Message, tc.raw) {
			t.Errorf("RemoteTarget(%q) message echoes the remote: %q", tc.raw, ref.Message)
		}
	}
}
