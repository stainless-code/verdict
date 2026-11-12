package reportcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// TestReportCleanHeadline pins the published metrics contract.
func TestReportCleanHeadline(t *testing.T) {
	for _, tc := range []struct {
		findings, want string
		clean          bool
	}{
		{"[]", "no findings — checked 2 files", true},
		{"[" + strings.ReplaceAll(openFinding, `"status":"open"`, `"status":"addressed"`) + "]", "no open findings — 1 addressed, 0 terminal", false},
	} {
		t.Run(tc.want, func(t *testing.T) {
			checkHeadline(t, tc.findings, tc.want, tc.clean)
		})
	}
}

func checkHeadline(t *testing.T, findings, want string, clean bool) {
	t.Helper()
	req := fixture(t, findings)
	writeFile(t, ".verdict/state/5101/PR.json", `{"headRefOid":"aaaaaaaa","files":[{"path":"a.go"},{"path":"b.go"}]}`)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(strings.Join(resp.Human, "\n"), want) {
		t.Fatalf("headline = %v", resp.Human)
	}
	if resp.Data.Counts.Clean != clean || resp.Data.Counts.CheckedFiles != 2 {
		t.Fatalf("counts = %+v", resp.Data.Counts)
	}
}
