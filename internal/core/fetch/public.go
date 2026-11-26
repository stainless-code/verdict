package fetchcore

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// fetchGitHubPublic reads one public GitHub pull request over the anonymous
// REST API: curl with no credential, no `gh`, and no checkout. It writes the
// same artifacts every other provider backend writes (PR.json, full.diff,
// filesource.json, reviews.json, inline.json, threads.json), so every later
// phase — the shared provider tail, record, post — reads an ordinary GitHub
// bundle. Thread resolution is the one GitHub read that needs a token, so the
// feed is recorded empty and truncated with one warning.
func fetchGitHubPublic(ctx context.Context, ctx2 *fetchRunCtx, req *Request) error {
	r, dir, repo, pr := ctx2.runner, ctx2.dir, req.Repo, req.Pr
	if err := r.needTool(binCurl); err != nil {
		return err
	}
	pull := publicPullPath(repo, pr)
	meta, err := publicMeta(ctx, r, dir, repo, pr, pull)
	if err != nil {
		return err
	}
	if err := publicDiff(ctx, r, dir, pull); err != nil {
		return err
	}
	files, truncated, ref := publicFiles(ctx, r, dir, pull)
	if ref != nil {
		return ref
	}
	if ref := publicLists(ctx, r, dir, pull); ref != nil {
		return ref
	}
	publicThreads(r, dir, pr, publicHeadSHA(meta))
	doc, normErr := normalizeGitHubPublic(meta, files, truncated)
	return writePRJSON(dir, "GitHub PR", pr, doc, normErr)
}

// publicPullPath is the pull-request resource under the anonymous API root.
func publicPullPath(repo, pr string) string {
	return publicAPIHost + apiReposPrefix + repo + "/pulls/" + pr
}

// publicMeta owns the PR read and its stderr sidecar.
func publicMeta(ctx context.Context, r *session, dir, repo, pr, pull string) (string, error) {
	fmt.Fprint(r.Human, core.Msgf("» public API: %s#%s (no gh login)\n", repo, pr))
	res, err := r.curlPublic(ctx, flagCurlPublicFail, pull)
	if werr := writeErrFile(dir+suffixMetaErr, res.Stderr); werr != nil {
		return "", werr
	}
	if err != nil {
		if ref := canceledRead(ctx); ref != nil {
			return "", ref
		}
		return "", publicRefusal(publicFailureText(res), "GitHub PR")
	}
	return res.Stdout, nil
}

