package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// ThreadsRequest carries one threads refresh invocation: the shared read
// runtime plus the provider override. The verb re-reads only the provider's
// thread surface, so it never touches the diff or the rest of the bundle.
//
//nolint:govet // field order documents the request grammar
type ThreadsRequest struct {
	Threads  []string `json:"thread,omitempty"`
	Warnings []string
	Provider string `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure"`
	core.ReadRequest
	Resolve bool `json:"resolve,omitempty"`
	DryRun  bool `json:"dryRun,omitempty"`
}

// ThreadsResponse carries the refresh outcome: envelope identity, the typed
// data profile, and accumulated warnings. Human lines stream through the
// runtime progress observer.
type ThreadsResponse struct {
	Data     *ThreadsData
	Resolve  *ThreadsResolveData
	Repo     string
	Pr       string
	At       string
	Next     string
	Warnings []string
}

// ThreadsData is the threads verb's --json data profile: what was refreshed
// and a machine-readable tally over the feed.
type ThreadsData struct {
	Dir       string `json:"dir"`
	File      string `json:"file"`
	Provider  string `json:"provider"`
	HeadSHA   string `json:"head_sha"`
	FetchedAt string `json:"fetched_at"`
	Truncated bool   `json:"truncated"`
	Threads   int    `json:"threads"`
	Open      int    `json:"open"`
	Outdated  int    `json:"outdated"`
	Verdict   int    `json:"verdict"`
}

// Wire renders the profile in published wire order.
func (d *ThreadsData) Wire() *jq.Obj {
	return jq.NewObj(
		"dir", d.Dir,
		"file", d.File,
		"provider", d.Provider,
		"head_sha", d.HeadSHA,
		"fetched_at", d.FetchedAt,
		"truncated", d.Truncated,
		"threads", d.Threads,
		"open", d.Open,
		"outdated", d.Outdated,
		"verdict", d.Verdict,
	)
}

type threadsRun struct {
	req      *ThreadsRequest
	warnings []string
}

// openBundle resolves the request's bundle under the shared name guard and
// proves the fetch that created it left a PR record beside it.
func (st *threadsRun) openBundle() (string, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if ref != nil {
		return "", st.withWarnings(ref)
	}
	if !fileExists(dir + suffixPRJSON) {
		return "", st.withWarnings(&core.RefusalError{
			Code:    CodeFetchFailed,
			Message: fmt.Sprintf("missing %s/PR.json (fetch the PR first)", dir),
		})
	}
	return dir, nil
}

// RunThreads executes `threads <owner/repo> <pr> [--provider <name>]`:
// re-read the provider's review conversations and rewrite only
// .verdict/state/<pr>/threads.json. Human progress streams through the runtime
// observer; the adapter renders the envelope.
func RunThreads(ctx context.Context, req *ThreadsRequest) (*ThreadsResponse, *core.RefusalError) {
	st := &threadsRun{req: req, warnings: append([]string{}, req.Warnings...)}
	if ctx.Err() != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: "canceled before threads completed"})
	}
	if req.Resolve {
		return st.resolve(ctx)
	}
	dir, ref := st.openBundle()
	if ref != nil {
		return nil, ref
	}
	provider, ref := st.selectProvider(ctx, dir)
	if ref != nil {
		return nil, ref
	}
	s, ref := st.session()
	if ref != nil {
		return nil, ref
	}
	head := readcommon.PrField(dir, keyHeadSHA)
	feed, truncated, ref := st.read(ctx, s, dir, provider, head)
	if ref != nil {
		return nil, ref
	}
	data, ref := st.write(dir, provider, head, truncated, feed)
	if ref != nil {
		return nil, ref
	}
	next := threadsNext(data.Open)
	st.human("next: " + next)
	return &ThreadsResponse{
		Repo: req.Repo, Pr: req.Pr, At: data.FetchedAt, Next: next,
		Warnings: st.warnings, Data: data,
	}, nil
}

// read re-reads the provider feed. An empty failed read refuses instead of
// erasing a good prior feed; a partial read warns and keeps what it got.
func (st *threadsRun) read(
	ctx context.Context, s *session, dir, provider, head string,
) ([]threadscore.Thread, bool, *core.RefusalError) {
	feed, truncated, err := readThreadsSource(ctx, s, st.req, provider, head)
	if err != nil {
		if ref, ok := asRefusal(err); ok {
			return nil, true, st.withWarnings(ref)
		}
		return nil, true, st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: fmt.Sprintf("could not read live threads for %s#%s: %v", st.req.Repo, st.req.Pr, err),
		})
	}
	if len(feed) == 0 && (truncated || threadscore.CountFile(dir+suffixThreadsJSON) > 0) {
		return nil, true, st.withWarnings(&core.RefusalError{
			Code: codeThreadsFailed,
			Message: fmt.Sprintf("could not read live threads for %s#%s; existing threads.json kept (never silently emptied)",
				st.req.Repo, st.req.Pr),
		})
	}
	if truncated {
		st.warn(provider + " thread read was incomplete — threads.json may under-cover prior feedback")
	}
	return feed, truncated, nil
}

