package calibratecore

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// toolGlab is the CLI every GitLab live read goes through.
const toolGlab = "glab"

const providerGitLab = "gitlab"

// gitlabAPIPath renders one MR-scoped REST path. The repo rides as a
// single URL-encoded segment, so the read needs no project-id lookup.
func gitlabAPIPath(repo, pr, suffix string) string {
	return "projects/" + url.PathEscape(repo) + "/merge_requests/" + pr + suffix
}

func gitlabLiveThreads(ctx context.Context, st *state) ([]any, *core.RefusalError) {
	stdout, _, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api",
		gitlabAPIPath(st.req.Repo, st.req.Pr, "/discussions?per_page=100"), "--paginate")
	if !ok {
		return nil, st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: msgThreadsFailed})
	}
	threads, err := gitlabDiscussionNodes(stdout)
	if err != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: msgThreadsFailed})
	}
	return threads, nil
}

func gitlabDiscussionNodes(stdout string) ([]any, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(stdout))
	if err != nil {
		return nil, jq.Sealed("gitlab discussions", err)
	}
	var out []any
	for _, doc := range docs {
		page, ok := doc.([]any)
		if !ok {
			return nil, jq.Sealed("gitlab discussions", errGitLabDiscussionShape)
		}
		out = append(out, page...)
	}
	return out, nil
}

var errGitLabDiscussionShape = errors.New("gitlab discussions read unreadable")

func gitlabLivePR(ctx context.Context, st *state) (map[string]any, *core.RefusalError) {
	stdout, _, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api", gitlabAPIPath(st.req.Repo, st.req.Pr, ""))
	if !ok {
		return nil, st.withWarnings(&core.RefusalError{Code: codePRReadFailed, Message: "could not read live PR state"})
	}
	if m, ok := decodeGitLabLivePR(stdout); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

// decodeGitLabLivePR folds one merge-request payload onto the join's
// canonical keys; false reads an empty live state like the GitHub path.
func decodeGitLabLivePR(stdout string) (map[string]any, bool) {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return nil, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, false
	}
	return gitlabLivePRMap(m), true
}

// gitlabLivePRMap folds a merge-request payload onto the join's canonical
// keys (headRefOid, state, mergedAt): the head prefers sha with the
// diff-refs fallback fetch uses, and the state folds to the GitHub
// vocabulary the report shares.
func gitlabLivePRMap(m map[string]any) map[string]any {
	out := map[string]any{
		"headRefOid": gitlabLiveHead(m),
		"state":      gitlabLiveState(m),
	}
	if s, ok := m["merged_at"].(string); ok && s != "" {
		out["mergedAt"] = s
	}
	return out
}

func gitlabLiveHead(m map[string]any) string {
	if sha, ok := m["sha"].(string); ok && sha != "" {
		return sha
	}
	if refs, ok := m["diff_refs"].(map[string]any); ok {
		if sha, ok := refs["head_sha"].(string); ok {
			return sha
		}
	}
	return ""
}

func gitlabLiveState(m map[string]any) string {
	switch m["state"] {
	case "opened":
		return "OPEN"
	case "merged":
		return "MERGED"
	case "closed", "locked":
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// gitlabLiveChanged reads the files changed between the reviewed head and
// the live head through the repository compare read. A failed read warns
// exactly like the GitHub compare path and leaves resolved-unfixed off.
func gitlabLiveChanged(ctx context.Context, st *state, head0, live string) []any {
	if head0 == live {
		return []any{}
	}
	stdout, _, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api",
		"projects/"+url.PathEscape(st.req.Repo)+"/repository/compare?from="+head0+"&to="+live)
	if !ok {
		st.warn("compare unavailable (rewritten history?) — changed-file evidence off, resolved reads as resolved-unfixed")
		return []any{}
	}
	files, err := gitlabCompareFiles(stdout)
	if err != nil {
		st.warn("compare unavailable (rewritten history?) — resolved reads as resolved-unfixed")
		return []any{}
	}
	return files
}

func gitlabCompareFiles(stdout string) ([]any, error) {
	doc, err := jq.Decode([]byte(stdout))
	if err != nil {
		return nil, jq.Sealed("gitlab compare", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, jq.Sealed("gitlab compare", errGitLabDiscussionShape)
	}
	var out []any
	for _, e := range jq.Elements(m["diffs"]) {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if path := gitlabDiffPath(em); path != "" {
			out = append(out, path)
		}
	}
	return out, nil
}

func gitlabDiffPath(em map[string]any) string {
	if s, ok := em["new_path"].(string); ok && s != "" {
		return s
	}
	if s, ok := em["old_path"].(string); ok {
		return s
	}
	return ""
}

// gitlabPostedBody renders the exact discussion body post writes for one
// finding: the body, the suggestion fence, and the hidden finding marker
// the join matches live notes against.
func gitlabPostedBody(entry any) string {
	body, ok := readcommon.FieldOf(entry, "body").(string)
	if !ok {
		body = ""
	}
	suggestion, ok := readcommon.FieldOf(entry, "suggestion").(string)
	if !ok {
		suggestion = ""
	}
	if suggestion != "" {
		body += "\n\n```suggestion\n" + suggestion + "\n```"
	}
	if sig := entrySigString(entry); sig != "" {
		body += "\n\n" + threadscore.FindingMarker(sig)
	}
	return body
}

func entrySigString(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return ""
	}
	sig, _ := m.Get("sig")
	if s, ok := sig.(string); ok {
		return s
	}
	return ""
}

// gitlabThreadMatches reports whether one live discussion holds the posted
// comment: a non-system note whose body and diff path both match.
func gitlabThreadMatches(thread any, postedBody string, path any) bool {
	m, ok := thread.(map[string]any)
	if !ok {
		return false
	}
	for _, n := range jq.Elements(m["notes"]) {
		if gitlabNoteMatches(n, postedBody, path) {
			return true
		}
	}
	return false
}

func gitlabNoteMatches(n any, postedBody string, path any) bool {
	nm, ok := n.(map[string]any)
	if !ok {
		return false
	}
	if sys, ok := nm["system"].(bool); ok && sys {
		return false
	}
	return readcommon.Cmp(noteBody(nm), postedBody) == 0 && readcommon.Cmp(notePath(nm), path) == 0
}

func noteBody(nm map[string]any) string {
	if s, ok := nm["body"].(string); ok {
		return s
	}
	return ""
}

func notePath(nm map[string]any) string {
	pos, ok := nm["position"].(map[string]any)
	if !ok || pos == nil {
		return ""
	}
	if s, ok := pos["new_path"].(string); ok && s != "" {
		return s
	}
	if s, ok := pos["old_path"].(string); ok {
		return s
	}
	return ""
}

// gitlabThreadResolved reports whether one live discussion is resolved:
// the first non-system note's resolved flag, fail-closed like the fetch
// feed the bundle stores.
func gitlabThreadResolved(thread any) bool {
	m, ok := thread.(map[string]any)
	if !ok {
		return false
	}
	root, ok := gitlabRootNote(m)
	if !ok {
		return false
	}
	resolved, ok := root["resolved"].(bool)
	if !ok {
		return false
	}
	return resolved
}

// gitlabRootNote returns the first non-system note: the note carrying
// the discussion's resolution state.
func gitlabRootNote(m map[string]any) (map[string]any, bool) {
	for _, n := range jq.Elements(m["notes"]) {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if sys, ok := nm["system"].(bool); ok && sys {
			continue
		}
		return nm, true
	}
	return nil, false
}
