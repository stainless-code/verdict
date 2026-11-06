package fetchcore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func runProvider(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags, budget *budgetState,
) (*core.FetchData, string, error) {
	r, dir := ctx2.runner, ctx2.dir
	if err := fetchProviderBundle(ctx, ctx2, req, f.Provider); err != nil {
		return nil, "", err
	}
	if ref := resolveControls(ctx, ctx2, "", readcommon.PrField(dir, keyBaseSHA), f.HeadControls); ref != nil {
		return nil, "", ref
	}
	if ref := phaseTwo(ctx, ctx2, req, f, dir); ref != nil {
		return nil, "", ref
	}
	fc, chunkActive, err := providerCap(r, ctx2.runCfg.resolved, dir, f.ChunkFlag)
	if err != nil {
		return nil, "", err
	}
	if err := writeProviderBundle(ctx2); err != nil {
		return nil, "", err
	}
	if _, err := writeIdentityFile(ctx, r.commander, dir,
		readcommon.PrField(dir, keyHeadSHA), readcommon.PrField(dir, keyBaseSHA)); err != nil {
		return nil, "", err
	}
	headSHA, baseSHA := readcommon.PrField(dir, keyHeadSHA), readcommon.PrField(dir, keyBaseSHA)
	chunkJSON, ref := maybeWriteChunks(r.Diag, dir, fc.maxBytes, chunkActive, headSHA, baseSHA, fc.source, r.privacy.Exclusions)
	if ref != nil {
		return nil, "", ref
	}
	return fetchTail(ctx, ctx2, req, f, budget, &fc, chunkJSON, nullStr)
}

// providerCap owns the provider cap gate and hunk index write.
func providerCap(r *session, res *config.Resolved, dir string, chunkFlag bool) (fetchCap, bool, error) {
	fc := resolveFetchCap(r, res)
	chunkActive, ref := checkDiffSize(r, res, dir+suffixFullDiff, fc, chunkFlag)
	if ref != nil {
		return fc, false, ref
	}
	if herr := buildHunksTSV(dir+suffixFullDiff, dir+"/hunks.tsv"); herr != nil {
		return fc, false, herr
	}
	return fc, chunkActive, nil
}

// writeProviderBundle owns the shared bundle stores after the diff gates.
func writeProviderBundle(ctx2 *fetchRunCtx) error {
	r, dir := ctx2.runner, ctx2.dir
	if err := writeProviderTOC(ctx2); err != nil {
		return err
	}
	if ref := emitContext(ctx2); ref != nil {
		return ref
	}
	if err := ensureStore(dir); err != nil {
		return err
	}
	extra := prExtraPaths(dir + suffixPRJSON)
	if ref := writeSensitiveManifest(r, dir, extra); ref != nil {
		return ref
	}
	writeKBRecall(r, dir, extra)
	return nil
}

func fetchProviderBundle(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, provider string) error {
	switch provider {
	case providerGitLab:
		return fetchGitLab(ctx, ctx2, req)
	case providerGitCafe:
		return fetchGitCafe(ctx, ctx2, req)
	case providerForgejo:
		return fetchForgejo(ctx, ctx2, req)
	case providerBitbucket:
		return fetchBitbucket(ctx, ctx2, req)
	case providerAzure:
		return fetchAzure(ctx, ctx2, req)
	default:
		return &core.RefusalError{Code: codeBadProvider, Message: fmt.Sprintf(
			"unknown --provider '%s' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)", provider)}
	}
}

// cliFetch runs a provider CLI through the shared bounded transient retry
// (the same contract as the gh reads).
func (s *session) cliFetch(ctx context.Context, bin string, args ...string) (core.Result, error) {
	return s.fetchWithRetry(ctx, bin, args)
}

// fetchGitLab mirrors the gitlab backend.
func fetchGitLab(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest) error {
	r, dir, repo, pr := ctx2.runner, ctx2.dir, req.Repo, req.Pr
	if err := r.needTool(binGlab); err != nil {
		return err
	}
	meta, changes, pid, err := gitlabReads(ctx, r, dir, repo, pr)
	if err != nil {
		return err
	}
	if err := gitlabDiff(ctx, r, dir, repo, pr); err != nil {
		return err
	}
	if err := gitlabThreads(ctx, r, dir, pid, pr, gitlabHeadSHA(meta)); err != nil {
		return err
	}
	numstat := diffNumstatMap(ctx, r, dir+suffixFullDiff)
	doc, normErr := normalizeGitLab(meta, changes, numstat)
	return writePRJSON(dir, "GitLab MR", pr, doc, normErr)
}

// gitlabReads owns the GitLab view and changes reads: the MR metadata, the
// diffs-or-changes payload (empty on total failure), and the project id the
// thread read pages by.
func gitlabReads(ctx context.Context, r *session, dir, repo, pr string) (meta, changes, pid string, err error) {
	metaRes, merr := r.cliFetch(ctx, binGlab, "mr", "view", pr, flagRepo, repo, "-F", keyJSON)
	if werr := writeErrFile(dir+suffixMetaErr, metaRes.Stderr); werr != nil {
		return "", "", "", werr
	}
	if merr != nil {
		return "", "", "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"glab mr view failed for %s!%s: %s (auth? iid?)", repo, pr, headBytes(metaRes.Stderr, numExcerpt))}
	}
	pid, perr := gitlabProjectID(ctx, r, repo)
	if perr != nil {
		return "", "", "", perr
	}
	return metaRes.Stdout, gitlabChanges(ctx, r, repo, pr, pid), pid, nil
}

// gitlabProjectID resolves the numeric project id the note/discussion reads
// page by (the path is escaped as one segment, never with a trailing newline).
func gitlabProjectID(ctx context.Context, r *session, repo string) (string, error) {
	pidRes, err := r.cliFetch(ctx, binGlab, ghCmdAPI, "projects/"+url.PathEscape(repo), "--jq", ".id")
	if err != nil {
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: "cannot resolve GitLab project id for " + repo}
	}
	return strings.TrimRight(pidRes.Stdout, newline), nil
}

func gitlabChanges(ctx context.Context, r *session, _, pr, pid string) string {
	if res, err := r.cliFetch(ctx, binGlab, ghCmdAPI, "projects/"+pid+"/merge_requests/"+pr+"/diffs", flagPaginate); err == nil {
		return gitlabReadable(r, res.Stdout)
	}
	if res, err := r.cliFetch(ctx, binGlab, ghCmdAPI, "projects/"+pid+"/merge_requests/"+pr+"/changes", flagPaginate); err == nil {
		return gitlabReadable(r, res.Stdout)
	}
	r.Warn("gitlab changed-file read failed (diffs and changes) — PR.json carries no file list; the bundle may under-cover")
	return "{\"changes\":[]}\n"
}

// gitlabReadable passes a changed-file payload through, warning when it holds
// no readable diff list: a 200 with a body that is neither one JSON document
// nor adjacent page arrays must not read as a merge request with no files.
func gitlabReadable(r *session, payload string) string {
	if _, ok := gitlabChangeEntries(payload); !ok {
		r.Warn("gitlab changed-file payload unreadable — PR.json carries no file list; the bundle may under-cover")
	}
	return payload
}

