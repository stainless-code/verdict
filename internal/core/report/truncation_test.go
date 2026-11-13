package reportcore_test

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func TestCheckedOutputReceipt(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/full.diff", "diff --git a/a b/a\ndiff --git a/b b/b\ndiff --git a/c b/c\ndiff --git a/d b/d\ndiff --git a/e b/e\ndiff --git a/f b/f\ndiff --git a/g b/g\ndiff --git a/h b/h\n")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: core.ReportFlags{Output: "report.md"}})
	if ref != nil {
		t.Fatal(ref)
	}
	receipt := resp.Data.Receipt
	if receipt == nil || receipt.TruncationReason != "response_cap" || receipt.Returned != 7 || receipt.Total == nil || *receipt.Total != 8 {
		t.Fatalf("checked output receipt = %+v", receipt)
	}
}
