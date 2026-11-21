package recordcore_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// aggregateReproductionReview is a two-finder aggregate input carrying one
// reproduced finding, the shape run's coordinator writes at review.json.
const aggregateReproductionReview = `{"schema_version":"2","execution":{"id":"execution-A","finders":2},` +
	`"findings":[{"path":"notes.txt","line":2,"side":"RIGHT","severity":"important",` +
	`"claim":"A reproduced defect.","quote":"new line","confidence":"high",` +
	`"rigor":"reproduced","reproduction":{"path":"","command":"false"}}],` +
	`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`

// TestAggregateReproductionReachesTheStoredEntry proves record captures the
// reproduction of a two-finder aggregate finding: the aggregate is the only
// review record reads in S5, so a capture pass wired to the single-pass path
// alone would store "reproduced" findings with a raw payload and no retained
// artifact.
func TestAggregateReproductionReachesTheStoredEntry(t *testing.T) {
	res := mergeDefectBundle(t, aggregateReproductionReview)
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %d", len(res.Entries))
	}
	entry, ok := res.Entries[0].(*jq.Obj)
	if !ok {
		t.Fatalf("entry = %v", res.Entries[0])
	}
	receipt := readcommon.FieldOf(entry, "reproduction")
	const artifact = "tests/e4c372b6be85faab079dae8ace6c8081a75c950f-2ed27c1421e6.sh"
	if path, command := readcommon.FieldOf(receipt, "path"), readcommon.FieldOf(receipt, "command"); path != artifact || command != "sh "+artifact {
		t.Fatalf("reproduction = %v, want the retained artifact %s", receipt, artifact)
	}
}
