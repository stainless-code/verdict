package readycore

import (
	"context"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// readyHost is one provider's live-read surface: the PR-state, CI-check,
// and thread reads the advisory joins. Provider selection stays one table
// lookup so a new host adds a row, never a host-name branch at a call
// site.
type readyHost struct {
	live    func(context.Context, *state, string) *core.RefusalError
	checks  func(context.Context, *state)
	threads func(context.Context, *state) *core.RefusalError
}

// Shared refusal codes and state words: one spelling across both hosts so
// the wire keeps a single vocabulary.
const (
	codeThreadsFailed = "threads-failed"
	stateOpen         = "OPEN"
)

var readyHosts = map[string]readyHost{
	providerGitHub: {
		live:    githubLive,
		checks:  githubChecks,
		threads: githubThreads,
	},
	providerGitLab: {
		live:    gitlabLive,
		checks:  gitlabChecks,
		threads: gitlabThreads,
	},
}

// readyHostFor resolves the live-read surface for one provider: false
// names a host verdict cannot advise on yet.
func readyHostFor(prov string) (readyHost, bool) {
	host, ok := readyHosts[prov]
	return host, ok
}

// readyHostNames lists the hosts the table serves, for the refusal that names
// what the verb does cover.
func readyHostNames() string {
	names := make([]string, 0, len(readyHosts))
	for name := range readyHosts {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// readyHost gates the advisory on the bundle kind: a host outside the
// table refuses typed naming the host.
func (st *state) readyHost(dir string) (readyHost, *core.RefusalError) {
	prov := readcommon.ProviderOf(dir)
	if prov == "" {
		prov = providerGitHub
	}
	if host, ok := readyHostFor(prov); ok {
		return host, nil
	}
	return readyHost{}, st.withWarnings(&core.RefusalError{
		Code: "provider-unimplemented",
		Message: "ready on " + prov + " is not implemented yet (no live reads for it — " +
			"supported hosts: " + readyHostNames() + ")",
	})
}
