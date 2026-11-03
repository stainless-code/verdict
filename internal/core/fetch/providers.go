package fetchcore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runProvider mirrors the non-GitHub fetch path: the provider bundle, the
// shared gates and writers, and the null-incremental tail.
func runProvider(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags, budget *budgetState,
) (*core.FetchData, string, error) {
	r, dir := ctx2.runner, ctx2.dir
	if err := fetchProviderBundle(ctx, ctx2, req, f.Provider); err != nil {
		return nil, "", err
	}
	if ref := resolveControls(ctx, ctx2, "", prField(dir, keyBaseSHA), f.HeadControls, budget, f.BudgetConsent); ref != nil {
		return nil, "", ref
	}
	fc, chunkActive, err := providerCap(r, dir, f.ChunkFlag)
	if err != nil {
		return nil, "", err
	}
	if err := writeProviderBundle(ctx2); err != nil {
		return nil, "", err
	}
	headSHA, baseSHA := prField(dir, keyHeadSHA), prField(dir, keyBaseSHA)
	chunkJSON, ref := maybeWriteChunks(r.Diag, dir, fc.maxBytes, chunkActive, headSHA, baseSHA, fc.source)
	if ref != nil {
		return nil, "", ref
	}
	return fetchTail(ctx2, req, f, budget, &fc, chunkJSON, nullStr)
}

// providerCap ports the provider cap gate and hunk index write.
func providerCap(r *session, dir string, chunkFlag bool) (fetchCap, bool, error) {
	fc, ref := resolveFetchCap(r)
	if ref != nil {
		return fc, false, ref
	}
	chunkActive, ref := checkDiffSize(r, dir+suffixFullDiff, fc, chunkFlag)
	if ref != nil {
		return fc, false, ref
	}
	if herr := buildHunksTSV(dir+suffixFullDiff, dir+"/hunks.tsv"); herr != nil {
		return fc, false, herr
	}
	return fc, chunkActive, nil
}

// writeProviderBundle ports the shared bundle stores after the diff gates.
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