// gitlabDiff owns the `glab mr diff` read with its raw flag.
func gitlabDiff(ctx context.Context, r *session, dir, repo, pr string) error {
	fmt.Fprintf(r.Human, "» glab mr diff -> %s/full.diff\n", dir)
	diffRes, err := r.cliFetch(ctx, binGlab, "mr", gitCmdDiff, pr, flagRepo, repo, "--raw")
	if werr := writeErrFile(dir+suffixDiffErr, diffRes.Stderr); werr != nil {
		return werr
	}
	if err != nil {
		return gitlabDiffFail(dir, diffRes.Stderr)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// gitlabDiffFail owns the failed-diff fork: the empty truncation lands
// first, then the typed refusal carries glab's excerpt.
func gitlabDiffFail(dir, stderr string) error {
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	return &core.RefusalError{Code: CodeFetchFailed, Message: "glab mr diff failed: " + headBytes(stderr, numExcerpt)}
}

// gitlabThreads owns the paged note-list read, the DiffNote split, and the
// authoritative discussions read the feed is built from.
func gitlabThreads(ctx context.Context, r *session, dir, pid, pr, head string) error {
	notes, truncated := gitlabNotes(ctx, r, pid, pr)
	inlineDoc, reviewsDoc := splitGitLabNotes(notes)
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(jq.Pretty(inlineDoc))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(jq.Pretty(reviewsDoc))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	feed, feedTruncated, feedErr := gitlabDiscussionFeed(ctx, r, pid, pr, head)
	threadWarning(r, providerGitLab, feed, feedTruncated, feedErr)
	if ref := writeThreadsJSON(dir, providerGitLab, pr, head, r.nowUTC(), truncated || feedTruncated, feed); ref != nil {
		return ref
	}
	return nil
}

// gitlabNotes pages the MR notes (per_page=100, stop on a short page): `glab
// mr note list` has no page flags, so the walk reads the notes endpoint
// directly. A single complete page returns verbatim; a failed page or the
// page cap warns instead of writing an empty/partial list silently.
func gitlabNotes(ctx context.Context, r *session, pid, pr string) (string, bool) {
	body, truncated, err := indexPageWalk(ctx, gitlabNotePageSize,
		func(ctx context.Context, page int) (string, error) {
			path := fmt.Sprintf("projects/%s/merge_requests/%s/notes?per_page=%d&page=%d",
				pid, pr, gitlabNotePageSize, page)
			res, ferr := r.cliFetch(ctx, binGlab, ghCmdAPI, path)
			if ferr != nil {
				return "", ferr
			}
			return res.Stdout, nil
		})
	warnPaging(r, providerGitLab, "notes", err, truncated)
	return body, err != nil || truncated
}

func splitGitLabNotes(notes string) (inline, reviews any) {
	list, ok := gitlabNoteList(notes)
	if !ok {
		return []any{}, []any{}
	}
	var in, rev []any
	for _, e := range list {
		if isDiffNote(e) {
			in = append(in, e)
		} else {
			rev = append(rev, e)
		}
	}
	if in == nil {
		in = []any{}
	}
	if rev == nil {
		rev = []any{}
	}
	return in, rev
}

// gitlabNoteList owns the note-list shape gate: arrays pass, maps read
// their notes (or discussions) member, and anything else — including a
// scalar element — reads failure.
func gitlabNoteList(notes string) ([]any, bool) {
	doc, err := jq.Decode([]byte(notes))
	if err != nil {
		return nil, false
	}
	if arr, ok := doc.([]any); ok {
		if !noteArrayValid(arr) {
			return nil, false
		}
		return arr, true
	}
	if m, ok := jq.AsMap(doc); ok {
		return gitlabNoteMember(m)
	}
	return nil, false
}

func gitlabNoteMember(m map[string]any) ([]any, bool) {
	if n, has := m["notes"]; has {
		return jq.Elements(n), true
	}
	if d, has := m["discussions"]; has {
		return jq.Elements(d), true
	}
	return nil, false
}

// noteArrayValid gates the note-array shape: nulls and objects pass, a
// scalar fails.
func noteArrayValid(arr []any) bool {
	for _, e := range arr {
		if e == nil {
			continue
		}
		if _, ok := e.(map[string]any); !ok {
			return false
		}
	}
	return true
}

func isDiffNote(e any) bool {
	m := objOrMap(e)
	if m == nil {
		return false
	}
	if jq.StrOf(m["type"]) == noteKindDiff {
		return true
	}
	return slices.ContainsFunc(jq.Elements(m["notes"]), noteTypeDiff)
}

// noteTypeDiff handles one nested note's type test (plain or ordered object).
func noteTypeDiff(n any) bool {
	if nm, ok := n.(map[string]any); ok && jq.StrOf(nm["type"]) == noteKindDiff {
		return true
	}
	if om, ok := n.(*jq.Obj); ok {
		if v, _ := om.Get("type"); jq.StrOf(v) == noteKindDiff {
			return true
		}
	}
	return false
}

// diffNumstatMap parses `git apply --numstat` output into
// a path → {adds, dels} map (rename/binary edges approximate; the API file
// list stays authoritative).
func diffNumstatMap(ctx context.Context, s *session, fullDiff string) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	res, err := s.run(ctx, binGit, "apply", "--numstat", fullDiff)
	if err != nil {
		return out
	}
	for _, line := range splitLines(res.Stdout) {
		parts := strings.Split(line, "\t")
		if len(parts) != repoParts {
			continue
		}
		out[parts[2]] = map[string]int64{keyAdds: numOrZero(parts[0]), keyDels: numOrZero(parts[1])}
	}
	return out
}

// numOrZero reads a count, mapping a dash or empty field to 0: a
// plain-digit field parses, anything else (including an overflow) reads zero.
func numOrZero(s string) int64 {
	if s == "" || s == "-" {
		return 0
	}
	if !readcommon.IsDigits(s) {
		return 0
	}
	n, err := strconv.ParseInt(s, numBase10, numBitSize)
	if err != nil {
		return 0
	}
	return n
}

// fetchGitCafe mirrors the gitcafe backend.
func fetchGitCafe(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest) error {
	r, dir, repo, pr := ctx2.runner, ctx2.dir, req.Repo, req.Pr
	if err := r.needTool(providerCafe); err != nil {
		return err
	}
	metaRes, err := cafeMeta(ctx, r, dir, repo, pr)
	if err != nil {
		return err
	}
	itemsRes, err := cafeDiff(ctx, r, dir, repo, pr)
	if err != nil {
		return err
	}
	revs, revTruncated, revErr := fetchCafePages(cafeReviewsPage(ctx, r, repo, pr))
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(revs)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	warnPaging(r, providerGitCafe, "reviews", revErr, revTruncated)
	cmts, cmtTruncated, cmtErr := fetchCafePages(cafeCommentsPage(ctx, r, repo, pr))
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(cmts)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	warnPaging(r, providerGitCafe, "comments", cmtErr, cmtTruncated)
	feed := threadscore.FromGitCafe(decodeThreadPayload(revs), decodeThreadPayload(cmts))
	truncated := revTruncated || cmtTruncated
	if ref := writeThreadsJSON(dir, providerGitCafe, pr, cafeHeadSHA(metaRes.Stdout), r.nowUTC(), truncated, feed); ref != nil {
		return ref
	}
	doc, normErr := normalizeGitCafe(metaRes.Stdout, itemsRes.Stdout)
	return writePRJSON(dir, "GitCafe PR", pr, doc, normErr)
}

// cafeMeta owns the `cafe pr view` read with its stderr sidecar.
func cafeMeta(ctx context.Context, r *session, dir, repo, pr string) (core.Result, error) {
	metaRes, err := r.cliFetch(ctx, providerCafe, ghCmdPR, "view", pr, flagRepo, repo, flagJSON)
	if werr := writeErrFile(dir+suffixMetaErr, metaRes.Stderr); werr != nil {
		return metaRes, werr
	}
	if err != nil {
		return metaRes, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"cafe pr view failed: %s (auth? number?)", headBytes(metaRes.Stderr, numExcerpt))}
	}
	return metaRes, nil
}

// cafeDiff owns the `cafe pr diff` read: the joined full.diff lands,
// warning when structured hunks go unrendered or no hunks index.
func cafeDiff(ctx context.Context, r *session, dir, repo, pr string) (core.Result, error) {
	itemsRes, err := r.cliFetch(ctx, providerCafe, ghCmdPR, gitCmdDiff, pr, flagRepo, repo, "--patch", flagJSON)
	if werr := writeErrFile(dir+suffixDiffErr, itemsRes.Stderr); werr != nil {
		return itemsRes, werr
	}
	if err != nil {
		return itemsRes, &core.RefusalError{
			Code:    CodeFetchFailed,
			Message: "cafe pr diff failed: " + headBytes(itemsRes.Stderr, numExcerpt),
		}
	}
	items := cafeItems(itemsRes.Stdout)
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(cafeFullDiff(items))); werr != nil {
		return itemsRes, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	if n := cafeUnhandledHunks(items); n != 0 {
		r.Warn(fmt.Sprintf("%d cafe files carry structured hunks the builder does not render — anchors unchecked for those paths", n))
	}
	raw, rerr := os.ReadFile(dir + suffixFullDiff)
	if rerr != nil {
		raw = nil
	}
	if !hasHunkHeader(string(raw)) {
		r.Warn("no hunks indexed from cafe patch — anchors unchecked, everything kept")
	}
	return itemsRes, nil
}

// cafeReviewsPage builds the review-page fetcher for the cursor loop.
func cafeReviewsPage(ctx context.Context, r *session, repo, pr string) func(string) (core.Result, error) {
	return func(cursor string) (core.Result, error) {
		args := []string{ghCmdPR, "reviews", pr, flagRepo, repo, flagJSON}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		return r.cliFetch(ctx, providerCafe, args...)
	}
}

