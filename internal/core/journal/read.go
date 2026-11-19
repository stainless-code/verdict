package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

// maxReadBytes caps one streaming read of a journal file so a corrupt or
// hand-grown file cannot exhaust memory.
const maxReadBytes int64 = 4 * MaxBytes

// rawRecord decodes any single journal line without failing on phase-specific
// fields.
type rawRecord struct {
	Phase     string        `json:"phase"`
	Step      string        `json:"step"`
	Verb      string        `json:"verb"`
	Channel   string        `json:"channel"`
	Version   string        `json:"version"`
	StartedAt string        `json:"started_at"`
	EndedAt   string        `json:"ended_at"`
	Refusal   *string       `json:"refusal"`
	Next      string        `json:"next"`
	At        string        `json:"at"`
	KeptFrom  string        `json:"kept_from"`
	Bundle    BundleDigests `json:"bundle"`
	Argv      []string      `json:"argv"`
	Wrote     []FileRecord  `json:"wrote"`
	Removed   []string      `json:"removed"`
	PID       int           `json:"pid"`
	Exit      int           `json:"exit"`
	Warnings  int           `json:"warnings"`
	Dropped   int           `json:"dropped"`
}

// Read streams path without taking a lock, folds paired start/end records by
// step ID, collects unpaired starts as unfinished, and detects bundle-manifest
// chain breaks between consecutive completed steps. An empty path or missing
// file returns an empty Summary and a nil error.
func Read(path, bundleName string) (*Summary, error) {
	summary := emptySummary(bundleName)
	if path == "" {
		return summary, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return summary, nil
		}
		return nil, wrapErr("open "+path, err)
	}
	defer func() { _ = f.Close() }()
	r, err := tailReader(f, maxReadBytes)
	if err != nil {
		return nil, wrapErr("read tail of "+path, err)
	}
	return foldStream(r, summary)
}

// tailReader returns a reader over the newest content of f: the whole file when
// it fits in maxBytes, and otherwise the last maxBytes starting at a record
// boundary. A capped read therefore keeps the newest records instead of the
// oldest, and the line the cut lands in is dropped rather than counted as torn.
func tailReader(f *os.File, maxBytes int64) (io.Reader, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= maxBytes {
		return f, nil
	}
	if _, err := f.Seek(info.Size()-maxBytes, io.SeekStart); err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	if err := discardPartialLine(br); err != nil {
		return nil, err
	}
	return br, nil
}

func discardPartialLine(br *bufio.Reader) error {
	for {
		b, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if b == '\n' {
			return nil
		}
	}
}

func emptySummary(bundleName string) *Summary {
	return &Summary{
		Bundle:      bundleName,
		Steps:       []Step{},
		Unfinished:  []UnfinishedStep{},
		ChainBreaks: []ChainBreak{},
	}
}

type foldState struct {
	summary      *Summary
	pending      map[string]UnfinishedStep
	lastAfter    string
	pendingOrder []string
}

func foldStream(r io.Reader, summary *Summary) (*Summary, error) {
	st := &foldState{
		summary: summary,
		pending: map[string]UnfinishedStep{},
	}
	br := bufio.NewReader(r)
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			st.consumeLine(raw)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, wrapErr("read journal stream", err)
		}
	}
	st.finishUnfinished()
	return st.summary, nil
}

func (st *foldState) consumeLine(raw []byte) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	var rec rawRecord
	if err := json.Unmarshal(trimmed, &rec); err != nil {
		st.summary.UnreadableLines++
		return
	}
	switch rec.Phase {
	case PhaseStart:
		st.applyStart(&rec)
	case PhaseEnd:
		st.applyEnd(&rec)
	case PhaseTruncated:
		st.applyTruncated(&rec)
	default:
		st.summary.UnreadableLines++
	}
}

func (st *foldState) applyStart(rec *rawRecord) {
	if rec.Step == "" || rec.Verb == "" {
		st.summary.UnreadableLines++
		return
	}
	if _, exists := st.pending[rec.Step]; !exists {
		st.pendingOrder = append(st.pendingOrder, rec.Step)
	}
	st.pending[rec.Step] = UnfinishedStep{
		Step:      rec.Step,
		Verb:      rec.Verb,
		Channel:   rec.Channel,
		PID:       rec.PID,
		StartedAt: rec.StartedAt,
		Argv:      nonNilStrings(rec.Argv),
		Version:   rec.Version,
	}
}

func (st *foldState) applyEnd(rec *rawRecord) {
	if rec.Step == "" || rec.Verb == "" {
		st.summary.UnreadableLines++
		return
	}
	start, hasStart := st.pending[rec.Step]
	if hasStart {
		delete(st.pending, rec.Step)
	}
	step := foldedStep(rec, &start, hasStart)
	if st.lastAfter != "" && step.Bundle.Before != nil && *step.Bundle.Before != "" && *step.Bundle.Before != st.lastAfter {
		st.summary.ChainBreaks = append(st.summary.ChainBreaks, ChainBreak{
			Step:          step.Step,
			Before:        *step.Bundle.Before,
			PreviousAfter: st.lastAfter,
		})
	}
	if step.Bundle.After != "" {
		st.lastAfter = step.Bundle.After
	}
	st.summary.Steps = append(st.summary.Steps, step)
}

func (st *foldState) applyTruncated(rec *rawRecord) {
	var at, keptFrom *string
	if rec.At != "" {
		v := rec.At
		at = &v
	}
	if rec.KeptFrom != "" {
		v := rec.KeptFrom
		keptFrom = &v
	}
	st.summary.Truncation = TruncatedInfo{
		Dropped:  rec.Dropped,
		At:       at,
		KeptFrom: keptFrom,
	}
}

func (st *foldState) finishUnfinished() {
	for _, id := range st.pendingOrder {
		if u, ok := st.pending[id]; ok {
			st.summary.Unfinished = append(st.summary.Unfinished, u)
		}
	}
}

func foldedStep(rec *rawRecord, start *UnfinishedStep, hasStart bool) Step {
	startedAt := rec.StartedAt
	if startedAt == "" && hasStart {
		startedAt = start.StartedAt
	}
	version := rec.Version
	if version == "" && hasStart {
		version = start.Version
	}
	var argv []string
	if hasStart {
		argv = start.Argv
	}
	wrote := rec.Wrote
	if wrote == nil {
		wrote = []FileRecord{}
	}
	return Step{
		Step:       rec.Step,
		Verb:       rec.Verb,
		Channel:    rec.Channel,
		PID:        rec.PID,
		Argv:       nonNilStrings(argv),
		Version:    version,
		StartedAt:  startedAt,
		EndedAt:    rec.EndedAt,
		Exit:       rec.Exit,
		Refusal:    rec.Refusal,
		Warnings:   rec.Warnings,
		Bundle:     rec.Bundle,
		Wrote:      wrote,
		Removed:    nonNilStrings(rec.Removed),
		Next:       rec.Next,
		DurationMs: durationMillis(startedAt, rec.EndedAt),
	}
}

func durationMillis(startedAt, endedAt string) int64 {
	s, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return 0
	}
	e, err := time.Parse(time.RFC3339Nano, endedAt)
	if err != nil || e.Before(s) {
		return 0
	}
	return e.Sub(s).Milliseconds()
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
