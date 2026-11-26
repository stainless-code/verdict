package core

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
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
	schemeSep   = "://"
	exeSuffix   = ".exe"
	pathSep     = "/"
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
	return pinArgv(argv, host, false)
}

// ToolName is the tool an argv[0] runs: the file name without its directory or
// a Windows `.exe`, so `/usr/local/bin/glab` is glab. The pin and the runner's
// environment check both key on it; a literal compare would let a path-spelled
// glab or gh skip both.
func ToolName(argv0 string) string {
	base := filepath.Base(argv0)
	if len(base) > len(exeSuffix) && strings.EqualFold(base[len(base)-len(exeSuffix):], exeSuffix) {
		return base[:len(base)-len(exeSuffix)]
	}
	return base
}

// PinHostArgvIn is [PinHostArgv] for a runner that can read the environment the
// child inherits (getenv). It differs for exactly one target: a glab call on a
// host with a port. glab's --hostname takes no port, so that call cannot be
// pinned; when the environment already makes glab's own default host exactly
// that host ([GlabDefaultConflict]), the call runs with no host pin at all and
// its -R as the bare `group/project`, so the default is what it reaches. Any
// other environment refuses with [CodeHostMismatch], as [PinHostArgv] does.
func PinHostArgvIn(argv []string, host string, getenv func(string) string) ([]string, *RefusalError) {
	if host == "" || len(argv) < 2 || ToolName(argv[0]) != pinGlab || !strings.Contains(host, ":") {
		return PinHostArgv(argv, host)
	}
	if ref := GlabDefaultConflict(host, getenv); ref != nil {
		return nil, ref
	}
	return pinArgv(argv, host, true)
}

// pinArgv is the shared shape check. defaultHost is set only for a ported glab
// host whose environment already is the default: the call then carries no host
// of its own, so every spelling that could name one still refuses.
func pinArgv(argv []string, host string, defaultHost bool) ([]string, *RefusalError) {
	if host == "" || len(argv) < 2 || argv[1] == pinVersion {
		return argv, nil
	}
	repoSub, qualify := "pr", qualifyGH
	switch ToolName(argv[0]) {
	case pinGH:
	case pinGlab:
		repoSub, qualify = "mr", glabQualifier(defaultHost)
	default:
		return argv, nil
	}
	if strings.Contains(host, ":") && !defaultHost {
		return nil, unpinnable(argv, host, argv[0]+"'s --hostname takes no port")
	}
	switch {
	case argv[1] == pinAPI:
		return pinAPICall(argv, host, defaultHost)
	case slices.Equal(argv[1:min(len(argv), authStatusEnd)], []string{pinAuth, pinStatus}):
		return pinAuthStatus(argv, host, defaultHost)
	case argv[1] == repoSub:
		return pinRepoFlag(argv, host, qualify)
	}
	return nil, unpinnable(argv, host, "verdict has no host-qualified form for it")
}

// pinAPICall inserts --hostname after `api`. An endpoint that is an absolute
// URL names its own host, which both CLIs use instead of --hostname.
func pinAPICall(argv []string, host string, defaultHost bool) ([]string, *RefusalError) {
	if hostFlags(argv) > 0 || slices.ContainsFunc(argv, isRepoFlag) {
		return nil, unpinnable(argv, host, "it already names a host or repository flag")
	}
	if slices.ContainsFunc(argv[2:], func(a string) bool {
		lower := strings.ToLower(a)
		return strings.HasPrefix(lower, schemeHTTPS+"://") || strings.HasPrefix(lower, schemeHTTP+"://")
	}) {
		return nil, unpinnable(argv, host, "its endpoint is an absolute URL, which overrides --hostname")
	}
	if defaultHost {
		return argv, nil
	}
	return slices.Concat(argv[:2], []string{pinHostname, host}, argv[2:]), nil
}

