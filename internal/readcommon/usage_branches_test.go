package readcommon_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestMatchContractUsageTiebreaks pins the two tiebreaks behind "the run that
// produced this cost record": the session that ends nearest at wins even when
// it started earlier, and an equal end hands it to the later start.
func TestMatchContractUsageTiebreaks(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		want  string
		usage []readcommon.ContractUsage
	}{
		{
			name: "nearest end beats the later start",
			usage: []readcommon.ContractUsage{
				{Source: "ends-soon", Started: at.Add(-5 * time.Hour), Ended: at.Add(time.Hour)},
				{Source: "ends-late", Started: at.Add(-time.Hour), Ended: at.Add(2 * time.Hour)},
			},
			want: "ends-soon",
		},
		{
			name: "equal end hands it to the later start",
			usage: []readcommon.ContractUsage{
				{Source: "later-start", Started: at.Add(-time.Hour), Ended: at.Add(time.Hour)},
				{Source: "earlier-start", Started: at.Add(-5 * time.Hour), Ended: at.Add(time.Hour)},
			},
			want: "later-start",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertMatchSource(t, tc.usage, at, tc.want)
		})
	}
}

// assertMatchSource checks the source a match resolves to, refusing a nil
// match outright.
func assertMatchSource(t *testing.T, usage []readcommon.ContractUsage, at time.Time, want string) {
	t.Helper()
	match := readcommon.MatchContractUsage(usage, at)
	if match == nil {
		t.Fatalf("MatchContractUsage = nil; want %q", want)
	}
	if match.Source != want {
		t.Errorf("match = %q; want %q", match.Source, want)
	}
}

// TestReadContractUsageAcceptsZeroValues pins the non-negative guards as
// inclusive: a measured zero price and zero token counts are measurements,
// not malformed handoffs, and refusing them drops a real run from the rollup.
func TestReadContractUsageAcceptsZeroValues(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "zero.json")
	writeUsageFixture(t, file, `{"schema_version":"1","source":"zero-run","usd":0,`+
		`"tokens":{"input":0,"output":0,"cache_read":0,"cache_write":0},`+
		`"started_at":"2026-01-01T00:00:00Z","ended_at":"2026-01-01T01:00:00Z"}`)
	usage, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": file})
	if err != nil {
		t.Fatalf("ReadContractUsage(zeroes) error: %v", err)
	}
	if len(usage) != 1 {
		t.Fatalf("records = %d; want 1", len(usage))
	}
	got := usage[0]
	if got.USD == nil || *got.USD != 0 {
		t.Errorf("USD = %v; want 0", got.USD)
	}
	if !got.HasTokens || got.Input != 0 || got.Output != 0 {
		t.Errorf("tokens = %+v; want present and zero", got)
	}
}

// TestReadContractUsageSizeCap pins the handoff size gate as inclusive at the
// MiB: a file of exactly the cap reads, one byte more refuses typed. An
// off-by-one makes a legitimate harness dump unusable (or admits an
// unbounded read).
func TestReadContractUsageSizeCap(t *testing.T) {
	t.Parallel()
	const capBytes = 1 << 20
	t.Run("exactly the cap reads", func(t *testing.T) {
		t.Parallel()
		assertUsageReads(t, paddedUsage(t, capBytes), "padded")
	})
	t.Run("one byte over refuses", func(t *testing.T) {
		t.Parallel()
		assertUsageRefused(t, paddedUsage(t, capBytes+1))
	})
}

// paddedUsage renders one contract record padded with spaces to exactly size
// bytes; the padding rides inside the JSON, which tolerates trailing blanks.
func paddedUsage(t *testing.T, size int) string {
	t.Helper()
	record := `{"schema_version":"1","source":"padded",` +
		`"started_at":"2026-01-01T00:00:00Z","ended_at":"2026-01-01T01:00:00Z"}`
	if len(record) > size {
		t.Fatalf("record is %d bytes, over the requested %d", len(record), size)
	}
	return record + strings.Repeat(" ", size-len(record))
}

// assertUsageReads checks that content reads back as exactly one record whose
// source is wantSource.
func assertUsageReads(t *testing.T, content, wantSource string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "usage.json")
	writeUsageFixture(t, file, content)
	usage, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": file})
	if err != nil {
		t.Fatalf("ReadContractUsage(%d bytes) error: %v", len(content), err)
	}
	if len(usage) != 1 || usage[0].Source != wantSource {
		t.Fatalf("records = %+v; want the %q record", usage, wantSource)
	}
}

// assertUsageRefused checks that content refuses with the typed code.
func assertUsageRefused(t *testing.T, content string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "usage.json")
	writeUsageFixture(t, file, content)
	_, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": file})
	usageRefusalCode(t, err)
}

// TestUsageRefusalNamesTheRecordIndex pins the refusal's where-clause: a
// per-record failure names the 1-based record (a harness reads that number to
// find the bad tuple), and a whole-file failure names the file alone — never
// "record 0", which reads as a record that exists.
func TestUsageRefusalNamesTheRecordIndex(t *testing.T) {
	t.Parallel()
	good := `{"schema_version":"1","source":"ok",` +
		`"started_at":"2026-01-01T00:00:00Z","ended_at":"2026-01-01T01:00:00Z"}`
	bad := `{"schema_version":"1","source":"bad","usd":-1,` +
		`"started_at":"2026-01-01T00:00:00Z","ended_at":"2026-01-01T01:00:00Z"}`
	tests := []struct {
		name    string
		content string
		want    string
		avoid   string
	}{
		{name: "second record", content: "[" + good + "," + bad + "]", want: " record 2:"},
		{name: "whole file", content: "{broken", want: "not usage JSON", avoid: "record "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertUsageRefusalSays(t, tc.content, tc.want, tc.avoid)
		})
	}
}

// assertUsageRefusalSays checks a refusal's where-clause: it names want and
// never carry.
func assertUsageRefusalSays(t *testing.T, content, want, avoid string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "usage.json")
	writeUsageFixture(t, file, content)
	_, err := readcommon.ReadContractUsage(map[string]string{"VERDICT_USAGE": file})
	usageRefusalCode(t, err)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal = %q; want it to name %q", err, want)
	}
	if avoid != "" && strings.Contains(err.Error(), avoid) {
		t.Errorf("refusal = %q; must not carry %q", err, avoid)
	}
}
