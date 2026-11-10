package fetchcore

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// ThreadsResolveData is the threads resolve --json data profile: the receipt
// path, the host and head it proves, the ids it closed, and the candidates it
// left open with a reason.
type ThreadsResolveData struct { //nolint:govet // field order is the published property order
	Dir        string                    `json:"dir"`
	File       string                    `json:"file"`
	Provider   string                    `json:"provider"`
	HeadSHA    string                    `json:"head_sha"`
	ResolvedAt string                    `json:"resolved_at"`
	DryRun     bool                      `json:"dry_run"`
	Resolved   []string                  `json:"resolved"`
	Skipped    []threadscore.ResolveSkip `json:"skipped"`
}

// Wire renders the profile in published wire order, with empty lists as [].
func (d *ThreadsResolveData) Wire() *jq.Obj {
	resolved := make([]any, 0, len(d.Resolved))
	for _, id := range d.Resolved {
		resolved = append(resolved, id)
	}
	skipped := make([]any, 0, len(d.Skipped))
	for _, skip := range d.Skipped {
		skipped = append(skipped, jq.NewObj("id", skip.ID, "reason", skip.Reason))
	}
	return jq.NewObj(
		"dir", d.Dir,
		"file", d.File,
		"provider", d.Provider,
		"head_sha", d.HeadSHA,
		"resolved_at", d.ResolvedAt,
		"dry_run", d.DryRun,
		"resolved", resolved,
		"skipped", skipped,
	)
}

// resolveSelection is what one run decided before any host call: the ids to
// close, and the candidates left open with their reasons.
type resolveSelection struct {
	resolved []string
	skipped  []threadscore.ResolveSkip
}

// resolve executes `threads resolve <owner/repo> <pr> [--thread <id>]...
// [--dry-run]`: close verdict-authored review threads whose finding the local
// store marks addressed, or only the named ids, and write the receipt.
func (st *threadsRun) resolve(ctx context.Context) (*ThreadsResponse, *core.RefusalError) {
	dir := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if ref := st.resolveBundle(dir); ref != nil {
		return nil, ref
	}
	provider, ref := st.resolveProvider(dir)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	feed, ref := st.resolveFeed(dir)
	if ref != nil {
		return nil, ref
	}
	sel, ref := st.selectThreads(feed, dir)
	if ref != nil {
		return nil, ref
	}
	at := st.req.TimeUTC()
	data := &ThreadsResolveData{
		Dir: dir, File: dir + "/" + threadscore.KeyResolveReceipt,
		Provider: provider, HeadSHA: feed.HeadSHA, ResolvedAt: at,
		DryRun: st.req.DryRun, Resolved: append([]string{}, sel.resolved...),
		Skipped: sel.skipped,
	}
	st.reportSelection(data)
	if st.req.DryRun || len(sel.resolved) == 0 {
		return st.resolveResponse(data, at), nil
	}
	if ref := st.writeThreads(ctx, dir, provider, feed, sel, at); ref != nil {
		return nil, ref
	}
	return st.resolveResponse(data, at), nil
}

// resolveBundle refuses a bundle that cannot supply the metadata and feed the
// mutation selects from.
func (st *threadsRun) resolveBundle(dir string) *core.RefusalError {
	if !fileExists(dir + suffixPRJSON) {
		return st.withWarnings(&core.RefusalError{
			Code:    CodeFetchFailed,
			Message: fmt.Sprintf("missing %s/PR.json (fetch the PR first)", dir),
		})
	}
	if !fileExists(dir + suffixThreadsJSON) {
		return st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: fmt.Sprintf("missing %s/threads.json (run threads first)", dir),
		})
	}
	return nil
}

// resolveFeed reads the stored feed and refuses a truncated one: a partial
// view is not a safe basis for a host write.
func (st *threadsRun) resolveFeed(dir string) (*threadscore.File, *core.RefusalError) {
	feed, err := threadscore.ReadFile(dir + suffixThreadsJSON)
	if err != nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: fmt.Sprintf("unreadable %s/threads.json: %v", dir, err),
		})
	}
	if feed.Truncated {
		return nil, st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: dir + "/threads.json is truncated — refresh threads before resolving",
		})
	}
	return feed, nil
}

