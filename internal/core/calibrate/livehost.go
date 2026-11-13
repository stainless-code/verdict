package calibratecore

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// liveHost is one provider's live-join surface: the thread, PR-state, and
// changed-file reads plus the thread match and resolution tests the join
// applies. Provider selection stays one table lookup so a new host adds a
// row, never a host-name branch at a call site.
type liveHost struct {
	threads  func(context.Context, *state) ([]any, *core.RefusalError)
	pr       func(context.Context, *state) (map[string]any, *core.RefusalError)
	changed  func(context.Context, *state, string, string) []any
	posted   func(entry any) string
	match    func(thread any, postedBody string, path any) bool
	resolved func(thread any) bool
}

const providerGitHub = "github"

// msgThreadsFailed is the shared threads-failed refusal message: every
// host's thread read maps its failure onto it.
const msgThreadsFailed = "could not read live review threads"

var liveHosts = map[string]liveHost{
	providerGitHub: {
		threads:  githubLiveThreads,
		pr:       githubLivePR,
		changed:  githubLiveChanged,
		posted:   postedBody,
		match:    threadMatches,
		resolved: readcommon.ThreadResolved,
	},
	providerGitLab: {
		threads:  gitlabLiveThreads,
		pr:       gitlabLivePR,
		changed:  gitlabLiveChanged,
		posted:   gitlabPostedBody,
		match:    gitlabThreadMatches,
		resolved: gitlabThreadResolved,
	},
}

// liveHostFor resolves the join surface for one provider: false names a
// host verdict cannot settle yet.
func liveHostFor(prov string) (liveHost, bool) {
	host, ok := liveHosts[prov]
	return host, ok
}

// liveHost gates the join on the bundle kind: local bundles have no live
// threads, and a host outside the table refuses typed naming the host.
func (st *state) liveHost(dir string) (liveHost, *core.RefusalError) {
	if isLocalDir(dir) {
		return liveHost{}, st.withWarnings(&core.RefusalError{
			Code: codeNoThreads,
			Message: "calibrate joins live threads — nothing to settle on a local bundle " +
				"(adopt + post first, then calibrate the PR)",
		})
	}
	prov, present := readcommon.OpenBundle(dir).ProviderMarker()
	if !present {
		prov = providerGitHub
	}
	if host, ok := liveHostFor(prov); ok {
		return host, nil
	}
	return liveHost{}, st.withWarnings(&core.RefusalError{Code: codeProviderUnimplemented, Message: fmt.Sprintf(
		"calibrate on %s is not implemented yet (no live thread join for it)", prov)})
}

func githubLiveThreads(ctx context.Context, st *state) ([]any, *core.RefusalError) {
	owner, name := readcommon.SplitOwner(st.req.Repo)
	nodes, _, err := readcommon.Threads(ctx, st.commander(), owner, name, st.req.Pr, true)
	if err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: msgThreadsFailed})
	}
	return nodes, nil
}

func githubLivePR(ctx context.Context, st *state) (map[string]any, *core.RefusalError) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(),
		"pr", "view", st.req.Pr, "--repo", st.req.Repo, "--json", "headRefOid,state,mergedAt")
	if !ok {
		return nil, st.withWarnings(&core.RefusalError{Code: codePRReadFailed, Message: "could not read live PR state"})
	}
	if m, ok := decodeLivePR(stdout); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

func githubLiveChanged(ctx context.Context, st *state, head0, liveHead string) []any {
	if head0 == liveHead {
		return []any{}
	}
	stdout, _, ok := coreexec.Gh(ctx, st.commander(),
		"api", "repos/"+st.req.Repo+"/compare/"+head0+"..."+liveHead, "--jq", "[.files[].filename]")
	if !ok {
		st.warn("compare unavailable (rewritten history?) — changed-file evidence off, resolved reads as resolved-unfixed")
		return []any{}
	}
	doc, derr := jq.Decode([]byte(stdout))
	if derr != nil {
		st.warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		st.warn("compare unavailable (rewritten history?) — changed-file evidence off, resolved reads as resolved-unfixed")
		return []any{}
	}
	list, ok := doc.([]any)
	if !ok {
		st.warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		return []any{}
	}
	return list
}