// cafeCommentsPage builds the comment-page fetcher for the cursor loop.
func cafeCommentsPage(ctx context.Context, r *session, repo, pr string) func(string) (core.Result, error) {
	return func(cursor string) (core.Result, error) {
		args := []string{ghCmdPR, "comments", pr, flagRepo, repo, "--filter", "all", flagJSON}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		return r.cliFetch(ctx, providerCafe, args...)
	}
}

// cafeItems owns the items normalization: arrays pass, envelopes read
// .data.items // .items, anything else reads empty.
func cafeItems(stdout string) []any {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return []any{}
	}
	if arr, ok := doc.([]any); ok {
		return arr
	}
	if m, ok := jq.AsMap(doc); ok {
		if items, found := envelopeItems(m); found {
			return items
		}
	}
	return []any{}
}

// envelopeItems reads `.data.items // .items`: the first present member at
// either depth.
func envelopeItems(m map[string]any) ([]any, bool) {
	if d, has := m[keyData]; has {
		if items, found := itemList(d); found {
			return items, true
		}
	}
	return itemList(m)
}

// itemList reads one value's items member (ok=false when not an object).
func itemList(v any) ([]any, bool) {
	m, ok := jq.AsMap(v)
	if !ok {
		return nil, false
	}
	if items, has := m[keyItems]; has {
		return jq.Elements(items), true
	}
	return nil, false
}

func cafeFullDiff(items []any) string {
	var parts []string
	for _, e := range items {
		m := objOrMap(e)
		if m == nil {
			m = map[string]any{}
		}
		path := jq.Interp(m[keyPath])
		parts = append(parts, "diff --git a/"+path+" b/"+path)
		patch := patchOf(m)
		if strings.Contains(patch, "@@") {
			parts = append(parts, patch)
		} else {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, newline)
}

// cafeUnhandledHunks owns the structured-hunks count: patch without @@
// but a non-null hunks field.
func cafeUnhandledHunks(items []any) int {
	n := 0
	for _, e := range items {
		m := objOrMap(e)
		if m == nil {
			continue
		}
		patch := patchOf(m)
		if h, has := m["hunks"]; !strings.Contains(patch, "@@") && has && h != nil {
			n++
		}
	}
	return n
}

// patchOf reads a provider entry's patch text ("" when absent).
func patchOf(m map[string]any) string {
	if p, ok := m["patch"].(string); ok {
		return p
	}
	return ""
}

// hasHunkHeader reports whether the diff carries a `@@` hunk header.
func hasHunkHeader(diff string) bool {
	for _, line := range splitLines(diff) {
		if strings.HasPrefix(line, "@@") {
			return true
		}
	}
	return false
}

func fetchCafePages(call func(cursor string) (core.Result, error)) (string, bool, error) {
	acc, truncated, err := cafeAccumulate(call)
	if acc == nil {
		acc = []any{}
	}
	return strings.TrimSuffix(jq.Pretty(plainAll(acc)), newline), truncated, err
}

func cafeAccumulate(call func(cursor string) (core.Result, error)) ([]any, bool, error) {
	cursor := ""
	var acc []any
	for page := range cafeMaxPages {
		raw, ferr := cafePage(call, cursor)
		if ferr != nil {
			return acc, true, ferr
		}
		items, ok := cafePageItems(raw)
		if !ok {
			return acc, true, internalErrorf("cafe page %d unreadable", page+1)
		}
		acc = append(acc, items...)
		if cursor = cafeCursor(raw); cursor == "" {
			return acc, false, nil
		}
	}
	return acc, true, nil
}

// cafePage handles one page read: a failed call reports the call's error so the
// walk can stop on it (the pages already read are kept by the caller).
func cafePage(call func(cursor string) (core.Result, error), cursor string) (string, error) {
	res, err := call(cursor)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// cafePageItems reads `.data.items // .items // []`: the page items,
// or ok=false when the page is not valid JSON.
func cafePageItems(page string) ([]any, bool) {
	doc, err := jq.Decode([]byte(page))
	if err != nil {
		return nil, false
	}
	if arr, ok := doc.([]any); ok {
		return arr, true
	}
	if m, ok := jq.AsMap(doc); ok {
		if items, found := envelopeItems(m); found {
			return items, true
		}
	}
	return []any{}, true
}

// plainAll converts ordered objects to plain maps for rendering.
func plainAll(in []any) []any {
	out := make([]any, len(in))
	for i, e := range in {
		out[i] = plainValue(e)
	}
	return out
}

func cafeCursor(stdout string) string {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	if s, found := cursorNext(m); found {
		return s
	}
	return ""
}

// cursorNext reads `.data.next // .next // empty`.
func cursorNext(m map[string]any) (string, bool) {
	if d, has := m[keyData]; has {
		if dm, ok := jq.AsMap(d); ok {
			if s, found := cafeNext(dm); found {
				return s, true
			}
		}
	}
	return cafeNext(m)
}

// cafeNext owns the cursor-member read: a string passes through, any
// other non-null value renders, and a missing member reads absent.
func cafeNext(m map[string]any) (string, bool) {
	n, has := m["next"]
	if !has || n == nil {
		return "", false
	}
	if s, ok := n.(string); ok {
		return s, true
	}
	return jq.R(n), true
}

// plainValue converts ordered objects to plain maps for compact rendering.
func plainValue(e any) any {
	if om, ok := e.(*jq.Obj); ok {
		return objFields(om)
	}
	return e
}

func curlArgs(auth []string, extra ...string) []string {
	args := append([]string{flagCurlFail}, auth...)
	return append(args, extra...)
}

func fetchForgejo(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest) error {
	r, dir, pr := ctx2.runner, ctx2.dir, req.Pr
	if err := r.needTool("curl"); err != nil {
		return err
	}
	auth, api, err := forgejoAuth(ctx2)
	if err != nil {
		return err
	}
	metaRes, err := forgejoMeta(ctx, r, dir, auth, api)
	if err != nil {
		return err
	}
	if err := forgejoDiff(ctx, r, dir, auth, api, metaRes.Stdout); err != nil {
		return err
	}
	files := forgejoFiles(ctx, r, auth, api)
	if err := forgejoThreads(ctx, r, dir, auth, api, pr, metaRes.Stdout); err != nil {
		return err
	}
	doc, normErr := normalizeForgejo(metaRes.Stdout, files)
	return writePRJSON(dir, "Forgejo PR", pr, doc, normErr)
}

func forgejoAuth(ctx2 *fetchRunCtx) (auth []string, api string, err error) {
	return forgejoAuthFor(ctx2.env, ctx2.repo, ctx2.pr)
}

// forgejoAuthFor is forgejoAuth over an explicit environment and positionals:
// the threads refresh has no fetchRunCtx but needs the same gate.
func forgejoAuthFor(env map[string]string, repo, pr string) (auth []string, api string, err error) {
	host, _ := env["FORGEJO_HOST"]
	token, _ := env["FORGEJO_TOKEN"]
	if host == "" {
		return nil, "", &core.RefusalError{Code: codeAuthMissing, Message: "forgejo needs FORGEJO_HOST=https://host and FORGEJO_TOKEN set"}
	}
	if token == "" {
		return nil, "", &core.RefusalError{Code: codeAuthMissing, Message: "forgejo needs FORGEJO_TOKEN set (app token with repo read)"}
	}
	return []string{flagH, "Authorization: token " + token}, host + "/api/v1/repos/" + repo + "/pulls/" + pr, nil
}

// forgejoMeta owns the PR read with its stderr sidecar.
func forgejoMeta(ctx context.Context, r *session, dir string, auth []string, api string) (core.Result, error) {
	metaRes, err := r.curlFetch(ctx, curlArgs(auth, api)...)
	if werr := writeErrFile(dir+suffixMetaErr, metaRes.Stderr); werr != nil {
		return metaRes, werr
	}
	if err != nil {
		return metaRes, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"forgejo PR fetch failed: %s (host? token? number?)", headBytes(metaRes.Stderr, numExcerpt))}
	}
	return metaRes, nil
}