// resolveProvider reads the provider from the bundle unless the request names
// one, and refuses a host whose capability table carries no resolve call.
func (st *threadsRun) resolveProvider(dir string) (string, *core.RefusalError) {
	provider := threadsProvider(st.req.Provider)
	if provider == "" {
		return "", st.withWarnings(&core.RefusalError{Code: codeBadProvider, Message: fmt.Sprintf(
			"unknown --provider '%s' (want github|gitlab|gitcafe|forgejo|bitbucket|azure)", st.req.Provider)})
	}
	if st.req.Provider == "" {
		provider = readcommon.ProviderOf(dir)
		if provider == "" {
			provider = providerGitHub
		}
	}
	caps := core.CapabilitiesFor(provider)
	if !caps.ThreadResolution {
		return "", st.withWarnings(&core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(
			"thread resolution on %s is not implemented (%s)",
			provider, caps.ReasonFor(core.CapabilityThreadResolution))})
	}
	return provider, nil
}

// findingsStore reads the local finding statuses keyed by sig. A missing or
// corrupt store refuses typed: the default selection's "addressed" rule is a
// local fact, and guessing it would close a thread nobody fixed.
func (st *threadsRun) findingsStore(dir string) (map[string]string, *core.RefusalError) {
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		if ref, ok := asRefusal(err); ok {
			return nil, st.withWarnings(ref)
		}
		return nil, st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: fmt.Sprintf("unreadable %s/findings.json: %v", dir, err),
		})
	}
	store := make(map[string]string, len(entries))
	for _, entry := range entries {
		if sig, status, ok := storeEntry(entry); ok {
			store[sig] = status
		}
	}
	return store, nil
}

// storeEntry reads one finding's sig and status; a non-object or a missing
// sig is not a store entry this selection can match.
func storeEntry(entry any) (sig, status string, ok bool) {
	obj, ok := entry.(*jq.Obj)
	if !ok || obj == nil {
		return "", "", false
	}
	rawSig, _ := obj.Get("sig")
	sig, ok = rawSig.(string)
	if !ok || sig == "" {
		return "", "", false
	}
	rawStatus, _ := obj.Get("status")
	if s, ok := rawStatus.(string); ok {
		status = s
	}
	return sig, status, true
}

// selectThreads applies the two selection rules: named ids must be
// verdict-authored, and the default closes only open verdict-authored threads
// whose linked finding the store marks addressed.
func (st *threadsRun) selectThreads(feed *threadscore.File, dir string) (resolveSelection, *core.RefusalError) {
	if len(st.req.Threads) > 0 {
		return st.selectNamed(feed)
	}
	store, ref := st.findingsStore(dir)
	if ref != nil {
		return resolveSelection{}, ref
	}
	return defaultSelection(feed, store), nil
}

// defaultSelection walks the feed in order, recording why each verdict-authored
// candidate stayed open. Human and bot threads are not candidates, so they
// never appear in the receipt.
func defaultSelection(feed *threadscore.File, store map[string]string) resolveSelection {
	var sel resolveSelection
	for i := range feed.Threads {
		thread := &feed.Threads[i]
		if thread.AuthorKind != threadscore.AuthorVerdict {
			continue
		}
		switch {
		case thread.Resolved:
			sel.skip(thread.ID, "already-resolved")
		case thread.FindingSig == nil:
			sel.skip(thread.ID, "no-finding-link")
		case store[*thread.FindingSig] != "addressed":
			sel.skip(thread.ID, "finding-not-addressed")
		default:
			sel.resolved = append(sel.resolved, thread.ID)
		}
	}
	return sel
}

// selectNamed validates every named id before any write: an unknown id or a
// thread verdict did not write refuses the whole run.
//
//nolint:revive // the named-id loop reads as one selection pass
func (st *threadsRun) selectNamed(feed *threadscore.File) (resolveSelection, *core.RefusalError) {
	byID := make(map[string]*threadscore.Thread, len(feed.Threads))
	for i := range feed.Threads {
		byID[feed.Threads[i].ID] = &feed.Threads[i]
	}
	var sel resolveSelection
	seen := make(map[string]bool, len(st.req.Threads))
	for _, id := range st.req.Threads {
		if seen[id] {
			continue
		}
		seen[id] = true
		skip, ref := st.namedID(byID, id)
		if ref != nil {
			return sel, ref
		}
		if skip {
			sel.skip(id, "already-resolved")
			continue
		}
		sel.resolved = append(sel.resolved, id)
	}
	return sel, nil
}

// namedID resolves one named id and validates the thread it names.
func (st *threadsRun) namedID(byID map[string]*threadscore.Thread, id string) (bool, *core.RefusalError) {
	thread, ok := byID[id]
	if !ok {
		return false, st.withWarnings(&core.RefusalError{Code: "unknown-thread", Message: fmt.Sprintf(
			"no thread %q in threads.json (refresh threads first)", id)})
	}
	return st.namedSelection(thread, id)
}

