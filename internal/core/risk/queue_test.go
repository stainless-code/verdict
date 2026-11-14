package risk

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	queueHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	queueHeadB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// queueCritical is one open critical security finding at said rigor.
	queueCritical = `[{"sig":"c1","kind":"inline","path":"notes.txt","line":2,"side":"RIGHT",` +
		`"anchored":true,"body":"The changed line drops the saved value.","status":"open",` +
		`"severity":"critical","category":"security"}]`
	// queueClean is a recorded store with no open findings.
	queueClean = `[]`
)

func queuePR(state string) string {
	return `{"state":"` + state + `","headRefOid":"` + queueHeadA + `","baseRefOid":"` + queueHeadB + `"}`
}

func queueLocal() string {
	return `{"head_sha":"` + queueHeadA + `","base_sha":"` + queueHeadB + `"}`
}

// queueBundle writes one bundle under the state home and returns its dir.
func queueBundle(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(".verdict", "state", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// queueRun runs the queue over the current directory and fails on a refusal.
func queueRun(t *testing.T, status string, limit int) *QueueResponse {
	t.Helper()
	resp, ref := Run(context.Background(), &QueueRequest{
		Repo:   "acme/app",
		Status: status,
		Limit:  limit,
	})
	if ref != nil {
		t.Fatalf("risk refusal: %v", ref)
	}
	return resp
}

func TestTriageRankOrdersBands(t *testing.T) {
	want := map[string]int{
		LevelCritical: 4, LevelHigh: 3, LevelMedium: 2, LevelUnknown: 1, LevelLow: 0,
	}
	for band, rank := range want {
		if got := TriageRank(band); got != rank {
			t.Errorf("TriageRank(%q) = %d, want %d", band, got, rank)
		}
	}
	if (TriageRank(LevelUnknown) <= TriageRank(LevelLow)) || (TriageRank(LevelUnknown) >= TriageRank(LevelMedium)) {
		t.Error("unknown must sort above low and below medium")
	}
}

// TestQueueOrdersByBandThenScoreThenBundle pins the queue's total order: the
// band decides first, the score breaks a band tie, and the bundle name breaks
// a score tie.
func TestQueueOrdersByBandThenScoreThenBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "7", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "9", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "5", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueCritical})
	queueBundle(t, "11", map[string]string{"PR.json": queuePR("OPEN")})

	resp := queueRun(t, QueueStatusOpen, 50)
	got := rowBundles(resp.Data.Rows)
	if want := []string{"5", "11", "7", "9"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if rank := resp.Data.Rows[0].Rank; rank != 1 {
		t.Errorf("top rank = %d, want 1", rank)
	}
}

// TestQueueBandOutranksScore pins the band ahead of the score in the queue's
// order: a critical bundle at 40 outranks a low bundle at 41, whose measured
// evidence happens to be richer. A score-first comparator flips this pair.
func TestQueueBandOutranksScore(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "6", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueCritical})
	queueBundle(t, "8", map[string]string{
		"PR.json":       queuePR("OPEN"),
		"findings.json": queueClean,
		"record.json":   queueLowRecord,
		"full.diff":     queueRichDiff,
		"impact.json":   `{"symbols":{"count":26},"callers":{"count":0}}`,
	})

	resp := queueRun(t, QueueStatusOpen, 50)
	if got, want := rowBundles(resp.Data.Rows), []string{"6", "8"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v: the band decides before the score", got, want)
	}
	critical, low := resp.Data.Rows[0], resp.Data.Rows[1]
	if critical.Rank != 1 {
		t.Fatalf("critical rank = %d, want 1", critical.Rank)
	}
	if critical.Reported != LevelCritical || critical.Score.Value != 40 {
		t.Fatalf("critical row = %q/%d, want critical/40", critical.Reported, critical.Score.Value)
	}
	if low.Reported != LevelLow || low.Score.Value != 41 {
		t.Fatalf("low row = %q/%d, want low/41", low.Reported, low.Score.Value)
	}
	if critical.Score.Value >= low.Score.Value {
		t.Fatalf("fixture is not an inverse pair: %d is not below %d", critical.Score.Value, low.Score.Value)
	}
}