func forgejoDiff(ctx context.Context, r *session, dir string, auth []string, api, metaStdout string) error {
	fmt.Fprintf(r.Human, "» forgejo diff -> %s/full.diff\n", dir)
	diffRes, err := r.curlFetch(ctx, curlArgs(auth, api+".diff")...)
	if err != nil {
		return forgejoDiffFallback(ctx, r, dir, auth, api, metaStdout)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

func forgejoDiffURL(metaStdout string) string {
	doc, derr := jq.Decode([]byte(metaStdout))
	if derr != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	u, has := m["diff_url"]
	if !has || u == nil {
		return ""
	}
	if s, ok := u.(string); ok {
		return s
	}
	return ""
}

// forgejoDiffFallback owns the metadata-URL fallback with its stderr
// sidecar and empty-diff truncation on total failure.
func forgejoDiffFallback(ctx context.Context, r *session, dir string, auth []string, api, metaStdout string) error {
	diffURL := forgejoDiffURL(metaStdout)
	if diffURL != "" && !sameOrigin(api, diffURL) {
		return offOriginRefusal(providerForgejo, diffURL)
	}
	diffRes, err := r.curlFetch(ctx, curlArgs(auth, diffURL)...)
	if werr := writeErrFile(dir+suffixDiffErr, diffRes.Stderr); werr != nil {
		return werr
	}
	if err != nil {
		if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: "forgejo diff fetch failed: " + headBytes(diffRes.Stderr, numExcerpt)}
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// forgejoFiles owns the paged file-list read, defaulting to [] on failure.
func forgejoFiles(ctx context.Context, r *session, auth []string, api string) string {
	return forgejoList(ctx, r, auth, api+"/files", "file list")
}

// forgejoList pages one Forgejo list endpoint (limit=50, stop on a short
// page). A single complete page returns verbatim, so existing bundles stay
// byte-identical; a failed page or the page cap warns instead of returning
// the first page silently.
func forgejoList(ctx context.Context, r *session, auth []string, base, what string) string {
	body, truncated, err := forgejoListRead(ctx, r, auth, base)
	warnPaging(r, providerForgejo, what, err, truncated)
	return body
}

// forgejoListRead pages one Forgejo list endpoint and hands back the partial
// body, truncation, and page error so a caller can decide warn-vs-refuse.
func forgejoListRead(
	ctx context.Context, r *session, auth []string, base string,
) (body string, truncated bool, err error) {
	return indexPageWalk(ctx, forgejoPageSize,
		func(ctx context.Context, page int) (string, error) {
			u := fmt.Sprintf("%s?limit=%d&page=%d", base, forgejoPageSize, page)
			res, ferr := r.curlFetch(ctx, curlArgs(auth, u)...)
			if ferr != nil {
				return "", ferr
			}
			return res.Stdout, nil
		})
}

func forgejoThreads(ctx context.Context, r *session, dir string, auth []string, api, pr, meta string) error {
	revs, revTruncated, revErr := forgejoListRead(ctx, r, auth, api+"/reviews")
	warnPaging(r, providerForgejo, "reviews", revErr, revTruncated)
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(revs)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	inline, complete := forgejoInline(ctx, r, api, auth, revs)
	if !complete {
		r.Warn("forgejo review-comment read failed — inline.json may be incomplete; the bundle may under-cover threads")
		if inline == "" {
			inline = emptyJSONArray
		}
	}
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(inline)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	truncated := revTruncated || !complete
	feed := threadscore.FromForgejo(decodeThreadPayload(revs), decodeThreadPayload(inline), forgejoHeadSHA(meta))
	if ref := writeThreadsJSON(dir, providerForgejo, pr, forgejoHeadSHA(meta), r.nowUTC(), truncated, feed); ref != nil {
		return ref
	}
	return nil
}

func forgejoInline(ctx context.Context, r *session, api string, auth []string, revs string) (string, bool) {
	doc, err := jq.Decode([]byte(revs))
	if err != nil {
		// Undecodable revs feed no ids; the empty stream still merges
		// to [].
		return jq.Pretty([]any{}), true
	}
	var ids []string
	for _, e := range forgejoItems(doc) {
		ids = append(ids, forgejoID(e))
	}
	docs, complete := forgejoCommentDocs(ctx, r, api, auth, ids)
	merged, ok := mergeAdd(docs)
	if !ok {
		return "", false
	}
	return jq.Pretty(merged), complete
}

func forgejoItems(doc any) []any {
	switch t := doc.(type) {
	case []any:
		return t
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var items []any
		for _, k := range keys {
			items = append(items, t[k])
		}
		return items
	}
	return nil
}

func forgejoCommentDocs(ctx context.Context, r *session, api string, auth, ids []string) ([]any, bool) {
	var docs []any
	complete := true
	for _, id := range ids {
		res, err := r.curlFetch(ctx, curlArgs(auth, api+"/reviews/"+id+"/comments")...)
		if err != nil {
			complete = false
			docs = append(docs, plainValue([]any{}))
			continue
		}
		doc, derr := jq.Decode([]byte(res.Stdout))
		if derr != nil {
			return nil, false
		}
		docs = append(docs, plainValue(doc))
	}
	return docs, complete
}

// forgejoID renders a review id for the URL.
func forgejoID(e any) string {
	if m, ok := e.(map[string]any); ok {
		if id, has := m[keyID]; has {
			return jq.R(id)
		}
		return nullStr
	}
	if om, ok := e.(*jq.Obj); ok {
		if id, has := om.Get(keyID); has {
			return jq.R(id)
		}
		return nullStr
	}
	return nullStr
}

// jsonArrayOrEmpty decodes an array payload: arrays pass, anything else
// reads empty.
func jsonArrayOrEmpty(raw string) []any {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return []any{}
	}
	arr, ok := doc.([]any)
	if !ok {
		return []any{}
	}
	return arr
}

// forgejoState owns the state fold: merged wins, open/closed pass, and
// anything else reads unknown.
func forgejoState(meta map[string]any) string {
	merged := false
	if mv, has := meta[stateMerged]; has && mv == true {
		merged = true
	}
	state := normStr(meta, keyState)
	switch {
	case merged:
		return stateMerged
	case state == stateOpen || state == stateClosed:
		return state
	default:
		return stateUnknown
	}
}

// forgejoDraft owns the draft read: the member wins, else the WIP title
// markers decide.
func forgejoDraft(meta map[string]any) any {
	draft := meta[keyDraft]
	if draft == nil {
		title := normStr(meta, keyTitle)
		return strings.HasPrefix(title, "WIP:") || strings.HasPrefix(title, "[DRAFT]")
	}
	return draft
}

// mergeAdd concatenates arrays across pieces; an empty stream reads [].
// Anything else fails the fold, because a mixed-shape add has no meaning.
func mergeAdd(docs []any) (any, bool) {
	if len(docs) == 0 {
		return []any{}, true
	}
	var out []any
	for _, d := range docs {
		arr, ok := d.([]any)
		if !ok {
			return nil, false
		}
		out = append(out, arr...)
	}
	if out == nil {
		out = []any{}
	}
	return out, true
}

func fetchBitbucket(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest) error {
	r, dir, repo, pr := ctx2.runner, ctx2.dir, req.Repo, req.Pr
	if err := r.needTool("curl"); err != nil {
		return err
	}
	auth, ref := bitbucketAuth(ctx2)
	if ref != nil {
		return ref
	}
	base := "https://api.bitbucket.org/2.0/repos/" + repo + "/pullrequests/" + pr
	meta, ref := bitbucketMeta(ctx, r, dir, auth, base)
	if ref != nil {
		return ref
	}
	if ref := bitbucketDiff(ctx, r, dir, auth, base); ref != nil {
		return ref
	}
	ds, ref := bitbucketThreads(ctx, r, dir, auth, base, pr, bitbucketHead(meta))
	if ref != nil {
		return ref
	}
	doc, normErr := normalizeBitbucket(meta, ds)
	return writePRJSON(dir, "Bitbucket PR", pr, doc, normErr)
}

// bitbucketAuth owns the token/user-password gate.
func bitbucketAuth(ctx2 *fetchRunCtx) ([]string, *core.RefusalError) {
	return bitbucketAuthFor(ctx2.env)
}

// bitbucketAuthFor is bitbucketAuth over an explicit environment: the threads
// refresh has no fetchRunCtx but needs the same credential gate.
func bitbucketAuthFor(env map[string]string) ([]string, *core.RefusalError) {
	token, _ := env["BITBUCKET_TOKEN"]
	user, _ := env["BITBUCKET_USERNAME"]
	pass, _ := env["BITBUCKET_PASSWORD"]
	switch {
	case token != "":
		return []string{flagH, "Authorization: Bearer " + token}, nil
	case user != "" && pass != "":
		return []string{"-u", user + ":" + pass}, nil
	default:
		return nil, &core.RefusalError{
			Code:    codeAuthMissing,
			Message: "bitbucket needs BITBUCKET_TOKEN (Bearer) or BITBUCKET_USERNAME+BITBUCKET_PASSWORD",
		}
	}
}

// bitbucketMeta owns the PR read with its stderr sidecar.
func bitbucketMeta(ctx context.Context, r *session, dir string, auth []string, base string) (string, *core.RefusalError) {
	metaRes, err := r.curlFetch(ctx, append(append([]string{flagCurlFail}, auth...), base)...)
	if werr := writeErrFile(dir+suffixMetaErr, metaRes.Stderr); werr != nil {
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: werr.Error()}
	}
	if err != nil {
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"bitbucket PR fetch failed: %s (auth? workspace/repo#id?)", headBytes(metaRes.Stderr, numExcerpt))}
	}
	return metaRes.Stdout, nil
}

