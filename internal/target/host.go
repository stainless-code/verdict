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
	gitlabDefault   = "gitlab.com"
)

// hostConflict refuses a target whose host no call could be pinned to. gh and
// glab take the host on every call (core.PinHostArgv), so GH_HOST and a
// GITLAB_HOST that names another host lose to it. Two cases cannot be made
// unambiguous and refuse naming both hosts instead of picking one: glab's
// --hostname takes no port, so a GitLab host on a non-default port would fall
// back to glab's default host; and Forgejo has no CLI, so every call goes to
// FORGEJO_HOST with FORGEJO_TOKEN, and a target on another host would be read
// from, or written to, the wrong server with that server's token.
func hostConflict(provider, host string, env map[string]string) *core.RefusalError {
	switch {
	case host == "":
		return nil
	case provider == providerGitLab && strings.Contains(host, ":"):
		return mismatch("the target is on %s, but glab's --hostname takes no port, so verdict cannot name that host "+
			"on a glab call and glab would pick its own (the working directory's remote, else GITLAB_HOST: %s); "+
			"to let glab decide, pass <group/project> <mr> --provider gitlab",
			host, cmp.Or(core.EnvHostAuthority(env[envGitLabHost]), gitlabDefault))
	case provider == providerForgejo:
		configured := core.EnvHostAuthority(env[envForgejoHost])
		if configured != "" && configured != host {
			return mismatch("the target is on %s, but FORGEJO_HOST names %s and every Forgejo call goes there "+
				"with FORGEJO_TOKEN; set FORGEJO_HOST and FORGEJO_TOKEN for %s, or pass a target on %s",
				host, configured, host, configured)
		}
	}
	return nil
}

func mismatch(format string, a ...any) *core.RefusalError {
	return &core.RefusalError{Code: core.CodeHostMismatch, Message: fmt.Sprintf(format, a...)}
}