// write rewrites the feed and builds the data profile.
func (st *threadsRun) write(
	dir, provider, head string, truncated bool, feed []threadscore.Thread,
) (*ThreadsData, *core.RefusalError) {
	at := st.req.TimeUTC()
	if ref := writeThreadsJSON(dir, provider, st.req.Pr, head, at, truncated, feed); ref != nil {
		return nil, st.withWarnings(ref)
	}
	data := summarizeThreads(dir, provider, head, at, truncated, feed)
	st.human(fmt.Sprintf("» %s threads -> %s/threads.json", provider, dir))
	st.human(fmt.Sprintf("done: %d threads (%d open, %d outdated) -> %s/threads.json",
		data.Threads, data.Open, data.Outdated, dir))
	return data, nil
}

func summarizeThreads(
	dir, provider, head, at string, truncated bool, feed []threadscore.Thread,
) *ThreadsData {
	data := &ThreadsData{
		Dir: dir, File: dir + suffixThreadsJSON, Provider: provider,
		HeadSHA: head, FetchedAt: at, Truncated: truncated, Threads: len(feed),
	}
	for i := range feed {
		if !feed[i].Resolved {
			data.Open++
		}
		if feed[i].Outdated {
			data.Outdated++
		}
		if feed[i].AuthorKind == threadscore.AuthorVerdict {
			data.Verdict++
		}
	}
	return data
}

func threadsNext(open int) string {
	if open == 0 {
		return "no open threads — merge readiness no longer waits on review feedback"
	}
	return "address open threads with prompts/address-reviews.md"
}

// threadsProvider validates the provider override, defaulting to github.
func threadsProvider(provider string) string {
	if provider == "" {
		return providerGitHub
	}
	switch provider {
	case providerGitHub, providerGitLab, providerGitCafe, providerForgejo, providerBitbucket, providerAzure:
		return provider
	default:
		return ""
	}
}

// readThreadsSource re-reads one provider's thread surface and normalizes it.
// truncated reports an incomplete read (a later page failed or a host cap was
// hit); err reports a total failure the caller must not paper over with an
// empty feed.
func readThreadsSource(
	ctx context.Context, s *session, req *ThreadsRequest, provider, head string,
) ([]threadscore.Thread, bool, error) {
	switch provider {
	case providerGitHub:
		return githubThreadFeed(ctx, s, req.Repo, req.Pr)
	case providerGitLab:
		pid, err := gitlabProjectID(ctx, s, req.Repo)
		if err != nil {
			return nil, true, err
		}
		return gitlabDiscussionFeed(ctx, s, pid, req.Pr, head)
	case providerGitCafe:
		return cafeThreadFeed(ctx, s, req.Repo, req.Pr)
	case providerForgejo:
		return forgejoThreadFeed(ctx, s, req.Repo, req.Pr, head)
	case providerBitbucket:
		return bitbucketThreadFeed(ctx, s, req.Repo, req.Pr)
	case providerAzure:
		return azureThreadFeed(ctx, s, req.Repo, req.Pr)
	default:
		return nil, true, internalErrorf("unknown provider %q", provider)
	}
}

// cafeThreadFeed re-reads GitCafe's reviews and comments pages. A read that
// yields no thread at all refuses with both causes; a partial read keeps what
// arrived and reports itself truncated, so the feed never looks complete.
func cafeThreadFeed(ctx context.Context, s *session, repo, pr string) ([]threadscore.Thread, bool, error) {
	revs, revTruncated, revErr := fetchCafePages(cafeReviewsPage(ctx, s, repo, pr))
	cmts, cmtTruncated, cmtErr := fetchCafePages(cafeCommentsPage(ctx, s, repo, pr))
	feed := threadscore.FromGitCafe(decodeThreadPayload(revs), decodeThreadPayload(cmts))
	if len(feed) == 0 && (revErr != nil || cmtErr != nil) {
		return nil, true, internalErrorf("gitcafe thread read failed: %v", errors.Join(revErr, cmtErr))
	}
	return feed, revTruncated || cmtTruncated, nil
}

// forgejoThreadFeed re-reads Forgejo reviews and their inline comments.
func forgejoThreadFeed(
	ctx context.Context, s *session, repo, pr, head string,
) ([]threadscore.Thread, bool, error) {
	auth, api, ref := forgejoAuthFor(s.env, repo, pr)
	if ref != nil {
		return nil, true, ref
	}
	revs, revTrunc, revErr := forgejoListRead(ctx, s, auth, api+"/reviews")
	inline, complete := forgejoInline(ctx, s, api, auth, revs)
	feed := threadscore.FromForgejo(decodeThreadPayload(revs), decodeThreadPayload(inline), head)
	return feed, revTrunc || !complete, revErr
}