// fetchProviderBundle mirrors fetch_provider_bundle: meta + diff + threads
// per provider, normalized to the PR.json schema post/report understand.
func fetchProviderBundle(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, provider string) error {
	switch provider {
	case "gitlab":
		return fetchGitLab(ctx, ctx2, req)
	case "gitcafe":
		return fetchGitCafe(ctx, ctx2, req)
	case "forgejo":
		return fetchForgejo(ctx, ctx2, req)
	case "bitbucket":
		return fetchBitbucket(ctx, ctx2, req)
	case "azure":
		return fetchAzure(ctx, ctx2, req)
	default:
		return &core.RefusalError{Code: "bad-provider", Message: fmt.Sprintf(
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
	meta, changes, err := gitlabReads(ctx, r, dir, repo, pr)
	if err != nil {
		return err
	}
	if err := gitlabDiff(ctx, r, dir, repo, pr); err != nil {
		return err
	}
	if err := gitlabThreads(ctx, r, dir, repo, pr); err != nil {
		return err
	}
	numstat := diffNumstatMap(ctx, r, dir+suffixFullDiff)
	doc, normErr := normalizeGitLab(meta, changes, numstat)
	return writePRJSON(dir, "GitLab MR", pr, doc, normErr)
}

// gitlabReads ports the GitLab view and changes reads: the MR metadata
// and the diffs-or-changes payload (empty on total failure).
func gitlabReads(ctx context.Context, r *session, dir, repo, pr string) (meta, changes string, err error) {
	metaRes, merr := r.cliFetch(ctx, binGlab, "mr", "view", pr, flagRepo, repo, "-F", keyJSON)
	if werr := writeErrFile(dir+suffixMetaErr, metaRes.Stderr); werr != nil {
		return "", "", werr
	}
	if merr != nil {
		return "", "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"glab mr view failed for %s!%s: %s (auth? iid?)", repo, pr, headBytes(metaRes.Stderr, numExcerpt))}
	}
	// The project id lookup encodes `repo\n`: `jq -sRr @uri <<<"$repo"`
	// slurps the herestring's trailing newline into the encoding.
	pidRes, perr := r.cliFetch(ctx, binGlab, ghCmdAPI, "projects/"+url.PathEscape(repo+newline), "--jq", ".id")
	if perr != nil {
		return "", "", &core.RefusalError{Code: CodeFetchFailed, Message: "cannot resolve GitLab project id for " + repo}
	}
	pid := strings.TrimRight(pidRes.Stdout, newline)
	return metaRes.Stdout, gitlabChanges(ctx, r, repo, pr, pid), nil
}

// gitlabChanges ports the diffs-or-changes read: diffs first, then
// changes, then the empty object the shell defaults to.
func gitlabChanges(ctx context.Context, r *session, _, pr, pid string) string {
	if res, err := r.cliFetch(ctx, binGlab, ghCmdAPI, "projects/"+pid+"/merge_requests/"+pr+"/diffs", flagPaginate); err == nil {
		return res.Stdout
	}
	if res, err := r.cliFetch(ctx, binGlab, ghCmdAPI, "projects/"+pid+"/merge_requests/"+pr+"/changes", flagPaginate); err == nil {
		return res.Stdout
	}
	return "{\"changes\":[]}\n"
}

// gitlabDiff ports the `glab mr diff` read with its raw flag.
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

// gitlabDiffFail ports the failed-diff fork: the empty truncation lands
// first, then the typed refusal carries glab's excerpt.
func gitlabDiffFail(dir, stderr string) error {
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	return &core.RefusalError{Code: CodeFetchFailed, Message: "glab mr diff failed: " + headBytes(stderr, numExcerpt)}
}

// gitlabThreads ports the note-list read and the DiffNote split.
func gitlabThreads(ctx context.Context, r *session, dir, repo, pr string) error {
	notes := emptyJSONArray
	if res, err := r.cliFetch(ctx, binGlab, "mr", "note", "list", pr, flagRepo, repo, "-F", keyJSON); err == nil {
		notes = res.Stdout
	}
	inlineDoc, reviewsDoc := splitGitLabNotes(notes)
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(jq.Pretty(inlineDoc))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(jq.Pretty(reviewsDoc))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	return nil
}

// splitGitLabNotes ports the DiffNote/non-DiffNote thread split. A scalar
// element fails the shell's jq outright (both files read []); nulls and
// objects split normally.
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

// gitlabNoteList ports the note-list shape gate: arrays pass, maps read
// their notes (or discussions) member, and anything else — including a
// scalar element, which fails the shell's jq the same way — reads failure.
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

// gitlabNoteMember ports the notes-then-discussions envelope read.
func gitlabNoteMember(m map[string]any) ([]any, bool) {
	if n, has := m["notes"]; has {
		return jq.Elements(n), true
	}
	if d, has := m["discussions"]; has {
		return jq.Elements(d), true
	}
	return nil, false
}

// noteArrayValid ports the note-array shape gate: nulls and objects pass,
// a scalar fails the shell's jq.
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

// isDiffNote ports `.type == noteKindDiff or any(.notes[]?; .type ==
// noteKindDiff)`.
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

// noteTypeDiff ports one nested note's type test (plain or ordered object).
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

// diffNumstatMap ports diff_numstat_map: `git apply --numstat` parsed into
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

// numOrZero ports `($1=="-"||$1=="")?0:$1` with `tonumber? // 0`: a
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
	revs := fetchCafePages(cafeReviewsPage(ctx, r, repo, pr))
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(revs)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	cmts := fetchCafePages(cafeCommentsPage(ctx, r, repo, pr))
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(cmts)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	doc, normErr := normalizeGitCafe(metaRes.Stdout, itemsRes.Stdout)
	return writePRJSON(dir, "GitCafe PR", pr, doc, normErr)
}

// cafeMeta ports the `cafe pr view` read with its stderr sidecar.
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

// cafeDiff ports the `cafe pr diff` read: the joined full.diff lands,
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

// cafeItems ports the items normalization: arrays pass, envelopes read
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

// envelopeItems ports `.data.items // .items`: the first present member at
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

// cafeFullDiff ports the cafe full.diff join: one `diff --git` header per
// file plus the patch when it carries hunks, joined without a trailing
// newline.
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

// cafeUnhandledHunks ports the structured-hunks count: patch without @@
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

// hasHunkHeader ports `grep -q '^@@'` over the diff.
func hasHunkHeader(diff string) bool {
	for _, line := range splitLines(diff) {
		if strings.HasPrefix(line, "@@") {
			return true
		}
	}
	return false
}

// fetchCafePages ports the 20-page cursor loop: items accumulate across
// pages and render jq-pretty without a trailing newline (printf %s). A
// failed call feeds '{}'; undecodable output breaks the loop leaving the
// accumulator empty, like the shell's failed --argjson.
func fetchCafePages(call func(cursor string) (core.Result, error)) string {
	acc, ok := cafeAccumulate(call)
	if !ok {
		return ""
	}
	if acc == nil {
		acc = []any{}
	}
	return strings.TrimSuffix(jq.Pretty(plainAll(acc)), newline)
}

// cafeAccumulate ports the cursor walk; ok=false on an undecodable page.
func cafeAccumulate(call func(cursor string) (core.Result, error)) ([]any, bool) {
	var acc []any
	cursor := ""
	for range cafeMaxPages {
		page := cafePage(call, cursor)
		items, ok := cafePageItems(page)
		if !ok {
			return nil, false
		}
		acc = append(acc, items...)
		if cursor = cafeCursor(page); cursor == "" {
			return acc, true
		}
	}
	return acc, true
}

// cafePage ports one page read: a failed call feeds '{}'.
func cafePage(call func(cursor string) (core.Result, error), cursor string) string {
	if res, err := call(cursor); err == nil {
		return res.Stdout
	}
	return "{}"
}

// cafePageItems ports `jq '.data.items // .items // []'`: the page items,
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

// cafeCursor ports `jq -r '.data.next // .next // empty'`: the cursor, or
// "" when absent.
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

// cursorNext ports `.data.next // .next // empty`.
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

// cafeNext ports the cursor-member read: a string passes through, any
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

// fetchForgejo mirrors the forgejo backend.
// curlArgs builds a curl argv with the silent-fail flags and auth first.
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
	if err := forgejoThreads(ctx, r, dir, auth, api); err != nil {
		return err
	}
	doc, normErr := normalizeForgejo(metaRes.Stdout, files)
	return writePRJSON(dir, "Forgejo PR", pr, doc, normErr)
}

// forgejoAuth ports the host/token gate: both set, or a typed refusal
// naming the missing piece.
func forgejoAuth(ctx2 *fetchRunCtx) (auth []string, api string, err error) {
	host, _ := ctx2.env["FORGEJO_HOST"]
	token, _ := ctx2.env["FORGEJO_TOKEN"]
	if host == "" {
		return nil, "", &core.RefusalError{Code: codeAuthMissing, Message: "forgejo needs FORGEJO_HOST=https://host and FORGEJO_TOKEN set"}
	}
	if token == "" {
		return nil, "", &core.RefusalError{Code: codeAuthMissing, Message: "forgejo needs FORGEJO_TOKEN set (app token with repo read)"}
	}
	repo, pr := ctx2.repo, ctx2.pr
	return []string{flagH, "Authorization: token " + token}, host + "/api/v1/repos/" + repo + "/pulls/" + pr, nil
}

// forgejoMeta ports the PR read with its stderr sidecar.
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

// forgejoDiff ports the diff read: the .diff endpoint first, then the
// metadata diff_url fallback.
func forgejoDiff(ctx context.Context, r *session, dir string, auth []string, api, metaStdout string) error {
	fmt.Fprintf(r.Human, "» forgejo diff -> %s/full.diff\n", dir)
	diffRes, err := r.curlFetch(ctx, curlArgs(auth, api+".diff")...)
	if err != nil {
		return forgejoDiffFallback(ctx, r, dir, auth, metaStdout)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// forgejoDiffURL ports the diff_url read: the metadata fallback URL,
// empty when absent (curl then fails the same way).
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

// forgejoDiffFallback ports the metadata-URL fallback with its stderr
// sidecar and empty-diff truncation on total failure.
func forgejoDiffFallback(ctx context.Context, r *session, dir string, auth []string, metaStdout string) error {
	diffRes, err := r.curlFetch(ctx, curlArgs(auth, forgejoDiffURL(metaStdout))...)
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

// forgejoFiles ports the file-list read, defaulting to [] on failure.
func forgejoFiles(ctx context.Context, r *session, auth []string, api string) string {
	if res, err := r.curlFetch(ctx, curlArgs(auth, api+"/files")...); err == nil {
		return res.Stdout
	}
	return emptyJSONArray
}

// forgejoThreads ports the review-thread reads: reviews land raw and the
// per-review comments concatenate, defaulting to [] on failure.
func forgejoThreads(ctx context.Context, r *session, dir string, auth []string, api string) error {
	revs := emptyJSONArray
	if res, err := r.curlFetch(ctx, curlArgs(auth, api+"/reviews")...); err == nil {
		revs = res.Stdout
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(revs)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	inline, ok := forgejoInline(ctx, r, api, auth, revs)
	if !ok {
		inline = emptyJSONArray
	}
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(inline)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	return nil
}

// forgejoInline ports the per-review comments concat: each review id's
// comments fetched and `jq -s 'add // []'`-merged (pretty, trailing
// newline). ok=false when any piece is undecodable, like the shell's jq
// failure falling back to "[]".
func forgejoInline(ctx context.Context, r *session, api string, auth []string, revs string) (string, bool) {
	doc, err := jq.Decode([]byte(revs))
	if err != nil {
		// Undecodable revs feed no ids; the empty stream still merges
		// to [], like the shell's `jq -s 'add // []'` over no input.
		return jq.Pretty([]any{}), true
	}
	var ids []string
	for _, e := range forgejoItems(doc) {
		ids = append(ids, forgejoID(e))
	}
	docs, ok := forgejoCommentDocs(ctx, r, api, auth, ids)
	if !ok {
		return "", false
	}
	merged, ok := mergeAdd(docs)
	if !ok {
		return "", false
	}
	return jq.Pretty(merged), true
}

// forgejoItems ports the `.[]` read: array elements pass through and
// object values iterate in sorted-key order, like jq.
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

// forgejoCommentDocs ports the per-review comments concat: each review
// id's comments decode and merge, failing the run on garbage.
func forgejoCommentDocs(ctx context.Context, r *session, api string, auth, ids []string) ([]any, bool) {
	var docs []any
	for _, id := range ids {
		res, err := r.curlFetch(ctx, curlArgs(auth, api+"/reviews/"+id+"/comments")...)
		part := "[]"
		if err == nil {
			part = res.Stdout
		}
		doc, err := jq.Decode([]byte(part))
		if err != nil {
			return nil, false
		}
		docs = append(docs, plainValue(doc))
	}
	return docs, true
}

// forgejoID ports `jq -r '.[].id'`: the review id rendered for the URL.
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

// forgejoFilesList ports the files shape gate: arrays pass, anything
// else reads empty.
func forgejoFilesList(filesRaw string) []any {
	var files []any
	if doc, err := jq.Decode([]byte(filesRaw)); err == nil {
		if arr, ok := doc.([]any); ok {
			files = arr
		}
	}
	if files == nil {
		files = []any{}
	}
	return files
}

// forgejoState ports the state fold: merged wins, open/closed pass, and
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

// forgejoDraft ports the draft read: the member wins, else the WIP title
// markers decide.
func forgejoDraft(meta map[string]any) any {
	draft := meta[keyDraft]
	if draft == nil {
		title := normStr(meta, keyTitle)
		return strings.HasPrefix(title, "WIP:") || strings.HasPrefix(title, "[DRAFT]")
	}
	return draft
}

// mergeAdd ports `jq -s 'add // []'`: arrays concatenate across pieces;
// an empty stream reads []. Anything else fails the filter (the shell
// falls back to "[]"), because jq's add errors on mixed shapes.
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

// fetchBitbucket mirrors the bitbucket backend.
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
	ds, ref := bitbucketThreads(ctx, r, dir, auth, base)
	if ref != nil {
		return ref
	}
	doc, normErr := normalizeBitbucket(meta, ds)
	return writePRJSON(dir, "Bitbucket PR", pr, doc, normErr)
}

// bitbucketAuth ports the token/user-password gate.
func bitbucketAuth(ctx2 *fetchRunCtx) ([]string, *core.RefusalError) {
	token, _ := ctx2.env["BITBUCKET_TOKEN"]
	user, _ := ctx2.env["BITBUCKET_USERNAME"]
	pass, _ := ctx2.env["BITBUCKET_PASSWORD"]
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

// bitbucketMeta ports the PR read with its stderr sidecar.
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

// bitbucketDiff ports the diff read: stderr sidecar, empty truncation, and
// the full.diff write.
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

// bitbucketThreads ports the diffstat/comments reads and the split writes,
// returning the diffstat entries for normalization.
func bitbucketThreads(ctx context.Context, r *session, dir string, auth []string, base string) ([]any, *core.RefusalError) {
	ds := bitbucketPages(ctx, r, auth, base+"/diffstat?pagelen=100", "values")
	cm := bitbucketPages(ctx, r, auth, base+"/comments?pagelen=100", "values")
	inline, reviews := splitBitbucketComments(cm)
	if err := atomic.WriteFile(dir+suffixInlineJSON, []byte(jq.Pretty(plainAll(inline)))); err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, err)}
	}
	if err := atomic.WriteFile(dir+suffixReviewsJSON, []byte(jq.Pretty(plainAll(reviews)))); err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, err)}
	}
	return ds, nil
}

