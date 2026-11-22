package evidence_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/evidence"
)

func TestVerifyReceiptsA3(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/app.go b/app.go\n" +
		"--- a/app.go\n" +
		"+++ b/app.go\n" +
		"@@ -10,3 +10,3 @@\n" +
		" ctxBefore()\n" +
		"-oldLogicCall(arg)\n" +
		"+newLogicCall(arg)\n" +
		" ctxAfter()\n" +
		"diff --git a/deleted.go b/deleted.go\n" +
		"deleted file mode 100644\n" +
		"--- a/deleted.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-package gone\n" +
		"-func obsolete() {}\n" +
		"diff --git a/gen.pb.go b/gen.pb.go\n" +
		"--- /dev/null\n" +
		"+++ b/gen.pb.go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+package pb\n" +
		"+var generated = 1\n"

	idx := evidence.IndexDiff([]byte(diff))
	exempt := func(p string) bool { return strings.HasSuffix(p, ".pb.go") }

	wantRequired := []string{
		"app.go\tNEW\t10\t12",
		"deleted.go\tOLD\t1\t2",
	}
	if got := idx.ReceiptKeys(exempt); !slices.Equal(got, wantRequired) {
		t.Fatalf("ReceiptKeys = %q, want %q", got, wantRequired)
	}

	tests := []struct {
		name         string
		receipts     []evidence.ReceiptInput
		wantMissing  []string
		wantChecks   []evidence.ReceiptCheck
		wantTotal    int
		wantCovered  int
		wantExempt   int
		wantComplete bool
	}{
		{
			name: "full coverage using new side and pure deletion old side",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tNEW\t10\t12", Quote: "newLogicCall(arg)"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "func obsolete() {}"},
			},
			wantTotal:    2,
			wantCovered:  2,
			wantExempt:   1,
			wantComplete: true,
			wantMissing:  nil,
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tNEW\t10\t12", Side: "RIGHT", Matched: true},
				{Index: 1, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
		{
			name: "modified hunk receipt matches deleted line on old side",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tNEW\t10\t12", Quote: "oldLogicCall(arg)"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "package gone"},
			},
			wantTotal:    2,
			wantCovered:  2,
			wantExempt:   1,
			wantComplete: true,
			wantMissing:  nil,
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tNEW\t10\t12", Side: "LEFT", Matched: true},
				{Index: 1, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
		{
			name: "one missing hunk is insufficient",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tNEW\t10\t12", Quote: "ctxBefore()"},
			},
			wantTotal:    2,
			wantCovered:  1,
			wantExempt:   1,
			wantComplete: false,
			wantMissing:  []string{"deleted.go\tOLD\t1\t2"},
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tNEW\t10\t12", Side: "RIGHT", Matched: true},
			},
		},
		{
			name: "both hunks.tsv rows for a modified hunk cover it once",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tOLD\t10\t12", Quote: "oldLogicCall(arg)"},
				{Hunk: "app.go\tNEW\t10\t12", Quote: "newLogicCall(arg)"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "func obsolete() {}"},
			},
			wantTotal:    2,
			wantCovered:  2,
			wantExempt:   1,
			wantComplete: true,
			wantMissing:  nil,
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tOLD\t10\t12", Side: "LEFT", Matched: true},
				{Index: 1, Hunk: "app.go\tNEW\t10\t12", Side: "RIGHT", Matched: true},
				{Index: 2, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
		{
			name: "old row alone covers its canonical new row",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tOLD\t10\t12", Quote: "oldLogicCall(arg)"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "package gone"},
			},
			wantTotal:    2,
			wantCovered:  2,
			wantExempt:   1,
			wantComplete: true,
			wantMissing:  nil,
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tOLD\t10\t12", Side: "LEFT", Matched: true},
				{Index: 1, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
		{
			name: "rows hunks.tsv never prints stay unknown",
			receipts: []evidence.ReceiptInput{
				{Hunk: "@@ -10,3 +10,3 @@", Quote: "newLogicCall(arg)"},
				{Hunk: "app.go\tOLD\t99\t105", Quote: "oldLogicCall(arg)"},
				{Hunk: "missing.go\tOLD\t1\t2", Quote: "ctxBefore()"},
			},
			wantTotal:    2,
			wantCovered:  0,
			wantExempt:   1,
			wantComplete: false,
			wantMissing:  []string{"app.go\tNEW\t10\t12", "deleted.go\tOLD\t1\t2"},
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "@@ -10,3 +10,3 @@", Reason: "receipt-hunk-unknown"},
				{Index: 1, Hunk: "app.go\tOLD\t99\t105", Reason: "receipt-hunk-unknown"},
				{Index: 2, Hunk: "missing.go\tOLD\t1\t2", Reason: "receipt-hunk-unknown"},
			},
		},
		{
			name: "fabricated quote on the old row still mismatches",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tOLD\t10\t12", Quote: "notInThisHunk()"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "package gone"},
			},
			wantTotal:    2,
			wantCovered:  1,
			wantExempt:   1,
			wantComplete: false,
			wantMissing:  []string{"app.go\tNEW\t10\t12"},
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tOLD\t10\t12", Reason: "receipt-mismatch"},
				{Index: 1, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
		{
			name: "old row mismatch beside its matched new row is a duplicate warning",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tNEW\t10\t12", Quote: "newLogicCall(arg)"},
				{Hunk: "app.go\tOLD\t10\t12", Quote: "notInThisHunk()"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "package gone"},
			},
			wantTotal:    2,
			wantCovered:  2,
			wantExempt:   1,
			wantComplete: true,
			wantMissing:  nil,
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tNEW\t10\t12", Side: "RIGHT", Matched: true},
				{Index: 1, Hunk: "app.go\tOLD\t10\t12", Reason: "receipt-mismatch", DuplicateMismatch: true},
				{Index: 2, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
		{
			name: "duplicate receipt mismatch flagged as warning while hunk stays covered",
			receipts: []evidence.ReceiptInput{
				{Hunk: "app.go\tNEW\t10\t12", Quote: "newLogicCall(arg)"},
				{Hunk: "app.go\tNEW\t10\t12", Quote: "notInThisHunk()"},
				{Hunk: "deleted.go\tOLD\t1\t2", Quote: "package gone"},
			},
			wantTotal:    2,
			wantCovered:  2,
			wantExempt:   1,
			wantComplete: true,
			wantMissing:  nil,
			wantChecks: []evidence.ReceiptCheck{
				{Index: 0, Hunk: "app.go\tNEW\t10\t12", Side: "RIGHT", Matched: true},
				{Index: 1, Hunk: "app.go\tNEW\t10\t12", Reason: "receipt-mismatch", DuplicateMismatch: true},
				{Index: 2, Hunk: "deleted.go\tOLD\t1\t2", Side: "LEFT", Matched: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evidence.VerifyReceipts(idx, tc.receipts, exempt)
			assertReceiptSummary(t, &got, tc.wantTotal, tc.wantCovered, tc.wantExempt, tc.wantComplete, tc.wantMissing, tc.wantChecks)
		})
	}
}

func assertReceiptSummary(
	t *testing.T,
	got *evidence.ReceiptCoverage,
	wantTotal, wantCovered, wantExempt int,
	wantComplete bool,
	wantMissing []string,
	wantChecks []evidence.ReceiptCheck,
) {
	t.Helper()
	if got.Total != wantTotal || got.Covered != wantCovered || got.Exempt != wantExempt || got.Complete != wantComplete {
		t.Fatalf("summary = {Total:%d Covered:%d Exempt:%d Complete:%v}, want {Total:%d Covered:%d Exempt:%d Complete:%v}",
			got.Total, got.Covered, got.Exempt, got.Complete, wantTotal, wantCovered, wantExempt, wantComplete)
	}
	if !slices.Equal(got.Missing, wantMissing) {
		t.Fatalf("Missing = %q, want %q", got.Missing, wantMissing)
	}
	if !slices.Equal(got.Checks, wantChecks) {
		t.Fatalf("Checks = %+v, want %+v", got.Checks, wantChecks)
	}
}

func TestVerifyReceiptsVacuousZeroHunks(t *testing.T) {
	t.Parallel()
	idx := evidence.IndexDiff(nil)
	got := evidence.VerifyReceipts(idx, nil, nil)
	if got.Total != 0 || got.Covered != 0 || !got.Complete || len(got.Missing) != 0 {
		t.Fatalf("vacuous receipt coverage = %+v, want Complete=true Total=0", got)
	}
}

// TestVerifyReceiptsExemptOldRow proves an exempt file's OLD row is still a
// valid name for its hunk and that matching it moves neither Total nor Covered:
// an exempt hunk is never required, and its extra receipt never inflates the
// count.
func TestVerifyReceiptsExemptOldRow(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/app.go b/app.go\n" +
		"--- a/app.go\n" +
		"+++ b/app.go\n" +
		"@@ -10,3 +10,3 @@\n" +
		" ctxBefore()\n" +
		"-oldLogicCall(arg)\n" +
		"+newLogicCall(arg)\n" +
		" ctxAfter()\n" +
		"diff --git a/api.pb.go b/api.pb.go\n" +
		"--- a/api.pb.go\n" +
		"+++ b/api.pb.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		" top\n" +
		"-generated = 1\n" +
		"+generated = 2\n" +
		" bottom\n"
	idx := evidence.IndexDiff([]byte(diff))
	exempt := func(p string) bool { return strings.HasSuffix(p, ".pb.go") }
	receipts := []evidence.ReceiptInput{
		{Hunk: "app.go\tOLD\t10\t12", Quote: "oldLogicCall(arg)"},
		{Hunk: "api.pb.go\tOLD\t1\t3", Quote: "generated = 1"},
	}
	wantChecks := []evidence.ReceiptCheck{
		{Index: 0, Hunk: "app.go\tOLD\t10\t12", Side: "LEFT", Matched: true},
		{Index: 1, Hunk: "api.pb.go\tOLD\t1\t3", Side: "LEFT", Matched: true},
	}
	got := evidence.VerifyReceipts(idx, receipts, exempt)
	assertReceiptSummary(t, &got, 1, 1, 1, true, nil, wantChecks)
}
