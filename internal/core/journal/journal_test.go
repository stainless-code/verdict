package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/storelock"
)

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mustWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func mustSweep(t *testing.T, dir string) Snapshot {
	t.Helper()
	snap, err := Sweep(dir)
	if err != nil {
		t.Fatalf("Sweep(%s): %v", dir, err)
	}
	return snap
}

func rewritePreservingMtime(t *testing.T, path string, data []byte) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func TestSweepAndDiffJ3ContentDigest(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "local-7")
	absent := mustSweep(t, dir)
	if absent.Exists || absent.Digest != "" || len(absent.Files) != 0 {
		t.Fatalf("Sweep(absent) = %+v, want empty non-existent snapshot", absent)
	}
	origBytes := []byte(`{"v":"aaaa"}`)
	subBytes := []byte("hello\n")
	findingsPath := filepath.Join(dir, "findings.json")
	mustWriteFile(t, findingsPath, origBytes, 0o644)
	mustWriteFile(t, filepath.Join(dir, "sub", "note.txt"), subBytes, 0o644)
	for _, skip := range []string{"journal.jsonl", "journal.jsonl.lock", "findings.json.lock"} {
		mustWriteFile(t, filepath.Join(dir, skip), []byte("ignored"), 0o600)
	}
	snap1 := mustSweep(t, dir)
	assertInitialSnapshot(t, snap1, origBytes, subBytes)

	// Same-size rewrite with preserved mtime (AMENDMENTS J3).
	rewritten := []byte(`{"v":"bbbb"}`)
	rewritePreservingMtime(t, findingsPath, rewritten)
	snap2 := mustSweep(t, dir)
	assertSameSizeRewriteDetected(t, snap1, snap2, rewritten)

	// File removal is captured in removed.
	if err := os.Remove(filepath.Join(dir, "sub", "note.txt")); err != nil {
		t.Fatal(err)
	}
	wrote3, removed3 := Diff(snap2, mustSweep(t, dir))
	if len(wrote3) != 0 || !slices.Equal(removed3, []string{"sub/note.txt"}) {
		t.Fatalf("Diff(remove) = (%+v, %v), want ([], [sub/note.txt])", wrote3, removed3)
	}
}

func assertInitialSnapshot(t *testing.T, snap Snapshot, origBytes, subBytes []byte) {
	t.Helper()
	if !snap.Exists || len(snap.Files) != 2 {
		t.Fatalf("snap = %+v, want Exists=true and 2 files", snap)
	}
	if snap.Files[0].Path != "findings.json" || snap.Files[1].Path != "sub/note.txt" {
		t.Fatalf("snap.Files = %+v, want findings.json and sub/note.txt", snap.Files)
	}
	wantManifest := "findings.json\x0012\x00" + sha256Hex(origBytes) + "\n" +
		"sub/note.txt\x006\x00" + sha256Hex(subBytes) + "\n"
	if want := sha256Hex([]byte(wantManifest)); snap.Digest != want {
		t.Fatalf("snap.Digest = %q, want %q", snap.Digest, want)
	}
}

func assertSameSizeRewriteDetected(t *testing.T, snap1, snap2 Snapshot, rewritten []byte) {
	t.Helper()
	if snap2.Digest == snap1.Digest {
		t.Fatalf("same-size rewrite with preserved mtime produced identical digest %q", snap2.Digest)
	}
	wrote, removed := Diff(snap1, snap2)
	if len(wrote) != 1 || wrote[0].Path != "findings.json" || wrote[0].SHA256 != sha256Hex(rewritten) || wrote[0].Bytes != 12 {
		t.Fatalf("Diff wrote = %+v, want findings.json with new sha256", wrote)
	}
	if len(removed) != 0 {
		t.Fatalf("Diff removed = %v, want empty", removed)
	}
}

func mustAppendStart(t *testing.T, dir string, line *StartLine, now time.Time) {
	t.Helper()
	if err := AppendStart(dir, line, now); err != nil {
		t.Fatalf("AppendStart: %v", err)
	}
}

func mustAppendEnd(t *testing.T, dir string, line *EndLine, now time.Time) {
	t.Helper()
	if err := AppendEnd(dir, line, now); err != nil {
		t.Fatalf("AppendEnd: %v", err)
	}
}

func appendRawBytes(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		t.Fatalf("appendRawBytes: write=%v close=%v", werr, cerr)
	}
}

