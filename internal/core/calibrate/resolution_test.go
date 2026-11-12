package calibratecore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func resolutionFixture() []outcome {
	return []outcome{
		{result: "fixed", sev: "critical", kb: []any{"a", "b", "a"}},
		{result: "fixed", sev: "critical"},
		{result: "fixed", sev: "important"},
		{result: "resolved-unfixed", sev: "important"},
		{result: "open", sev: "important"},
		{result: "unknown", sev: "informational"},
	}
}

// TestResolutionRate pins the published metrics contract.
func TestResolutionRate(t *testing.T) {
	r := liveResult{outcomes: resolutionFixture()}
	p, _ := r.Wire().Get("precision")
	doc, ok := p.(*jq.Obj)
	if !ok {
		t.Fatal("missing precision")
	}
	for k, want := range map[string]any{"fixed": 3, "resolved_unfixed": 1, "open": 1, "unknown": 1, "judged": 5, "sample_size": 6, "resolution_rate": 0.6} {
		if got, _ := doc.Get(k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
}

// TestResolutionRateZeroDenominator pins the published metrics contract.
func TestResolutionRateZeroDenominator(t *testing.T) {
	r := liveResult{outcomes: []outcome{{result: "unknown"}}}
	precision, _ := r.Wire().Get("precision")
	doc, ok := precision.(*jq.Obj)
	if !ok {
		t.Fatal("missing precision")
	}
	rate, _ := doc.Get("resolution_rate")
	if rate != nil {
		t.Fatal("zero denominator must be null")
	}
	if got := resolutionHuman(r.outcomes); !strings.Contains(got, "0/0 (n/a)") {
		t.Fatalf("zero denominator human: %s", got)
	}
}

// TestBySeverityFixedKeys pins the published metrics contract.
func TestBySeverityFixedKeys(t *testing.T) {
	doc := jq.NewObj()
	extendPrecision(doc, nil)
	got, _ := doc.Get("by_severity")
	want := `{"critical":{"fixed":0,"resolved_unfixed":0,"open":0,"unknown":0,"judged":0,"resolution_rate":null},"important":{"fixed":0,"resolved_unfixed":0,"open":0,"unknown":0,"judged":0,"resolution_rate":null},"informational":{"fixed":0,"resolved_unfixed":0,"open":0,"unknown":0,"judged":0,"resolution_rate":null},"untagged":{"fixed":0,"resolved_unfixed":0,"open":0,"unknown":0,"judged":0,"resolution_rate":null}}`
	if compactDoc(got) != want {
		t.Fatalf("severity = %s", compactDoc(got))
	}
}

// TestByRuleMultiCite pins the published metrics contract.
func TestByRuleMultiCite(t *testing.T) {
	doc := jq.NewObj()
	extendPrecision(doc, resolutionFixture())
	got, _ := doc.Get("by_rule")
	want := `{"a":{"fixed":1,"resolved_unfixed":0,"open":0,"unknown":0,"judged":1,"resolution_rate":1},"b":{"fixed":1,"resolved_unfixed":0,"open":0,"unknown":0,"judged":1,"resolution_rate":1}}`
	if compactDoc(got) != want {
		t.Fatalf("rules = %s", compactDoc(got))
	}
}

// TestJudgeMixed pins the published metrics contract.
func TestJudgeMixed(t *testing.T) {
	outcomes := []outcome{{result: "fixed"}, {result: "fixed", entry: jq.NewObj("history", []any{jq.NewObj("event", "revalidation")})}, {result: "unknown"}}
	counts, judge := judgeCounts(outcomes)
	if judge != "mixed" || counts.MarshalCompact() != `{"human":1,"agent":1,"mixed":0,"none":1}` {
		t.Fatalf("judge %s counts %s", judge, counts.MarshalCompact())
	}
}

func compactDoc(v any) string {
	if doc, ok := v.(*jq.Obj); ok {
		return doc.MarshalCompact()
	}
	return jq.Compact(v)
}

// TestCalibrationWrite pins the published metrics contract.
func TestCalibrationWrite(t *testing.T) {
	dir := t.TempDir()
	st := state{live: &liveResult{dir: dir, asOf: "2026-11-03T12:00:00Z", outcomes: resolutionFixture()}}
	st.persistCalibration(dir)
	raw, err := os.ReadFile(filepath.Join(dir, "calibration.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt core.Calibration
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SchemaVersion != "1" || receipt.Precision.Judged != 5 || receipt.Precision.ResolutionRate == nil || *receipt.Precision.ResolutionRate != 0.6 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

// TestCalibrationNotWrittenOnRefusal pins the refusal write boundary.
func TestCalibrationNotWrittenOnRefusal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	request := &Request{}
	request.RepoRoot = root
	request.Repo = "acme/app"
	request.Pr = "7"
	_, ref := Run(t.Context(), request)
	if ref == nil || ref.Code != "missing-post" {
		t.Fatalf("refusal = %v", ref)
	}
	if _, err := os.Stat(filepath.Join(dir, "calibration.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused calibration wrote receipt: %v", err)
	}
}