// TestSortRowsTieBreaksOnBundleName exercises the comparator's last key
// directly: [os.ReadDir] already hands the queue lexical order, so only a
// shuffled slice can fail when the name tie-break is removed.
func TestSortRowsTieBreaksOnBundleName(t *testing.T) {
	rows := []QueueRow{
		{Bundle: "9", Reported: LevelLow},
		{Bundle: "7", Reported: LevelLow},
		{Bundle: "8", Reported: LevelLow, Score: core.RiskScore{Value: 1}},
	}
	sortRows(rows)
	if got, want := rowBundles(rows), []string{"8", "7", "9"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v: the score breaks first, then the bundle name", got, want)
	}
}

// seedMixedStates lands the four host states the filter reads: a live PR, a
// local review, and one merged and one closed PR.
func seedMixedStates(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	queueBundle(t, "12", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "local-wip", map[string]string{"source.json": queueLocal(), "findings.json": queueClean})
	queueBundle(t, "13", map[string]string{"PR.json": queuePR("MERGED"), "findings.json": queueClean})
	queueBundle(t, "14", map[string]string{"PR.json": queuePR("CLOSED"), "findings.json": queueClean})
}

// TestQueueStatusKeepsLiveBundles proves the default filter keeps host-open and
// local bundles, drops merged and closed ones, and names the count it skipped.
func TestQueueStatusKeepsLiveBundles(t *testing.T) {
	seedMixedStates(t)
	open := queueRun(t, QueueStatusOpen, 50)
	if got, want := rowBundles(open.Data.Rows), []string{"12", "local-wip"}; !slices.Equal(got, want) {
		t.Errorf("open rows = %v, want %v", got, want)
	}
	if open.Data.Bundles != 2 {
		t.Errorf("bundles = %d, want 2", open.Data.Bundles)
	}
	if got, want := rowState(open.Data.Rows, "local-wip"), "local"; got != want {
		t.Errorf("local bundle state = %q, want %q", got, want)
	}
	if !slices.Contains(open.Human, "skipped: 2 bundle(s) not open (--status all includes them)") {
		t.Errorf("human = %v, want the skipped line naming 2 bundles", open.Human)
	}
}

// TestQueueStatusAllKeepsEveryBundle proves --status all keeps all four states
// and drops the skipped line.
func TestQueueStatusAllKeepsEveryBundle(t *testing.T) {
	seedMixedStates(t)
	all := queueRun(t, QueueStatusAll, 50)
	if got, want := rowBundles(all.Data.Rows), []string{"12", "13", "14", "local-wip"}; !slices.Equal(got, want) {
		t.Errorf("all rows = %v, want %v", got, want)
	}
	if got, want := rowState(all.Data.Rows, "13"), "merged"; got != want {
		t.Errorf("merged bundle state = %q, want %q", got, want)
	}
	for _, line := range all.Human {
		if strings.HasPrefix(line, "skipped:") {
			t.Errorf("--status all rendered %q, want no skipped line", line)
		}
	}
}

// TestQueueUnknownStateReadsOpen proves a state token the queue does not know
// cannot smuggle text into the published enum: it reads open and stays queued.
func TestQueueUnknownStateReadsOpen(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "21", map[string]string{"PR.json": queuePR("REOPENED-ish"), "findings.json": queueClean})

	resp := queueRun(t, QueueStatusOpen, 50)
	if len(resp.Data.Rows) != 1 {
		t.Fatalf("rows = %d, want the unrecognized-state bundle kept", len(resp.Data.Rows))
	}
	if got, want := resp.Data.Rows[0].State, "open"; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
}

// TestQueueIncludesUnreviewedBundle proves a bundle with no findings.json is
// queued, not dropped: it reads the missing-coverage floor at medium with both
// evidence components unmeasured.
func TestQueueIncludesUnreviewedBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "31", map[string]string{"PR.json": queuePR("OPEN"), "full.diff": queueDiff})

	resp := queueRun(t, QueueStatusOpen, 50)
	if len(resp.Data.Rows) != 1 {
		t.Fatalf("rows = %d, want the unreviewed bundle queued", len(resp.Data.Rows))
	}
	row := resp.Data.Rows[0]
	if row.Reported != LevelMedium || row.Claimed != LevelUnknown {
		t.Errorf("reported/claimed = %q/%q, want medium/unknown", row.Reported, row.Claimed)
	}
	if !slices.Contains(row.ReceiptReasons, ReasonMissingCov) {
		t.Errorf("receipt reasons = %v, want %q", row.ReceiptReasons, ReasonMissingCov)
	}
	if row.Score.Category.Measured || row.Score.Rung.Measured {
		t.Error("unmeasured category/rung, want both false without findings.json")
	}
	if !row.NeedsHuman {
		t.Error("needs_human = false, want true: an unreviewed bundle needs a human")
	}
}

