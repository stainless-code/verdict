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
	if isLocalDir(dir) || readcommon.ProviderOf(dir) != "" && readcommon.ProviderOf(dir) != providerGitHub {
		return nil, st.withWarnings(&core.RefusalError{Code: codeProviderUnimplemented, Message: "calibrate --reactions supports posted GitHub reviews only"})
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
	st.human("reaction proposals only — review helpful signals; submit reasoned false-positive claims through triage")
	return &Response{Repo: st.req.Repo, Pr: st.req.Pr, At: st.req.TimeUTC(), Next: "review proposals before triage", Warnings: st.warnings, Data: &Data{reactions: jq.NewObj("proposals", proposals)}}, nil
}

func reactionProposals(entries, pages []any, reviewID, reason string) []any {
	proposals := []any{}
	for _, entry := range entries {
		if readcommon.FieldOf(entry, "kind") != "inline" {
			continue
		}
		sig := jq.StrOf(readcommon.FieldOf(entry, "sig"))
		helpful, down := false, false
		for _, page := range pages {
			comments, _ := page.([]any)
			for _, comment := range comments {
				m, ok := comment.(map[string]any)
				if !ok || jq.R(m["pull_request_review_id"]) != reviewID || m["body"] != postedBody(entry) || readcommon.Cmp(m["path"], readcommon.FieldOf(entry, "path")) != 0 {
					continue
				}
				reactions, _ := m["reactions"].(map[string]any)
				plus, _ := readcommon.NumVal(reactions["+1"])
				minus, _ := readcommon.NumVal(reactions["-1"])
				helpful = helpful || plus > 0
				down = down || minus > 0
			}
		}
		if helpful {
			proposals = append(proposals, jq.NewObj("sig", sig, "proposed", "helpful", "claim", nil))
		}
		if down && reason != "" {
			proposals = append(proposals, jq.NewObj("sig", sig, "proposed", "false-positive", "claim", core.TriageClaim{Sig: sig, Status: "false-positive", Rationale: reason}))
		}
	}
	slices.SortStableFunc(proposals, func(a, b any) int { return strings.Compare(jq.PrettyOrdered(a), jq.PrettyOrdered(b)) })
	return proposals
}
