package target

import (
	"encoding/json"
	"slices"
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
		{
			name: "ssh gitlab origin, GITLAB_HOST on the same host", want: "git.corp.example",
			g: fakeGit{origin: "git@git.corp.example:g/app.git"},
			q: Request{Verb: "post", Repo: "4", Env: map[string]string{"GITLAB_HOST": "https://git.corp.example"}},
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

// TestResolveRefusesAHostNoCallCanBePinnedTo pins the targets verdict cannot
// make unambiguous: a Forgejo target on another host than FORGEJO_HOST (port
// included), a GitLab host with a port whose environment does not make glab's
// default host exactly that host, and an ssh GitLab origin whose host
// GITLAB_HOST serves on a port. Each refuses typed, naming both hosts.
//
// The ssh origin stays refused even though GITLAB_HOST names a port, the one
// thing that lets a ported target through: an ssh remote names no web port
// (its port is the ssh daemon's), so the target's authority is the portless
// `git.corp.example`, a different host than `git.corp.example:8443`. Running
// glab unpinned would reach GITLAB_HOST's port, not provably the origin's
// host, so only a target that itself names the port may take that path.
func TestResolveRefusesAHostNoCallCanBePinnedTo(t *testing.T) {
	for _, tc := range []hostRefusalCase{
		{name: "codeberg URL, FORGEJO_HOST elsewhere", hosts: []string{"codeberg.org", "forge.corp.example"}, q: Request{
			Verb: "fetch", Repo: "https://codeberg.org/acme/app/pulls/5", Env: map[string]string{"FORGEJO_HOST": "https://forge.corp.example"},
		}},
		{
			name: "forgejo origin on another port", hosts: []string{"forge.corp.example:3000", "names forge.corp.example "},
			g: fakeGit{origin: forgeOrigin}, q: Request{Verb: "post", Repo: "5", Env: map[string]string{"FORGEJO_HOST": "forge.corp.example"}},
		},
		{
			name: "gitlab URL with a port, GITLAB_API_HOST elsewhere", hosts: []string{"the target is on git.corp.example:8443", "GITLAB_API_HOST names git.corp.example"},
			q: Request{
				Verb: "fetch", Repo: "https://git.corp.example:8443/g/app/-/merge_requests/4",
				Env: map[string]string{"GITLAB_HOST": "https://git.corp.example:8443", "GITLAB_API_HOST": "git.corp.example"},
			},
		},
		{
			name: "gitlab URL with a port, GITLAB_REPO set", hosts: []string{"the target is on git.corp.example:8443", "GITLAB_REPO"},
			q: Request{
				Verb: "fetch", Repo: "https://git.corp.example:8443/g/app/-/merge_requests/4",
				Env: map[string]string{"GITLAB_HOST": "https://git.corp.example:8443", "GITLAB_REPO": "https://other.example/g/app"},
			},
		},
		{
			name: "gitlab URL with a port, CI auto-login server elsewhere", hosts: []string{"git.corp.example:8443", "CI_SERVER_FQDN"},
			q: Request{
				Verb: "fetch", Repo: "https://git.corp.example:8443/g/app/-/merge_requests/4",
				Env: map[string]string{
					"GITLAB_HOST": "https://git.corp.example:8443", "GLAB_ENABLE_CI_AUTOLOGIN": "true", "GITLAB_CI": "true",
					"CI_SERVER_FQDN": "git.corp.example",
				},
			},
		},
		{
			name:  "ssh gitlab origin, GITLAB_HOST on another port",
			hosts: []string{"the target is on git.corp.example ", "GITLAB_HOST names git.corp.example:8443"},
			g:     fakeGit{origin: "git@git.corp.example:g/app.git"},
			q:     Request{Verb: "post", Repo: "4", Env: map[string]string{"GITLAB_HOST": "https://git.corp.example:8443"}},
		},
		{
			name: "gitlab URL, GITLAB_API_HOST elsewhere", hosts: []string{"gitlab.com", "GITLAB_API_HOST names git.corp.example"},
			q: Request{
				Verb: "fetch", Repo: "https://gitlab.com/g/app/-/merge_requests/4",
				Env: map[string]string{"GITLAB_API_HOST": "git.corp.example"},
			},
		},
		{
			name: "github origin with a port", hosts: []string{"the target is on github.com:8443", "gh's --hostname takes no port"},
			g: fakeGit{origin: "https://github.com:8443/acme/app.git"}, q: Request{Verb: "post", Repo: "12"},
		},
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

// TestGitLabHostWithAPortRunsUnpinnedOnlyAsGlabsDefault pins the allowed half
// of a ported GitLab target: a URL, or an https origin, whose authority
// GITLAB_HOST names exactly resolves with its host kept (the runner then drops
// the pin and glab's default host, which is that host, answers).
func TestGitLabHostWithAPortRunsUnpinnedOnlyAsGlabsDefault(t *testing.T) {
	env := map[string]string{"GITLAB_HOST": "https://git.corp.example:8443"}
	for _, tc := range []struct {
		name string
		q    Request
		g    fakeGit
	}{
		{name: "URL", q: Request{
			Verb: "fetch", Repo: "https://git.corp.example:8443/g/app/-/merge_requests/4", Env: env,
		}},
		{name: "https origin", g: fakeGit{origin: "https://git.corp.example:8443/g/app.git"}, q: Request{
			Verb: "post", Repo: "4", Env: env,
		}},
	} {
		got := resolve(t, &tc.g, checkout(t, nil), &tc.q)
		if got.Refusal != nil || got.Host != "git.corp.example:8443" {
			t.Errorf("%s: Host = %q (refusal %v), want git.corp.example:8443 and no refusal", tc.name, got.Host, got.Refusal)
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
// pull request on the origin's hostname but another port is not trusted. A
// ported origin already refuses before the lookup (hostConflict), so the
// reply check is defense in depth and is exercised directly.
func TestHostLookupRejectsAnAnswerFromAnotherPort(t *testing.T) {
	env := map[string]string{"GITLAB_HOST": "git.corp.example"}
	f := &found{repo: "acme/sub/app", provider: providerGitLab, host: "git.corp.example:8443"}
	e := map[string]any{
		"iid": json.Number("7"), "source_branch": "fix-x",
		"web_url": "https://git.corp.example/acme/sub/app/-/merge_requests/7",
	}
	if _, ok := validEntry(e, &gitlabFields, f, "fix-x", env); ok {
		t.Fatal("an answer from another port is not the origin's PR")
	}
	f.host = "git.corp.example"
	if _, ok := validEntry(e, &gitlabFields, f, "fix-x", env); !ok {
		t.Fatal("the same entry on the origin's own host must validate")
	}
}

// TestHostLookupRunsPinnedToTheOriginsHost pins that the resolver's own host
// lookup runs with the origin's host on its context, so the production runner
// verifies the lookup argv like every other host call.
func TestHostLookupRunsPinnedToTheOriginsHost(t *testing.T) {
	g := &fakeGit{origin: ghOrigin, branch: "fix-x", hostOut: oneOpenPR}
	if got := lookup(t, g, &Request{Verb: "fetch"}); got.Refusal != nil || got.Pr != "12" {
		t.Fatalf("lookup: %+v", got)
	}
	if len(g.pinned) != 1 || g.pinned[0] != "github.com" {
		t.Fatalf("lookup ran pinned to %q, want one call pinned to github.com", g.pinned)
	}
	argv, ref := core.PinHostArgv(g.calls[len(g.calls)-1], "github.com")
	if ref != nil || !slices.Equal(argv, g.calls[len(g.calls)-1]) {
		t.Fatalf("the runner would not pass the lookup argv unchanged: %q, %v", argv, ref)
	}
}

// TestHostConflictRefusesBeforeTheLookup pins the order: a host no call can be
// pinned to refuses before the branch lookup asks it anything.
func TestHostConflictRefusesBeforeTheLookup(t *testing.T) {
	g := &fakeGit{origin: "https://github.com:8443/acme/app.git", branch: "fix-x", hostOut: oneOpenPR}
	got := lookup(t, g, &Request{Verb: "fetch"})
	if got.Refusal == nil || got.Refusal.Code != core.CodeHostMismatch {
		t.Fatalf("want a host-mismatch refusal, got %+v", got)
	}
	if n := hostCalls(g); n != 0 {
		t.Fatalf("the lookup made %d host call(s) before refusing", n)
	}
}

// TestGitLabAPIHostOnTheTargetResolves keeps the legitimate setup working:
// GITLAB_API_HOST naming the target's own host does not refuse.
func TestGitLabAPIHostOnTheTargetResolves(t *testing.T) {
	got := resolve(t, &fakeGit{}, checkout(t, nil), &Request{
		Verb: "fetch", Repo: "https://gitlab.com/g/app/-/merge_requests/4",
		Env: map[string]string{"GITLAB_API_HOST": "https://gitlab.com"},
	})
	if got.Refusal != nil || got.Host != "gitlab.com" {
		t.Fatalf("GITLAB_API_HOST on the target's host must resolve: %+v", got)
	}
}
