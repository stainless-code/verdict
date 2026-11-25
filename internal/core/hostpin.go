package core

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// CodeHostMismatch is the refusal for a call verdict cannot pin to the
// target's host: the environment names another host for the same tool and
// the tool offers no per-call override that wins over it, or the bundle on
// disk was fetched from another host than the one the target names.
const CodeHostMismatch = "host-mismatch"

type hostKey struct{}

// WithHost returns ctx carrying the target's code host (an authority from
// [URLAuthority] or [RemoteHost]). Every gh and glab call the production
// runner spawns under it names that host explicitly, so GH_HOST, GITLAB_HOST
// and the CLIs' own default hosts cannot send it elsewhere. An empty host
// returns ctx unchanged.
func WithHost(ctx context.Context, host string) context.Context {
	if host == "" {
		return ctx
	}
	return context.WithValue(ctx, hostKey{}, host)
}

// HostOf is the host [WithHost] pinned on ctx, or "" when none was.
func HostOf(ctx context.Context) string {
	if host, ok := ctx.Value(hostKey{}).(string); ok {
		return host
	}
	return ""
}

// URLAuthority is u's lowercase host[:port] with the scheme's default port
// dropped, the form every host-equality check compares: two hosts that differ
// only by a non-default port are different hosts.
func URLAuthority(u *url.URL) string {
	host := strings.ToLower(u.Host)
	switch {
	case u.Scheme == schemeHTTPS && u.Port() == "443", u.Scheme == schemeHTTP && u.Port() == "80":
		return strings.ToLower(u.Hostname())
	}
	return host
}

// EnvHostAuthority reads a host variable (GITLAB_HOST, FORGEJO_HOST) the way
// the target table maps it, a bare host or a URL, as a [URLAuthority]; "" when
// unset or unreadable.
func EnvHostAuthority(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !IsTargetURL(raw) {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return URLAuthority(u)
}

const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
	// ghHostedParts is gh's HOST/OWNER/REPO; OWNER/REPO has one part fewer.
	ghHostedParts = 3
)

// Host CLI argv tokens the pin reads and writes.
const (
	pinGH       = "gh"
	pinGlab     = "glab"
	pinAPI      = "api"
	pinHostname = "--hostname"
	pinRepo     = "--repo"
	pinRepoR    = "-R"
	pinVersion  = "--version"
	pinAuth     = "auth"
	pinStatus   = "status"
	// authStatusEnd ends the `<tool> auth status` prefix.
	authStatusEnd = 3
)

// PinHostArgv qualifies one gh or glab argv with host, so the call reaches
// host whatever the environment's default host is:
//
//   - `gh api ...` and `glab api ...` gain `--hostname host`, which both CLIs
//     read ahead of GH_HOST and GITLAB_HOST;
//   - `gh pr ... --repo owner/repo` becomes `--repo host/owner/repo`;
//   - `glab mr ... -R group/project` becomes `-R https://host/group/project`;
//   - `auth status` gains `--hostname host` (or keeps the one naming host);
//     `--version` touches no host.
//
// Anything the pin cannot make the only host the CLI reads refuses with
// [CodeHostMismatch] rather than run against the tool's default host: any
// other gh or glab shape; a host with a port (neither CLI's --hostname takes
// one); a host flag or repository flag already present in another spelling
// or more than once (the CLIs' flag parser keeps the last); a repository on
// another host; and an api endpoint given as an absolute URL, which both CLIs
// read ahead of --hostname. Other tools and an empty host pass through.
func PinHostArgv(argv []string, host string) ([]string, *RefusalError) {
	if host == "" || len(argv) < 2 || argv[1] == pinVersion {
		return argv, nil
	}
	repoSub, qualify := "pr", qualifyGH
	switch argv[0] {
	case pinGH:
	case pinGlab:
		repoSub, qualify = "mr", qualifyGlab
	default:
		return argv, nil
	}
	if strings.Contains(host, ":") {
		return nil, unpinnable(argv, host, argv[0]+"'s --hostname takes no port")
	}
	switch {
	case argv[1] == pinAPI:
		return pinAPICall(argv, host)
	case slices.Equal(argv[1:min(len(argv), authStatusEnd)], []string{pinAuth, pinStatus}):
		return pinAuthStatus(argv, host)
	case argv[1] == repoSub:
		return pinRepoFlag(argv, host, qualify)
	}
	return nil, unpinnable(argv, host, "verdict has no host-qualified form for it")
}

// pinAPICall inserts --hostname after `api`. An endpoint that is an absolute
// URL names its own host, which both CLIs use instead of --hostname.
func pinAPICall(argv []string, host string) ([]string, *RefusalError) {
	if hostFlags(argv) > 0 || slices.ContainsFunc(argv, isRepoFlag) {
		return nil, unpinnable(argv, host, "it already names a host or repository flag")
	}
	if slices.ContainsFunc(argv[2:], func(a string) bool {
		lower := strings.ToLower(a)
		return strings.HasPrefix(lower, schemeHTTPS+"://") || strings.HasPrefix(lower, schemeHTTP+"://")
	}) {
		return nil, unpinnable(argv, host, "its endpoint is an absolute URL, which overrides --hostname")
	}
	return slices.Concat(argv[:2], []string{pinHostname, host}, argv[2:]), nil
}

