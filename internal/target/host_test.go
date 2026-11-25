package target

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const forgeOrigin = "https://forge.corp.example:3000/acme/app.git"

// TestResolvedCarriesTheTargetsHost pins the host each target source hands
// the front door to pin every host call to: the URL's or the origin's
// authority, port kept, and none for a typed repository.
func TestResolvedCarriesTheTargetsHost(t *testing.T) {
	forgeEnv := map[string]string{"FORGEJO_HOST": "https://forge.corp.example:3000"}
	for _, tc := range []struct {
		bundles    map[string]string
		name, want string
		q          Request
		g          fakeGit
	}{
		{name: "github URL", want: "github.com", q: Request{Verb: "fetch", Repo: "https://github.com/acme/app/pull/12"}},
		{name: "gitlab self-hosted URL", want: "git.corp.example", q: Request{
			Verb: "fetch", Repo: "https://git.corp.example/g/app/-/merge_requests/4", Env: map[string]string{"GITLAB_HOST": "git.corp.example"},
		}},
		{name: "number from an ssh origin", want: "github.com", g: fakeGit{origin: ghOrigin}, q: Request{Verb: "post", Repo: "12"}},
		{
			name: "number from an https origin with a port", want: "forge.corp.example:3000", g: fakeGit{origin: forgeOrigin},
			q: Request{Verb: "post", Repo: "5", Env: forgeEnv},
		},
		{
			name: "inferred from a recorded bundle", want: "github.com", g: fakeGit{origin: ghOrigin, branch: "fix-x"},
			bundles: map[string]string{"12": ghBundle12}, q: Request{Verb: "post"},
		},
		{name: "typed pair", want: "", q: Request{Verb: "post", Repo: "acme/app", Pr: "12"}},
		{
			name: "typed repo, pr from a bundle", want: "", g: fakeGit{branch: "fix-x"},
			bundles: map[string]string{"12": ghBundle12}, q: Request{Verb: "post", Repo: "acme/app"},
		},
	} {
		got := resolve(t, &tc.g, checkout(t, tc.bundles), &tc.q)
		if got.Refusal != nil || got.Host != tc.want {
			t.Errorf("%s: Host = %q (refusal %v), want %q", tc.name, got.Host, got.Refusal, tc.want)
		}
	}
}

// TestResolveRefusesAHostNoCallCanBePinnedTo pins the two targets verdict
// cannot make unambiguous: a Forgejo target on another host than FORGEJO_HOST
// (port included), and a GitLab host with a port. Each refuses typed, naming
// both hosts.
func TestResolveRefusesAHostNoCallCanBePinnedTo(t *testing.T) {
	for _, tc := range []hostRefusalCase{
		{name: "codeberg URL, FORGEJO_HOST elsewhere", hosts: []string{"codeberg.org", "forge.corp.example"}, q: Request{
			Verb: "fetch", Repo: "https://codeberg.org/acme/app/pulls/5", Env: map[string]string{"FORGEJO_HOST": "https://forge.corp.example"},
		}},
		{
			name: "forgejo origin on another port", hosts: []string{"forge.corp.example:3000", "names forge.corp.example "},
			g: fakeGit{origin: forgeOrigin}, q: Request{Verb: "post", Repo: "5", Env: map[string]string{"FORGEJO_HOST": "forge.corp.example"}},
		},
		{name: "gitlab URL with a port", hosts: []string{"the target is on git.corp.example:8443", "GITLAB_HOST: git.corp.example:8443"}, q: Request{
			Verb: "fetch", Repo: "https://git.corp.example:8443/g/app/-/merge_requests/4",
			Env: map[string]string{"GITLAB_HOST": "https://git.corp.example:8443"},
		}},
	} {
		checkHostRefusal(t, &tc)
	}
}

type hostRefusalCase struct {
	hosts []string
	name  string
	q     Request
	g     fakeGit
}

func checkHostRefusal(t *testing.T, tc *hostRefusalCase) {
	t.Helper()
	got := resolve(t, &tc.g, checkout(t, nil), &tc.q)
	if got.Refusal == nil || got.Refusal.Code != core.CodeHostMismatch {
		t.Errorf("%s: want a host-mismatch refusal, got %+v", tc.name, got)
		return
	}
	for _, h := range tc.hosts {
		if !strings.Contains(got.Refusal.Message, h) {
			t.Errorf("%s: refusal does not name %q: %s", tc.name, h, got.Refusal.Message)
		}
	}
}

// TestGHHostNeverRefuses pins that gh's own default host does not refuse:
// every gh call names the target's host, which wins over GH_HOST.
func TestGHHostNeverRefuses(t *testing.T) {
	got := resolve(t, &fakeGit{}, checkout(t, nil), &Request{
		Verb: "fetch", Repo: "https://github.com/acme/app/pull/12", Env: map[string]string{"GH_HOST": "ghe.corp.example"},
	})
	if got.Refusal != nil || got.Host != "github.com" {
		t.Fatalf("GH_HOST cannot redirect a pinned gh call, so it must not refuse: %+v", got)
	}
}

// TestBundleFromAnotherPortIsAnotherHost pins host equality by authority: a
// bundle recorded on the origin's hostname but another port is not the
// origin's pull request.
func TestBundleFromAnotherPortIsAnotherHost(t *testing.T) {
	g := &fakeGit{origin: forgeOrigin, branch: "fix-x"}
	d := checkout(t, map[string]string{
		"5": `{"provider":"forgejo","head_ref":"fix-x","url":"https://forge.corp.example/acme/app/pulls/5"}`,
	})
	got := resolve(t, g, d, &Request{Verb: "report", Env: map[string]string{"FORGEJO_HOST": "https://forge.corp.example:3000"}})
	if got.Refusal == nil || got.Refusal.Code != core.CodeTargetUnresolved || got.Pr != "" {
		t.Fatalf("a bundle on another port must not answer for the origin: %+v", got)
	}
}

// TestHostLookupRejectsAnAnswerFromAnotherPort is the lookup's half: a listed
// pull request on the origin's hostname but another port is not trusted.
func TestHostLookupRejectsAnAnswerFromAnotherPort(t *testing.T) {
	env := map[string]string{"GITLAB_HOST": "git.corp.example"}
	out := "[" + glEntry("https://git.corp.example/acme/sub/app/-/merge_requests/7") + "]"
	g := &fakeGit{origin: "https://git.corp.example:8443/acme/sub/app.git", branch: "fix-x", hostOut: out}
	got := lookup(t, g, &Request{Verb: "fetch", Env: env})
	if got.Refusal == nil || !strings.Contains(got.Refusal.Message, "does not trust") {
		t.Fatalf("an answer from another port is not the origin's PR: %+v", got)
	}
}
