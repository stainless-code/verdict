package core

import (
	"context"
	"maps"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// TestPinHostArgvQualifiesEveryShippedShape pins the host-qualified argv for
// every gh and glab call shape a host verb spawns. Each input is the literal
// argv its call site builds (named beside it); the want is the literal argv
// the production runner sends once the target's host rides on the context.
func TestPinHostArgvQualifiesEveryShippedShape(t *testing.T) {
	t.Parallel()
	const gh, gl = "github.com", "gitlab.corp.example"
	for _, tc := range []struct {
		name, host string
		in, want   []string
	}{
		// fetch: internal/core/fetch/github.go pr view, pr diff.
		{
			"fetch pr view", gh,
			[]string{"gh", "pr", "view", "5", "--repo", "acme/app", "--json", "url"},
			[]string{"gh", "pr", "view", "5", "--repo", "github.com/acme/app", "--json", "url"},
		},
		{
			"fetch pr diff", gh,
			[]string{"gh", "pr", "diff", "5", "--repo", "acme/app"},
			[]string{"gh", "pr", "diff", "5", "--repo", "github.com/acme/app"},
		},
		// ready: internal/core/ready/service.go pr checks.
		{
			"ready pr checks", gh,
			[]string{"gh", "pr", "checks", "5", "--repo", "acme/app", "--json", "name"},
			[]string{"gh", "pr", "checks", "5", "--repo", "github.com/acme/app", "--json", "name"},
		},
		// post: internal/core/post/submit.go review submit and head read.
		{
			"post review submit", gh,
			[]string{"gh", "api", "repos/acme/app/pulls/5/reviews", "--method", "POST", "--input", "-"},
			[]string{"gh", "api", "--hostname", "github.com", "repos/acme/app/pulls/5/reviews", "--method", "POST", "--input", "-"},
		},
		{
			"post head read", gh,
			[]string{"gh", "api", "repos/acme/app/pulls/5", "--jq", ".head.sha"},
			[]string{"gh", "api", "--hostname", "github.com", "repos/acme/app/pulls/5", "--jq", ".head.sha"},
		},
		{
			"post self login", gh,
			[]string{"gh", "api", "user", "--jq", ".login"},
			[]string{"gh", "api", "--hostname", "github.com", "user", "--jq", ".login"},
		},
		// threads, ready, calibrate, pr-wait: readcommon.ThreadsArgv and the
		// resolve mutation (internal/core/post/host_github.go).
		{
			"threads graphql", gh,
			[]string{"gh", "api", "graphql", "-F", "owner=acme"},
			[]string{"gh", "api", "--hostname", "github.com", "graphql", "-F", "owner=acme"},
		},
		// doctor: auth status.
		{
			"gh auth status", gh,
			[]string{"gh", "auth", "status"},
			[]string{"gh", "auth", "status", "--hostname", "github.com"},
		},
		{"gh version", gh, []string{"gh", "--version"}, []string{"gh", "--version"}},
		{
			"gh repo already on the host", "ghe.corp.example",
			[]string{"gh", "pr", "list", "--repo", "ghe.corp.example/acme/app"},
			[]string{"gh", "pr", "list", "--repo", "ghe.corp.example/acme/app"},
		},
		{
			"demo's login ladder already on the host", "github.com",
			[]string{"gh", "auth", "status", "--hostname", "github.com", "--active", "--no-refresh"},
			[]string{"gh", "auth", "status", "--hostname", "github.com", "--active", "--no-refresh"},
		},
		// fetch: internal/core/fetch/providers.go mr view, mr diff, api reads;
		// post: internal/core/post/submit_gitlab.go writes.
		{
			"fetch mr view", gl,
			[]string{"glab", "mr", "view", "7", "-R", "g/sub/app", "-F", "json"},
			[]string{"glab", "mr", "view", "7", "-R", "https://gitlab.corp.example/g/sub/app", "-F", "json"},
		},
		{
			"fetch mr diff", gl,
			[]string{"glab", "mr", "diff", "7", "-R", "g/app", "--raw"},
			[]string{"glab", "mr", "diff", "7", "-R", "https://gitlab.corp.example/g/app", "--raw"},
		},
		{
			"glab api read", gl,
			[]string{"glab", "api", "projects/g%2Fapp/merge_requests/7"},
			[]string{"glab", "api", "--hostname", "gitlab.corp.example", "projects/g%2Fapp/merge_requests/7"},
		},
		{
			"post glab write", gl,
			[]string{"glab", "api", "projects/g%2Fapp/merge_requests/7/notes", "--method", "POST", "--input", "-"},
			[]string{
				"glab", "api", "--hostname", "gitlab.corp.example", "projects/g%2Fapp/merge_requests/7/notes",
				"--method", "POST", "--input", "-",
			},
		},
		{
			"glab auth status", gl,
			[]string{"glab", "auth", "status"},
			[]string{"glab", "auth", "status", "--hostname", "gitlab.corp.example"},
		},
		{
			"glab repo URL already on the host", gl,
			[]string{"glab", "mr", "list", "-R", "https://gitlab.corp.example/g/app"},
			[]string{"glab", "mr", "list", "-R", "https://gitlab.corp.example/g/app"},
		},
		// Tools that carry their host in a URL, and git, pass untouched.
		{"curl", gl, []string{"curl", "https://forge.example/api/v1/x"}, []string{"curl", "https://forge.example/api/v1/x"}},
		{"git", gh, []string{"git", "rev-parse", "HEAD"}, []string{"git", "rev-parse", "HEAD"}},
		{"no host", "", []string{"gh", "repo", "view"}, []string{"gh", "repo", "view"}},
	} {
		got, ref := PinHostArgv(tc.in, tc.host)
		if ref != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: PinHostArgv(%q, %q) = %q, %v; want %q", tc.name, tc.in, tc.host, got, ref, tc.want)
		}
	}
}

