package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	metaSuffix    = ".meta.json"
	recordingExt  = ".json"
	fixtureFormat = "fixture-recording-v1"
)

// FixtureRecording is the corpus's own input shape: a flat list of findings
// and the gate outcome. There is no reader for any other review format.
type FixtureRecording struct {
	GatePassed    *bool            `json:"gate_passed"`
	SchemaVersion string           `json:"schema_version"`
	Findings      []FixtureFinding `json:"findings"`
}

// FixtureFinding is one finding in a fixture recording. Quote, Side, and
// StartLine are optional so historical pre-S4 recordings keep decoding and
// report no-quote honestly.
type FixtureFinding struct {
	Severity  *string `json:"severity"`
	Line      *int    `json:"line"`
	StartLine *int    `json:"start_line,omitempty"`
	Side      *string `json:"side,omitempty"`
	Quote     *string `json:"quote,omitempty"`
	Path      string  `json:"path"`
	Text      string  `json:"text"`
}

// LoadFixtureRecording reads one fixture-format recording with strict JSON.
func LoadFixtureRecording(path string) (*FixtureRecording, *core.RefusalError) {
	raw, err := jq.CappedFile(path, maxReviewBytes)
	if err != nil {
		return nil, invalidRecording(path, "unreadable fixture recording")
	}
	var rec FixtureRecording
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return nil, invalidRecording(path, "unreadable fixture recording: "+err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidRecording(path, "trailing data after fixture recording")
	}
	if err := validateRecording(&rec, path); err != nil {
		return nil, err
	}
	return &rec, nil
}

func validateRecording(rec *FixtureRecording, path string) *core.RefusalError {
	if rec.SchemaVersion != schemaVersion {
		return invalidRecording(path, `missing schema_version "1"`)
	}
	for i := range rec.Findings {
		if rec.Findings[i].Path == "" || rec.Findings[i].Text == "" {
			return invalidRecording(path, "finding without path or text")
		}
	}
	return nil
}

// Canonical converts one fixture finding into the scorer's input shape. A
// positive line makes the finding anchored, which is what the gating
// predicate and the S4 quote verifier see.
func (f *FixtureFinding) Canonical() Finding {
	anchored := f.Line != nil && *f.Line > 0
	return Finding{
		Path: f.Path, Claim: f.Text, Severity: stringOf(f.Severity),
		Quote: stringOf(f.Quote), Side: stringOf(f.Side),
		Line: intOf(f.Line), StartLine: intOf(f.StartLine), Anchored: &anchored,
	}
}

func intOf(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// RecordingFiles lists the fixture recordings under one fixture's directory.
// Meta files are the sidecars, never scorer input.
func RecordingFiles(dir string) ([]string, *core.RefusalError) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, invalidRecording(dir, "unreadable recordings directory")
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, recordingExt) || strings.HasSuffix(name, metaSuffix) ||
			strings.HasPrefix(name, ".") || name != filepath.Base(name) {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files, nil
}

// MetaPath returns the sidecar path for one fixture recording.
func MetaPath(recording string) string {
	return strings.TrimSuffix(recording, recordingExt) + metaSuffix
}

func stringOf(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