// publicDiff reads the diff media type into full.diff: the same artifact the
// CLI path writes, so the cap gate, hunk index, and chunk plan are shared.
func publicDiff(ctx context.Context, r *session, dir, pull string) error {
	fmt.Fprint(r.Human, core.Msgf("» public API: diff -> %s/full.diff\n", r.display(dir)))
	res, err := r.curlPublic(ctx, flagCurlPublicFail, flagH, publicDiffAccept, pull)
	if werr := writeErrFile(dir+suffixDiffErr, res.Stderr); werr != nil {
		return werr
	}
	if err != nil {
		return publicDiffFailure(ctx, dir, res)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(res.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: core.Msgf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// publicDiffFailure classifies one failed anonymous diff read: a stopped
// context first, then an empty body the cap gate refuses, then the host's own
// diff refusal, and finally the unfiltered public refusal.
func publicDiffFailure(ctx context.Context, dir string, res core.Result) *core.RefusalError {
	if ref := canceledRead(ctx); ref != nil {
		return ref
	}
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	if ref := upstreamDiffRefusal(res.Stderr); ref != nil {
		return ref
	}
	return publicRefusal(publicFailureText(res), "GitHub diff")
}

// publicFailureText is what a public refusal quotes and classifies: curl's own
// status line on stderr plus any error document the host answered on stdout. A
// silent `-s -f` curl leaves only the status line, while a host (or a stub in a
// test) that reports the cause in the body still gets read.
func publicFailureText(res core.Result) string {
	stderr := strings.TrimSpace(res.Stderr)
	stdout := strings.TrimSpace(res.Stdout)
	switch {
	case stderr == "":
		return stdout
	case stdout == "":
		return stderr
	default:
		return stderr + "\n" + stdout
	}
}

// canceledRead maps a stopped context to the shipped cancel refusal. Without
// it a cancel would take the failure path and blame the anonymous hourly cap,
// which is a wrong remedy for a read the operator stopped.
func canceledRead(ctx context.Context) *core.RefusalError {
	if ctx.Err() == nil {
		return nil
	}
	return &core.RefusalError{Code: codeCanceled, Message: msgCanceledFetch}
}

// publicRefusal maps one anonymous read failure. The hourly-cap advice appears
// only when the host's own stderr says that is what happened (403/429 or a rate
// limit note): a 404 is a missing or private pull request, and telling that
// operator to wait for quota would send them after the wrong cause.
func publicRefusal(stderr, what string) *core.RefusalError {
	head := headBytes(stderr, numExcerpt)
	switch {
	case rateLimited(stderr):
		return &core.RefusalError{Code: CodeFetchFailed, Message: core.Msgf(
			"%s read failed over the public API: %s (anonymous reads are capped at %d requests/hour — "+
				"run 'gh auth login' or wait, then re-run)",
			what, head, publicRateLimit)}
	case notFound(stderr):
		return &core.RefusalError{Code: CodeFetchFailed, Message: core.Msgf(
			"%s read failed over the public API: %s (the pull request is missing or private — "+
				"check the URL, or run 'gh auth login' for a private one)", what, head)}
	default:
		return &core.RefusalError{Code: CodeFetchFailed, Message: core.Msgf(
			"%s read failed over the public API: %s", what, head)}
	}
}

// rateLimited reports the one failure the anonymous cap advice belongs to: a
// 403 or 429, or the host's own rate-limit note.
func rateLimited(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, " 403") || strings.Contains(lower, "(403)") ||
		strings.Contains(lower, " 429") || strings.Contains(lower, "(429)") ||
		strings.Contains(lower, "rate limit")
}

// notFound reports a missing or private pull request.
func notFound(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, " 404") || strings.Contains(lower, "(404)")
}

// publicFiles pages the file listing to a short page or the provider ceiling.
// A failed page keeps what was read and warns: the diff still carries the
// review, and a partial checklist is reported rather than hidden.
func publicFiles(ctx context.Context, r *session, dir, pull string) ([]any, bool, *core.RefusalError) {
	files, truncated, ref := collectFilePages(ctx, r, pull)
	if ref != nil {
		return nil, false, ref
	}
	rows := fileRows(files, func(m map[string]any) *jq.Obj { return entryFileRow(m, keyFilename) })
	writePublicFilesource(r, dir, rows, truncated)
	return rows, truncated, nil
}

// collectFilePages walks the listing to a short page or the provider ceiling,
// keeping what was read when a page could not be read in full.
func collectFilePages(ctx context.Context, r *session, pull string) ([]any, bool, *core.RefusalError) {
	var files []any
	truncated := false
	for page := 1; page <= providerMaxPages; page++ {
		entries, done, incomplete, ref := publicFilePage(ctx, r, pull, page)
		if ref != nil {
			return nil, false, ref
		}
		files = append(files, entries...)
		truncated = truncated || incomplete
		if pageDone(done, len(files)) {
			return files, truncated || !done, nil
		}
	}
	// The walk ran out of pages without a short page: the listing is longer
	// than the provider lets an anonymous reader page, so it is truncated even
	// though every page read cleanly.
	return files, true, nil
}

// pageDone reports whether the listing walk stops after this page: a short page
// finished it, and the provider ceiling caps a long one.
func pageDone(done bool, read int) bool {
	return done || read >= providerFileCeiling
}

// publicFilePage reads one page of the file listing: done reports that the
// walk is complete (a short page), incomplete that this page could not be read
// in full.
func publicFilePage(
	ctx context.Context, r *session, pull string, page int,
) (entries []any, done, incomplete bool, ref *core.RefusalError) {
	res, err := r.curlPublic(ctx, flagCurlPublicFail, publicPageURL(pull+publicFilesPath, page))
	if err != nil {
		if ref := canceledRead(ctx); ref != nil {
			return nil, true, false, ref
		}
		r.Warn(fmt.Sprintf("public API: file list page %d failed (%s) — the checklist may be incomplete",
			page, headBytes(res.Stderr, numExcerptShort)))
		return nil, true, true, nil
	}
	entries, derr := jsonArray(res.Stdout)
	if derr != nil {
		r.Warn(fmt.Sprintf("public API: file list page %d is unreadable (%s) — the checklist may be incomplete",
			page, headBytes(derr.Error(), numExcerptShort)))
		return nil, true, true, nil
	}
	return entries, len(entries) < publicPerPage, false, nil
}

// writePublicFilesource records how the file list was read, so the coverage
// receipt names the anonymous API the way the other providers' receipts name
// their own listing.
func writePublicFilesource(r *session, dir string, files []any, truncated bool) {
	note := jq.NewObj(
		keySource, publicFilesSource,
		"per_page", publicPerPage,
		"provider_ceiling", providerFileCeiling,
		"count", len(files),
	)
	core.ScanCut(truncated, len(files)).Apply(note)
	if err := atomic.WriteFile(dir+publicFilesourceFile, []byte(jq.PrettyOrdered(note))); err != nil {
		r.Warn(fmt.Sprintf("public API: could not write %s%s: %v", r.display(dir), publicFilesourceFile, err))
	}
}

// publicLists reads the review and inline-comment lists. Both are raw
// anonymous bodies: a failed read keeps an empty list and warns, exactly like
// the CLI path's warn-or-refuse posture for these two sidecars.
func publicLists(ctx context.Context, r *session, dir, pull string) *core.RefusalError {
	for _, list := range []struct {
		path string
		url  string
	}{
		{suffixReviewsJSON, pull + publicReviewsPath},
		{suffixInlineJSON, pull + publicCommentsPath},
	} {
		body, ref := publicListBody(ctx, r, list.url)
		if ref != nil {
			return ref
		}
		if err := atomic.WriteFile(dir+list.path, []byte(body)); err != nil {
			r.Warn(fmt.Sprintf("public API: could not write %s%s: %v", r.display(dir), list.path, err))
		}
	}
	return nil
}

// publicListBody reads one list, answering the JSON array of what was read (an
// empty array when no page could be read).
func publicListBody(ctx context.Context, r *session, resource string) (string, *core.RefusalError) {
	var items []any
	for page := 1; page <= providerMaxPages; page++ {
		entries, done, ref := publicListPage(ctx, r, resource, page)
		if ref != nil {
			return "", ref
		}
		items = append(items, entries...)
		if done {
			return prettyList(items), nil
		}
	}
	// Every page read and none was short: the list is longer than an anonymous
	// reader may page through, and a silently short list would report fewer
	// threads than the pull request has.
	r.Warn(fmt.Sprintf("public API: %s is longer than %d pages — the list is incomplete",
		resource, providerMaxPages))
	return prettyList(items), nil
}

// prettyList renders the collected list, always as a JSON array.
func prettyList(items []any) string {
	if items == nil {
		items = []any{}
	}
	return jq.Pretty(items)
}

// publicListPage reads one page of a list: done reports that the walk is
// complete, whether because the page was short or because it could not be read
// (the warning already named the failure).
func publicListPage(
	ctx context.Context, r *session, resource string, page int,
) (entries []any, done bool, ref *core.RefusalError) {
	res, err := r.curlPublic(ctx, flagCurlPublicFail, publicPageURL(resource, page))
	if err != nil {
		if ref := canceledRead(ctx); ref != nil {
			return nil, true, ref
		}
		r.Warn(fmt.Sprintf("public API: %s page %d failed (%s) — the list is incomplete",
			resource, page, headBytes(res.Stderr, numExcerptShort)))
		return nil, true, nil
	}
	entries, derr := jsonArray(res.Stdout)
	if derr != nil {
		r.Warn(fmt.Sprintf("public API: %s page %d is unreadable (%s) — the list is incomplete",
			resource, page, headBytes(derr.Error(), numExcerptShort)))
		return nil, true, nil
	}
	return entries, len(entries) < publicPerPage, nil
}

// publicThreads records the empty, truncated thread feed: REST carries no
// resolution and GraphQL needs a token, so the bundle says so instead of
// implying the PR had no conversation.
func publicThreads(r *session, dir, pr, head string) {
	r.Warn(msgPublicThreads)
	if ref := writeThreadsJSON(dir, providerGitHub, pr, head, r.nowUTC(), true, nil); ref != nil {
		r.Warn("public API: could not record threads.json: " + ref.Message)
	}
}

// publicPageURL is one list page's URL: the per_page/page pair every GitHub
// list endpoint takes.
func publicPageURL(resource string, page int) string {
	return resource + "?per_page=" + strconv.Itoa(publicPerPage) + "&page=" + strconv.Itoa(page)
}

// normalizeGitHubPublic maps the anonymous REST PR document onto the one
// PR.json record every provider shares, so a public bundle is an ordinary
// GitHub bundle downstream.
func normalizeGitHubPublic(metaRaw string, files []any, truncated bool) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	if err := publicMetaShape(meta); err != nil {
		return nil, err
	}
	base, head := nestedMap(meta, publicKeyBase), nestedMap(meta, publicKeyHead)
	doc := buildPR(&prSpec{
		Provider: providerGitHub,
		Number:   firstPresent(meta[keyNumber], meta["id"]),
		Title:    normStr(meta, keyTitle),
		Body:     normStr(meta, keyBody),
		BaseRef:  normStr(base, "ref"),
		HeadRef:  normStr(head, "ref"),
		BaseSHA:  normStr(base, publicKeySHA),
		HeadSHA:  normStr(head, publicKeySHA),
		Author:   normStr(nestedMap(meta, "user"), keyLogin),
		Labels:   prLabels(meta[keyLabels]),
		URL:      normStr(meta, "html_url"),
		State:    publicState(meta),
		Draft:    meta[keyDraft],
		Files:    files,
	})
	core.ScanCut(truncated, len(files)).Apply(doc)
	return doc, nil
}