// TestQueueCountsTallyAndNeedsHuman proves by_band carries all five bands,
// sums to bundles, and needs_human counts every non-low row.
func TestQueueCountsTallyAndNeedsHuman(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "41", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueCritical})
	queueBundle(t, "42", map[string]string{
		"PR.json": queuePR("OPEN"), "findings.json": queueClean,
		"record.json": queueLowRecord,
	})
	queueBundle(t, "43", map[string]string{"PR.json": queuePR("OPEN")})

	resp := queueRun(t, QueueStatusOpen, 50)
	data := resp.Data
	sum := data.ByBand.Critical + data.ByBand.High + data.ByBand.Medium + data.ByBand.Unknown + data.ByBand.Low
	if sum != data.Bundles || data.Bundles != len(data.Rows) {
		t.Errorf("by_band sums to %d, bundles = %d, rows = %d", sum, data.Bundles, len(data.Rows))
	}
	want := QueueBands{Critical: 1, Medium: 1, Low: 1}
	if data.ByBand != want {
		t.Errorf("by_band = %+v, want %+v", data.ByBand, want)
	}
	if data.NeedsHuman != 2 {
		t.Errorf("needs_human = %d, want 2 (the low bundle needs no human)", data.NeedsHuman)
	}
	if len(resp.Human) == 0 || !strings.HasPrefix(resp.Human[0],
		"risk: acme/app — 3 bundle(s), 2 need a human (critical=1 high=0 medium=1 unknown=0 low=1)") {
		t.Errorf("header = %q, want the tallied summary line", first(resp.Human))
	}
}

// TestQueueCapsRows proves --limit bounds the published rows and carries the
// response-cap receipt while the tallies still describe the whole queue.
func TestQueueCapsRows(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "51", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "52", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})

	resp := queueRun(t, QueueStatusOpen, 1)
	if len(resp.Data.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(resp.Data.Rows))
	}
	receipt := resp.Data.Receipt
	if receipt == nil || !receipt.Truncated || receipt.TruncationReason != "response_cap" ||
		receipt.Returned != 1 || receipt.Total == nil || *receipt.Total != 2 {
		t.Fatalf("receipt = %+v, want response_cap returned=1 total=2", receipt)
	}
	if resp.Data.Bundles != 2 {
		t.Errorf("bundles = %d, want the whole queue", resp.Data.Bundles)
	}
	if wire := resp.Data.Wire().MarshalCompact(); !strings.Contains(wire,
		`"truncated":true,"truncation_reason":"response_cap","returned":1,"total":2,"next_offset":null`) {
		t.Errorf("wire = %s, want the flat response-cap receipt the schema publishes", wire)
	}
	if !slices.Contains(resp.Human, "limit: 1 shown, 1 hidden") {
		t.Errorf("human = %v, want the limit line", resp.Human)
	}
}

// TestQueueWarnsMissingImpact proves an unmeasured reach never reads as a
// clean zero: the run warns with the count and names the backfill.
func TestQueueWarnsMissingImpact(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "61", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "62", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})

	resp := queueRun(t, QueueStatusOpen, 50)
	want := "risk: 2 bundle(s) carry no usable impact.json (reach unmeasured; backfill with verdict fetch acme/app <pr> --index origin/main, or fetch-local for a local bundle)"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %v, want %q", resp.Warnings, want)
	}
	if got, want := resp.Next, "verdict fetch acme/app 61 --index origin/main"; got != want {
		t.Errorf("next = %q, want the fetch backfill %q", got, want)
	}
}