// bitbucketPages ports the pagelen loop: `curl -sfL` pages decoded with
// `.values // []`, concatenated; failed pages feed '{}' and `.next // empty`
// ends the walk.
func bitbucketPages(ctx context.Context, r *session, auth []string, first, field string) []any {
	var out []any
	for u := first; u != "" && u != nullStr; {
		m, ok := bitbucketPage(ctx, r, auth, u)
		if !ok {
			break
		}
		out = append(out, jq.Elements(m[field])...)
		u = bitbucketNext(m)
	}
	if out == nil {
		out = []any{}
	}
	return out
}

// bitbucketPage fetches and decodes one page (ok=false on bad JSON).
func bitbucketPage(ctx context.Context, r *session, auth []string, u string) (map[string]any, bool) {
	page := "{}"
	if res, err := r.curlFetch(ctx, append(append([]string{"-sfL"}, auth...), u)...); err == nil {
		page = res.Stdout
	}
	doc, err := jq.Decode([]byte(page))
	if err != nil {
		return nil, false
	}
	m, _ := jq.AsMap(doc)
	return m, true
}

// bitbucketNext ports `.next // empty` over a decoded page.
func bitbucketNext(m map[string]any) string {
	if next, has := m["next"]; has && next != nil {
		return jq.R(next)
	}
	return ""
}