func TestAppendReadChainBreakCrashAndTornTail(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "local-7")
	target := filepath.Join(dir, "findings.json")
	mustWriteFile(t, target, []byte(`{"state":"one"}`), 0o644)
	s1 := mustSweep(t, dir)

	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	step1 := t0.Format(time.RFC3339Nano)
	t1 := t0.Add(250 * time.Millisecond)
	mustAppendStart(t, dir, &StartLine{
		SchemaVersion: SchemaVersion, Phase: PhaseStart, Step: step1,
		Verb: "fetch-local", Channel: ChannelCLI, PID: 1001,
		Argv: []string{"fetch-local", "acme/app", "local-7"}, Version: "0.14.2", StartedAt: step1,
	}, t0)
	mustAppendEnd(t, dir, &EndLine{
		SchemaVersion: SchemaVersion, Phase: PhaseEnd, Step: step1,
		Verb: "fetch-local", Channel: ChannelCLI, PID: 1001,
		StartedAt: step1, EndedAt: t1.Format(time.RFC3339Nano),
		Bundle: BundleDigests{Before: nil, After: s1.Digest},
		Wrote:  s1.Files, Removed: []string{}, Next: "run the review",
	}, t1)

	// Out-of-band same-size edit with preserved mtime between steps -> chain break.
	rewritePreservingMtime(t, target, []byte(`{"state":"two"}`))
	s2 := mustSweep(t, dir)
	t2 := t1.Add(time.Second)
	step2 := t2.Format(time.RFC3339Nano)
	t3 := t2.Add(500 * time.Millisecond)
	before2 := s2.Digest
	mustAppendStart(t, dir, &StartLine{
		SchemaVersion: SchemaVersion, Phase: PhaseStart, Step: step2,
		Verb: "record", Channel: ChannelCLI, PID: 1002,
		Argv: []string{"record", "acme/app", "local-7"}, Version: "0.14.2", StartedAt: step2,
	}, t2)
	mustAppendEnd(t, dir, &EndLine{
		SchemaVersion: SchemaVersion, Phase: PhaseEnd, Step: step2,
		Verb: "record", Channel: ChannelCLI, PID: 1002,
		StartedAt: step2, EndedAt: t3.Format(time.RFC3339Nano),
		Warnings: 1, Bundle: BundleDigests{Before: &before2, After: s2.Digest},
		Wrote: []FileRecord{}, Removed: []string{}, Next: "report",
	}, t3)

	// Simulated crash (unpaired start) + torn tail line.
	t4 := t3.Add(time.Second)
	step3 := t4.Format(time.RFC3339Nano)
	mustAppendStart(t, dir, &StartLine{
		SchemaVersion: SchemaVersion, Phase: PhaseStart, Step: step3,
		Verb: "report", Channel: ChannelCLI, PID: 1003,
		Argv: []string{"report", "acme/app", "local-7"}, Version: "0.14.2", StartedAt: step3,
	}, t4)
	appendRawBytes(t, filepath.Join(dir, JournalFile), `{"schema_version":"1","phase":"end","step":"truncated`)

	sum, err := Read(dir, "local-7")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertFoldedAuditSummary(t, sum, step2, step3, s1.Digest, s2.Digest)
	assertAuditHumanOutput(t, sum)
}

func assertFoldedAuditSummary(t *testing.T, sum *Summary, step2, step3, after1, before2 string) {
	t.Helper()
	assertFoldedSteps(t, sum)
	if len(sum.ChainBreaks) != 1 || sum.ChainBreaks[0] != (ChainBreak{Step: step2, Before: before2, PreviousAfter: after1}) {
		t.Fatalf("ChainBreaks = %+v, want step=%s before=%s prev=%s", sum.ChainBreaks, step2, before2, after1)
	}
	assertUnfinishedAndUnreadable(t, sum, step3)
}

func assertFoldedSteps(t *testing.T, sum *Summary) {
	t.Helper()
	if sum.Bundle != "local-7" || len(sum.Steps) != 2 {
		t.Fatalf("Summary bundle=%q steps=%d, want local-7 and 2", sum.Bundle, len(sum.Steps))
	}
	if sum.Steps[0].Verb != "fetch-local" || sum.Steps[0].DurationMs != 250 {
		t.Fatalf("Steps[0] = %+v, want fetch-local 250ms", sum.Steps[0])
	}
	if sum.Steps[1].Verb != "record" || sum.Steps[1].DurationMs != 500 || sum.Steps[1].Warnings != 1 {
		t.Fatalf("Steps[1] = %+v, want record 500ms 1 warning", sum.Steps[1])
	}
}

func assertUnfinishedAndUnreadable(t *testing.T, sum *Summary, step3 string) {
	t.Helper()
	if len(sum.Unfinished) != 1 || sum.Unfinished[0].Step != step3 || sum.Unfinished[0].Verb != "report" || sum.Unfinished[0].PID != 1003 {
		t.Fatalf("Unfinished = %+v, want report pid 1003", sum.Unfinished)
	}
	if sum.UnreadableLines != 1 {
		t.Fatalf("UnreadableLines = %d, want 1", sum.UnreadableLines)
	}
}

func assertAuditHumanOutput(t *testing.T, sum *Summary) {
	t.Helper()
	human := strings.Join(FormatHuman(sum), "\n")
	for _, want := range []string{
		"journal: local-7 (2 steps, 1 unfinished, 1 chain breaks)",
		"fetch-local",
		"next: run the review",
		"unfinished:",
		"report  pid 1003  (no end recorded)",
		"chain breaks:",
		"unreadable: 1 line skipped",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("FormatHuman missing %q in:\n%s", want, human)
		}
	}
}