// TestQueueSkipsReplayAndCorpusBundles proves the queue leaves a benchmark
// fixture and a backtest replay out of the review history, naming both counts.
func TestQueueSkipsReplayAndCorpusBundles(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "71", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "81", map[string]string{
		"PR.json": queuePR("OPEN"), "findings.json": queueClean,
		"backtest.json": `{"run":"r1"}`,
	})
	queueBundle(t, "corpus-one", map[string]string{"findings.json": queueClean})

	resp := queueRun(t, QueueStatusOpen, 50)
	if got, want := rowBundles(resp.Data.Rows), []string{"71"}; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	for _, want := range []string{
		"risk skips 1 backtest bundle(s) (replay input, not your review history)",
		"risk skips 1 corpus bundle(s) (benchmark fixture, not your review history)",
	} {
		if !slices.Contains(resp.Warnings, want) {
			t.Errorf("warnings = %v, want %q", resp.Warnings, want)
		}
	}
}

// TestQueueRefusesHostileBundleName proves a name the shared guard refuses is
// warned and skipped before any read: the bundle beside it still queues.
func TestQueueRefusesHostileBundleName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a colon in a directory name is not creatable on Windows")
	}
	t.Chdir(t.TempDir())
	queueBundle(t, "91", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "9:1", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueCritical})

	resp := queueRun(t, QueueStatusOpen, 50)
	if got, want := rowBundles(resp.Data.Rows), []string{"91"}; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v (the refused name never reads)", got, want)
	}
	joined := strings.Join(resp.Warnings, "\n")
	if !strings.Contains(joined, "risk skips 9:1 (") {
		t.Errorf("warnings = %v, want the refused name warned", resp.Warnings)
	}
}

// TestQueueSkipsOversizedFindingsStore proves a store over the cap is skipped
// with the scan-cut receipt, never read as a clean bundle.
func TestQueueSkipsOversizedFindingsStore(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := queueBundle(t, "101", map[string]string{"PR.json": queuePR("OPEN")})
	filler := strings.Repeat(" ", 8<<20)
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte("["+filler+"]"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := queueRun(t, QueueStatusOpen, 50)
	if len(resp.Data.Rows) != 0 {
		t.Fatalf("rows = %d, want the oversized store skipped", len(resp.Data.Rows))
	}
	if resp.Data.Receipt == nil || resp.Data.Receipt.TruncationReason != "scan_cap" {
		t.Errorf("receipt = %+v, want the scan-cap cut", resp.Data.Receipt)
	}
}

// TestQueueRefusesBadOptions proves a caller bypassing the CLI gets the same
// bounds the usage errors enforce.
func TestQueueRefusesBadOptions(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := []struct {
		name   string
		status string
		limit  int
	}{
		{name: "status", status: "weird"},
		{name: "negative limit", limit: -1},
		{name: "limit over the cap", limit: MaxQueueLimit + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ref := Run(context.Background(), &QueueRequest{
				Repo:   "acme/app",
				Status: tc.status,
				Limit:  tc.limit,
			})
			if ref == nil || ref.Code != "bad-flags" {
				t.Fatalf("refusal = %v, want bad-flags", ref)
			}
		})
	}
}

// TestQueueTwiceIsIdentical proves the queue is a pure read: two runs over the
// same stores publish the same rows and the same human lines.
func TestQueueTwiceIsIdentical(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "111", map[string]string{
		"PR.json": queuePR("OPEN"), "findings.json": queueCritical,
		"full.diff": queueDiff,
	})
	queueBundle(t, "local-112", map[string]string{
		"source.json": queueLocal(), "findings.json": queueClean,
		"full.diff": queueDiff,
	})

	first := queueRun(t, QueueStatusOpen, 50)
	second := queueRun(t, QueueStatusOpen, 50)
	if got, want := rowBundles(second.Data.Rows), rowBundles(first.Data.Rows); !slices.Equal(got, want) {
		t.Errorf("second run rows = %v, want %v", got, want)
	}
	if !slices.Equal(second.Human, first.Human) {
		t.Errorf("second run human = %v, want %v", second.Human, first.Human)
	}
	if got, want := first.Data.Wire().MarshalCompact(), second.Data.Wire().MarshalCompact(); got != want {
		t.Errorf("second wire differs:\n%s\n%s", got, want)
	}
}