// namedSelection validates one named thread: a thread verdict did not author
// refuses the whole run, and an already-resolved thread is a skip.
func (st *threadsRun) namedSelection(thread *threadscore.Thread, id string) (bool, *core.RefusalError) {
	if thread.AuthorKind != threadscore.AuthorVerdict {
		return false, st.withWarnings(&core.RefusalError{Code: "not-verdict-authored", Message: fmt.Sprintf(
			"thread %q is %s-authored, not verdict-authored — refusing to resolve it", id, thread.AuthorKind)})
	}
	return thread.Resolved, nil
}

// skip records one candidate the run left open.
func (s *resolveSelection) skip(id, reason string) {
	s.skipped = append(s.skipped, threadscore.ResolveSkip{ID: id, Reason: reason})
}

// writeThreads closes every selected thread, then records the result in the
// feed and the receipt. A host failure stops the run; the ids already closed
// stay in the host, and a re-run is idempotent.
func (st *threadsRun) writeThreads(
	ctx context.Context, dir, provider string, feed *threadscore.File, sel resolveSelection, at string,
) *core.RefusalError {
	s, ref := st.session()
	if ref != nil {
		return ref
	}
	for _, id := range sel.resolved {
		if ref := postcore.ResolveThread(ctx, s.commander, provider, s.env, st.req.Repo, st.req.Pr, id); ref != nil {
			return st.withWarnings(ref)
		}
		st.human("resolved " + id)
	}
	markResolved(feed, sel.resolved)
	if err := threadscore.WriteFile(dir+suffixThreadsJSON, feed); err != nil {
		return st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: fmt.Sprintf("could not write %s/threads.json: %v", dir, err),
		})
	}
	receipt := &threadscore.ResolveReceipt{
		SchemaVersion: threadscore.SchemaVersion,
		Provider:      provider,
		Repo:          st.req.Repo,
		PR:            threadscore.PRNumber(st.req.Pr),
		HeadSHA:       feed.HeadSHA,
		ResolvedAt:    at,
		Resolved:      append([]string{}, sel.resolved...),
		Skipped:       append([]threadscore.ResolveSkip{}, sel.skipped...),
	}
	if err := threadscore.WriteResolveReceipt(dir+"/"+threadscore.KeyResolveReceipt, receipt); err != nil {
		return st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: fmt.Sprintf("could not write %s/%s: %v", dir, threadscore.KeyResolveReceipt, err),
		})
	}
	st.human(fmt.Sprintf("done: resolved %d thread(s) -> %s/%s",
		len(sel.resolved), dir, threadscore.KeyResolveReceipt))
	return nil
}

// markResolved flips the feed's resolved flags for the ids the host accepted,
// so a second run reads its own result instead of calling the host again.
func markResolved(feed *threadscore.File, ids []string) {
	closed := make(map[string]bool, len(ids))
	for _, id := range ids {
		closed[id] = true
	}
	for i := range feed.Threads {
		if closed[feed.Threads[i].ID] {
			feed.Threads[i].Resolved = true
		}
	}
}

// reportSelection streams the plan and the skips, so a dry run and a real run
// read the same before the first write.
func (st *threadsRun) reportSelection(data *ThreadsResolveData) {
	if data.DryRun {
		st.human(fmt.Sprintf("dry-run: would resolve %d thread(s)", len(data.Resolved)))
	} else {
		st.human(fmt.Sprintf("resolve: %d thread(s)", len(data.Resolved)))
	}
	for _, id := range data.Resolved {
		st.human("  " + id)
	}
	for _, skip := range data.Skipped {
		st.human(fmt.Sprintf("  skip %s (%s)", skip.ID, skip.Reason))
	}
}

// resolveResponse builds the envelope inputs for one resolve run.
func (st *threadsRun) resolveResponse(data *ThreadsResolveData, at string) *ThreadsResponse {
	next := "no verdict-authored addressed threads to resolve"
	if data.DryRun && len(data.Resolved) > 0 {
		next = fmt.Sprintf("re-run without --dry-run to resolve %d thread(s)", len(data.Resolved))
	} else if len(data.Resolved) > 0 {
		next = "re-run threads to refresh resolution state"
	}
	return &ThreadsResponse{
		Repo: st.req.Repo, Pr: st.req.Pr, At: at,
		Warnings: st.warnings, Next: next, Resolve: data,
	}
}