func TestAppendTruncationPreservesWholeLines(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "local-cap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, JournalFile)
	baseTime := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	const (
		testMaxBytes  int64 = 1200
		testKeepBytes int64 = 600
	)
	for i := range 12 {
		appendSyntheticEnd(t, path, baseTime, i, testMaxBytes, testKeepBytes)
	}
	header := assertAllWholeJSONLines(t, path)
	sum, err := Read(dir, "local-cap")
	if err != nil {
		t.Fatalf("Read after truncation: %v", err)
	}
	if sum.UnreadableLines != 0 || sum.Truncated.Dropped != header.Dropped || sum.Truncated.At == nil || sum.Truncated.KeptFrom == nil {
		t.Fatalf("Summary after truncation = %+v, want 0 unreadable and dropped=%d", sum, header.Dropped)
	}
	if len(sum.Steps)+sum.Truncated.Dropped != 12 {
		t.Fatalf("kept (%d) + dropped (%d) != 12", len(sum.Steps), sum.Truncated.Dropped)
	}
}

func appendSyntheticEnd(t *testing.T, path string, base time.Time, idx int, maxBytes, keepBytes int64) {
	t.Helper()
	ts := base.Add(time.Duration(idx) * time.Second)
	step := ts.Format(time.RFC3339Nano)
	raw, err := marshalLine(&EndLine{
		SchemaVersion: SchemaVersion, Phase: PhaseEnd, Step: step,
		Verb: "record", Channel: ChannelCLI, PID: 2000 + idx,
		StartedAt: step, EndedAt: ts.Add(10 * time.Millisecond).Format(time.RFC3339Nano),
		Bundle: BundleDigests{After: "aaaa"}, Wrote: []FileRecord{}, Removed: []string{}, Next: "report",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := appendLineWithLimits(path, raw, ts, storelock.DefaultWait, maxBytes, keepBytes); err != nil {
		t.Fatalf("append %d: %v", idx, err)
	}
}

func assertAllWholeJSONLines(t *testing.T, path string) TruncatedLine {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitWholeLines(raw)
	if len(lines) < 2 {
		t.Fatalf("expected truncated header + kept lines, got %d lines", len(lines))
	}
	header := decodeTruncatedHeader(t, lines[0])
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			t.Fatalf("line %d invalid JSON: %v (%q)", i, err, string(line))
		}
	}
	return header
}

func decodeTruncatedHeader(t *testing.T, line []byte) TruncatedLine {
	t.Helper()
	var header TruncatedLine
	if err := json.Unmarshal(line, &header); err != nil {
		t.Fatalf("first line invalid JSON: %v", err)
	}
	if header.Phase != PhaseTruncated || header.Dropped <= 0 || header.KeptFrom == "" {
		t.Fatalf("unexpected truncated header: %+v", header)
	}
	return header
}

func TestAppendRefusesSymlinkAndBusyLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	mustWriteFile(t, outside, []byte("keep"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, JournalFile)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	err := AppendStart(dir, &StartLine{
		SchemaVersion: SchemaVersion, Phase: PhaseStart, Step: t0.Format(time.RFC3339Nano),
		Verb: "record", Channel: ChannelCLI, PID: 1, Argv: []string{"record"}, Version: "1.0.0",
		StartedAt: t0.Format(time.RFC3339Nano),
	}, t0)
	if !errors.Is(err, errSymlinkJournal) {
		t.Fatalf("AppendStart on symlink err = %v, want errSymlinkJournal", err)
	}
	if gotOutside, rerr := os.ReadFile(outside); rerr != nil || string(gotOutside) != "keep" {
		t.Fatalf("symlink target mutated: %q err=%v", string(gotOutside), rerr)
	}

	lockDir := t.TempDir()
	jpath := filepath.Join(lockDir, JournalFile)
	unlock, err := storelock.Acquire(jpath, storelock.DefaultWait)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer unlock()
	err = appendLineWithLimits(jpath, []byte(`{"phase":"start"}`+"\n"), t0, 20*time.Millisecond, MaxBytes, KeepBytes)
	if !errors.Is(err, storelock.ErrBusy) {
		t.Fatalf("append under held lock err = %v, want storelock.ErrBusy", err)
	}
}

func TestReadMissingJournalAndWireShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sum, err := Read(dir, "local-empty")
	if err != nil {
		t.Fatalf("Read(missing): %v", err)
	}
	if len(sum.Steps) != 0 || len(sum.Unfinished) != 0 || len(sum.ChainBreaks) != 0 || sum.UnreadableLines != 0 {
		t.Fatalf("Read(missing) = %+v, want empty", sum)
	}
	if human := FormatHuman(sum); !slices.Equal(human, []string{"journal: local-empty (no calls recorded)"}) {
		t.Fatalf("FormatHuman(empty) = %q, want single empty line", human)
	}
	wire := sum.Wire().MarshalCompact()
	wantWire := `{"bundle":"local-empty","steps":[],"unfinished":[],"chain_breaks":[],"truncated":{"dropped":0,"at":null,"kept_from":null},"unreadable_lines":0}`
	if wire != wantWire {
		t.Fatalf("Wire = %s, want %s", wire, wantWire)
	}
}