// The anonymous document's nested field names, used once each here so a
// misspelling cannot drift between the shape check and the normalizer.
const (
	publicKeyBase = "base"
	publicKeyHead = "head"
	publicKeySHA  = "sha"
)

// publicMetaShape rejects a document that is not a pull request: the anonymous
// read is the one path with no host client validating the answer, so an empty
// body, a rate-limit document, or a truncated page must refuse here instead of
// normalizing into a bundle with no number and no commit pair.
func publicMetaShape(meta map[string]any) error {
	if n, ok := jq.IntOf(meta[keyNumber]); !ok || n <= 0 {
		return internalErrorf("the public API answered no pull-request number")
	}
	for _, side := range []string{publicKeyBase, publicKeyHead} {
		if normStr(nestedMap(meta, side), publicKeySHA) == "" {
			return internalErrorf("the public API answered no %s commit", side)
		}
	}
	return nil
}

// publicState folds a public PR document to its state. The list endpoint says
// "closed" for a merged pull request and carries the fact in `merged`, so the
// flag decides first: reading `state` alone would call every merged PR closed.
func publicState(meta map[string]any) string {
	if merged, ok := meta["merged"].(bool); ok && merged {
		return stateMerged
	}
	return providerState(providerGitHub, normStr(meta, keyState))
}

// publicHeadSHA reads the head commit out of an anonymous PR document, for the
// thread feed's head pin.
func publicHeadSHA(metaRaw string) string {
	doc, err := jq.Decode([]byte(metaRaw))
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return normStr(nestedMap(m, publicKeyHead), publicKeySHA)
}