// pinAuthStatus appends --hostname, or keeps the argv when it already names
// exactly this host in the spaced form (demo's login ladder does).
func pinAuthStatus(argv []string, host string, defaultHost bool) ([]string, *RefusalError) {
	if slices.ContainsFunc(argv, func(a string) bool { return a == "-h" || strings.HasPrefix(a, "-h=") }) {
		return nil, unpinnable(argv, host, "it names a host in the short -h form")
	}
	if defaultHost {
		return keepUnpinnedAuthStatus(argv, host)
	}
	switch at := slices.Index(argv, pinHostname); {
	case hostFlags(argv) == 0:
		return slices.Concat(argv, []string{pinHostname, host}), nil
	case hostFlags(argv) == 1 && at >= 0 && at+1 < len(argv) && strings.EqualFold(argv[at+1], host):
		return argv, nil
	}
	return nil, unpinnable(argv, host, "it already names another --hostname")
}

// keepUnpinnedAuthStatus keeps an `auth status` that names no host: glab asks
// its default host.
func keepUnpinnedAuthStatus(argv []string, host string) ([]string, *RefusalError) {
	if hostFlags(argv) > 0 {
		return nil, unpinnable(argv, host, "it names a --hostname, which takes no port")
	}
	return argv, nil
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

// glabQualifier is the -R rewrite of a pinned call, or of one with no host.
func glabQualifier(defaultHost bool) func(repo, host string) (string, bool) {
	if defaultHost {
		return unqualifyGlab
	}
	return qualifyGlab
}

// unqualifyGlab is [qualifyGlab] for a call that carries no host: glab resolves
// a bare `group/project` against its default host, so a URL on host becomes its
// path and a URL on another host refuses. A bare name glab could read as
// `HOST/OWNER/REPO` (a colon, or a dotted first segment of three or more: glab
// takes that segment for a host when its own config lists it) refuses too.
// glab: internal/glrepo/repo.go FromFullName, `case 2`.
func unqualifyGlab(repo, host string) (string, bool) {
	if !IsTargetURL(repo) {
		return repo, hostFreeRepo(repo)
	}
	u, err := url.Parse(repo)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || URLAuthority(u) != host {
		return "", false
	}
	// The path is what glab reads as the repository once the URL is dropped,
	// so it takes the same test a bare name does: `-R https://h:8443/a.b/g/p`
	// would otherwise become `a.b/g/p`, which glab reads as host a.b.
	path := strings.Trim(u.Path, pathSep)
	return path, hostFreeRepo(path)
}

// hostFreeRepo reports a bare repository name glab cannot read as
// `HOST/OWNER/REPO`: no colon or `@`, no empty segment, and no dotted first
// segment of a three-part-or-longer path.
func hostFreeRepo(repo string) bool {
	parts := strings.Split(repo, pathSep)
	if repo == "" || strings.ContainsAny(repo, ":@") || slices.Contains(parts, "") {
		return false
	}
	return len(parts) < ghHostedParts || !strings.Contains(parts[0], ".")
}

func unpinnable(argv []string, host, why string) *RefusalError {
	return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
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
	if glabCIAutologin(getenv) {
		name = envCIServerFQDN
	}
	return endpointConflict(host, name, getenv)
}

func endpointConflict(host, name string, getenv func(string) string) *RefusalError {
	configured := EnvHostAuthority(getenv(name))
	if configured == "" || strings.EqualFold(configured, host) {
		return nil
	}
	return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
		"the target is on %s, but %s names %s and %s; unset %s, or set it to %s",
		host, name, configured, glabEndpointNote, name, host)}
}

// glabCIAutologin reads glab's CI auto-login switch. glab's own test is the
// literal "true" on both variables (internal/config/config_mapping.go
// CIAutologinEnabled); a looser parse here only widens what refuses.
func glabCIAutologin(getenv func(string) string) bool {
	return truthy(getenv(envGlabCILogin)) && truthy(getenv(envGitLabCI))
}

const (
	envGitLabHost = "GITLAB_HOST"
	envGlabRepo   = "GITLAB_REPO"
)