func bitbucketDiff(ctx context.Context, r *session, dir string, auth []string, base string) *core.RefusalError {
	fmt.Fprintf(r.Human, "» bitbucket diff -> %s/full.diff\n", dir)
	diffRes, err := r.curlFetch(ctx, append(append([]string{"-sfL"}, auth...), base+"/diff")...)
	if werr := writeErrFile(dir+suffixDiffErr, diffRes.Stderr); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: werr.Error()}
	}
	if err != nil {
		if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: "bitbucket diff failed: " + headBytes(diffRes.Stderr, numExcerpt)}
	}
	if err := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, err)}
	}
	return nil
}

func bitbucketThreads(ctx context.Context, r *session, dir string, auth []string, base, pr, head string) ([]any, *core.RefusalError) {
	ds, _, ref := bitbucketPages(ctx, r, auth, base+"/diffstat?pagelen=100")
	if ref != nil {
		return nil, ref
	}
	cm, truncated, ref := bitbucketPages(ctx, r, auth, base+"/comments?pagelen=100")
	if ref != nil {
		return nil, ref
	}
	inline, reviews := splitBitbucketComments(cm)
	if err := atomic.WriteFile(dir+suffixInlineJSON, []byte(jq.Pretty(plainAll(inline)))); err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, err)}
	}
	if err := atomic.WriteFile(dir+suffixReviewsJSON, []byte(jq.Pretty(plainAll(reviews)))); err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, err)}
	}
	feed := threadscore.FromBitbucket(cm)
	if ref := writeThreadsJSON(dir, providerBitbucket, pr, head, r.nowUTC(), truncated, feed); ref != nil {
		return nil, ref
	}
	return ds, nil
}

func bitbucketPages(ctx context.Context, r *session, auth []string, first string) ([]any, bool, *core.RefusalError) {
	out, truncated, _, err := pageWalk(ctx, first, providerMaxPages,
		func(ctx context.Context, u string) ([]any, string, error) {
			target, ok := resolveOnOrigin(first, u)
			if !ok {
				return nil, "", offOriginRefusal(providerBitbucket, u)
			}
			m, perr := bitbucketPage(ctx, r, auth, target)
			if perr != nil {
				return nil, "", internalErrorf("bitbucket page unreadable: %v", perr)
			}
			return jq.Elements(m["values"]), bitbucketNext(m), nil
		})
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return nil, true, ref
	}
	bitbucketWarnPaging(r, err, truncated)
	if out == nil {
		out = []any{}
	}
	//nolint:nilerr // a failed page is a warning path: the partial list and its truncated flag still return
	return out, err != nil || truncated, nil
}

// bitbucketWarnPaging surfaces a partial or early-stopped page walk.
func bitbucketWarnPaging(r *session, err error, truncated bool) {
	switch {
	case err != nil:
		r.Warn("bitbucket paging stopped on an unreadable page (" +
			headBytes(err.Error(), numExcerptShort) + ") — the bundle may be incomplete")
	case truncated:
		r.Warn("bitbucket paging ended early (page cap or repeated next cursor) — the bundle may be incomplete")
	}
}

// bitbucketPage fetches and decodes one page. A failed call or a body that is
// not a JSON object is an error, never an empty page: the walk then stops with
// the pages it has and the caller warns and marks the read truncated, instead
// of reading a failed diffstat or comment list as "no files" / "no threads".
func bitbucketPage(ctx context.Context, r *session, auth []string, u string) (map[string]any, error) {
	res, err := r.curlFetch(ctx, append(append([]string{"-sfL"}, auth...), u)...)
	if err != nil {
		return nil, err
	}
	doc, err := jq.Decode([]byte(res.Stdout))
	if err != nil {
		return nil, internalErrorf("page is not JSON: %v", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, internalErrorf("page is not a JSON object")
	}
	return m, nil
}

// bitbucketNext reads `.next // empty` from a decoded page.
func bitbucketNext(m map[string]any) string {
	if next, has := m["next"]; has && next != nil {
		if s := jq.R(next); s != nullStr {
			return s
		}
	}
	return ""
}

func splitBitbucketComments(cm []any) (inline, reviews []any) {
	if !noteObjectsValid(cm) {
		return []any{}, []any{}
	}
	var in, rev []any
	for _, e := range cm {
		if bitbucketInline(e) {
			in = append(in, plainValue(e))
		} else {
			rev = append(rev, plainValue(e))
		}
	}
	if in == nil {
		in = []any{}
	}
	if rev == nil {
		rev = []any{}
	}
	return in, rev
}

// noteObjectsValid gates the element shape: a non-object element reads
// both files empty.
func noteObjectsValid(list []any) bool {
	for _, e := range list {
		if e == nil {
			continue
		}
		if _, ok := e.(map[string]any); ok {
			continue
		}
		if _, ok := e.(*jq.Obj); ok {
			continue
		}
		return false
	}
	return true
}

// bitbucketInline owns the inline-thread test: an `inline` member set.
func bitbucketInline(e any) bool {
	m := objOrMap(e)
	if m == nil {
		return false
	}
	_, present := m["inline"]
	return present && m["inline"] != nil
}

// azureConn carries the resolved Azure connection: org, project,
// repo name, token, and the PR metadata stdout.
type azureConn struct {
	org     string
	project string
	rname   string
	tok     string
	meta    string
}

func fetchAzure(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest) error {
	r, dir, repo, pr := ctx2.runner, ctx2.dir, req.Repo, req.Pr
	if err := r.needTool(binAz); err != nil {
		return err
	}
	conn, err := azureSetup(ctx, ctx2, repo, pr)
	if err != nil {
		return err
	}
	tmp, err := azureCloneInit(ctx, r)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := azureCloneDiff(ctx, r, dir, tmp, &conn); err != nil {
		return err
	}
	if err := azureThreads(ctx, r, dir, &conn, pr); err != nil {
		return err
	}
	changes := azureChanges(ctx, r, &conn, pr)
	numstat := diffNumstatMap(ctx, r, dir+suffixFullDiff)
	doc, normErr := normalizeAzure(conn.meta, changes, numstat)
	return writePRJSON(dir, "Azure PR", pr, doc, normErr)
}

func azureSetup(ctx context.Context, ctx2 *fetchRunCtx, repo, pr string) (azureConn, error) {
	r, dir := ctx2.runner, ctx2.dir
	var conn azureConn
	org, _ := ctx2.env["AZURE_DEVOPS_ORG"]
	if org == "" {
		return conn, &core.RefusalError{
			Code:    codeAuthMissing,
			Message: "azure needs AZURE_DEVOPS_ORG=https://dev.azure.com/{org} (and az login or AZURE_DEVOPS_EXT_PAT)",
		}
	}
	project, _, _ := strings.Cut(repo, pathSep)
	rname := repo
	if _, after, ok := strings.CutLast(repo, pathSep); ok {
		rname = after
	}
	metaRes, err := r.cliFetch(ctx, binAz, "repos", ghCmdPR, "show", "--id", pr,
		"--org", org, "--project", project, "--repository", rname, flagOut, keyJSON)
	if werr := writeErrFile(dir+suffixMetaErr, metaRes.Stderr); werr != nil {
		return conn, werr
	}
	if err != nil {
		return conn, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"az repos pr show failed: %s (login? extension? project/repo#id?)", headBytes(metaRes.Stderr, numExcerpt))}
	}
	remote, hs, bs := azureMergeRef(metaRes.Stdout)
	if azureMergeMissing(remote, hs, bs) {
		return conn, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("azure PR %s carries no merge commits/remote", pr)}
	}
	if !sameOrigin(org, remote) {
		return conn, offOriginRefusal(providerAzure, remote)
	}
	tok, err := azureToken(ctx, r, ctx2)
	if err != nil {
		return conn, err
	}
	return azureConn{org: org, project: project, rname: rname, tok: tok, meta: metaRes.Stdout}, nil
}

func azureMergeMissing(remote, hs, bs string) bool {
	return remote == "" || hs == "" || bs == ""
}