// TestQueueSkipsUnparsableFindingsStore proves a store whose findings.json is
// not a findings array warns, skips its bundle, and carries the scan-cut
// receipt: an unreadable store never reads as a clean bundle.
func TestQueueSkipsUnparsableFindingsStore(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "71", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "72", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": `{"not":"an array"}`})
	queueBundle(t, "73", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": `[{"sig":`})

	resp := queueRun(t, QueueStatusOpen, 50)
	if got, want := rowBundles(resp.Data.Rows), []string{"71"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v: an unparsable store is skipped, never clean", got, want)
	}
	if resp.Data.Bundles != 1 {
		t.Errorf("bundles = %d, want 1", resp.Data.Bundles)
	}
	for _, name := range []string{"72", "73"} {
		want := skipWarn + name + " (findings.json is not a findings array)"
		if !slices.Contains(resp.Warnings, want) {
			t.Errorf("warnings = %v, want %q", resp.Warnings, want)
		}
	}
	receipt := resp.Data.Receipt
	if receipt == nil || !receipt.Truncated || receipt.TruncationReason != "scan_cap" ||
		receipt.Returned != 1 || receipt.Total != nil {
		t.Errorf("receipt = %+v, want scan_cap with returned=1 and an unknown total", receipt)
	}
}

// TestQueueKeepsScanCapUnderLimit proves a cut store keeps its scan signal in
// the receipt when the row cap also applies: the response receipt names the
// cut, and the warning still names the store it skipped.
func TestQueueKeepsScanCapUnderLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "81", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "82", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	queueBundle(t, "83", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": `{"not":"an array"}`})

	resp := queueRun(t, QueueStatusOpen, 1)
	receipt := resp.Data.Receipt
	if receipt == nil || !receipt.Truncated || receipt.TruncationReason != "scan_cap" ||
		receipt.Returned != 1 || receipt.Total != nil {
		t.Fatalf("receipt = %+v, want scan_cap with returned=1 and an unknown total", receipt)
	}
	if want := skipWarn + "83 (findings.json is not a findings array)"; !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %v, want %q", resp.Warnings, want)
	}
}

// TestQueueReadsTheClockOnce proves the envelope's at and data.generated_at are
// one reading: two calls let a run straddle a second between the two fields.
func TestQueueReadsTheClockOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	queueBundle(t, "91", map[string]string{"PR.json": queuePR("OPEN"), "findings.json": queueClean})
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	reads := 0
	rt := core.Runtime{Now: func() time.Time {
		reads++
		return start.Add(time.Duration(reads) * time.Hour)
	}}
	req := new(QueueRequest)
	req.Runtime = &rt
	req.Repo = "acme/app"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("risk refusal: %v", ref)
	}
	if resp.At != resp.Data.GeneratedAt {
		t.Errorf("at = %q, generated_at = %q, want one clock reading", resp.At, resp.Data.GeneratedAt)
	}
	if reads != 1 {
		t.Errorf("clock read %d times, want 1", reads)
	}
}

// queueLowRecord is a recorded bundle with a matching low claim and complete
// coverage, so the queue reads it as reported=low.
const queueLowRecord = `{"head":"` + queueHeadA + `","risk":{"level":"low",` +
	`"rationale":"One note line, fully covered.","assessed_head":"` + queueHeadA + `"},` +
	`"coverage":{"gaps":0}}`

// queueDiff is a one-line replacement so churn measures one added and one
// deleted line.
const queueDiff = `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
`

// queueRichDiff is a twelve-line replacement, so churn measures 26 lines and
// earns the 25-point bucket.
const queueRichDiff = `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,14 +1,14 @@
 top
-old 1
+new 1
-old 2
+new 2
-old 3
+new 3
-old 4
+new 4
-old 5
+new 5
-old 6
+new 6
-old 7
+new 7
-old 8
+new 8
-old 9
+new 9
-old 10
+new 10
-old 11
+new 11
-old 12
+new 12
 bottom
`

func rowBundles(rows []QueueRow) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Bundle)
	}
	return out
}

func rowState(rows []QueueRow, bundle string) string {
	for i := range rows {
		if rows[i].Bundle == bundle {
			return rows[i].State
		}
	}
	return ""
}

func first(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}
