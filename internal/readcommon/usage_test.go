package readcommon_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// writeUsageFixture writes one file under root, creating parents.
func writeUsageFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadClaudeTranscript proves the Claude Code reader prefers the
// authoritative cost-state snapshot, dedupes repeated request lines, and
// keeps transcript timestamps.
func TestReadClaudeTranscript(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	dir := filepath.Join(t.TempDir(), "projects", readcommon.ClaudeProjectSlug(cwd))
	transcript := filepath.Join(dir, "s1.jsonl")
	writeUsageFixture(t, transcript, `{"type":"assistant","sessionId":"s1","cwd":"`+cwd+`","timestamp":"2026-01-01T00:00:00Z","requestId":"r1","costUSD":0.10,"message":{"id":"m1"}}
{"type":"assistant","sessionId":"s1","cwd":"`+cwd+`","timestamp":"2026-01-01T00:01:00Z","requestId":"r1","costUSD":0.10,"message":{"id":"m1"}}
{"type":"assistant","sessionId":"s1","cwd":"`+cwd+`","timestamp":"2026-01-01T00:02:00Z","requestId":"r2","costUSD":0.20,"message":{"id":"m2"}}
{"type":"cost-state","sessionId":"s1","cwd":"`+cwd+`","totalCostUSD":0.50,"startTime":1767225600000}
`)
	env := map[string]string{"CLAUDE_CONFIG_DIR": filepath.Dir(filepath.Dir(dir))}
	usage := readcommon.ReadHarnessUsage(cwd, env)
	if len(usage) != 1 {
		t.Fatalf("usage sessions = %d, want 1", len(usage))
	}
	got := usage[0]
	if got.Source != "claude-code" || got.Session != "s1" {
		t.Errorf("identity = %s/%s", got.Source, got.Session)
	}
	if got.USD == nil || *got.USD != 0.50 {
		t.Errorf("USD = %v, want the cost-state 0.50", got.USD)
	}
	if !got.Started.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("started = %v", got.Started)
	}
	if !got.Ended.Equal(time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC)) {
		t.Errorf("ended = %v", got.Ended)
	}
}

// TestReadClaudeTranscriptFallbacks proves summed per-request costs price a
// transcript without a cost-state, and tokens without a price stay nil.
func TestReadClaudeTranscriptFallbacks(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	base := t.TempDir()
	dir := filepath.Join(base, "projects", readcommon.ClaudeProjectSlug(cwd))
	priced := filepath.Join(dir, "priced.jsonl")
	writeUsageFixture(t, priced, `{"type":"assistant","sessionId":"p","cwd":"`+cwd+`","requestId":"a","costUSD":0.4}
{"type":"assistant","sessionId":"p","cwd":"`+cwd+`","requestId":"b","costUSD":0.6}
`)
	tokensOnly := filepath.Join(dir, "tokens.jsonl")
	writeUsageFixture(t, tokensOnly, `{"type":"assistant","sessionId":"t","cwd":"`+cwd+`","message":{"id":"m","usage":{"input_tokens":10,"output_tokens":5}}}
`)
	usage := readcommon.ReadHarnessUsage(cwd, map[string]string{"CLAUDE_CONFIG_DIR": base})
	if len(usage) != 2 {
		t.Fatalf("sessions = %d, want 2", len(usage))
	}
	byID := map[string]readcommon.HarnessUsage{}
	for _, record := range usage {
		byID[record.Session] = record
	}
	if got := byID["p"].USD; got == nil || *got != 1.0 {
		t.Errorf("summed USD = %v, want 1.0", got)
	}
	if got := byID["t"].USD; got != nil {
		t.Errorf("token-only USD = %v, want nil (the tool never prices)", got)
	}
}

// TestReadHarnessUsageSkipsOtherDirectories proves a transcript from another
// checkout never feeds this checkout's estimate.
func TestReadHarnessUsageSkipsOtherDirectories(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	base := t.TempDir()
	dir := filepath.Join(base, "projects", readcommon.ClaudeProjectSlug(cwd))
	writeUsageFixture(t, filepath.Join(dir, "other.jsonl"),
		`{"type":"cost-state","sessionId":"o","cwd":"/somewhere/else","totalCostUSD":9.0}`)
	if usage := readcommon.ReadHarnessUsage(cwd, map[string]string{"CLAUDE_CONFIG_DIR": base}); len(usage) != 0 {
		t.Fatalf("other-directory session leaked: %+v", usage)
	}
}

// TestReadOpencodeExport proves the opencode export shape reads the session
// total, falls back to message costs, and honors the session directory.
func TestReadOpencodeExport(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	total := filepath.Join(t.TempDir(), "session.json")
	writeUsageFixture(t, total, `{"info":{"id":"ses_a","directory":"`+cwd+`","cost":1.25,`+
		`"time":{"created":1000,"updated":2000}},"messages":[]}`)
	messages := filepath.Join(t.TempDir(), "messages.json")
	writeUsageFixture(t, messages, `{"info":{"id":"ses_b","directory":"`+cwd+`","time":{"created":3000,"updated":4000}},`+
		`"messages":[{"info":{"cost":0.4}},{"info":{"cost":0.6}}]}`)
	other := filepath.Join(t.TempDir(), "other.json")
	writeUsageFixture(t, other, `{"info":{"id":"ses_c","directory":"/elsewhere","cost":9},"messages":[]}`)
	env := map[string]string{"VERDICT_HARNESS_USAGE": total + string(os.PathListSeparator) + messages + string(os.PathListSeparator) + other}
	usage := readcommon.ReadHarnessUsage(cwd, env)
	if len(usage) != 2 {
		t.Fatalf("sessions = %d, want 2", len(usage))
	}
	byID := map[string]readcommon.HarnessUsage{}
	for _, record := range usage {
		byID[record.Session] = record
	}
	if got := byID["ses_a"]; got.Source != "opencode" || got.USD == nil || *got.USD != 1.25 {
		t.Errorf("total session = %+v", got)
	}
	if got := byID["ses_b"].USD; got == nil || *got != 1.0 {
		t.Errorf("message-summed USD = %v, want 1.0", got)
	}
}

// TestMatchHarnessUsage proves a cost record joins the session whose window
// brackets it, nearest end first, and that no containment reads as nil.
func TestMatchHarnessUsage(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	usage := []readcommon.HarnessUsage{
		{Source: "claude-code", Session: "outer", Started: start, Ended: start.Add(4 * time.Hour)},
		{Source: "claude-code", Session: "inner", Started: start.Add(time.Hour), Ended: start.Add(2 * time.Hour)},
	}
	match := readcommon.MatchHarnessUsage(usage, start.Add(90*time.Minute))
	if match == nil || match.Session != "inner" {
		t.Fatalf("match = %+v, want inner", match)
	}
	if got := readcommon.MatchHarnessUsage(usage, start.Add(5*time.Hour)); got != nil {
		t.Fatalf("outside match = %+v, want nil", got)
	}
}
