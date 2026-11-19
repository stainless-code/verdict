// Package journal owns the per-bundle append-only audit trail
// (<bundle>/journal.jsonl): start/end invocation records, content-digest bundle
// sweeps and write diffs, lock-guarded appends with whole-line truncation at
// the size cap, and the lock-free streaming fold reader.
package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/storelock"
)

const (
	// JournalFile is the bundle-relative filename of the append-only journal.
	JournalFile = "journal.jsonl"
	// JournalLockFile is the advisory lock file beside [JournalFile].
	JournalLockFile = JournalFile + storelock.LockSuffix
	// SchemaVersion is the wire schema version every journal line carries.
	SchemaVersion = "1"

	// PhaseStart marks the opening line of a verb invocation.
	PhaseStart = "start"
	// PhaseEnd marks the closing line of a verb invocation.
	PhaseEnd = "end"
	// PhaseTruncated marks a truncation notice prepended after a cap rewrite.
	PhaseTruncated = "truncated"

	// ChannelCLI identifies an invocation entered through the CLI front door.
	ChannelCLI = "cli"
	// ChannelMCP identifies an invocation entered through the MCP server.
	ChannelMCP = "mcp"

	// MaxBytes is the journal size cap (1 MiB) that triggers whole-line
	// truncation after an append.
	MaxBytes int64 = 1 << 20
	// KeepBytes is the target tail size (512 KiB) retained on truncation.
	KeepBytes int64 = 512 << 10

	// journalFileMode is the owner-only permission mode for journal.jsonl.
	journalFileMode os.FileMode = 0o600
)

// IsJournalArtifact reports whether a bundle entry is the journal itself or an
// advisory lock file that sweeps and receipts must exclude.
func IsJournalArtifact(relSlash, base string) bool {
	return relSlash == JournalFile || relSlash == JournalLockFile || strings.HasSuffix(base, storelock.LockSuffix)
}

// WarningMessage formats the frozen diagnostic warning for an append failure.
func WarningMessage(path string, err error) string {
	return fmt.Sprintf("journal: could not append to %s: %v", path, err)
}

// AppendStart marshals and appends a start line to <dir>/journal.jsonl under
// the bundle's advisory store lock.
func AppendStart(dir string, line *StartLine, now time.Time) error {
	raw, err := marshalLine(line)
	if err != nil {
		return err
	}
	return appendLineWithLimits(filepath.Join(dir, JournalFile), raw, now, storelock.DefaultWait, MaxBytes, KeepBytes)
}

// AppendEnd marshals and appends an end line to <dir>/journal.jsonl under the
// bundle's advisory store lock.
func AppendEnd(dir string, line *EndLine, now time.Time) error {
	raw, err := marshalLine(line)
	if err != nil {
		return err
	}
	return appendLineWithLimits(filepath.Join(dir, JournalFile), raw, now, storelock.DefaultWait, MaxBytes, KeepBytes)
}

type opError struct {
	err error
	op  string
}

func (e opError) Error() string { return e.op + ": " + e.err.Error() }

func (e opError) Unwrap() error { return e.err }

func wrapErr(op string, err error) error { return opError{err: err, op: op} }

var errSymlinkJournal = errors.New("refusing symlinked journal path")

func marshalLine(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, wrapErr("encode journal line", err)
	}
	return append(raw, '\n'), nil
}

// appendLineWithLimits appends one newline-terminated JSON line under
// [storelock.Acquire], refusing a symlinked journal path and truncating whole
// lines atomically when the file exceeds maxBytes.
func appendLineWithLimits(path string, raw []byte, now time.Time, wait time.Duration, maxBytes, keepBytes int64) error {
	if err := refuseSymlink(path); err != nil {
		return err
	}
	unlock, err := storelock.Acquire(path, wait)
	if err != nil {
		return wrapErr("acquire lock for "+path, err)
	}
	defer unlock()
	if err := refuseSymlink(path); err != nil {
		return err
	}
	size, err := appendRawLocked(path, raw)
	if err != nil {
		return err
	}
	if maxBytes > 0 && size > maxBytes {
		return truncateLocked(path, now, keepBytes)
	}
	return nil
}

func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return wrapErr("stat "+path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return wrapErr(path, errSymlinkJournal)
	}
	return nil
}

func appendRawLocked(path string, raw []byte) (int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, journalFileMode)
	if err != nil {
		return 0, wrapErr("open "+path, err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return 0, wrapErr("write "+path, err)
	}
	info, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil {
		return 0, wrapErr("stat "+path, statErr)
	}
	if closeErr != nil {
		return 0, wrapErr("close "+path, closeErr)
	}
	return info.Size(), nil
}

func truncateLocked(path string, now time.Time, keepBytes int64) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return wrapErr("read "+path+" for truncation", err)
	}
	lines := splitWholeLines(raw)
	if len(lines) == 0 {
		return nil
	}
	splitIdx := keepSuffixIndex(lines, keepBytes)
	dropped := countDroppedLines(lines[:splitIdx])
	kept := lines[splitIdx:]
	at := now.UTC().Format(time.RFC3339Nano)
	notice := TruncatedLine{
		SchemaVersion: SchemaVersion,
		Phase:         PhaseTruncated,
		At:            at,
		KeptFrom:      firstLineTimestamp(kept[0], at),
		Dropped:       dropped,
	}
	header, err := marshalLine(&notice)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	out.Write(header)
	for _, line := range kept {
		out.Write(line)
		out.WriteByte('\n')
	}
	if err := atomic.WriteFileMode(path, out.Bytes(), journalFileMode); err != nil {
		return wrapErr("rewrite truncated "+path, err)
	}
	return nil
}

func splitWholeLines(raw []byte) [][]byte {
	parts := bytes.Split(raw, []byte{'\n'})
	lines := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			lines = append(lines, part)
		}
	}
	return lines
}

// keepSuffixIndex returns the starting index of the longest trailing slice of
// whole lines whose total size (including newlines) fits within keepBytes.
// At least the final line is always kept.
func keepSuffixIndex(lines [][]byte, keepBytes int64) int {
	var total int64
	idx := len(lines)
	for i, line := range slices.Backward(lines) {
		lineBytes := int64(len(line)) + 1
		if idx < len(lines) && total+lineBytes > keepBytes {
			break
		}
		total += lineBytes
		idx = i
	}
	return idx
}

func countDroppedLines(droppedLines [][]byte) int {
	dropped := 0
	for _, line := range droppedLines {
		var probe struct {
			Phase   string `json:"phase"`
			Dropped int    `json:"dropped"`
		}
		if err := json.Unmarshal(line, &probe); err == nil && probe.Phase == PhaseTruncated && probe.Dropped > 0 {
			dropped += probe.Dropped
			continue
		}
		dropped++
	}
	return dropped
}

func firstLineTimestamp(line []byte, fallback string) string {
	var probe struct {
		StartedAt string `json:"started_at"`
		EndedAt   string `json:"ended_at"`
		Step      string `json:"step"`
		At        string `json:"at"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return fallback
	}
	for _, ts := range []string{probe.StartedAt, probe.EndedAt, probe.Step, probe.At} {
		if ts != "" {
			return ts
		}
	}
	return fallback
}