// TestPinHostArgvRefusesWhatItCannotPin pins the fail-closed half: a shape with
// no host-qualified form, a repository already on another host, a host with a
// port, a host or repository flag in another spelling or twice, and an
// absolute-URL endpoint refuse typed, naming the target host, instead of
// running against a host the pin does not control.
func TestPinHostArgvRefusesWhatItCannotPin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, host string
		argv       []string
	}{
		{"gh unknown subcommand", "github.com", []string{"gh", "repo", "view", "acme/app"}},
		{"gh pr without --repo", "github.com", []string{"gh", "pr", "view", "5"}},
		{"gh repo on another host", "github.com", []string{"gh", "pr", "view", "5", "--repo", "ghe.corp.example/acme/app"}},
		{"gh api already naming a host", "github.com", []string{"gh", "api", "--hostname", "ghe.corp.example", "user"}},
		{"glab host with a port", "gitlab.corp.example:8443", []string{"glab", "api", "user"}},
		{"glab repo URL on another host", "gitlab.com", []string{"glab", "mr", "view", "7", "-R", "https://gitlab.corp.example/g/app"}},
		{"glab unknown subcommand", "gitlab.com", []string{"glab", "issue", "list"}},
		{"gh host with a port, pr", "ghe.corp.example:8443", []string{"gh", "pr", "view", "5", "--repo", "acme/app"}},
		{"gh host with a port, api", "github.com:8443", []string{"gh", "api", "user"}},
		{"gh host with a port, auth", "github.com:8443", []string{"gh", "auth", "status"}},
		{"glab host with a port, mr", "gitlab.corp.example:8443", []string{"glab", "mr", "view", "7", "-R", "g/app"}},
		{"gh api --hostname=", "github.com", []string{"gh", "api", "--hostname=ghe.corp.example", "user"}},
		{"glab api --hostname=", "gitlab.com", []string{"glab", "api", "user", "--hostname=gitlab.corp.example"}},
		{"gh pr --hostname=", "github.com", []string{"gh", "pr", "view", "5", "--repo", "acme/app", "--hostname=x.example"}},
		{"gh pr --repo=", "github.com", []string{"gh", "pr", "view", "5", "--repo=acme/app"}},
		{"gh pr two --repo", "github.com", []string{"gh", "pr", "view", "5", "--repo", "acme/app", "--repo", "ghe.corp.example/acme/app"}},
		{"gh pr --repo then -R", "github.com", []string{"gh", "pr", "view", "5", "--repo", "acme/app", "-R", "evil/app"}},
		{"glab mr -R=", "gitlab.com", []string{"glab", "mr", "view", "7", "-R=g/app"}},
		{"glab mr attached -R", "gitlab.com", []string{"glab", "mr", "view", "7", "-Rg/app"}},
		{"gh api with a repo flag", "github.com", []string{"gh", "api", "user", "--repo", "acme/app"}},
		{"gh api absolute URL", "github.com", []string{"gh", "api", "https://ghe.corp.example/api/v3/user"}},
		{"glab api absolute URL", "gitlab.com", []string{"glab", "api", "--method", "GET", "HTTP://gitlab.corp.example/api/v4/user"}},
		{"auth status on another host", "github.com", []string{"gh", "auth", "status", "--hostname", "ghe.corp.example"}},
		{"auth status short -h", "github.com", []string{"gh", "auth", "status", "-h", "ghe.corp.example"}},
		{"auth status twice", "github.com", []string{"gh", "auth", "status", "--hostname", "github.com", "--hostname=ghe.corp.example"}},
	} {
		got, ref := PinHostArgv(tc.argv, tc.host)
		if ref == nil || ref.Code != CodeHostMismatch || !strings.Contains(ref.Message, tc.host) {
			t.Errorf("%s: PinHostArgv = %q, %v; want a host-mismatch refusal naming %s", tc.name, got, ref, tc.host)
		}
	}
}

func TestWithHostRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if HostOf(ctx) != "" || WithHost(ctx, "") != ctx {
		t.Fatal("an unpinned context reports a host, or pinning nothing changed it")
	}
	if got := HostOf(WithHost(context.WithoutCancel(ctx), "github.com")); got != "github.com" {
		t.Fatalf("HostOf = %q, want github.com", got)
	}
}

// TestHostsCompareByAuthority pins that a non-default port is part of the
// host every equality check compares, the default port is not, and an ssh
// remote's port (the ssh daemon's) never is.
func TestHostsCompareByAuthority(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"https://GHE.corp.example:8443/acme/app.git": "ghe.corp.example:8443",
		"https://github.com:443/acme/app.git":        "github.com",
		"http://gitlab.corp.example:80/g/app.git":    "gitlab.corp.example",
		"http://gitlab.corp.example:8080/g/app.git":  "gitlab.corp.example:8080",
		"ssh://git@gitlab.corp.example:2222/g/app":   "gitlab.corp.example",
		"git@github.com:acme/app.git":                "github.com",
		"git@ssh.dev.azure.com:v3/o/p/r":             "dev.azure.com",
	} {
		if got := RemoteHost(raw); got != want {
			t.Errorf("RemoteHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestURLAndEnvAuthority pins the same authority form for a URL and for a host
// variable spelled bare or as a URL.
func TestURLAndEnvAuthority(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("https://Forge.example:3000/acme/app/pulls/5")
	if err != nil {
		t.Fatal(err)
	}
	if got := URLAuthority(u); got != "forge.example:3000" {
		t.Errorf("URLAuthority = %q, want forge.example:3000", got)
	}
	for raw, want := range map[string]string{
		"https://forge.example:3000": "forge.example:3000", "forge.example": "forge.example",
		"https://forge.example:443/": "forge.example", "": "",
	} {
		if got := EnvHostAuthority(raw); got != want {
			t.Errorf("EnvHostAuthority(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestGlabEndpointConflict pins which environment redirects a pinned glab
// call: GITLAB_API_HOST, or CI_SERVER_FQDN once glab's CI auto-login is on
// (which then replaces GITLAB_API_HOST). An unset variable or one naming the
// target passes; another server refuses naming both.
func TestGlabEndpointConflict(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		env   map[string]string
		names []string
	}{
		{name: "unset", env: map[string]string{}},
		{name: "same host as a URL", env: map[string]string{"GITLAB_API_HOST": "https://GitLab.com/"}},
		{
			name: "another API host", env: map[string]string{"GITLAB_API_HOST": "gitlab.corp.example"},
			names: []string{"gitlab.com", "GITLAB_API_HOST names gitlab.corp.example"},
		},
		{
			name: "CI auto-login server elsewhere", names: []string{"CI_SERVER_FQDN names gitlab.corp.example"},
			env: map[string]string{"GLAB_ENABLE_CI_AUTOLOGIN": "1", "GITLAB_CI": "true", "CI_SERVER_FQDN": "gitlab.corp.example"},
		},
		{name: "CI auto-login replaces GITLAB_API_HOST", env: map[string]string{
			"GLAB_ENABLE_CI_AUTOLOGIN": "true", "GITLAB_CI": "true", "CI_SERVER_FQDN": "gitlab.com",
			"GITLAB_API_HOST": "gitlab.corp.example",
		}},
		{name: "CI server without auto-login", env: map[string]string{"GITLAB_CI": "true", "CI_SERVER_FQDN": "gitlab.corp.example"}},
	} {
		checkEndpointConflict(t, tc.name, tc.env, tc.names)
	}
}

func checkEndpointConflict(t *testing.T, name string, env map[string]string, names []string) {
	t.Helper()
	ref := GlabEndpointConflict("gitlab.com", func(k string) string { return env[k] })
	if (ref != nil) != (names != nil) {
		t.Errorf("%s: refusal = %v, want refusal %v", name, ref, names != nil)
		return
	}
	for _, s := range names {
		if !strings.Contains(ref.Message, s) {
			t.Errorf("%s: refusal does not name %q: %s", name, s, ref.Message)
		}
	}
}

func envOf(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// TestPinHostArgvInRunsAPortedGlabHostUnpinned pins the one host the pin
// cannot name: glab's --hostname takes no port, so on a host with a port the
// call carries no host at all and reaches glab's default, which GITLAB_HOST
// makes that host. The want is the literal argv glab receives: no --hostname,
// and -R as the bare group/project.
func TestPinHostArgvInRunsAPortedGlabHostUnpinned(t *testing.T) {
	t.Parallel()
	const host = "gitlab.corp.example:8443"
	env := envOf(map[string]string{"GITLAB_HOST": "https://gitlab.corp.example:8443"})
	for _, tc := range []struct {
		name     string
		in, want []string
	}{
		{"api read", []string{"glab", "api", "projects/g%2Fapp/merge_requests/7"}, []string{"glab", "api", "projects/g%2Fapp/merge_requests/7"}},
		{
			"api review POST",
			[]string{"glab", "api", "projects/g%2Fapp/merge_requests/7/notes", "--method", "POST", "--input", "-"},
			[]string{"glab", "api", "projects/g%2Fapp/merge_requests/7/notes", "--method", "POST", "--input", "-"},
		},
		{"mr view, bare repo", []string{"glab", "mr", "view", "7", "-R", "g/app"}, []string{"glab", "mr", "view", "7", "-R", "g/app"}},
		{
			"mr list, repo as the lookup's URL",
			[]string{"glab", "mr", "list", "-R", "https://gitlab.corp.example:8443/g/sub/app", "--source-branch=fix-x"},
			[]string{"glab", "mr", "list", "-R", "g/sub/app", "--source-branch=fix-x"},
		},
		{
			"mr list, URL path with a dotted group",
			[]string{"glab", "mr", "list", "-R", "https://gitlab.corp.example:8443/my.group/app"},
			[]string{"glab", "mr", "list", "-R", "my.group/app"},
		},
		{"path-spelled glab", []string{"/usr/local/bin/glab", "api", "user"}, []string{"/usr/local/bin/glab", "api", "user"}},
		{"auth status", []string{"glab", "auth", "status"}, []string{"glab", "auth", "status"}},
		{"version touches no host", []string{"glab", "--version"}, []string{"glab", "--version"}},
	} {
		got, ref := PinHostArgvIn(tc.in, host, env)
		if ref != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: PinHostArgvIn(%q) = %q, %v; want %q", tc.name, tc.in, got, ref, tc.want)
		}
	}
}

// TestPinHostArgvInRefusesWhatStaysAmbiguousUnpinned pins the fail-closed half
// of the unpinned form: with the environment right, a call that names a host of
// its own, or whose repository glab could read as a host, still refuses; and
// gh, which has no unpinned form, refuses a ported host whatever the env says.
func TestPinHostArgvInRefusesWhatStaysAmbiguousUnpinned(t *testing.T) {
	t.Parallel()
	const host = "gitlab.corp.example:8443"
	env := envOf(map[string]string{"GITLAB_HOST": host, "GH_HOST": host})
	for _, tc := range []struct {
		name, host string
		argv       []string
	}{
		{"api naming --hostname", host, []string{"glab", "api", "--hostname", "gitlab.corp.example", "user"}},
		{"api with a repo flag", host, []string{"glab", "api", "user", "-R", "g/app"}},
		{"api absolute URL", host, []string{"glab", "api", "https://gitlab.other.example/api/v4/user"}},
		{"auth status naming a host", host, []string{"glab", "auth", "status", "--hostname", "gitlab.other.example"}},
		{"mr repo URL on another host", host, []string{"glab", "mr", "view", "7", "-R", "https://gitlab.other.example/g/app"}},
		{"mr repo URL on the same host, another port", host, []string{"glab", "mr", "view", "7", "-R", "https://gitlab.corp.example:9443/g/app"}},
		{"mr repo with a host-looking first segment", host, []string{"glab", "mr", "view", "7", "-R", "gitlab.other.example/g/app"}},
		{"mr repo URL whose path starts with a host", host, []string{"glab", "mr", "view", "7", "-R", "https://gitlab.corp.example:8443/gitlab.com/g/p"}},
		{"mr repo URL whose path starts with a host and port", host, []string{"glab", "mr", "view", "7", "-R", "https://gitlab.corp.example:8443/gitlab.other.example:8443/g/p"}},
		{"path-spelled glab, GITLAB_HOST elsewhere", host, []string{"/usr/local/bin/glab", "api", "--hostname", "gitlab.corp.example", "user"}},
		{"mr repo with a colon", host, []string{"glab", "mr", "view", "7", "-R", "gitlab.other.example:8443/g/app"}},
		{"mr repo as a git URL", host, []string{"glab", "mr", "view", "7", "-R", "git@gitlab.other.example:g/app.git"}},
		{"mr two repos", host, []string{"glab", "mr", "view", "7", "-R", "g/app", "-R", "g/other"}},
		{"mr unnamed repo", host, []string{"glab", "mr", "view", "7"}},
		{"unknown subcommand", host, []string{"glab", "issue", "list"}},
		{"gh on a ported host", "ghe.corp.example:8443", []string{"gh", "api", "user"}},
	} {
		got, ref := PinHostArgvIn(tc.argv, tc.host, env)
		if ref == nil || ref.Code != CodeHostMismatch {
			t.Errorf("%s: PinHostArgvIn = %q, %v; want a host-mismatch refusal", tc.name, got, ref)
		}
	}
}

// TestGlabDefaultConflict pins exactly which environment lets a ported glab
// host run unpinned: GITLAB_HOST names the authority with its port, no other
// variable moves glab's host or API server, and anything else refuses naming
// the variable to fix. Each env is literal; a case that must refuse names the
// text the operator needs to act on.
func TestGlabDefaultConflict(t *testing.T) {
	t.Parallel()
	const host = "gitlab.corp.example:8443"
	ci := map[string]string{"GLAB_ENABLE_CI_AUTOLOGIN": "true", "GITLAB_CI": "true"}
	with := func(base map[string]string, kv ...string) map[string]string {
		out := maps.Clone(base)
		for i := 0; i < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string // "" allows; else a substring of the refusal
	}{
		{"URL form", map[string]string{"GITLAB_HOST": "https://gitlab.corp.example:8443"}, ""},
		{"bare form, any case", map[string]string{"GITLAB_HOST": "GitLab.Corp.Example:8443"}, ""},
		{"trailing slash", map[string]string{"GITLAB_HOST": "https://gitlab.corp.example:8443/"}, ""},
		{"API host is the same", map[string]string{"GITLAB_HOST": host, "GITLAB_API_HOST": "GitLab.Corp.Example:8443"}, ""},
		{"API host as a URL", map[string]string{"GITLAB_HOST": host, "GITLAB_API_HOST": "https://" + host}, `GITLAB_API_HOST is "https://gitlab.corp.example:8443"`},
		{"API host with a subfolder", map[string]string{"GITLAB_HOST": host, "GITLAB_API_HOST": host + "/gitlab"}, "GITLAB_API_HOST is"},
		{"API host on another port", map[string]string{"GITLAB_HOST": host, "GITLAB_API_HOST": "gitlab.corp.example:9443"}, "GITLAB_API_HOST is"},
		{"CI auto-login names it too", with(ci, "GITLAB_HOST", host, "CI_SERVER_FQDN", host), ""},
		{"GITLAB_HOST unset", map[string]string{}, "GITLAB_HOST is unset"},
		{"GITLAB_HOST another port", map[string]string{"GITLAB_HOST": "gitlab.corp.example:9443"}, "GITLAB_HOST is gitlab.corp.example:9443"},
		{"GITLAB_HOST no port", map[string]string{"GITLAB_HOST": "gitlab.corp.example"}, "GITLAB_HOST is gitlab.corp.example"},
		{"GITLAB_HOST another host", map[string]string{"GITLAB_HOST": "gitlab.other.example:8443"}, "GITLAB_HOST is gitlab.other.example:8443"},
		{"GITLAB_HOST with a subfolder", map[string]string{"GITLAB_HOST": host + "/gitlab"}, "carries a path"},
		{"API host elsewhere", map[string]string{"GITLAB_HOST": host, "GITLAB_API_HOST": "gitlab.corp.example"}, `GITLAB_API_HOST is "gitlab.corp.example"`},
		{"GITLAB_REPO set", map[string]string{"GITLAB_HOST": host, "GITLAB_REPO": "g/app"}, "GITLAB_REPO"},
		{"CI auto-login, FQDN unset", with(ci, "GITLAB_HOST", host), "CI_SERVER_FQDN (unset)"},
		{"CI auto-login, FQDN elsewhere", with(ci, "GITLAB_HOST", host, "CI_SERVER_FQDN", "gitlab.corp.example"), "CI_SERVER_FQDN (gitlab.corp.example)"},
	} {
		checkGlabDefault(t, tc.name, GlabDefaultConflict(host, envOf(tc.env)), tc.want)
	}
}

func checkGlabDefault(t *testing.T, name string, ref *RefusalError, want string) {
	t.Helper()
	switch {
	case want == "" && ref != nil:
		t.Errorf("%s: refused, want the unpinned form allowed: %v", name, ref)
	case want != "" && (ref == nil || ref.Code != CodeHostMismatch || !strings.Contains(ref.Message, want)):
		t.Errorf("%s: GlabDefaultConflict = %v, want a host-mismatch refusal containing %q", name, ref, want)
	}
}

// TestPinHostArgvKeysOnTheToolNotThePath pins that a path-spelled gh or glab is
// still gh or glab: the pin rewrites it, a ported host with an environment
// that is not glab's default refuses it, and the same literal argv with a
// bare name behaves identically. A literal argv[0] compare would let
// `/usr/local/bin/glab` skip both.
func TestPinHostArgvKeysOnTheToolNotThePath(t *testing.T) {
	t.Parallel()
	got, ref := PinHostArgv([]string{"/opt/homebrew/bin/gh", "api", "user"}, "github.com")
	if want := []string{"/opt/homebrew/bin/gh", "api", "--hostname", "github.com", "user"}; ref != nil || !slices.Equal(got, want) {
		t.Errorf("PinHostArgv(path-spelled gh) = %q, %v; want %q", got, ref, want)
	}
}

// TestPinHostArgvInGatesAPathSpelledGlab pins the environment-aware half: a
// ported host with no GITLAB_HOST refuses glab however it is spelled.
func TestPinHostArgvInGatesAPathSpelledGlab(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"glab", "/usr/local/bin/glab", "./glab", "/x/GLAB.exe"} {
		if got := ToolName(name); !strings.EqualFold(got, "glab") {
			t.Errorf("ToolName(%q) = %q, want glab", name, got)
		}
	}
	const host = "gitlab.corp.example:8443"
	for _, bin := range []string{"glab", "/usr/local/bin/glab"} {
		_, ref := PinHostArgvIn([]string{bin, "api", "user"}, host, envOf(nil))
		if ref == nil || ref.Code != CodeHostMismatch || !strings.Contains(ref.Message, "GITLAB_HOST is unset") {
			t.Errorf("PinHostArgvIn(%s, no GITLAB_HOST) = %v, want a host-mismatch refusal naming GITLAB_HOST", bin, ref)
		}
	}
}
