package reportcore_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// wantDim is one expected gate dimension: its id, its status, and the detail
// text the gate prints (empty detail means the status alone is pinned).
type wantDim struct {
	id, status, detail string
}

// dimOf returns the gate dimension a run composed, failing the test when the
// run carried no gate or never composed that dimension.
func dimOf(t *testing.T, resp *reportcore.Response, id string) core.GateDimension {
	t.Helper()
	if resp.Gate == nil {
		t.Fatalf("run composed no gate; want a %s dimension", id)
	}
	for _, dim := range resp.Gate.Dimensions {
		if dim.ID == id {
			return dim
		}
	}
	t.Fatalf("gate dimensions = %+v, want a %s dimension", resp.Gate.Dimensions, id)
	return core.GateDimension{}
}

// requireDims asserts each wanted dimension's status and exact detail.
func requireDims(t *testing.T, resp *reportcore.Response, want ...wantDim) {
	t.Helper()
	for _, w := range want {
		got := dimOf(t, resp, w.id)
		if got.Status != w.status {
			t.Errorf("%s status = %q, want %q (detail %q)", w.id, got.Status, w.status, got.Detail)
		}
		if w.detail != "" && got.Detail != w.detail {
			t.Errorf("%s detail = %q, want %q", w.id, got.Detail, w.detail)
		}
	}
}

// requireDimDetail asserts the dimension's detail carries every substring.
func requireDimDetail(t *testing.T, resp *reportcore.Response, id string, subs ...string) {
	t.Helper()
	got := dimOf(t, resp, id).Detail
	for _, sub := range subs {
		if !strings.Contains(got, sub) {
			t.Errorf("%s detail = %q, want it to contain %q", id, got, sub)
		}
	}
}

// dimSummary renders every dimension for a failure message.
func dimSummary(resp *reportcore.Response) string {
	if resp.Gate == nil {
		return "(no gate)"
	}
	var parts []string
	for _, dim := range resp.Gate.Dimensions {
		parts = append(parts, dim.ID+" "+dim.Status+": "+dim.Detail)
	}
	return strings.Join(parts, "\n")
}