// splitBitbucketComments ports the inline/non-inline comment split. A
// scalar element fails the shell's jq outright (both files read []).
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

// noteObjectsValid ports the element shape gate: a non-object element
// fails the shell's jq the same way, reading both files empty.
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

// bitbucketInline ports the inline-thread test: an `inline` member set.
func bitbucketInline(e any) bool {
	m := objOrMap(e)
	if m == nil {
		return false
	}
	_, present := m["inline"]
	return present && m["inline"] != nil
}

// fetchAzure mirrors the azure backend.
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

// azureSetup ports the Azure preamble: org gate, metadata read,
// merge-ref gate, and token resolution.
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
	tok, err := azureToken(ctx, r, ctx2)
	if err != nil {
		return conn, err
	}
	return azureConn{org: org, project: project, rname: rname, tok: tok, meta: metaRes.Stdout}, nil
}

// azureMergeMissing ports the merge-ref gate: all three present, or the
// PR has nothing to fetch.
func azureMergeMissing(remote, hs, bs string) bool {
	return remote == "" || hs == "" || bs == ""
}

// azureToken ports the token resolution: the PAT env wins, else the az
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

// azureCloneInit ports the temp clone setup: init plus the Basic-auth
// config header, failing typed when the clone cannot start.
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

// azureCloneAuth ports the clone auth header: the PAT as a Basic header
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

// azureCloneDiff ports the clone fetch and diff: the remote heads land,
// then the merge-base diff becomes full.diff.
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

// azureDiffFail ports the failed-diff fork: the empty truncation lands
// first, then the typed refusal.
func azureDiffFail(dir, stderr string) error {
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	return &core.RefusalError{Code: CodeFetchFailed, Message: "azure diff failed: " + headBytes(stderr, numExcerpt)}
}

// azureAPI builds one Azure REST argv: GET on the repo URL with a jq
// selector and output format.
func azureAPI(org, project, rname, pr, path, query, format string) []string {
	return []string{
		"rest", "--method", "get",
		"--url", org + pathSep + project + "/_apis/git/repositories/" + rname + "/pullRequests/" + pr + path,
		flagQuery, query, flagOut, format,
	}
}

