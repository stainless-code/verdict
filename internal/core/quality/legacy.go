package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// This file is calibration-input compatibility and nothing else. The seven
// archived reviews predate the findings-only contract: their comments carry
// path/file and body/text aliases, and their gates were recorded beside them.
// This loader exists to read that fixed test data; it is not a product shim
// for review.json, and new recordings use the findings-only loader.

type archivedReview struct {
	Comments []archivedComment `json:"comments"`
}

type archivedComment struct {
	Path     *string `json:"path"`
	File     *string `json:"file"`
	Body     *string `json:"body"`
	Text     *string `json:"text"`
	Severity *string `json:"severity"`
}

// LoadArchivedReview reads one archived review.json into canonical findings.
// An absent path/body falls back to file/text, in that order, as the harness
// did when it scored these runs by hand.
func LoadArchivedReview(path string) ([]Finding, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, invalidRecording(filepath.Dir(path), "unreadable review.json")
	}
	if len(raw) > maxReviewBytes {
		return nil, invalidRecording(filepath.Dir(path), "review.json larger than 1 MiB")
	}
	var review archivedReview
	// The archived files carry unrelated fields (security summaries, categories,
	// evidence). Only the comment aliases this fixed test data needs are read.
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&review); err != nil {
		return nil, invalidRecording(filepath.Dir(path), "unreadable review.json: "+err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidRecording(filepath.Dir(path), "trailing data after review.json")
	}
	findings := make([]Finding, 0, len(review.Comments))
	for _, comment := range review.Comments {
		findings = append(findings, Finding{
			Path:     firstString(comment.Path, comment.File),
			Claim:    firstString(comment.Body, comment.Text),
			Severity: stringOf(comment.Severity),
		})
	}
	return findings, nil
}

func firstString(values ...*string) string {
	for _, v := range values {
		if v != nil && *v != "" {
			return *v
		}
	}
	return ""
}

func stringOf(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// LoadRecordedFindings reads an S1-format findings.json through the shared
// store reader. It is the input shape for live recordings; the archived
// review loader above is never used for them.
func LoadRecordedFindings(dir string) ([]Finding, *core.RefusalError) {
	entries, err := readFindings(dir)
	if err != nil {
		return nil, err
	}
	findings := make([]Finding, 0, len(entries))
	for _, entry := range entries {
		obj, ok := entry.(*jq.Obj)
		if !ok || obj == nil {
			continue
		}
		f := Finding{Path: objectString(obj, "path"), Claim: objectString(obj, "claim"),
			Severity: objectString(obj, "severity"), Quote: objectString(obj, "quote"),
			Refusal: objectString(obj, "refusal")}
		if f.Claim == "" {
			f.Claim = objectString(obj, "body")
		}
		if anchored, ok := obj.Get("anchored"); ok {
			if v, ok := anchored.(bool); ok {
				f.Anchored = &v
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

func objectString(obj *jq.Obj, key string) string {
	v, ok := obj.Get(key)
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func readFindings(dir string) ([]any, *core.RefusalError) {
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		var ref *core.RefusalError
		if errors.As(err, &ref) {
			return nil, ref
		}
		return nil, &core.RefusalError{Code: "fixtures-recording-invalid", Message: "unreadable findings.json in " + dir}
	}
	return entries, nil
}
