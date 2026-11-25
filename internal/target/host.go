package target

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	providerForgejo = "forgejo"
	envGitLabHost   = "GITLAB_HOST"
	envForgejoHost  = "FORGEJO_HOST"
)

// hostConflict refuses a target whose host no call could be pinned to. gh and
// glab take the host on every call (core.PinHostArgv), so GH_HOST and a
// GITLAB_HOST that names another host lose to it. These cases cannot be made
// unambiguous and refuse naming both hosts instead of picking one. A verb
// that may ask the host for the branch's pull request runs this check before
// that lookup, so a conflict makes no host call:
//   - a gh host with a port: gh's --hostname takes none, so the calls would
//     reach gh's own default host;
//   - a glab host with a port: --hostname takes none either, so it is allowed
//     only when the environment already makes glab's default host exactly the
//     target (core.GlabDefaultConflict) and the calls run unpinned, else it
//     refuses;
//   - a GitLab target read from an ssh origin (which names no web port) whose
//     host GITLAB_HOST serves on a non-default port: the pin would name the
//     default port, another host, and glab cannot be given the configured one.
//     The origin's authority is ambiguous (the web port is unknown, and the
//     ssh port is the daemon's), so the call stays refused even though
//     GITLAB_HOST names a port: the unpinned form would reach that port, which
//     may not be the origin's host;
//   - a GitLab target while GITLAB_API_HOST (or, under glab's CI auto-login,
//     CI_SERVER_FQDN) names another server: glab sends its API calls there
//     ahead of --hostname (core.GlabEndpointConflict);
//   - a Forgejo target on another host than FORGEJO_HOST: Forgejo has no CLI,
//     so every call goes to FORGEJO_HOST with FORGEJO_TOKEN, and the target
//     would be read from, or written to, the wrong server with its token.
func hostConflict(provider, host string, env map[string]string) *core.RefusalError {
	switch {
	case host == "":
		return nil
	case provider == providerGitLab && strings.Contains(host, ":"):
		return glabPortConflict(host, env)
	case provider == providerGitHub && strings.Contains(host, ":"):
		return ghPortConflict(host)
	case provider == providerGitLab:
		return cmp.Or(gitlabPortConflict(host, core.EnvHostAuthority(env[envGitLabHost])),
			core.GlabEndpointConflict(host, func(name string) string { return env[name] }))
	case provider == providerForgejo:
		return forgejoConflict(host, core.EnvHostAuthority(env[envForgejoHost]))
	}
	return nil
}

// glabPortConflict is core.GlabDefaultConflict with the way out a target-less
// read has: a typed pair lets glab pick its own host.
func glabPortConflict(host string, env map[string]string) *core.RefusalError {
	ref := core.GlabDefaultConflict(host, func(name string) string { return env[name] })
	if ref != nil {
		ref.Message += "; or, to let glab decide, pass <group/project> <mr> --provider gitlab"
	}
	return ref
}

// ghPortConflict refuses a gh host with a port, naming the host gh would fall
// back to. A glab host with a port goes to core.GlabDefaultConflict instead.
func ghPortConflict(host string) *core.RefusalError {
	return mismatch("the target is on %s, but gh's --hostname takes no port, so verdict cannot name that host "+
		"on a gh call and gh would pick its own (the working directory's remote, else GH_HOST, else github.com); "+
		"to let gh decide, pass <owner/repo> <pr> --provider github", host)
}

// forgejoConflict refuses a Forgejo target off FORGEJO_HOST.
func forgejoConflict(host, configured string) *core.RefusalError {
	if configured == "" || configured == host {
		return nil
	}
	return mismatch("the target is on %s, but FORGEJO_HOST names %s and every Forgejo call goes there "+
		"with FORGEJO_TOKEN; set FORGEJO_HOST and FORGEJO_TOKEN for %s, or pass a target on %s",
		host, configured, host, configured)
}

// gitlabPortConflict refuses a portless target whose hostname GITLAB_HOST
// serves on another port: the two are different hosts, and glab cannot be
// pinned to the configured one.
func gitlabPortConflict(host, configured string) *core.RefusalError {
	name, _, _ := strings.Cut(configured, ":")
	if configured == "" || configured == host || !strings.EqualFold(name, host) {
		return nil
	}
	return mismatch("the target is on %s (an ssh origin names no web port), but GITLAB_HOST names %s, the same "+
		"server on another port, and glab's --hostname cannot carry a port; to let glab decide, "+
		"pass <group/project> <mr> --provider gitlab", host, configured)
}

func mismatch(format string, a ...any) *core.RefusalError {
	return &core.RefusalError{Code: core.CodeHostMismatch, Message: fmt.Sprintf(format, a...)}
}
