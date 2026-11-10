package fetchcore

import (
	"context"
	"io"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// LiveThreadRead is one live-thread read for a gate's live dimension: the
// host's own stderr when the read failed (the caller stages it beside the
// bundle), the open and malformed tallies, and whether every page was read.
type LiveThreadRead struct {
	Stderr    string
	Open      int
	Malformed int
	Truncated bool
}

// ReadLiveThreads reads the provider's live review-thread surface for a gate:
// the open tally the gate compares against the local store, and whether the
// read covered every page. warn receives the adapter's own diagnostics (a host
// that caps a page walk, an unreadable inline list); a nil warn drops them.
//
// GitHub keeps the resolution-only GraphQL read the ready and report probes
// already publish: one paginated call whose stderr the gate goldens pin, and
// whose malformed nodes count as unresolved. Every other host reuses the
// threads refresh's adapter, so the gate reads the same surface `threads`
// writes instead of skipping the dimension.
func ReadLiveThreads(
	ctx context.Context, req core.ReadRequest, provider, head string, warn func(string),
) (LiveThreadRead, error) {
	switch threadsProvider(provider) {
	case providerGitHub:
		return githubLiveThreads(ctx, req)
	case "":
		// threadsProvider answers "" only for a host the refresh cannot read,
		// so the probe fails closed exactly as an unreachable read does.
		return LiveThreadRead{}, internalErrorf("unknown provider %q", provider)
	default:
		return adapterLiveThreads(ctx, req, provider, head, warn)
	}
}

// githubLiveThreads reads the paginated review threads through gh, the same
// argv the report probe has always issued.
func githubLiveThreads(ctx context.Context, req core.ReadRequest) (LiveThreadRead, error) {
	var cmdr core.Commander
	if req.Runtime != nil {
		cmdr = req.Commander
	}
	owner, name := readcommon.SplitOwner(req.Repo)
	nodes, res, err := readcommon.Threads(ctx, cmdr, owner, name, req.Pr, false)
	read := LiveThreadRead{Stderr: res.Stderr, Malformed: readcommon.MalformedThreadCount(nodes)}
	if err != nil {
		return read, err //nolint:wrapcheck // the probe maps this to its warn-or-fail-closed posture
	}
	read.Open = readcommon.OpenThreadCount(nodes)
	return read, nil
}

// adapterLiveThreads reads one host's thread surface through the threads
// refresh's per-host adapter and tallies the feed. The commander is wrapped so
// the host's own stderr — the diagnosis the GitHub read returns directly —
// reaches the caller for every provider.
func adapterLiveThreads(
	ctx context.Context, req core.ReadRequest, provider, head string, warn func(string),
) (LiveThreadRead, error) {
	var adapterWarnings []string
	s, ref := threadSession(req, nil, &adapterWarnings)
	if ref != nil {
		return LiveThreadRead{}, ref
	}
	recorder := &stderrRecorder{Commander: s.commander}
	s.commander = recorder
	feed, truncated, err := readThreadsSource(ctx, s, &ThreadsRequest{ReadRequest: req}, provider, head)
	drainWarnings(adapterWarnings, warn)
	if err != nil {
		return LiveThreadRead{Truncated: true, Stderr: recorder.stderr}, err
	}
	return LiveThreadRead{Truncated: truncated, Open: openThreads(feed)}, nil
}

// drainWarnings forwards the adapter's diagnostics to the caller's sink.
func drainWarnings(warnings []string, warn func(string)) {
	if warn == nil {
		return
	}
	for _, w := range warnings {
		warn(w)
	}
}

// openThreads counts unresolved threads. Only an exactly-resolved thread counts
// resolved, the fail-closed posture the feed's adapters already apply.
func openThreads(feed []threadscore.Thread) int {
	open := 0
	for i := range feed {
		if !feed[i].Resolved {
			open++
		}
	}
	return open
}

// stderrRecorder keeps the last non-empty stderr a read produced.
type stderrRecorder struct {
	Commander
	stderr string
}

func (r *stderrRecorder) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	res, err := r.Commander.Run(ctx, cmd)
	if res.Stderr != "" {
		r.stderr = res.Stderr
	}
	return res, err //nolint:wrapcheck // the seam error passes through by design; callers map it per site
}

// threadSession builds the read session the threads verb and the live probe
// share: the injected commander, clock, and environment, with no human stream
// and the caller's warning sink.
func threadSession(
	req core.ReadRequest, progress core.Progress, warnings *[]string,
) (*session, *core.RefusalError) {
	cmdr := fetchCommander(req)
	if cmdr == nil {
		return nil, &core.RefusalError{
			Code:    codeThreadsFailed,
			Message: "threads failed: no commander in runtime",
		}
	}
	env := map[string]string{}
	if req.Runtime != nil && req.Env != nil {
		env = req.Env
	}
	return &session{
		Human:     io.Discard,
		Diag:      io.Discard,
		progress:  progress,
		commander: cmdr,
		Now:       req.Now,
		env:       env,
		warnings:  warnings,
	}, nil
}

// fetchCommander adapts the request's commander to the fetch session, which
// pre-checks the tool on PATH. A commander without that probe (the SDK's
// default runner) reports every tool as present, so a genuinely missing binary
// surfaces as the run error and a live read fails the same way it does
// otherwise.
func fetchCommander(req core.ReadRequest) Commander {
	if req.Runtime == nil || req.Commander == nil {
		return nil
	}
	if cmdr, ok := req.Commander.(Commander); ok {
		return cmdr
	}
	return runOnly{req.Commander}
}

// runOnly is a core commander with no PATH probe.
type runOnly struct{ core.Commander }

func (runOnly) LookPath(string) error { return nil }
