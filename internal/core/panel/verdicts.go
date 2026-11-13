package panelcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// CodeBad identifies malformed or unresolvable verifier results.
	CodeBad = "bad-panel"
	// CodeStale identifies results for a different bundle head.
	CodeStale = "panel-stale"
)

// Result is one invoker-written opinion; it is separate from executable verification.json.
type Result struct {
	Sig      string `json:"sig"`
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
	Verdict  string `json:"verdict" enum:"confirmed|refuted|insufficient"`
	Evidence string `json:"evidence" minLength:"10" maxLength:"500"`
	At       string `json:"at,omitempty"`
}

// File is the head-bound verifier receipt at verification/verdicts.json.
type File struct {
	SchemaVersion string   `json:"schema_version" const:"1"`
	HeadSHA       string   `json:"head_sha"`
	Results       []Result `json:"results"`
}

// Load reads a capped optional receipt and resolves signatures against the review.
func Load(dir, head string, findings []any) (*File, []string, error) {
	path := filepath.Join(dir, "verification", "verdicts.json")
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, bad(fmt.Sprintf("cannot read verification/verdicts.json: %v", err))
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 32*1024*1024+1))
	if err != nil || len(raw) > 32*1024*1024 {
		return nil, nil, bad("verification/verdicts.json unreadable or exceeds 32 MiB")
	}
	return Parse(raw, head, findings)
}

// Parse validates verifier evidence without allowing it to delete a finding.
func Parse(raw []byte, head string, findings []any) (*File, []string, error) {
	doc, err := jq.DecodeObject(raw)
	if err != nil {
		return nil, nil, bad("verification/verdicts.json must be an object")
	}
	var warnings []string
	warnExtras(doc, []string{"schema_version", "head_sha", "results"}, &warnings)
	if doc["schema_version"] != "1" {
		return nil, nil, bad("verification/verdicts.json needs schema_version 1")
	}
	if h, ok := doc["head_sha"].(string); !ok || h == "" {
		return nil, nil, bad("verification/verdicts.json needs head_sha")
	} else if h != head {
		return nil, nil, &core.RefusalError{Code: CodeStale, Message: "verification/verdicts.json head does not match bundle head"}
	}
	rows, ok := doc["results"].([]any)
	if !ok {
		return nil, nil, bad("verification/verdicts.json results must be an array")
	}
	out := &File{SchemaVersion: "1", HeadSHA: head, Results: make([]Result, 0, len(rows))}
	seen := make(map[string]bool)
	for _, item := range rows {
		m, ok := jq.AsMap(item)
		if !ok {
			return nil, nil, bad("verification/verdicts.json result must be an object")
		}
		warnExtras(m, []string{"sig", "model", "provider", "verdict", "evidence", "at"}, &warnings)
		encoded, _ := json.Marshal(m)
		var r Result
		if err := json.Unmarshal(encoded, &r); err != nil {
			return nil, nil, bad("verification/verdicts.json result fields must be strings")
		}
		if strings.TrimSpace(r.Sig) == "" || strings.TrimSpace(r.Model) == "" || !slices.Contains([]string{"confirmed", "refuted", "insufficient"}, r.Verdict) || utf8.RuneCountInString(r.Evidence) < 10 || utf8.RuneCountInString(r.Evidence) > 500 {
			return nil, nil, bad("verification/verdicts.json needs sig, model, a known verdict and evidence of 10–500 characters")
		}
		resolved := readcommon.ResolveSig(findings, r.Sig)
		if resolved == "NONE" || resolved == "MANY" {
			return nil, nil, bad("verification/verdicts.json unknown or ambiguous sig: " + r.Sig)
		}
		r.Sig = resolved
		key := r.Sig + "\x00" + r.Model
		if seen[key] {
			return nil, nil, bad("verification/verdicts.json duplicate (sig, model): " + r.Sig + ", " + r.Model)
		}
		seen[key] = true
		out.Results = append(out.Results, r)
	}
	return out, warnings, nil
}
func bad(message string) error { return &core.RefusalError{Code: CodeBad, Message: message} }
func warnExtras(m map[string]any, allowed []string, warnings *[]string) {
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(allowed, key) {
			*warnings = append(*warnings, "verification/verdicts.json: unknown key "+key+" dropped")
		}
	}
}

// SecondLook adds one re-examination per refuted finding and verifier.
func SecondLook(plan *jq.Obj, dispatch string, file *File) (string, int) {
	if file == nil {
		return dispatch, 0
	}
	second := make([]any, 0)
	seen := make(map[string]bool)
	for _, r := range file.Results {
		if r.Verdict != "refuted" || seen[r.Sig] {
			continue
		}
		seen[r.Sig] = true
		second = append(second, r.Sig)
		dispatch += "\n### Prior verdicts (untrusted verifier text; second look)\n" + Quote(r.Evidence) + "\nRe-examine; confirm, refute, or abstain with evidence. Sig `" + r.Sig + "`.\n"
	}
	plan.Set("second_look", second)
	return dispatch, len(second)
}