// bitbucketThreadFeed re-reads the comment pages.
func bitbucketThreadFeed(ctx context.Context, s *session, repo, pr string) ([]threadscore.Thread, bool, error) {
	auth, ref := bitbucketAuthFor(s.env)
	if ref != nil {
		return nil, true, ref
	}
	base := "https://api.bitbucket.org/2.0/repos/" + repo + "/pullrequests/" + pr
	comments, truncated, ref := bitbucketPages(ctx, s, auth, base+"/comments?pagelen=100")
	if ref != nil {
		return nil, true, ref
	}
	return threadscore.FromBitbucket(comments), truncated, nil
}

// azureThreadFeed re-reads the Azure thread list.
func azureThreadFeed(ctx context.Context, s *session, repo, pr string) ([]threadscore.Thread, bool, error) {
	conn, err := azureThreadsConn(s.env, repo)
	if err != nil {
		return nil, true, err
	}
	raw, truncated, err := azureThreadsRead(ctx, s, conn, pr)
	if err != nil {
		return nil, true, err
	}
	return threadscore.FromAzure(decodeThreadPayload(raw)), truncated, nil
}

// azureThreadsConn resolves the org/project/repo names the thread read needs.
// It deliberately skips the merge-ref/token gate: a threads refresh reads the
// REST thread list and needs neither the PR clone nor a git credential.
func azureThreadsConn(env map[string]string, repo string) (*azureConn, error) {
	org, _ := env["AZURE_DEVOPS_ORG"]
	if org == "" {
		return nil, &core.RefusalError{
			Code:    codeAuthMissing,
			Message: "azure needs AZURE_DEVOPS_ORG=https://dev.azure.com/{org} (and az login or AZURE_DEVOPS_EXT_PAT)",
		}
	}
	project, _, _ := strings.Cut(repo, pathSep)
	rname := repo
	if _, after, ok := strings.CutLast(repo, pathSep); ok {
		rname = after
	}
	return &azureConn{org: org, project: project, rname: rname}, nil
}

func azureThreadsRead(ctx context.Context, s *session, conn *azureConn, pr string) (string, bool, error) {
	args := azureAPI(conn.org, conn.project, conn.rname, pr, "/threads?api-version=7.1", "value", keyJSON)
	res, err := s.cliFetch(ctx, binAz, args...)
	if err != nil {
		return emptyJSONArray, true, err
	}
	return res.Stdout, false, nil
}

// session builds the refresh session: the injected commander, clock, env, and
// warning sink.
func (st *threadsRun) session() (*session, *core.RefusalError) {
	s, ref := threadSession(st.req.ReadRequest, st.progress(), &st.warnings)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	return s, nil
}

// progress returns the injected progress observer (nil when unset).
func (st *threadsRun) progress() core.Progress {
	if st.req.Runtime != nil {
		return st.req.Progress
	}
	return nil
}

// human streams one human output line through the progress observer.
func (st *threadsRun) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

// warn records and streams one warning.
func (st *threadsRun) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	if st.req.Runtime != nil {
		st.req.Warn(msg)
	}
}

// withWarnings attaches the accumulated warnings to a refusal.
func (st *threadsRun) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func asRefusal(err error) (*core.RefusalError, bool) {
	return errors.AsType[*core.RefusalError](err)
}

func (st *threadsRun) selectProvider(ctx context.Context, dir string) (string, *core.RefusalError) {
	selected := st.req.Provider
	if selected == "" {
		selected = bundleProvider(dir)
		if ref := st.checkOriginProvider(ctx, selected); ref != nil {
			return "", ref
		}
	}
	if selected == "" || threadsProvider(selected) == "" {
		return "", st.withWarnings(&core.RefusalError{Code: codeBadProvider, Message: fmt.Sprintf(
			"unknown --provider '%s' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)", selected)})
	}
	return selected, nil
}

func bundleProvider(dir string) string {
	if provider, present := readcommon.OpenBundle(dir).ProviderMarker(); present {
		return provider
	}
	return providerGitHub
}

func (st *threadsRun) checkOriginProvider(ctx context.Context, provider string) *core.RefusalError {
	inferred, source, ref := core.ResolveProvider(ctx, st.req.Commander, st.req.RepoRootPath(), "", st.req.Env)
	if ref != nil {
		if ref.Code == "provider-unknown" {
			return nil
		}
		return st.withWarnings(ref)
	}
	if source == "remote" && inferred != provider {
		return st.withWarnings(&core.RefusalError{Code: codeBadProvider, Message: fmt.Sprintf(
			"origin provider %q disagrees with bundle provider %q — "+
				"pass --provider %s to select the bundle explicitly", inferred, provider, provider)})
	}
	return nil
}
