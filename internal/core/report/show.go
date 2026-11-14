package reportcore

import (
	"context"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// RunShow executes the show service: one full finding record selected by a
// sig (short prefix ok).
func RunShow(_ context.Context, req *core.ShowRequest) (*core.ShowResponse, *core.RefusalError) {
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, ref
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return nil, loadRefusal(err)
	}
	if req.Sig == "" {
		return nil, &core.RefusalError{Code: "missing-sig", Message: "show needs --sig <sig> (short prefix ok)"}
	}
	receipt := readcommon.BundleTruncation(dir)
	full := readcommon.ResolveSig(entries, req.Sig)
	if full == "NONE" && receipt == nil {
		return nil, &core.RefusalError{Code: "unknown-sig", Message: "show: sig not in store"}
	}
	if full == "MANY" {
		return nil, &core.RefusalError{Code: "ambiguous-sig", Message: "show: sig prefix ambiguous"}
	}
	resp := &core.ShowResponse{Repo: req.Repo, Pr: req.Pr, At: req.TimeUTC(), Warnings: req.Warnings, Receipt: receipt.ForResult(0)}

	if rec := findRecord(entries, full); rec != nil {
		resp.Item = &core.FindingItem{Raw: rec}
		resp.Fix = buildFix(rec, dir)
		resp.Receipt = receipt.ForResult(1)
	}
	return resp, nil
}

func findRecord(entries []any, full string) *jq.Obj {
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		if sig, _ := m.Get("sig"); sig == full {
			return m
		}
	}
	return nil
}