// azureToken owns the token resolution: the PAT env wins, else the az
// login token, else a typed refusal.
func azureToken(ctx context.Context, r *session, ctx2 *fetchRunCtx) (string, error) {
	tok, _ := ctx2.env["AZURE_DEVOPS_EXT_PAT"]
	if tok == "" {
		if res, err := r.cliFetch(ctx, binAz, "account", "get-access-token", flagQuery, "accessToken", flagOut, "tsv"); err == nil {
			tok = strings.TrimRight(res.Stdout, newline)
		}
	}
	if tok == "" {
		return "", &core.RefusalError{
			Code:    codeAuthMissing,
			Message: "azure git fetch needs AZURE_DEVOPS_EXT_PAT or an az login with token access",
		}
	}
	return tok, nil
}

func azureCloneInit(ctx context.Context, r *session) (string, error) {
	tmp, err := os.MkdirTemp("", "verdict-azure")
	if err != nil {
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("azure temp clone init failed: %v", err)}
	}
	if res, err := r.run(ctx, binGit, "init", "-q", tmp); err != nil {
		// No stderr redirect on the init call: git's own text leaks.
		fmt.Fprint(r.Human, res.Stderr)
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: "azure temp clone init failed"}
	}
	return tmp, nil
}

// azureCloneAuth owns the clone auth header: the PAT as a Basic header
// appended to the temp clone config.
func azureCloneAuth(tmp, tok string) error {
	enc := base64Token(tok)
	cfg, err := os.OpenFile(tmp+"/.git/config", os.O_APPEND|os.O_WRONLY, gitConfigMode)
	if err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: "azure temp clone config failed"}
	}
	if _, err := cfg.WriteString("[http]\nextraHeader = AUTHORIZATION: Basic " + enc + newline); err != nil {
		cfg.Close()
		return &core.RefusalError{Code: CodeFetchFailed, Message: "azure temp clone config failed"}
	}
	cfg.Close()
	return nil
}

func azureCloneDiff(ctx context.Context, r *session, dir, tmp string, conn *azureConn) error {
	if err := azureCloneAuth(tmp, conn.tok); err != nil {
		return err
	}
	remote, hs, bs := azureMergeRef(conn.meta)
	fetchRes, err := r.run(ctx, binGit, "-C", tmp, "fetch", "-q", remote, hs, bs)
	if werr := writeErrFile(dir+suffixDiffErr, fetchRes.Stderr); werr != nil {
		return werr
	}
	if err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: "azure git fetch failed: " + headBytes(fetchRes.Stderr, numExcerpt)}
	}
	diffRes, err := r.run(ctx, binGit, "-C", tmp, gitCmdDiff, bs, hs)
	if err != nil {
		return azureDiffFail(dir, diffRes.Stderr)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// azureDiffFail owns the failed-diff fork: the empty truncation lands
// first, then the typed refusal.
func azureDiffFail(dir, stderr string) error {
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	return &core.RefusalError{Code: CodeFetchFailed, Message: "azure diff failed: " + headBytes(stderr, numExcerpt)}
}

func azureAPI(org, project, rname, pr, path, query, format string) []string {
	return []string{
		"rest", "--method", "get",
		"--url", org + pathSep + project + "/_apis/git/repositories/" + rname + "/pullRequests/" + pr + path,
		flagQuery, query, flagOut, format,
	}
}

func azureThreads(ctx context.Context, r *session, dir string, conn *azureConn, pr string) error {
	raw := emptyJSONArray
	truncated := false
	args := azureAPI(conn.org, conn.project, conn.rname, pr, "/threads?api-version=7.1", "value", keyJSON)
	if res, err := r.cliFetch(ctx, binAz, args...); err == nil {
		raw = res.Stdout
	} else {
		truncated = true
		r.Warn("azure review-thread read failed — inline.json and reviews.json are empty; the bundle may under-cover threads")
	}
	inline, reviews := splitAzureThreads(raw)
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(jq.Pretty(plainAll(inline)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(jq.Pretty(plainAll(reviews)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	feed := threadscore.FromAzure(decodeThreadPayload(raw))
	if ref := writeThreadsJSON(dir, providerAzure, pr, azureHeadSHA(conn.meta), r.nowUTC(), truncated, feed); ref != nil {
		return ref
	}
	return nil
}

func azureChanges(ctx context.Context, r *session, conn *azureConn, pr string) string {
	iid := azureIterationID(ctx, r, conn, pr)
	if iid == "" {
		r.Warn("azure iteration read failed — PR.json carries no file list; the bundle may under-cover")
		return emptyJSONArray
	}
	items, truncated, _, err := pageWalk(ctx, "0", providerMaxPages,
		func(ctx context.Context, cursor string) ([]any, string, error) {
			return azureChangePage(ctx, r, conn, pr, iid, cursor)
		})
	warnPaging(r, providerAzure, "change entries", err, truncated)
	if items == nil {
		items = []any{}
	}
	return jq.Pretty(items)
}

// azureIterationID reads the PR's latest iteration id ("" on failure).
func azureIterationID(ctx context.Context, r *session, conn *azureConn, pr string) string {
	iterArgs := azureAPI(conn.org, conn.project, conn.rname, pr, "/iterations?api-version=7.1",
		"max_by(value, &id).id", "tsv")
	res, err := r.cliFetch(ctx, binAz, iterArgs...)
	if err != nil {
		return ""
	}
	return strings.TrimRight(res.Stdout, newline)
}

// azureChangePage fetches and decodes one `$top`/`$skip` change-entry page.
func azureChangePage(
	ctx context.Context, r *session, conn *azureConn, pr, iid, cursor string,
) ([]any, string, error) {
	skip, ok := cursorPage(cursor)
	if !ok {
		return nil, "", internalErrorf("bad change-entry skip cursor %q", cursor)
	}
	path := fmt.Sprintf("/iterations/%s/changes?api-version=7.1&$top=%d&$skip=%d",
		iid, azureChangePageSize, skip)
	args := azureAPI(conn.org, conn.project, conn.rname, pr, path, "changeEntries", keyJSON)
	res, ferr := r.cliFetch(ctx, binAz, args...)
	if ferr != nil {
		return nil, "", ferr
	}
	list, derr := jsonArray(res.Stdout)
	if derr != nil {
		return nil, "", internalErrorf("azure change-entry page unreadable: %v", derr)
	}
	if len(list) < azureChangePageSize {
		return list, "", nil
	}
	return list, strconv.Itoa(skip + azureChangePageSize), nil
}

// azureMergeRef owns the merge-commit/remote read: empty when absent.
func azureMergeRef(meta string) (remote, hs, bs string) {
	doc, err := jq.Decode([]byte(meta))
	if err != nil {
		return "", "", ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return "", "", ""
	}
	remote = jq.StrOf(nestedStr(m, "repository", "remoteUrl"))
	hs = jq.StrOf(nestedStr(m, "lastMergeSourceCommit", keyCommitID))
	bs = jq.StrOf(nestedStr(m, "lastMergeTargetCommit", keyCommitID))
	return remote, hs, bs
}

// nestedStr reads a two-level string field ("" when absent or non-string).
func nestedStr(m map[string]any, outer, inner string) string {
	if o, ok := m[outer].(map[string]any); ok {
		return strOrEmpty(o, inner)
	}
	om, ok := m[outer].(*jq.Obj)
	if !ok {
		return ""
	}
	if s, has := om.Get(inner); has {
		if str, ok := s.(string); ok {
			return str
		}
	}
	return ""
}

func base64Token(tok string) string {
	return base64.StdEncoding.EncodeToString([]byte(":" + tok))
}

func splitAzureThreads(raw string) (inline, reviews []any) {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return []any{}, []any{}
	}
	list := jq.Elements(doc)
	if !noteObjectsValid(list) {
		return []any{}, []any{}
	}
	var in, rev []any
	for _, e := range list {
		if azureInline(e) {
			in = append(in, plainValue(e))
		} else {
			rev = append(rev, plainValue(e))
		}
	}
	if in == nil {
		in = []any{}
	}
	if rev == nil {
		rev = []any{}
	}
	return in, rev
}

// azureInline owns the inline-thread test: a `threadContext` member set.
func azureInline(e any) bool {
	m := objOrMap(e)
	if m == nil {
		return false
	}
	v, ok := m["threadContext"]
	return ok && v != nil
}

func writePRJSON(dir, kind, pr string, doc *jq.Obj, err error) error {
	if err != nil || doc == nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not normalize %s %s", kind, pr)}
	}
	if err := atomic.WriteFile(dir+suffixPRJSON, []byte(jq.PrettyOrdered(doc))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/PR.json: %v", dir, err)}
	}
	return nil
}

func writeProviderTOC(ctx2 *fetchRunCtx) error {
	total, statLines := providerStat(ctx2.dir + suffixPRJSON)
	withHunks, derr := diffFiles(ctx2.dir+suffixFullDiff, ctx2.dir)
	if derr != nil {
		return derr
	}
	listed := prFilePaths(ctx2.dir + suffixPRJSON)
	omitted := omittedPaths(listed, withHunks)
	title := fmt.Sprintf("# TOC for %s #%s (%s) — read ranges as coverage checklist", providerName(ctx2.dir), ctx2.pr, ctx2.repo)
	return writeTocRoles(ctx2, title, total, statLines, withHunks, omitted)
}

// providerName reads the bundle provider for the toc title.
func providerName(dir string) string {
	raw, err := os.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return providerGitHub
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return providerGitHub
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return providerGitHub
	}
	if p := jq.StrOf(m[keyProvider]); p != "" {
		return p
	}
	return providerGitHub
}

func providerStat(prJSON string) (string, []string) {
	total := "?"
	raw, err := os.ReadFile(prJSON)
	if err != nil {
		return total, nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return total, nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return total, nil
	}
	files := jq.Elements(m[keyFiles])
	total = strconv.Itoa(len(files))
	var lines []string
	for _, f := range files {
		fm := objOrMap(f)
		if fm == nil {
			continue
		}
		lines = append(lines, fileStatLine(jq.R(fm[keyPath]), numOrEmpty(fm[keyAdditions]), numOrEmpty(fm[keyDeletions])))
	}
	return total, lines
}

// numOrEmpty renders a stat count with `// 0` defaults via jq rendering.
func numOrEmpty(v any) any {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case jq.Number:
		return t
	case json.Number:
		return jq.Number(t.String())
	case float64, int, int64:
		return t
	default:
		return 0
	}
}

func writeTocRoles(ctx2 *fetchRunCtx, title, total string, statLines []string, withHunks, omitted []string) error {
	var sb strings.Builder
	sb.WriteString(title + "\n\n")
	sb.WriteString(diffHeaderIndex(ctx2.dir + suffixFullDiff))
	sb.WriteString("\n## stat\n")
	for _, l := range statLines {
		sb.WriteString(l + newline)
	}
	sb.WriteString("\n## coverage manifest\n")
	fmt.Fprintf(&sb, "files in PR: %s | files with diff hunks: %d\n", total, len(withHunks))
	if len(omitted) > 0 {
		sb.WriteString("omitted (no hunks — binary, renamed without changes, or mode-only):\n")
		for _, p := range omitted {
			sb.WriteString("- " + p + newline)
		}
	} else {
		sb.WriteString("omitted: (none)\n")
	}
	if werr := atomic.WriteFile(ctx2.dir+"/toc.txt", []byte(sb.String())); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/toc.txt: %v", ctx2.dir, werr)}
	}
	fmt.Fprintf(ctx2.runner.Human, "» roles -> %s/roles.txt (heuristic file roles for triage)\n", ctx2.dir)
	paths := prFilePaths(ctx2.dir + suffixPRJSON)
	if werr := atomic.WriteFile(ctx2.dir+"/roles.txt", []byte(rolesTextEmpty(paths))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/roles.txt: %v", ctx2.dir, werr)}
	}
	return nil
}

