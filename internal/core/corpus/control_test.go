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
