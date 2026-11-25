package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	coordinatorFinding = `{"path":"a.go","line":1,"side":"RIGHT","severity":"important","claim":"Changed value breaks the caller.","quote":"new line 1","confidence":"high"}`
	coordinatorReview  = `{"schema_version":"1","findings":[` + coordinatorFinding + `],` + fixtureReceipts + `}`
	coordinatorEmpty   = `{"schema_version":"1","findings":[],` + fixtureReceipts + `}`
)

func unionInput(t *testing.T, raw string) *jq.Obj {
	t.Helper()
	value, err := jq.DecodeOrdered([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := value.(*jq.Obj)
	if !ok {
		t.Fatal("not an object")
	}
	return obj
}

func TestDiscoveryUnionSelectsWholeVerifiedTupleAndSeverity(t *testing.T) {
	t.Parallel()
	first := unionInput(t, `{"findings":[{"path":"a.go","line":9,"side":"RIGHT","severity":"critical","claim":"Changed value breaks the caller.","quote":"imaginary","confidence":"low"}]}`)
	second := unionInput(t, coordinatorReview)
	review, plan, err := discoveryUnion("test", []byte(fixtureDiff), []*jq.Obj{first, second})
	if err != nil {
		t.Fatal(err)
	}
	rows := jq.Elements(readcommon.FieldOf(review, "findings"))
	want := `[{"path":"a.go","line":1,"side":"RIGHT","severity":"critical","claim":"Changed value breaks the caller.","quote":"new line 1","confidence":"high"}]`
	if got := compactRows(rows); got != want {
		t.Fatalf("findings = %s", got)
	}
	checks := readcommon.FieldOf(plan, "checks")
	if got := compactRows(jq.Elements(checks)); got != `[{"sig":"b02621c6e34339ac215c21bbc73527b558e69185","origin":"finder-1","checker":"finder-2"},{"sig":"b02621c6e34339ac215c21bbc73527b558e69185","origin":"finder-2","checker":"finder-1"}]` {
		t.Fatalf("checks = %s", got)
	}
}

func compactRows(rows []any) string {
	return strings.TrimSuffix(strings.TrimPrefix(jq.NewObj("rows", rows).MarshalCompact(), `{"rows":`), "}")
}

func TestDiscoveryUnionOrdersBySeverityThenSignature(t *testing.T) {
	t.Parallel()
	review := unionInput(t, `{"findings":[{"path":"a.go","claim":"Changed value breaks the caller.","severity":"important"},{"path":"a.go","claim":"Other defect breaks cleanup.","severity":"critical"}]}`)
	for range 20 {
		got, _, err := discoveryUnion("test", nil, []*jq.Obj{review, nil})
		if err != nil {
			t.Fatal(err)
		}
		rows := jq.Elements(readcommon.FieldOf(got, "findings"))
		if len(rows) != 2 || readcommon.FieldOf(rows[0], "claim") != "Other defect breaks cleanup." ||
			readcommon.FieldOf(rows[1], "claim") != "Changed value breaks the caller." {
			t.Fatalf("order = %s", compactRows(rows))
		}
	}
}

func TestFinderProceduresShareS4ContractAndDistinctMethods(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	first, ref := finderProcedure(dir, filepath.Join(dir, "x-finder-1"), 0)
	if ref != nil {
		t.Fatal(ref)
	}
	second, ref := finderProcedure(dir, filepath.Join(dir, "x-finder-2"), 1)
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(string(first), "Read spec/context first.") || !strings.Contains(string(second), "last file toward the first") {
		t.Fatal("finder methods lost their different entry points")
	}
	assertFinderContract(t, first, filepath.Join(dir, "x-finder-1", "review.json"))
	assertFinderContract(t, second, filepath.Join(dir, "x-finder-2", "review.json"))
}

func assertFinderContract(t *testing.T, raw []byte, outputPath string) {
	t.Helper()
	if strings.Contains(string(raw), "verdict record --dry-run") || !strings.Contains(string(raw), "Do not invoke record or post during discovery.") {
		t.Fatal("finder invokes aggregate checking before discovery closes")
	}
	if !strings.Contains(string(raw), outputPath) || strings.Contains(string(raw), "<bundle>") {
		t.Fatal("finder procedure does not name its own review artifact")
	}
	for _, contract := range []string{"Every suspected defect", "quote", "confidence", "Do not spawn sub-agents", "Capability map"} {
		if !strings.Contains(string(raw), contract) {
			t.Fatalf("missing %q", contract)
		}
	}
}

func TestDiscoveryUnionUsesSignatureTieBreak(t *testing.T) {
	t.Parallel()
	review := unionInput(t, `{"findings":[{"path":"a.go","claim":"Changed value breaks the caller.","severity":"important"},{"path":"a.go","claim":"Other defect breaks cleanup.","severity":"important"}]}`)
	for range 20 {
		got, _, err := discoveryUnion("test", nil, []*jq.Obj{review, nil})
		if err != nil {
			t.Fatal(err)
		}
		rows := jq.Elements(readcommon.FieldOf(got, "findings"))
		if len(rows) != 2 || readcommon.FieldOf(rows[0], "claim") != "Other defect breaks cleanup." ||
			readcommon.FieldOf(rows[1], "claim") != "Changed value breaks the caller." {
			t.Fatalf("order = %s", compactRows(rows))
		}
	}
}

func TestDiscoveryUnionDeduplicatesReceiptTuples(t *testing.T) {
	t.Parallel()
	first := unionInput(t, `{"receipts":[{"hunk":"literal-hunk","quote":"literal-quote"}]}`)
	second := unionInput(t, `{"receipts":[{"quote":"literal-quote","hunk":"literal-hunk"}]}`)
	review, _, err := discoveryUnion("test", nil, []*jq.Obj{first, second})
	if err != nil {
		t.Fatal(err)
	}
	rows := jq.Elements(readcommon.FieldOf(review, "receipts"))
	if got := compactRows(rows); got != `[{"hunk":"literal-hunk","quote":"literal-quote"}]` {
		t.Fatalf("receipts = %s", got)
	}
}

func TestDiscoveryUnionKeepsOneOriginPerFinder(t *testing.T) {
	t.Parallel()
	twice := unionInput(t, `{"findings":[`+
		`{"path":"a.go","line":1,"side":"RIGHT","severity":"important","claim":"Changed value breaks the caller.","quote":"new line 1","confidence":"high"},`+
		`{"path":"a.go","line":3,"side":"RIGHT","severity":"important","claim":"Changed value breaks the caller.","quote":"old line 3","confidence":"high"}]}`)
	review, plan, err := discoveryUnion("test", []byte(fixtureDiff), []*jq.Obj{twice, unionInput(t, coordinatorReview)})
	if err != nil {
		t.Fatal(err)
	}
	rows := jq.Elements(readcommon.FieldOf(review, "findings"))
	if got := compactRows(rows); got != `[{"path":"a.go","line":1,"side":"RIGHT","severity":"important","claim":"Changed value breaks the caller.","quote":"new line 1","confidence":"high"}]` {
		t.Fatalf("findings = %s", got)
	}
	if got := compactRows(jq.Elements(readcommon.FieldOf(plan, "checks"))); got != `[{"sig":"b02621c6e34339ac215c21bbc73527b558e69185","origin":"finder-1","checker":"finder-2"},{"sig":"b02621c6e34339ac215c21bbc73527b558e69185","origin":"finder-2","checker":"finder-1"}]` {
		t.Fatalf("checks = %s", got)
	}
	sources := jq.Elements(readcommon.FieldOf(plan, "sources"))
	if len(sources) != 1 {
		t.Fatalf("sources = %s", compactRows(sources))
	}
	origins := jq.Elements(readcommon.FieldOf(sources[0], "origins"))
	finders := make([]string, 0, len(origins))
	for _, origin := range origins {
		finders = append(finders, jq.StrOf(readcommon.FieldOf(origin, "finder")))
	}
	if got := strings.Join(finders, ","); got != "finder-1,finder-2" {
		t.Fatalf("origins = %s", got)
	}
}

func TestDiscoveryUnionCarriesReadAttestations(t *testing.T) {
	t.Parallel()
	first := unionInput(t, `{"coverage":[{"file":"a.go","ranges":"1-3"}],"chunks":["chunk-1"],"receipts":[]}`)
	second := unionInput(t, `{"coverage":[{"file":"old.go","ranges":"all"},{"file":"a.go","ranges":"1-3"}],"chunks":["chunk-2","chunk-1"]}`)
	review, _, err := discoveryUnion("test", nil, []*jq.Obj{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if got := compactRows(jq.Elements(readcommon.FieldOf(review, "coverage"))); got != `[{"file":"a.go","ranges":"1-3"},{"file":"old.go","ranges":"all"}]` {
		t.Fatalf("coverage = %s", got)
	}
	if got := compactRows(jq.Elements(readcommon.FieldOf(review, "chunks"))); got != `["chunk-1","chunk-2"]` {
		t.Fatalf("chunks = %s", got)
	}
}

func TestDiscoveryUnionOmitsUnattestedReadClaims(t *testing.T) {
	t.Parallel()
	review, _, err := discoveryUnion("test", nil, []*jq.Obj{unionInput(t, coordinatorReview)})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"coverage", "chunks"} {
		if _, ok := review.Get(key); ok {
			t.Fatalf("%s = %v, want the field absent", key, readcommon.FieldOf(review, key))
		}
	}
}

// TestDiscoveryUnionKeepsReproductionFromTheDuplicate proves a reproduced
// finding keeps the reproduction it was reported with: the artifact is what
// record captures, so a merge that keeps the tuple but drops the payload stores
// a "reproduced" finding with nothing to run.
func TestDiscoveryUnionKeepsReproductionFromTheDuplicate(t *testing.T) {
	t.Parallel()
	payload := `{"path":"","command":"false"}`
	first := unionInput(t, `{"findings":[{"path":"a.go","line":1,"side":"RIGHT","severity":"important",`+
		`"claim":"Changed value breaks the caller.","quote":"new line 1","confidence":"high",`+
		`"rigor":"reproduced","reproduction":`+payload+`}]}`)
	second := unionInput(t, `{"findings":[{"path":"a.go","line":3,"side":"RIGHT","severity":"critical",`+
		`"claim":"Changed value breaks the caller.","quote":"old line 3","confidence":"high",`+
		`"rigor":"reproduced","reproduction":`+payload+`}]}`)
	review, _, err := discoveryUnion("test", []byte(fixtureDiff), []*jq.Obj{first, second})
	if err != nil {
		t.Fatal(err)
	}
	rows := jq.Elements(readcommon.FieldOf(review, "findings"))
	want := `[{"path":"a.go","line":1,"side":"RIGHT","severity":"critical","claim":"Changed value breaks the caller.","quote":"new line 1","confidence":"high","rigor":"reproduced","reproduction":` + payload + `}]`
	if got := compactRows(rows); got != want {
		t.Fatalf("findings = %s", got)
	}
}