func normDoc(raw string) (map[string]any, error) {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return nil, internalErrorf("normalizer input unreadable: %v", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, internalErrorf("normalizer input is not an object")
	}
	return m, nil
}

// normStr reads a string field with fallbacks ("" when absent or
// non-string; hosts send strings, so exotic scalars read empty).
func normStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, has := m[k]; has {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// nestedMap reads a nested object field (nil when absent or non-object).
func nestedMap(m map[string]any, key string) map[string]any {
	if v, has := m[key]; has {
		if nm, ok := v.(map[string]any); ok {
			return nm
		}
		if om, ok := v.(*jq.Obj); ok {
			return objFields(om)
		}
	}
	return nil
}

func sumStats(files []any) (adds, dels float64) {
	for _, f := range files {
		var m map[string]any
		if om, ok := f.(*jq.Obj); ok {
			m = objFields(om)
		} else if pm, ok := f.(map[string]any); ok {
			m = pm
		} else {
			continue
		}
		adds += readcommon.NumOr(m[keyAdditions])
		dels += readcommon.NumOr(m[keyDeletions])
	}
	return adds, dels
}

func renderStats(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}

// prSpec is one provider's normalized PR values; buildPR emits the one
// ordered PR.json record from it.
type prSpec struct {
	Provider string
	Number   any
	Title    string
	Body     string
	BaseRef  string
	HeadRef  string
	BaseSHA  any
	HeadSHA  any
	Author   any
	URL      any
	State    string
	Draft    any
	Files    []any
}

// buildPR builds the single PR.json record: a nil draft reads false and a
// nil file slice reads [] so every provider emits both keys.
func buildPR(spec *prSpec) *jq.Obj {
	files := spec.Files
	if files == nil {
		files = []any{}
	}
	adds, dels := sumStats(files)
	draft := spec.Draft
	if draft == nil {
		draft = false
	}
	return jq.NewObj(
		keySchema, schemaV1,
		keyProvider, spec.Provider,
		keyNumber, spec.Number,
		keyTitle, spec.Title,
		keyBody, spec.Body,
		keyBaseRef, spec.BaseRef,
		keyHeadRef, spec.HeadRef,
		keyBaseSHA, spec.BaseSHA,
		keyHeadSHA, spec.HeadSHA,
		keyAuthor, spec.Author,
		keyURL, spec.URL,
		keyState, spec.State,
		keyDraft, draft,
		keyFiles, files,
		keyAdditions, renderStats(adds),
		keyDeletions, renderStats(dels),
	)
}

func prFileRow(path string, status, additions, deletions any) *jq.Obj {
	return jq.NewObj(keyPath, path, keyStatus, status,
		keyAdditions, additions, keyDeletions, deletions)
}

// fileRows normalizes a provider file list: non-object entries drop and an
// empty list stays [].
func fileRows(entries []any, row func(map[string]any) *jq.Obj) []any {
	files := make([]any, 0, len(entries))
	for _, e := range entries {
		if m := objOrMap(e); m != nil {
			files = append(files, row(m))
		}
	}
	return files
}

func elementsOrEmpty(v any) []any {
	if elems := jq.Elements(v); elems != nil {
		return elems
	}
	return []any{}
}

var providerStates = map[string]map[string]string{
	providerGitLab:    {"opened": stateOpen, stateClosed: stateClosed, stateMerged: stateMerged},
	providerGitCafe:   {stateOpen: stateOpen, stateClosed: stateClosed, stateMerged: stateMerged},
	providerBitbucket: {"OPEN": stateOpen, "MERGED": stateMerged, "DECLINED": stateClosed, "SUPERSEDED": stateClosed},
	providerAzure:     {stateActive: stateOpen, "abandoned": stateClosed, "completed": stateMerged},
}

// providerState folds a provider's wire state through providerStates; an
// unknown state reads "unknown".
func providerState(provider, state string) string {
	if mapped, ok := providerStates[provider][state]; ok {
		return mapped
	}
	return stateUnknown
}

func statusOrModified(v any) any {
	if v == nil {
		return statusModified
	}
	return v
}

func normalizeGitLab(metaRaw, changesRaw string, ns map[string]map[string]int64) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	diffRefs := nestedMap(meta, "diff_refs")
	headSHA := normStr(meta, "sha")
	if headSHA == "" {
		headSHA = normStr(diffRefs, keyHeadSHA)
	}
	return buildPR(&prSpec{
		Provider: providerGitLab, Number: firstPresent(meta["iid"], meta[keyID]),
		Title: normStr(meta, keyTitle), Body: normStr(meta, keyDescription),
		BaseRef: normStr(meta, "target_branch"), HeadRef: normStr(meta, "source_branch"),
		BaseSHA: normStr(diffRefs, keyBaseSHA, "start_sha"), HeadSHA: headSHA,
		Author: normStr(nestedMap(meta, keyAuthor), "username", "name"), URL: normStr(meta, "web_url"),
		State: providerState(providerGitLab, normStr(meta, keyState)), Draft: meta[keyDraft],
		Files: gitlabFileRows(changesRaw, ns),
	}), nil
}

func gitlabFileRows(changesRaw string, ns map[string]map[string]int64) []any {
	entries, ok := gitlabChangeEntries(changesRaw)
	if !ok {
		return []any{}
	}
	return fileRows(entries, func(fm map[string]any) *jq.Obj {
		return gitlabFileRow(fm, ns)
	})
}