// GlabDefaultConflict is nil when the environment glab inherits (read through
// getenv) makes glab's own default host exactly host, a host with a port: the
// one case a call to it can go unpinned, because --hostname takes no port
// (glab: internal/glinstance/host.go HostnameValidator). It holds when
//
//   - GITLAB_HOST names exactly host, port included: glab keeps the port in
//     its default host (cmdutils/factory.go NewFactory, StripHostProtocol),
//     resolves a bare `-R group/project` against it (FromFullName, `case 1`),
//     and an api call with no --hostname or -R builds its client for it
//     (commands/api/api.go: repoHost "" becomes the factory's default). The
//     environment variable beats the config file and any git remote, which must
//     match it or glab errors; GITLAB_HOST is read ahead of GITLAB_URI and
//     GL_HOST (config.GetFromEnvWithSource), so those cannot override it;
//   - GITLAB_HOST has no path, which glab would read as a subfolder;
//   - GITLAB_API_HOST, if set, is exactly host: glab sends the call there
//     instead (NewClientFromConfig reads api_host) and splices it, unparsed,
//     into `<protocol>://<api_host>/api/v4/` (glinstance.APIEndpoint), so a
//     scheme or a path makes it a different server or a subfolder and refuses;
//   - under CI auto-login, CI_SERVER_FQDN is set and is exactly host too:
//     glab then reads host and api_host from it and ignores GITLAB_HOST
//     (config_mapping.go ciAutologinEnvOverrides);
//   - GITLAB_REPO is unset: an api call with no -R would resolve its host from
//     that repository instead (cmdutils/repo_override.go).
//
// Each failure refuses with [CodeHostMismatch] naming the variable to set.
func GlabDefaultConflict(host string, getenv func(string) string) *RefusalError {
	configured := EnvHostAuthority(getenv(envGitLabHost))
	switch {
	case configured != host:
		return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
			"the target is on %s, but glab's --hostname takes no port, so verdict can run glab for it only when "+
				"glab's own default host is exactly that, and GITLAB_HOST is %s; set GITLAB_HOST to %s",
			host, orUnset(configured), host)}
	case hostEnvHasPath(getenv(envGitLabHost)):
		return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
			"the target is on %s, but GITLAB_HOST carries a path, which glab reads as a subfolder of its default "+
				"host; set GITLAB_HOST to %s alone", host, host)}
	case strings.TrimSpace(getenv(envGlabRepo)) != "":
		return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
			"the target is on %s, but GITLAB_REPO is set and glab would take its host from that repository on a call "+
				"with no -R; unset GITLAB_REPO", host)}
	}
	// GITLAB_API_HOST is checked even under CI auto-login, which makes glab
	// ignore it: a refusal there only over-refuses, never reaches another host.
	if api := strings.TrimSpace(getenv(envGlabAPIHost)); api != "" && !strings.EqualFold(api, host) {
		return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
			"the target is on %s, but GITLAB_API_HOST is %q and glab reads it as a bare host:port (a scheme or a "+
				"path makes it a different server or a subfolder) and %s; unset GITLAB_API_HOST, or set it to %s",
			host, api, glabEndpointNote, host)}
	}
	if fqdn := strings.TrimSpace(getenv(envCIServerFQDN)); glabCIAutologin(getenv) && !strings.EqualFold(fqdn, host) {
		return &RefusalError{Code: CodeHostMismatch, Message: Msgf(
			"the target is on %s, but under glab's CI auto-login its default host is CI_SERVER_FQDN (%s), "+
				"not GITLAB_HOST, and glab reads it as a bare host:port; set CI_SERVER_FQDN to %s", host, orUnset(fqdn), host)}
	}
	return nil
}

func orUnset(v string) string {
	if v == "" {
		return "unset"
	}
	return v
}

// hostEnvHasPath reports a host variable written with a path after the
// authority (`https://h:8443/gitlab`).
func hostEnvHasPath(raw string) bool {
	raw = strings.TrimSpace(raw)
	if !IsTargetURL(raw) {
		raw = schemeHTTPS + schemeSep + raw
	}
	u, err := url.Parse(raw)
	return err != nil || strings.Trim(u.Path, pathSep) != "" || u.RawQuery != "" || u.Fragment != ""
}

func truthy(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}
