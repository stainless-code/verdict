package reportcore

import (
	"context"
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// RunNext executes the next service: the top-ranked actionable finding plus
// the command to act on it. It calls the report service directly, so the
// store reads and failure posture match the report verb while the transport
// stays out of the domain. The report run carries the request's warnings
// unchanged: prefs are resolved once, by the adapter.
func RunNext(ctx context.Context, req *core.NextRequest) (*core.NextResponse, *core.RefusalError) {
	statusF := orDefault(req.Status, statusOpen)
	resp := &core.NextResponse{
		Repo:     req.Repo,
		Pr:       req.Pr,
		At:       req.TimeUTC(),
		Status:   statusF,
		Warnings: req.Warnings,
	}
	if !validStatus(statusF) {
		resp.StoreUnreadable = true
		return resp, nil
	}
	reportResp, ref := Run(ctx, &core.ReportRequest{
		ReadRequest: req.ReadRequest,
		Flags:       core.ReportFlags{Status: statusF, Kind: statusAll, Live: liveAuto},
		Effective:   req.Effective,
		Warnings:    req.Warnings,
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

func nextTopLine(top *core.FindingItem) string {
	locator := top.Locator
	if locator == "" {
		locator = "?"
	}
	return fmt.Sprintf("next: %s [%s] %s — address via address-reviews mode, then post",
		sigOf(top), statusOf(top), locator)
}

func sigOf(top *core.FindingItem) string {
	if top.Raw == nil {
		return jq.Str(top.Sig)
	}
	if sig, ok := readcommon.FieldOf(top.Raw, "sig").(string); ok {
		return readcommon.Prefix(sig)
	}
	return jq.Str(readcommon.FieldOf(top.Raw, "sig"))
}

func statusOf(top *core.FindingItem) string {
	if top.Raw == nil {
		return jq.Str(top.Status)
	}
	return jq.Str(readcommon.FieldOf(top.Raw, "status"))
}

func validStatus(s string) bool {
	switch s {
	case statusAll, statusOpen, "addressed", "deferred", "wont-fix", "false-positive":
		return true
	default:
		return false
	}
}