func gitlabChangeEntries(raw string) ([]any, bool) {
	if doc, err := jq.Decode([]byte(raw)); err == nil {
		return gitlabChangePage(doc)
	}
	return gitlabChangeSlurp(raw)
}

// gitlabChangePage reads one decoded diffs document: a bare array is the page
// list, and the `changes` envelope wraps the same rows.
func gitlabChangePage(doc any) ([]any, bool) {
	if arr, ok := doc.([]any); ok {
		return arr, true
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, false
	}
	return elementsOrEmpty(m["changes"]), true
}

// gitlabChangeSlurp concatenates the per-page arrays of a paginated list; a
// stream holding anything but arrays is unreadable rather than partial.
func gitlabChangeSlurp(raw string) ([]any, bool) {
	docs, err := jq.DecodeSlurp(strings.NewReader(raw))
	if err != nil {
		return nil, false
	}
	var out []any
	for _, doc := range docs {
		arr, ok := doc.([]any)
		if !ok {
			return nil, false
		}
		out = append(out, arr...)
	}
	return out, true
}

func gitlabFileRow(fm map[string]any, ns map[string]map[string]int64) *jq.Obj {
	newPath := strOrEmpty(fm, "new_path")
	oldPath := strOrEmpty(fm, "old_path")
	path := newPath
	if path == devNullPath || path == "" {
		path = oldPath
	}
	status := statusModified
	switch {
	case !jq.Falsey(fm["new_file"]):
		status = "added"
	case !jq.Falsey(fm["deleted_file"]):
		status = "deleted"
	case !jq.Falsey(fm["renamed_file"]):
		status = "renamed"
	}
	var adds, dels int64
	if s, ok := ns[newPath]; ok {
		adds, dels = s[keyAdds], s[keyDels]
	} else if s, ok := ns[oldPath]; ok {
		adds, dels = s[keyAdds], s[keyDels]
	}
	return prFileRow(path, status, adds, dels)
}

func strOrEmpty(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func firstPresent(vals ...any) any {
	for _, v := range vals {
		if v != nil && v != false {
			return v
		}
	}
	return nil
}

func normalizeGitCafe(metaRaw, itemsRaw string) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	res := cafeResource(meta)
	return buildPR(&prSpec{
		Provider: providerGitCafe, Number: res[keyNumber],
		Title: normStr(res, keyTitle), Body: normStr(res, keyDescription),
		BaseRef: normStr(res, "targetBranch"), HeadRef: normStr(res, "sourceBranch"),
		BaseSHA: firstPresent(res["observedBaseOid"], res["baseOid"]), HeadSHA: firstPresent(res["sourceOid"], res["headOid"]),
		Author: cafeAuthor(res), URL: cafeURL(meta, res),
		State: providerState(providerGitCafe, normStr(res, keyState)), Draft: res[keyDraft],
		Files: fileRows(cafeItemsList(itemsRaw), func(m map[string]any) *jq.Obj { return entryFileRow(m, keyPath) }),
	}), nil
}

func cafeItemsList(itemsRaw string) []any {
	doc, err := jq.Decode([]byte(itemsRaw))
	if err != nil {
		return []any{}
	}
	if arr, ok := doc.([]any); ok {
		return arr
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return []any{}
	}
	data, _ := jq.AsMap(m[keyData])
	for _, src := range []map[string]any{data, m} {
		if it, has := src[keyItems]; has && it != nil {
			return elementsOrEmpty(it)
		}
	}
	return []any{}
}

func cafeResource(meta map[string]any) map[string]any {
	res := nestedMap(meta, keyData)
	if res == nil {
		if rm, ok := meta["resource"].(map[string]any); ok {
			return rm
		}
		return meta
	}
	if r, has := res["resource"]; has {
		if rm := objOrMap(r); rm != nil {
			return rm
		}
	}
	return res
}

func cafeAuthor(res map[string]any) string {
	for _, m := range []map[string]any{res, objOrMap(res[keyAuthor])} {
		for _, k := range []string{"handle", keyLogin, "displayName", "authorHandle"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func cafeURL(meta, res map[string]any) any {
	if d, ok := meta[keyData].(map[string]any); ok && d[keyURL] != nil {
		return d[keyURL]
	}
	return res[keyURL]
}

func normalizeForgejo(metaRaw, filesRaw string) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	base, head := nestedMap(meta, "base"), nestedMap(meta, "head")
	return buildPR(&prSpec{
		Provider: providerForgejo, Number: firstPresent(meta[keyIndex], meta[keyNumber]),
		Title: normStr(meta, keyTitle), Body: normStr(meta, keyBody),
		BaseRef: normStr(base, "label"), HeadRef: normStr(head, "label"),
		BaseSHA: normStr(base, "sha"), HeadSHA: normStr(head, "sha"),
		Author: normStr(nestedMap(meta, "user"), keyLogin), URL: normStr(meta, "html_url"),
		State: forgejoState(meta), Draft: forgejoDraft(meta),
		Files: fileRows(jsonArrayOrEmpty(filesRaw), func(m map[string]any) *jq.Obj { return entryFileRow(m, keyFilename) }),
	}), nil
}

func entryFileRow(m map[string]any, pathKey string) *jq.Obj {
	return prFileRow(normStr(m, pathKey), statusOrModified(m[keyStatus]),
		numOrEmpty(m[keyAdditions]), numOrEmpty(m[keyDeletions]))
}

func normalizeBitbucket(metaRaw string, ds []any) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	dest, src := nestedMap(meta, "destination"), nestedMap(meta, keySource)
	body := normStr(meta, keyDescription)
	if raw := normStr(nestedMap(meta, "summary"), "raw"); raw != "" {
		body = raw
	}
	return buildPR(&prSpec{
		Provider: providerBitbucket, Number: meta[keyID],
		Title: normStr(meta, keyTitle), Body: body,
		BaseRef: normStr(nestedMap(dest, modeBranch), "name"), HeadRef: normStr(nestedMap(src, modeBranch), "name"),
		BaseSHA: normStr(nestedMap(dest, "commit"), "hash"), HeadSHA: normStr(nestedMap(src, "commit"), "hash"),
		// The key chain stops at the first string (even ""), so nickname is
		// authoritative and display_name never wins.
		Author: normStr(nestedMap(meta, keyAuthor), "nickname"),
		URL:    normStr(nestedMap(nestedMap(meta, "links"), "html"), "href"),
		State:  providerState(providerBitbucket, normStr(meta, keyState)),
		Draft:  meta[keyDraft], Files: fileRows(ds, bitbucketFileRow),
	}), nil
}

func bitbucketFileRow(m map[string]any) *jq.Obj {
	path := normStr(nestedMap(m, "new"), keyPath)
	if path == "" {
		path = normStr(nestedMap(m, "old"), keyPath)
	}
	return prFileRow(path, statusOrModified(m[keyStatus]),
		numOrEmpty(m["lines_added"]), numOrEmpty(m["lines_removed"]))
}

func normalizeAzure(metaRaw, changesRaw string, ns map[string]map[string]int64) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	files := fileRows(jsonArrayOrEmpty(changesRaw), func(m map[string]any) *jq.Obj { return azureFileRow(m, ns) })
	return buildPR(&prSpec{
		Provider: providerAzure, Number: meta["pullRequestId"],
		Title: normStr(meta, keyTitle), Body: normStr(meta, keyDescription),
		BaseRef: strings.TrimPrefix(normStr(meta, "targetRefName"), "refs/heads/"),
		HeadRef: strings.TrimPrefix(normStr(meta, "sourceRefName"), "refs/heads/"),
		BaseSHA: normStr(nestedMap(meta, "lastMergeTargetCommit"), keyCommitID),
		HeadSHA: normStr(nestedMap(meta, "lastMergeSourceCommit"), keyCommitID),
		// The key chain stops at the first string (even ""), so uniqueName is
		// authoritative and displayName never wins.
		Author: normStr(nestedMap(meta, "createdBy"), "uniqueName"), URL: normStr(meta, keyURL),
		State: providerState(providerAzure, normStr(meta, keyStatus)),
		Draft: meta["isDraft"], Files: files,
	}), nil
}

func azureFileRow(m map[string]any, ns map[string]map[string]int64) *jq.Obj {
	p := strings.TrimPrefix(normStr(nestedMap(m, "item"), keyPath), pathSep)
	var adds, dels int64
	if s, ok := ns[p]; ok {
		adds, dels = s[keyAdds], s[keyDels]
	}
	return prFileRow(p, m["changeType"], adds, dels)
}