// pinAuthStatus appends --hostname, or keeps the argv when it already names
// exactly this host in the spaced form (demo's login ladder does).
func pinAuthStatus(argv []string, host string) ([]string, *RefusalError) {
	if slices.ContainsFunc(argv, func(a string) bool { return a == "-h" || strings.HasPrefix(a, "-h=") }) {
		return nil, unpinnable(argv, host, "it names a host in the short -h form")
	}
	switch at := slices.Index(argv, pinHostname); {
	case hostFlags(argv) == 0:
		return slices.Concat(argv, []string{pinHostname, host}), nil
	case hostFlags(argv) == 1 && at >= 0 && at+1 < len(argv) && strings.EqualFold(argv[at+1], host):
		return argv, nil
	}
	return nil, unpinnable(argv, host, "it already names another --hostname")
}

// pinRepoFlag rewrites the one repository operand of a pr or mr subcommand.
func pinRepoFlag(argv []string, host string, qualify func(repo, host string) (string, bool)) ([]string, *RefusalError) {
	if hostFlags(argv) > 0 {
		return nil, unpinnable(argv, host, "it already names a --hostname")
	}
	if n := len(slices.DeleteFunc(slices.Clone(argv), func(a string) bool { return !isRepoFlag(a) })); n != 1 {
		return nil, unpinnable(argv, host, "it does not name exactly one repository")
	}
	out := slices.Clone(argv)
	at := slices.IndexFunc(out, func(a string) bool { return a == pinRepo || a == pinRepoR })
	if at < 0 || at+1 >= len(out) {
		return nil, unpinnable(argv, host, "its repository is not in the spaced --repo/-R form")
	}
	qualified, ok := qualify(out[at+1], host)
	if !ok {
		return nil, unpinnable(argv, host, fmt.Sprintf("its repository %q names another host", out[at+1]))
	}
	out[at+1] = qualified
	return out, nil
}

// hostFlags counts --hostname in every spelling the CLIs' flag parser takes.
func hostFlags(argv []string) int {
	return len(slices.DeleteFunc(slices.Clone(argv), func(a string) bool {
		return a != pinHostname && !strings.HasPrefix(a, pinHostname+"=")
	}))
}

// isRepoFlag matches --repo and -R in every spelling: spaced, `=`, and the
// attached short form.
func isRepoFlag(a string) bool {
	return a == pinRepo || strings.HasPrefix(a, pinRepo+"=") || strings.HasPrefix(a, pinRepoR)
}

// qualifyGH is gh's `[HOST/]OWNER/REPO`: a two-part name takes host, a
// three-part one must already be on it.
func qualifyGH(repo, host string) (string, bool) {
	switch parts := strings.Split(repo, "/"); len(parts) {
	case ghHostedParts - 1:
		return host + "/" + repo, true
	case ghHostedParts:
		return repo, strings.EqualFold(parts[0], host)
	}
	return "", false
}

// qualifyGlab is glab's `-R`: a GitLab path nests groups, so only a URL names
// its host unambiguously (glab reads the URL's host, port included).
func qualifyGlab(repo, host string) (string, bool) {
	if !IsTargetURL(repo) {
		return schemeHTTPS + "://" + host + "/" + repo, true
	}
	u, err := url.Parse(repo)
	return repo, err == nil && URLAuthority(u) == host
}

func unpinnable(argv []string, host, why string) *RefusalError {
	return &RefusalError{Code: CodeHostMismatch, Message: fmt.Sprintf(
		"refusing `%s %s`: the target is on %s, but %s, so the call could reach the tool's default host instead",
		argv[0], argv[1], host, why)}
}

// glab reads its API server from these variables ahead of --hostname: with CI
// auto-login on, CI_SERVER_FQDN; otherwise GITLAB_API_HOST. Either applies to
// every host, so a pinned call would carry the pinned host's token to it.
const (
	envGlabAPIHost   = "GITLAB_API_HOST"
	envGlabCILogin   = "GLAB_ENABLE_CI_AUTOLOGIN"
	envGitLabCI      = "GITLAB_CI"
	envCIServerFQDN  = "CI_SERVER_FQDN"
	glabEndpointNote = "glab sends API calls to that server instead of the pinned --hostname"
)

// GlabEndpointConflict refuses a glab call pinned to host when the environment
// glab inherits (read through getenv) points its API at another server:
// GITLAB_API_HOST, or CI_SERVER_FQDN when glab's CI auto-login is on. A
// separate API hostname is a legitimate self-hosted setup, but then the call
// cannot be pinned, so it refuses naming both instead of stripping either. An
// unset variable, or one naming host itself, passes.
func GlabEndpointConflict(host string, getenv func(string) string) *RefusalError {
	if host == "" {
		return nil
	}
	name := envGlabAPIHost
	if truthy(getenv(envGlabCILogin)) && truthy(getenv(envGitLabCI)) {
		name = envCIServerFQDN
	}
	configured := EnvHostAuthority(getenv(name))
	if configured == "" || strings.EqualFold(configured, host) {
		return nil
	}
	return &RefusalError{Code: CodeHostMismatch, Message: fmt.Sprintf(
		"the target is on %s, but %s names %s and %s; unset %s, or set it to %s",
		host, name, configured, glabEndpointNote, name, host)}
}

func truthy(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}
