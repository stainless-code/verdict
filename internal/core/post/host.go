package postcore

import (
	"context"
	"errors"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
)

// codeThreadsFailed is the refusal every resolve-path failure reports when
// the host did not name a narrower cause.
const codeThreadsFailed = "threads-failed"

// hostWriter is the write half of one code host: the tool the submit path
// spawns and the call that lands one payload.
type hostWriter interface {
	tool() string
	post(ctx context.Context, cmdr Commander, env map[string]string,
		caps core.HostCapabilities, repo, pr, dir, payload string) postAttempt
}

// hostReader is the read half: the live head the drift check compares, the
// authenticated login the authorship gate compares, the open-thread recheck
// the approval guard counts, and the local gate that runs before any call.
type hostReader interface {
	publication(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (remotePublication, error)
	liveSHA(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (string, error)
	selfLogin(ctx context.Context, cmdr Commander, env map[string]string) (string, error)
	recheck(ctx context.Context, cmdr Commander, env map[string]string, repo, pr, dir string) (int, string, error)
	auth(env map[string]string) *core.RefusalError
}

// hostResolver is the resolve half: the one call that closes a review thread.
// A host with no documented resolve endpoint returns a nil resolver, and its
// thread_resolution capability stays false.
type hostResolver interface {
	resolveThread(ctx context.Context, cmdr Commander, env map[string]string,
		repo, pr, threadID string) error
}

// hostFor is the post path's only provider switch: every host behavior a verb
// reaches goes through the triple it returns. A provider outside the switch
// gets GitHub's writer and reader, because checkBundle refuses a host it
// cannot post to before any call is built, and no resolver, because a host
// without its own resolve endpoint must not advertise thread_resolution.
func hostFor(provider string) (hostWriter, hostReader, hostResolver) {
	switch provider {
	case providerGitHub:
		return githubHost{}, githubHost{}, githubHost{}
	case providerGitLab:
		return gitlabHost{}, gitlabHost{}, gitlabHost{}
	case providerForgejo:
		return forgejoHost{}, forgejoHost{}, nil
	default:
		return githubHost{}, githubHost{}, nil
	}
}

// SelfLogin reads the authenticated login the resolve identity gate compares
// the thread root author against.
func SelfLogin(
	ctx context.Context, cmdr Commander, provider string, env map[string]string,
) (string, error) {
	_, reader, _ := hostFor(provider)
	return reader.selfLogin(ctx, cmdr, env)
}

// ResolveThread closes one review thread through the host seam. A host that
// advertises no thread_resolution refuses typed before any call is built; a
// host whose call fails keeps the host's own words where it has them.
func ResolveThread(
	ctx context.Context, cmdr Commander, provider string, env map[string]string,
	repo, pr, threadID string,
) *core.RefusalError {
	caps := core.CapabilitiesFor(provider)
	_, _, resolver := hostFor(provider)
	if !caps.ThreadResolution || resolver == nil {
		return &core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(
			"thread resolution on %s is not implemented (%s)",
			provider, caps.ReasonFor(core.CapabilityThreadResolution))}
	}
	if cmdr == nil {
		return &core.RefusalError{Code: codeThreadsFailed, Message: "threads failed: no commander in runtime"}
	}
	if err := resolver.resolveThread(ctx, cmdr, env, repo, pr, threadID); err != nil {
		if ref, ok := errors.AsType[*core.RefusalError](err); ok {
			return ref
		}
		return &core.RefusalError{Code: codeThreadsFailed, Message: fmt.Sprintf(
			"could not resolve thread %s: %v", threadID, err)}
	}
	return nil
}
