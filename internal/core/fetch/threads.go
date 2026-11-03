package fetchcore

import (
	"context"
	"fmt"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// githubFirstThreadPage is the pageWalk sentinel for the cursor-less first
// GitHub page: pageWalk starts with a real cursor, and an empty after:
// argument would be an invalid GraphQL cursor.
const githubFirstThreadPage = "\x00first"

// writeThreadsJSON writes the normalized PR-conversation feed beside the raw
// provider files. threads.json is additive: inline.json/reviews.json keep
// their exact bytes, and the feed is the one shape every reader consumes.
func writeThreadsJSON(
	dir, provider, pr, head, fetchedAt string, truncated bool, feed []threadscore.Thread,
) *core.RefusalError {
	doc := threadscore.File{
		SchemaVersion: threadscore.SchemaVersion,
		Provider:      provider,
		PR:            threadscore.PRNumber(pr),
		HeadSHA:       head,
		FetchedAt:     fetchedAt,
		Truncated:     truncated,
		Threads:       feed,
	}
	if err := threadscore.WriteFile(dir+suffixThreadsJSON, &doc); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: err.Error()}
	}
	return nil
}

// decodeThreadPayload decodes one raw provider payload as a list. An
// undecodable payload reads empty: the raw file is still kept, and the failed
// read already warned at its call site.
func decodeThreadPayload(raw string) []any {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	return jq.Elements(doc)
}

// githubThreadFeed reads the authoritative GitHub review threads through
// GraphQL (REST carries no resolution) with the shared page walker. A failed
// later page keeps the threads already read and marks the feed truncated; a
// failed first page returns no threads, and the caller decides warn-vs-refuse.
func githubThreadFeed(
	ctx context.Context, r *session, repo, pr string,
) ([]threadscore.Thread, bool, error) {
	owner, name := readcommon.SplitOwner(repo)
	nodes, truncated, _, err := pageWalk(ctx, githubFirstThreadPage, providerMaxPages,
		func(ctx context.Context, cursor string) ([]any, string, error) {
			return githubThreadPage(ctx, r, owner, name, pr, cursor)
		})
	if err != nil {
		return nil, true, jq.Sealed("github thread pages", err)
	}
	feed, capped := threadscore.FromGitHubThreads(nodes)
	return feed, truncated || capped, nil
}

// githubThreadPage reads one reviewThreads page and reports its next cursor.
func githubThreadPage(
	ctx context.Context, r *session, owner, name, pr, cursor string,
) ([]any, string, error) {
	if cursor == githubFirstThreadPage {
		cursor = ""
	}
	res, err := r.ghFetch(ctx, readcommon.ReviewThreadsArgv(owner, name, pr, cursor)...)
	if err != nil {
		return nil, "", err
	}
	page, next, more, err := readcommon.ThreadsPage(res.Stdout)
	if err != nil {
		return nil, "", jq.Sealed("github thread page", err)
	}
	if !more {
		next = ""
	}
	return page, next, nil
}

// gitlabDiscussionFeed reads MR discussions (the native resolved/resolvable
// container) with the shared page walker. Notes carry the thread split for
// inline.json; discussions carry the thread state for threads.json.
func gitlabDiscussionFeed(
	ctx context.Context, r *session, pid, pr, head string,
) ([]threadscore.Thread, bool, error) {
	items, truncated, _, err := pageWalk(ctx, "1", providerMaxPages,
		func(ctx context.Context, cursor string) ([]any, string, error) {
			return gitlabDiscussionPage(ctx, r, pid, pr, cursor)
		})
	if err != nil {
		return nil, true, jq.Sealed("gitlab discussion pages", err)
	}
	return threadscore.FromGitLab(items, head), truncated, nil
}

// gitlabDiscussionPage reads one discussions page and reports its next page.
func gitlabDiscussionPage(
	ctx context.Context, r *session, pid, pr, cursor string,
) ([]any, string, error) {
	page, ok := cursorPage(cursor)
	if !ok {
		return nil, "", internalErrorf("bad page cursor %q", cursor)
	}
	path := fmt.Sprintf("projects/%s/merge_requests/%s/discussions?per_page=%d&page=%d",
		pid, pr, gitlabNotePageSize, page)
	res, err := r.cliFetch(ctx, binGlab, ghCmdAPI, path)
	if err != nil {
		return nil, "", err
	}
	list, err := jsonArray(res.Stdout)
	if err != nil {
		return nil, "", internalErrorf("discussion page unreadable: %v", err)
	}
	if len(list) < gitlabNotePageSize {
		return list, "", nil
	}
	return list, strconv.Itoa(page + 1), nil
}

// gitlabHeadSHA reads the MR head from the metadata payload.
func gitlabHeadSHA(meta string) string {
	m := decodedMap(meta)
	if sha, ok := m["sha"].(string); ok && sha != "" {
		return sha
	}
	return jq.StrOf(nestedMap(m, "diff_refs")["head_sha"])
}

// forgejoHeadSHA reads the PR head from the Forgejo metadata payload.
func forgejoHeadSHA(meta string) string {
	return jq.StrOf(nestedMap(decodedMap(meta), "head")["sha"])
}

// bitbucketHead reads the PR head from the Bitbucket metadata payload.
func bitbucketHead(meta string) string {
	return jq.StrOf(nestedMap(nestedMap(decodedMap(meta), "source"), "commit")["hash"])
}

// cafeHeadSHA reads the PR head from the GitCafe metadata payload.
func cafeHeadSHA(meta string) string {
	res := cafeResource(decodedMap(meta))
	return jq.StrOf(firstPresent(res["sourceOid"], res["headOid"]))
}

// azureHeadSHA reads the PR head from the Azure metadata payload.
func azureHeadSHA(meta string) string {
	return jq.StrOf(nestedMap(decodedMap(meta), "lastMergeSourceCommit")[keyCommitID])
}

// decodedMap decodes one provider payload as a map (nil map when unreadable).
func decodedMap(raw string) map[string]any {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	m, _ := jq.AsMap(doc)
	return m
}

// threadWarning reports a failed or capped thread read. A total failure
// writes an empty feed with this warning (fetch keeps going); a partial page
// walk keeps what it read and warns the feed may under-cover.
func threadWarning(r *session, provider string, feed []threadscore.Thread, truncated bool, err error) {
	switch {
	case err != nil && len(feed) == 0:
		r.Warn(provider + " thread read failed — threads.json is empty; re-fetch after confirming auth")
	case err != nil || truncated:
		r.Warn(provider + " thread read was incomplete — threads.json may under-cover prior feedback")
	}
}