// azureThreads ports the thread read and split: threads land as inline
// and review files.
func azureThreads(ctx context.Context, r *session, dir string, conn *azureConn, pr string) error {
	threads := emptyJSONArray
	args := azureAPI(conn.org, conn.project, conn.rname, pr, "/threads?api-version=7.1", "value", keyJSON)
	if res, err := r.cliFetch(ctx, binAz, args...); err == nil {
		threads = res.Stdout
	}
	inline, reviews := splitAzureThreads(threads)
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(jq.Pretty(plainAll(inline)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(jq.Pretty(plainAll(reviews)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	return nil
}

// azureChanges ports the iteration-changes read: the latest iteration id
// selects the change entries, defaulting to [] on failure.
func azureChanges(ctx context.Context, r *session, conn *azureConn, pr string) string {
	iid := ""
	iterArgs := azureAPI(conn.org, conn.project, conn.rname, pr, "/iterations?api-version=7.1",
		"max_by(value, &id).id", "tsv")
	if res, err := r.cliFetch(ctx, binAz, iterArgs...); err == nil {
		iid = strings.TrimRight(res.Stdout, newline)
	}
	changes := emptyJSONArray
	if iid != "" {
		changeArgs := azureAPI(conn.org, conn.project, conn.rname, pr,
			"/iterations/"+iid+"/changes?api-version=7.1", "changeEntries", keyJSON)
		if res, err := r.cliFetch(ctx, binAz, changeArgs...); err == nil {
			changes = res.Stdout
		}
	}
	return changes
}

// azureMergeRef ports the merge-commit/remote read: empty when absent.
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
	return nestedObjStr(m[outer], inner)
}

// nestedObjStr reads a string member from an ordered object.
func nestedObjStr(v any, inner string) string {
	om, ok := v.(*jq.Obj)
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

// base64Token ports `printf ':%s' | base64 | tr -d '\n'`.
func base64Token(tok string) string {
	return base64.StdEncoding.EncodeToString([]byte(":" + tok))
}

// splitAzureThreads ports the threadContext split (null reads the reviews
// file). A scalar element fails the shell's jq outright (both read []).
func splitAzureThreads(threads string) (inline, reviews []any) {
	doc, err := jq.Decode([]byte(threads))
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

// azureInline ports the inline-thread test: a `threadContext` member set.
func azureInline(e any) bool {
	m := objOrMap(e)
	if m == nil {
		return false
	}
	v, ok := m["threadContext"]
	return ok && v != nil
}

// writePRJSON ports the `normalize_* > PR.json` step: the normalized
// record rendered jq-pretty with a trailing newline. A normalization
// failure refuses fetch-failed, naming the PR (or MR for GitLab).
func writePRJSON(dir, kind, pr string, doc *jq.Obj, err error) error {
	if err != nil || doc == nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not normalize %s %s", kind, pr)}
	}
	if err := atomic.WriteFile(dir+suffixPRJSON, []byte(jq.PrettyOrdered(doc))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/PR.json: %v", dir, err)}
	}
	return nil
}

// writeProviderTOC ports the provider write_toc_roles call.
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

// providerStat ports the provider stat block: total plus the
// `path (+adds/-dels)` lines with `// 0` defaults.
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

// writeTocRoles ports write_toc_roles with an explicit title line: the
// diff header index, the stat block, and the coverage manifest, then the
// roles file with the `» roles` notice.
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

// normDoc decodes a normalizer input, refusing fetch-failed on garbage
// (the shell's normalize jq fails the same way).
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
// non-string; the shell keeps exotic scalars, but hosts send strings).
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

// sumStats sums the additions/deletions of normalized file entries.
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
		adds += numFloat(m[keyAdditions])
		dels += numFloat(m[keyDeletions])
	}
	return adds, dels
}

// renderStats renders a float sum the way jq's add does.
func renderStats(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}

// normalizeGitLab ports normalize_gitlab.
func normalizeGitLab(metaRaw, changesRaw string, ns map[string]map[string]int64) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	var fl []any
	for _, f := range gitlabFilesList(changesRaw) {
		fm := objOrMap(f)
		if fm == nil {
			continue
		}
		fl = append(fl, gitlabFileEntry(fm, ns))
	}
	if fl == nil {
		fl = []any{}
	}
	adds, dels := sumStats(plainAll(fl))
	diffRefs := nestedMap(meta, "diff_refs")
	author := nestedMap(meta, keyAuthor)
	obj := jq.NewObj(
		keySchema, schemaV1,
		keyProvider, "gitlab",
		keyNumber, firstPresent(meta["iid"], meta[keyID]),
		keyTitle, normStr(meta, keyTitle),
		keyBody, normStr(meta, keyDescription),
		keyBaseRef, normStr(meta, "target_branch"),
		keyHeadRef, normStr(meta, "source_branch"),
		keyBaseSHA, normStr(diffRefs, keyBaseSHA, "start_sha"),
		keyHeadSHA, normStr(meta, "sha"),
		keyAuthor, normStr(author, "username", "name"),
		keyURL, normStr(meta, "web_url"),
		keyState, gitlabState(normStr(meta, keyState)),
		keyDraft, boolOrFalse(meta[keyDraft]),
		keyFiles, fl,
		keyAdditions, renderStats(adds),
		keyDeletions, renderStats(dels),
	)
	gitlabHeadFallback(obj, diffRefs)
	return obj, nil
}

// gitlabFilesList ports the changes shape gate: arrays pass, maps read
// their changes member, and anything else reads empty.
func gitlabFilesList(changesRaw string) []any {
	doc, err := jq.Decode([]byte(changesRaw))
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
	if ch, has := m["changes"]; has && ch != nil {
		return jq.Elements(ch)
	}
	return []any{}
}

// gitlabFileEntry ports one normalized file row: path, status, and the
// numstat counts keyed by new (then old) path.
func gitlabFileEntry(fm map[string]any, ns map[string]map[string]int64) any {
	newPath := strOrEmpty(fm, "new_path")
	oldPath := strOrEmpty(fm, "old_path")
	path := newPath
	if path == devNullPath || path == "" {
		path = oldPath
	}
	status := statusModified
	switch {
	case truthy(fm["new_file"]):
		status = "added"
	case truthy(fm["deleted_file"]):
		status = "deleted"
	case truthy(fm["renamed_file"]):
		status = "renamed"
	}
	var adds, dels int64
	if s, ok := ns[newPath]; ok {
		adds, dels = s[keyAdds], s[keyDels]
	} else if s, ok := ns[oldPath]; ok {
		adds, dels = s[keyAdds], s[keyDels]
	}
	return jq.NewObj(keyPath, path, keyStatus, status, keyAdditions, adds, keyDeletions, dels)
}

// gitlabHeadFallback ports the head_sha fallback: diff_refs head_sha
// wins when the record head is empty.
func gitlabHeadFallback(obj *jq.Obj, diffRefs map[string]any) {
	if hs := objHeadSHA(obj); hs == "" {
		if diffRefs != nil {
			if s := normStr(diffRefs, keyHeadSHA); s != "" {
				obj.Set(keyHeadSHA, s)
			}
		}
	}
}

// strOrEmpty reads a string field, defaulting to "" (the shell's `// ""`).
func strOrEmpty(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// truthy ports jq truthiness (only null and false are false).
func truthy(v any) bool {
	return v != nil && v != false
}

// firstPresent ports `a // b` for values (null/missing/false fall
// through, matching the record port's Alt).
func firstPresent(vals ...any) any {
	for _, v := range vals {
		if v != nil && v != false {
			return v
		}
	}
	return nil
}

// boolOrFalse ports `// false` for draft flags.
func boolOrFalse(v any) any {
	if v == nil {
		return false
	}
	return v
}

// gitlabState ports the gitlab state map.
func gitlabState(s string) string {
	switch s {
	case "opened":
		return stateOpen
	case stateClosed:
		return stateClosed
	case stateMerged:
		return stateMerged
	default:
		return stateUnknown
	}
}

// objHeadSHA reads head_sha from a built record.
func objHeadSHA(obj *jq.Obj) string {
	if v, has := obj.Get(keyHeadSHA); has {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// normalizeGitCafe ports normalize_gitcafe.
func normalizeGitCafe(metaRaw, itemsRaw string) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	items := cafeItemsList(itemsRaw)
	res := cafeResource(meta)
	var fl []any
	for _, e := range items {
		var m map[string]any
		if pm, ok := e.(map[string]any); ok {
			m = pm
		} else if om, ok := e.(*jq.Obj); ok {
			m = objFields(om)
		} else {
			continue
		}
		fl = append(fl, jq.NewObj(
			keyPath, normStr(m, keyPath),
			keyStatus, cafeStatus(m[keyStatus]),
			keyAdditions, numOrEmpty(m[keyAdditions]),
			keyDeletions, numOrEmpty(m[keyDeletions]),
		))
	}
	if fl == nil {
		fl = []any{}
	}
	adds, dels := sumStats(plainAll(fl))
	return jq.NewObj(
		keySchema, schemaV1,
		keyProvider, "gitcafe",
		keyNumber, res[keyNumber],
		keyTitle, normStr(res, keyTitle),
		keyBody, normStr(res, keyDescription),
		keyBaseRef, normStr(res, "targetBranch"),
		keyHeadRef, normStr(res, "sourceBranch"),
		keyBaseSHA, firstPresent(res["observedBaseOid"], res["baseOid"]),
		keyHeadSHA, firstPresent(res["sourceOid"], res["headOid"]),
		keyAuthor, cafeAuthor(res),
		keyURL, firstPresent(nestedURL(meta, res), nil),
		keyState, cafeState(normStr(res, keyState)),
		keyDraft, boolOrFalse(res[keyDraft]),
		keyFiles, fl,
		keyAdditions, renderStats(adds),
		keyDeletions, renderStats(dels),
	), nil
}

// cafeItemsList ports the items shape gate: arrays pass, maps read
// .data.items (then .items), and anything else reads empty.
func cafeItemsList(itemsRaw string) []any {
	var items []any
	if doc, err := jq.Decode([]byte(itemsRaw)); err == nil {
		if arr, ok := doc.([]any); ok {
			items = arr
		} else if m, ok := jq.AsMap(doc); ok {
			items = cafeItemsFromMap(m)
		}
	}
	if items == nil {
		items = []any{}
	}
	return items
}

// cafeItemsFromMap ports the map shape: .data.items wins when present
// (even when empty), else the top .items member.
func cafeItemsFromMap(m map[string]any) []any {
	if items, ok := cafeDataItems(m); ok {
		return items
	}
	if top, has := m[keyItems]; has && top != nil {
		return jq.Elements(top)
	}
	return []any{}
}

// cafeDataItems ports the .data.items read, reporting whether the member
// matched (an explicitly empty list still wins).
func cafeDataItems(m map[string]any) ([]any, bool) {
	d, has := m[keyData]
	if !has {
		return nil, false
	}
	dm, ok := jq.AsMap(d)
	if !ok {
		return nil, false
	}
	it, has := dm[keyItems]
	if !has || it == nil {
		return nil, false
	}
	return jq.Elements(it), true
}

// cafeResource ports the resource shape gate: .data (or .data.resource)
// unwraps to the record, else the top object wins.
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

// cafeStatus ports `(.status // statusModified)`.
func cafeStatus(v any) any {
	if v == nil {
		return statusModified
	}
	return v
}

// cafeAuthor ports the author handle chain.
func cafeAuthor(res map[string]any) string {
	if res == nil {
		return ""
	}
	if s := cafeHandle(res); s != "" {
		return s
	}
	if a, has := res[keyAuthor]; has {
		if am, ok := a.(map[string]any); ok {
			return cafeHandle(am)
		}
	}
	return ""
}

// cafeHandle reads the first non-empty handle-ish member: the shell's
// handle/login/displayName chain.
func cafeHandle(m map[string]any) string {
	for _, k := range []string{"handle", keyLogin, "displayName", "authorHandle"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// nestedURL ports `($m.data.url // $r.url // null)`.
func nestedURL(meta, res map[string]any) any {
	if d, has := meta[keyData]; has {
		if u, found := memberURL(d); found {
			return u
		}
	}
	if u, has := res[keyURL]; has && u != nil {
		return u
	}
	return nil
}

// memberURL reads a url member from a plain object (found=false when absent).
func memberURL(d any) (any, bool) {
	dm, ok := d.(map[string]any)
	if !ok {
		return nil, false
	}
	u, has := dm[keyURL]
	if !has || u == nil {
		return nil, false
	}
	return u, true
}

// cafeState ports the gitcafe state gate.
func cafeState(s string) string {
	switch s {
	case stateOpen, stateClosed, stateMerged:
		return s
	default:
		return stateUnknown
	}
}

// normalizeForgejo ports normalize_forgejo.
func normalizeForgejo(metaRaw, filesRaw string) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	files := forgejoFilesList(filesRaw)
	var fl []any
	for _, e := range files {
		var m map[string]any
		if pm, ok := e.(map[string]any); ok {
			m = pm
		} else if om, ok := e.(*jq.Obj); ok {
			m = objFields(om)
		} else {
			continue
		}
		fl = append(fl, jq.NewObj(
			keyPath, normStr(m, keyFilename),
			keyStatus, forgejoStatus(m[keyStatus]),
			keyAdditions, numOrEmpty(m[keyAdditions]),
			keyDeletions, numOrEmpty(m[keyDeletions]),
		))
	}
	if fl == nil {
		fl = []any{}
	}
	adds, dels := sumStats(plainAll(fl))
	base := nestedMap(meta, "base")
	head := nestedMap(meta, "head")
	user := nestedMap(meta, "user")
	state := forgejoState(meta)
	draft := forgejoDraft(meta)
	return jq.NewObj(
		keySchema, schemaV1,
		keyProvider, "forgejo",
		keyNumber, firstPresent(meta[keyIndex], meta[keyNumber]),
		keyTitle, normStr(meta, keyTitle),
		keyBody, normStr(meta, keyBody),
		keyBaseRef, normStr(base, "label"),
		keyHeadRef, normStr(head, "label"),
		keyBaseSHA, normStr(base, "sha"),
		keyHeadSHA, normStr(head, "sha"),
		keyAuthor, normStr(user, keyLogin),
		keyURL, normStr(meta, "html_url"),
		keyState, state,
		keyDraft, draft,
		keyFiles, fl,
		keyAdditions, renderStats(adds),
		keyDeletions, renderStats(dels),
	), nil
}

// forgejoStatus ports `(.status // statusModified)`.
func forgejoStatus(v any) any {
	if v == nil {
		return statusModified
	}
	return v
}

// normalizeBitbucket ports normalize_bitbucket.
func normalizeBitbucket(metaRaw string, ds []any) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	fl := bitbucketFileRows(ds)
	adds, dels := sumStats(plainAll(fl))
	destBranch, srcBranch, destCommit, srcCommit := bitbucketRefs(meta)
	author := nestedMap(meta, keyAuthor)
	return jq.NewObj(
		keySchema, schemaV1,
		keyProvider, "bitbucket",
		keyNumber, meta[keyID],
		keyTitle, normStr(meta, keyTitle),
		keyBody, bitbucketBody(meta),
		keyBaseRef, destBranch,
		keyHeadRef, srcBranch,
		keyBaseSHA, destCommit,
		keyHeadSHA, srcCommit,
		keyAuthor, firstPresent(authorString(author, "nickname"), authorString(author, "display_name")),
		keyURL, normStr(nestedMap(nestedMap(meta, "links"), "html"), "href"),
		keyState, bitbucketState(normStr(meta, keyState)),
		keyDraft, boolOrFalse(meta[keyDraft]),
		keyFiles, fl,
		keyAdditions, renderStats(adds),
		keyDeletions, renderStats(dels),
	), nil
}

// bitbucketFileRows ports the file list: path from new then old, status
// and line counts from the diffstat entry.
func bitbucketFileRows(ds []any) []any {
	var fl []any
	for _, e := range ds {
		m := objOrMap(e)
		if m == nil {
			continue
		}
		fl = append(fl, jq.NewObj(
			keyPath, bitbucketEntryPath(m),
			keyStatus, bitbucketStatus(m[keyStatus]),
			keyAdditions, numOrEmpty(m["lines_added"]),
			keyDeletions, numOrEmpty(m["lines_removed"]),
		))
	}
	if fl == nil {
		fl = []any{}
	}
	return fl
}

// bitbucketEntryPath ports `new.path // old.path // ""`.
func bitbucketEntryPath(m map[string]any) string {
	if p := normStr(nestedMap(m, "new"), keyPath); p != "" {
		return p
	}
	return normStr(nestedMap(m, "old"), keyPath)
}

// bitbucketRefs ports the destination/source branch and commit reads.
func bitbucketRefs(meta map[string]any) (destBranch, srcBranch, destCommit, srcCommit string) {
	if dest := nestedMap(meta, "destination"); dest != nil {
		destBranch = normStr(nestedMap(dest, modeBranch), "name")
		destCommit = normStr(nestedMap(dest, "commit"), "hash")
	}
	if src := nestedMap(meta, keySource); src != nil {
		srcBranch = normStr(nestedMap(src, modeBranch), "name")
		srcCommit = normStr(nestedMap(src, "commit"), "hash")
	}
	return destBranch, srcBranch, destCommit, srcCommit
}

// bitbucketStatus ports `(.status // statusModified)`.
func bitbucketStatus(v any) any {
	if v == nil {
		return statusModified
	}
	return v
}

// bitbucketBody ports `.summary.raw // .description // ""`.
func bitbucketBody(meta map[string]any) string {
	if s := nestedMap(meta, "summary"); s != nil {
		if r := normStr(s, "raw"); r != "" {
			return r
		}
	}
	return normStr(meta, keyDescription)
}

// authorString reads one author string field ("" when absent).
func authorString(author map[string]any, key string) string {
	if author == nil {
		return ""
	}
	if s, ok := author[key].(string); ok {
		return s
	}
	return ""
}

// bitbucketState ports the bitbucket state map.
func bitbucketState(s string) string {
	switch s {
	case "OPEN":
		return stateOpen
	case "MERGED":
		return stateMerged
	case "DECLINED", "SUPERSEDED":
		return stateClosed
	default:
		return stateUnknown
	}
}

// normalizeAzure ports normalize_azure.
func normalizeAzure(metaRaw, changesRaw string, ns map[string]map[string]int64) (*jq.Obj, error) {
	meta, err := normDoc(metaRaw)
	if err != nil {
		return nil, err
	}
	fl := azureFileRows(changesRaw, ns)
	adds, dels := sumStats(plainAll(fl))
	created := nestedMap(meta, "createdBy")
	return jq.NewObj(
		keySchema, schemaV1,
		keyProvider, "azure",
		keyNumber, meta["pullRequestId"],
		keyTitle, normStr(meta, keyTitle),
		keyBody, normStr(meta, keyDescription),
		keyBaseRef, strings.TrimPrefix(normStr(meta, "targetRefName"), "refs/heads/"),
		keyHeadRef, strings.TrimPrefix(normStr(meta, "sourceRefName"), "refs/heads/"),
		keyBaseSHA, normStr(nestedMap(meta, "lastMergeTargetCommit"), keyCommitID),
		keyHeadSHA, normStr(nestedMap(meta, "lastMergeSourceCommit"), keyCommitID),
		keyAuthor, firstPresent(authorString(created, "uniqueName"), authorString(created, "displayName")),
		keyURL, normStr(meta, keyURL),
		keyState, azureState(normStr(meta, keyStatus)),
		keyDraft, boolOrFalse(meta["isDraft"]),
		keyFiles, fl,
		keyAdditions, renderStats(adds),
		keyDeletions, renderStats(dels),
	), nil
}

// azureChangesEntries ports the iteration-changes shape gate: an array
// passes, anything else reads empty.
func azureChangesEntries(changesRaw string) []any {
	doc, err := jq.Decode([]byte(changesRaw))
	if err != nil {
		return []any{}
	}
	arr, ok := doc.([]any)
	if !ok {
		return []any{}
	}
	return arr
}

// azureFileRows ports the change entries: item.path with numstat counts.
func azureFileRows(changesRaw string, ns map[string]map[string]int64) []any {
	var fl []any
	for _, e := range azureChangesEntries(changesRaw) {
		m := objOrMap(e)
		if m == nil {
			continue
		}
		p := strings.TrimPrefix(normStr(nestedMap(m, "item"), keyPath), pathSep)
		var adds, dels int64
		if s, ok := ns[p]; ok {
			adds, dels = s[keyAdds], s[keyDels]
		}
		fl = append(fl, jq.NewObj(
			keyPath, p,
			keyStatus, azureStatus(m["changeType"]),
			keyAdditions, adds,
			keyDeletions, dels,
		))
	}
	if fl == nil {
		fl = []any{}
	}
	return fl
}

// azureStatus ports the changeType read (null preserved like the shell).
func azureStatus(v any) any {
	if v == nil {
		return nil
	}
	return v
}

// azureState ports the azure status map.
func azureState(s string) string {
	switch s {
	case "active":
		return stateOpen
	case "abandoned":
		return stateClosed
	case "completed":
		return stateMerged
	default:
		return stateUnknown
	}
}
