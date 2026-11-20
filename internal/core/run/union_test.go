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
	assertFinderContract(t, first)
	assertFinderContract(t, second)
}

func assertFinderContract(t *testing.T, raw []byte) {
	t.Helper()
	for _, contract := range []string{"Every suspected defect", "quote", "confidence", "Do not spawn sub-agents", "Capability map"} {
		if !strings.Contains(string(raw), contract) {
			t.Fatalf("missing %q", contract)
		}
	}
}
