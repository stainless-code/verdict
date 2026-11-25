package core

import (
	"context"
	"fmt"
	"net/url"
	"slices"
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
)

// PinHostArgv qualifies one gh or glab argv with host, so the call reaches
// host whatever the environment's default host is:
//
//   - `gh api ...` and `glab api ...` gain `--hostname host`, which both CLIs
//     read ahead of GH_HOST and GITLAB_HOST;
//   - `gh pr ... --repo owner/repo` becomes `--repo host/owner/repo`;
//   - `glab mr ... -R group/project` becomes `-R https://host/group/project`;
//   - `auth status` gains `--hostname host`; `--version` touches no host.
//
// Any other gh or glab shape, a repository already qualified with another
// host, and a glab host with a port (glab's --hostname rejects one) refuse
// with [CodeHostMismatch] rather than run against whatever host the CLI
// defaults to. Other tools and an empty host pass through unchanged.
func PinHostArgv(argv []string, host string) ([]string, *RefusalError) {
	if host == "" || len(argv) < 2 || argv[1] == pinVersion {
		return argv, nil
	}
	switch argv[0] {
	case pinGH:
		return pinArgv(argv, host, "pr", qualifyGH)
	case pinGlab:
		if strings.Contains(host, ":") {
			return nil, unpinnable(argv, host, "glab's --hostname takes no port")
		}
		return pinArgv(argv, host, "mr", qualifyGlab)
	}
	return argv, nil
}

// pinArgv applies the shapes both CLIs share; repoSub is the subcommand whose
// repository flag carries the host, qualify how its operand names one.
func pinArgv(argv []string, host, repoSub string, qualify func(repo, host string) (string, bool)) ([]string, *RefusalError) {
	switch {
	case argv[1] == pinAPI:
		if slices.Contains(argv, pinHostname) {
			return nil, unpinnable(argv, host, "it already names a --hostname")
		}
		return slices.Concat(argv[:2], []string{pinHostname, host}, argv[2:]), nil
	case slices.Equal(argv[1:], []string{pinAuth, pinStatus}):
		return slices.Concat(argv, []string{pinHostname, host}), nil
	case argv[1] == repoSub:
		return pinRepoFlag(argv, host, qualify)
	}
	return nil, unpinnable(argv, host, "verdict has no host-qualified form for it")
}

// pinRepoFlag rewrites the one repository operand of a pr or mr subcommand.
func pinRepoFlag(argv []string, host string, qualify func(repo, host string) (string, bool)) ([]string, *RefusalError) {
	out := slices.Clone(argv)
	at := slices.IndexFunc(out, func(a string) bool { return a == pinRepo || a == pinRepoR })
	if at < 0 || at+1 >= len(out) {
		return nil, unpinnable(argv, host, "it names no repository")
	}
	qualified, ok := qualify(out[at+1], host)
	if !ok {
		return nil, unpinnable(argv, host, fmt.Sprintf("its repository %q names another host", out[at+1]))
	}
	out[at+1] = qualified
	return out, nil
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
