package loopcore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCaptureLastJudgesChangeNotClocks pins how a review attempt claims the
// bundle's cost.json: by whether the record changed since the attempt was
// admitted, never by comparing its modification time with a wall clock. The
// filesystem stamps files from a coarser clock than [time.Now], so a record
// written just after admission can carry an earlier stamp; that flaked the
// self-report fallback on Linux.
func TestCaptureLastJudgesChangeNotClocks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		before string // the record at admission; "" for none
		after  string // the record once the attempt ran; "" leaves it alone
		want   string
	}{
		{name: "a fresh record stamped before admission counts", after: `{"est_usd":0.1}`, want: `{"est_usd":0.1}`},
		{name: "an earlier attempt's untouched record is not this one's", before: `{"est_usd":0.1}`},
		{name: "a rewrite with an equal stamp counts", before: `{"est_usd":0.1}`, after: `{"est_usd":0.2}`, want: `{"est_usd":0.2}`},
		{name: "no record at all", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := captureAround(t, tc.before, tc.after); got != tc.want {
				t.Fatalf("record = %q, want %q", got, tc.want)
			}
		})
	}
}

// captureAround admits one attempt with before as the standing record, lets
// the attempt leave after, and returns the record the attempt claims. Every
// write is stamped an hour back, as a coarse filesystem clock may.
func captureAround(t *testing.T, before, after string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, costFile)
	past := time.Now().Add(-time.Hour)
	stamp := func(body string) {
		if body == "" {
			return
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatal(err)
		}
	}
	stamp(before)
	attempts := []*attemptCost{{id: "a-1", prior: snapshotCost(dir)}}
	stamp(after)
	captureLast(dir, attempts)
	return string(attempts[0].record)
}
