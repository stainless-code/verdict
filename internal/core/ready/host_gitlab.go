package readycore

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// toolGlab is the CLI every GitLab live read goes through.
const toolGlab = "glab"

const providerGitLab = "gitlab"

// gitlabAPIPath renders one MR-scoped REST path. The repo rides as a
// single URL-encoded segment, so the read needs no project-id lookup.
func gitlabAPIPath(repo, pr, suffix string) string {
	return "projects/" + url.PathEscape(repo) + "/merge_requests/" + pr + suffix
}

// gitlabLive reads the live merge request through glab and folds it onto
// the canonical keys the shared tail applies (isDraft, mergeable,
// mergeStateStatus, body, state, headRefOid, mergedAt).
func gitlabLive(ctx context.Context, st *state, dir string) *core.RefusalError {
	stdout, stderr, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api", gitlabAPIPath(st.repo, st.pr, ""))
	writeErrFile(st, dir+"/.ready.err", stderr)
	if !ok {
		return st.withWarnings(&core.RefusalError{
			Code:    "pr-read-failed",
			Message: "could not read live merge request state for " + st.repo + hashSep + st.pr + ": " + firstLine(stderr),
		})
	}
	doc, derr := jq.Decode([]byte(stdout))
	if derr != nil {
		st.live = map[string]any{}
	} else if m, ok := jq.AsMap(doc); ok {
		st.live = gitlabLiveMap(m)
	} else {
		st.live = map[string]any{}
	}
	applyLive(st, dir)
	return nil
}

// gitlabLiveMap folds a merge-request payload onto the advisory's
// canonical keys: draft, has_conflicts, and detailed_merge_status become
// the draft/mergeable/mergeState triple, description becomes the body,
// and the head prefers sha with the diff-refs fallback fetch uses.
func gitlabLiveMap(m map[string]any) map[string]any {
	out := map[string]any{
		"isDraft":          gitlabDraft(m),
		"mergeable":        gitlabMergeable(m),
		"mergeStateStatus": gitlabMergeState(m),
		"body":             gitlabStr(m, "description"),
		"state":            gitlabState(m),
		"headRefOid":       gitlabHead(m),
	}
	if s := gitlabStr(m, "merged_at"); s != "" {
		out["mergedAt"] = s
	}
	if hp, ok := m["head_pipeline"]; ok && hp != nil {
		out["head_pipeline"] = hp
	}
	return out
}

func gitlabStr(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func gitlabDraft(m map[string]any) bool {
	draft, _ := m["draft"].(bool)
	return draft
}

func gitlabMergeable(m map[string]any) string {
	if conflicts, ok := m["has_conflicts"].(bool); ok {
		if conflicts || strings.EqualFold(gitlabStr(m, "detailed_merge_status"), "conflict") {
			return "CONFLICTING"
		}
		return "MERGEABLE"
	}
	return mergeUnknown
}

func gitlabMergeState(m map[string]any) string {
	if s := gitlabStr(m, "detailed_merge_status"); s != "" {
		return strings.ToUpper(s)
	}
	return mergeUnknown
}

func gitlabState(m map[string]any) string {
	switch gitlabStr(m, "state") {
	case "opened":
		return "OPEN"
	case "merged":
		return "MERGED"
	case "closed", "locked":
		return "CLOSED"
	default:
		return mergeUnknown
	}
}

func gitlabHead(m map[string]any) string {
	if sha := gitlabStr(m, "sha"); sha != "" {
		return sha
	}
	if refs, ok := m["diff_refs"].(map[string]any); ok {
		return gitlabStr(refs, "head_sha")
	}
	return ""
}

// gitlabChecks tallies the merge request's pipelines: a failed or canceled
// pipeline is one failing check. An empty list falls back to the head
// pipeline status, so a degraded list read still reports a failed head.
func gitlabChecks(ctx context.Context, st *state) {
	stdout, _, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api", gitlabAPIPath(st.repo, st.pr, "/pipelines?per_page=100"))
	raw := "[]"
	if ok {
		raw = stdout
	}
	list := checkElements(raw)
	failNames := []string{}
	anomaly := false
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			anomaly = true
			break
		}
		failNames = countFailPipeline(st, m, failNames)
	}
	if anomaly {
		st.ciFail = 0
		st.ciTotal = 0
		return
	}
	st.ciTotal = len(list)
	if st.ciTotal == 0 {
		if name, ok := headPipelineFail(st); ok {
			st.ciFail = 1
			st.ciTotal = 1
			failNames = append(failNames, name)
		}
	}
	appendCIFailing(st, failNames)
}

func countFailPipeline(st *state, m map[string]any, failNames []string) []string {
	switch jq.StrOf(m["status"]) {
	case "failed", "canceled":
		st.ciFail++
		return append(failNames, pipelineName(m))
	default:
		return failNames
	}
}

func pipelineName(m map[string]any) string {
	return "#" + jq.R(jq.Alt(m["id"], "?")) + " " + jq.StrOf(m["status"])
}

// headPipelineFail names the failed head pipeline when the pipeline list
// is empty: the degraded-path signal for a failed head.
func headPipelineFail(st *state) (string, bool) {
	v, ok := st.live["head_pipeline"]
	if !ok || v == nil {
		return "", false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	switch jq.StrOf(m["status"]) {
	case "failed", "canceled":
		return "head pipeline " + jq.StrOf(m["status"]), true
	default:
		return "", false
	}
}

// gitlabThreads counts the live unresolved discussions: the first
// non-system note's resolved flag decides, exactly like the fetch feed,
// and a discussion without a readable flag counts unresolved fail-closed.
func gitlabThreads(ctx context.Context, st *state) *core.RefusalError {
	stdout, _, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api",
		gitlabAPIPath(st.repo, st.pr, "/discussions?per_page=100"), "--paginate")
	if !ok {
		return st.withWarnings(&core.RefusalError{
			Code:    "threads-failed",
			Message: "could not read live review threads for " + st.repo + hashSep + st.pr,
		})
	}
	open, malformed, err := gitlabThreadCounts(stdout)
	if err != nil {
		return st.withWarnings(&core.RefusalError{
			Code:    "threads-failed",
			Message: "could not read live review threads for " + st.repo + hashSep + st.pr,
		})
	}
	applyThreadCounts(st, open, malformed)
	return nil
}

func gitlabThreadCounts(stdout string) (open, malformed int, err error) {
	docs, derr := jq.DecodeSlurp(strings.NewReader(stdout))
	if derr != nil {
		return 0, 0, derr
	}
	for _, doc := range docs {
		page, ok := doc.([]any)
		if !ok {
			return 0, 0, errGitLabThreadsShape
		}
		for _, d := range page {
			o, m := gitlabDiscussionState(d)
			open += o
			malformed += m
		}
	}
	return open, malformed, nil
}

func gitlabDiscussionState(d any) (open, malformed int) {
	m, ok := d.(map[string]any)
	if !ok {
		return 0, 1
	}
	for _, n := range jq.Elements(m["notes"]) {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		if sys, _ := nm["system"].(bool); sys {
			continue
		}
		resolved, ok := nm["resolved"].(bool)
		if !ok {
			return 0, 1
		}
		if resolved {
			return 0, 0
		}
		return 1, 0
	}
	return 0, 1
}

var errGitLabThreadsShape = errors.New("gitlab discussions read unreadable")
