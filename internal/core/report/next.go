package reportcore

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// RunNext executes the next service: the top-ranked actionable finding plus
// the command to act on it. It calls the report service directly (never a
// CLI replay), so the store reads and failure posture match the report verb
// while the transport stays out of the domain.
func RunNext(ctx context.Context, req *core.NextRequest) (*core.NextResponse, *core.RefusalError) {
	statusF := orDefault(req.Status, statusOpen)
	resp := &core.NextResponse{
		Repo:     req.Repo,
		Pr:       req.Pr,
		At:       req.TimeUTC(),
		Status:   statusF,
		Warnings: req.Warnings,
	}
	// The sub-report validates --status like report does: a missing value
	// reads open (the shell's ${2:-open}), and an invalid value fails the
	// replay — bash's sub-invocation usage-errors, so the replay reads
	// empty and the parent reports the store unreadable (exit 1), never
	// an empty ranking.
	if !validStatus(statusF) {
		resp.StoreUnreadable = true
		return resp, nil
	}
	reportResp, ref := Run(ctx, &core.ReportRequest{
		ReadRequest: req.ReadRequest,
		Flags:       core.ReportFlags{Status: statusF, Kind: statusAll, Live: liveAuto},
		Effective:   req.Effective,
		Warnings:    req.ReportWarnings,
	})
	if ref != nil {
		resp.StoreUnreadable = true
		resp.ReportRefusal = ref
		//nolint:nilerr // the report refusal is classified into the response, not returned
		return resp, nil
	}
	if len(reportResp.Data.Items) == 0 {
		resp.Human = "next: none " + statusF + " — nothing to act on"
		return resp, nil
	}
	top := reportResp.Data.Items[0]
	resp.Item = &top
	resp.Human = nextTopLine(&top)
	return resp, nil
}

// nextTopLine ports the top-finding human line.
func nextTopLine(top *core.FindingItem) string {
	locator := top.Locator
	if locator == "" {
		locator = "?"
	}
	return fmt.Sprintf("next: %s [%s] %s — address via address-reviews mode, then post",
		sigOf(top), statusOf(top), locator)
}

// sigOf ports \(.sig[0:8]) for next's human line.
func sigOf(top *core.FindingItem) string {
	if top.Raw == nil {
		return jq.Str(top.Sig)
	}
	if sig, ok := fieldOf(top.Raw, "sig").(string); ok {
		return readcommon.Prefix(sig)
	}
	return jq.Str(fieldOf(top.Raw, "sig"))
}

// statusOf renders the item status like the shell's jq interpolation.
func statusOf(top *core.FindingItem) string {
	if top.Raw == nil {
		return jq.Str(top.Status)
	}
	return jq.Str(fieldOf(top.Raw, "status"))
}

// validStatus reports whether s is a known status filter.
func validStatus(s string) bool {
	switch s {
	case statusAll, statusOpen, "addressed", "deferred", "wont-fix", "false-positive":
		return true
	default:
		return false
	}
}
