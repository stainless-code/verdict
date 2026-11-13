package calibratecore

import (
	"context"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func (st *state) runReactions(ctx context.Context, dir string) (*Response, *core.RefusalError) {
	if reactionsUnsupported(dir) {
		return nil, st.withWarnings(&core.RefusalError{
			Code:    codeProviderUnimplemented,
			Message: "calibrate --reactions supports posted GitHub reviews only",
		})
	}
	entries, reviewID, _, ref := st.liveBundle(dir)
	if ref != nil {
		return nil, ref
	}
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "api", "repos/"+st.req.Repo+"/pulls/"+st.req.Pr+"/comments", "--paginate", "--slurp")
	if !ok {
		return nil, st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: "could not read posted comment reactions"})
	}
	doc, err := jq.Decode([]byte(stdout))
	pages, valid := doc.([]any)
	if err != nil || !valid {
		return nil, st.withWarnings(&core.RefusalError{Code: codeThreadsFailed, Message: "invalid posted comment reactions response"})
	}
	proposals := reactionProposals(entries, pages, reviewID, strings.TrimSpace(st.req.ReactionReason))
	for _, proposal := range proposals {
		st.human(jq.PrettyOrdered(proposal))
	}
	st.human("reaction proposals only — review helpful signals; submit reasoned false-positive claims through triage")
	return &Response{
		Repo: st.req.Repo, Pr: st.req.Pr, At: st.req.TimeUTC(), Next: "review proposals before triage",
		Warnings: st.warnings, Data: &Data{reactions: jq.NewObj("proposals", proposals)},
	}, nil
}

func reactionProposals(entries, pages []any, reviewID, reason string) []any {
	proposals := []any{}
	for _, entry := range entries {
		if readcommon.FieldOf(entry, "kind") != "inline" {
			continue
		}
		proposals = append(proposals, findingProposals(entry, pages, reviewID, reason)...)
	}
	slices.SortStableFunc(proposals, func(a, b any) int { return strings.Compare(jq.PrettyOrdered(a), jq.PrettyOrdered(b)) })
	return proposals
}

func reactionsUnsupported(dir string) bool {
	return isLocalDir(dir) || readcommon.ProviderOf(dir) != "" && readcommon.ProviderOf(dir) != providerGitHub
}

func findingReactions(entry any, pages []any, reviewID string) (helpful, down bool) {
	for _, page := range pages {
		comments, ok := page.([]any)
		if !ok {
			continue
		}
		p, m := pageReactions(entry, comments, reviewID)
		helpful = helpful || p
		down = down || m
	}
	return helpful, down
}

func pageReactions(entry any, comments []any, reviewID string) (helpful, down bool) {
	for _, comment := range comments {
		p, m := commentReactions(comment, entry, reviewID)
		helpful = helpful || p
		down = down || m
	}
	return helpful, down
}

func commentReactions(comment, entry any, reviewID string) (bool, bool) {
	m, ok := comment.(map[string]any)
	if !ok || !reactionCommentMatches(m, entry, reviewID) {
		return false, false
	}
	reactions, ok := m["reactions"].(map[string]any)
	if !ok {
		return false, false
	}
	plus, _ := readcommon.NumVal(reactions["+1"])
	minus, _ := readcommon.NumVal(reactions["-1"])
	return plus > 0, minus > 0
}

func reactionCommentMatches(m map[string]any, entry any, reviewID string) bool {
	return jq.R(m["pull_request_review_id"]) == reviewID && m["body"] == postedBody(entry) &&
		readcommon.Cmp(m["path"], readcommon.FieldOf(entry, "path")) == 0
}

func findingProposals(entry any, pages []any, reviewID, reason string) []any {
	proposals := []any{}
	sig := jq.StrOf(readcommon.FieldOf(entry, "sig"))
	helpful, down := findingReactions(entry, pages, reviewID)

	if helpful {
		proposals = append(proposals, jq.NewObj("sig", sig, "proposed", "helpful", "claim", nil))
	}
	if down && reason != "" {
		proposals = append(proposals, jq.NewObj("sig", sig, "proposed", "false-positive", "claim",
			core.TriageClaim{Sig: sig, Status: "false-positive", Rationale: reason}))
	}
	return proposals
}
