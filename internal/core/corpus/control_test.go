package corpuscore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestControlEmptyTruthOneReportEqualsOneFalsePositive(t *testing.T) {
	c := &core.CorpusCase{SchemaVersion: "1", ID: "clean", Kind: "control", Provenance: core.CorpusProvenance{Kind: "seeded", Injected: "clean rename"}, Bugs: []core.CorpusBug{}}
	if ref := validate(c, "fixture"); ref != nil {
		t.Fatal(ref)
	}
	r := Evaluate(c, []any{jq.NewObj("path", "sample.go", "line", 3, "body", "regression")})
	if r.Matched != 0 || r.FalsePositives != 1 || r.Reported != 1 {
		t.Fatalf("control counts = %+v; want matched=0 false-positive=1 reported=1", r)
	}
}

func TestControlRejectsNullAndNonemptyBugs(t *testing.T) {
	for _, bugs := range [][]core.CorpusBug{nil, {{Path: "sample.go", Line: 3, Kind: "race", Severity: "important"}}} {
		c := &core.CorpusCase{SchemaVersion: "1", ID: "clean", Kind: "control", Provenance: core.CorpusProvenance{Kind: "seeded", Injected: "clean rename"}, Bugs: bugs}
		if ref := validate(c, "fixture"); ref == nil {
			t.Fatalf("accepted control bugs=%v", bugs)
		}
	}
}

// TestRealControlNeedsNoFixCommit pins the one provenance field a clean
// control cannot carry: the later commit that fixed a defect it does not
// have. A real bug case still requires it (TestLoadRejects).
func TestRealControlNeedsNoFixCommit(t *testing.T) {
	pr := 2415
	c := &core.CorpusCase{
		SchemaVersion: "1",
		ID:            "clean",
		Kind:          "control",
		Provenance: core.CorpusProvenance{
			Kind:    "real",
			Repo:    "urfave/cli",
			PR:      &pr,
			BaseSHA: "1305a08200f7c4d873e28bd2bbad1ff257c6cfc4",
			HeadSHA: "d0d280f7f83d6a1d96afc8b4ff6d517032dc7f87",
		},
		Bugs: []core.CorpusBug{},
	}
	if ref := validate(c, "fixture"); ref != nil {
		t.Fatalf("real control without a fix commit: %v", ref)
	}
	c.Provenance.PR = nil
	if ref := validate(c, "fixture"); ref == nil || ref.Code != codeCorpusInvalid {
		t.Fatalf("real control without a PR = %v, want corpus-invalid", ref)
	}
}
