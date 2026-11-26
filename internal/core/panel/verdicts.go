package panelcore

import (
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
	fieldModel      = "model"
	fieldEvidence   = "evidence"
	maxReceiptBytes = 32 * 1024 * 1024
	// MinEvidence is the shortest evidence a result carries, in characters.
	MinEvidence = 10
	// MaxEvidence is the longest evidence a result carries, in characters.
	MaxEvidence = 500
	// CodeBad identifies malformed or unresolvable verifier results.
	CodeBad = "bad-panel"
	// CodeStale identifies results for a different bundle head.
	CodeStale = "panel-stale"
)

// Result is one invoker-written opinion; it is separate from executable
// verification.json.
type Result struct {
	Checker  string     `json:"checker"                        minLength:"1"`
	Quote    *CodeQuote `anyOf:"null"                          json:"quote,omitempty"`
	Sig      string     `json:"sig"                            minLength:"1"`
	Model    string     `json:"model,omitempty"`
	Provider string     `json:"provider,omitempty"`
	Verdict  string     `enum:"confirmed|refuted|insufficient" json:"verdict"`
	Evidence string     `json:"evidence"                       maxLength:"500"        minLength:"10"`
	At       string     `json:"at,omitempty"`
}

// File is the head-bound verifier receipt at verification/verdicts.json.
type File struct {
	ExecutionID   string   `json:"execution_id,omitempty"`
	DiffSHA256    string   `json:"diff_sha256,omitempty"`
	ReviewSHA256  string   `json:"review_sha256,omitempty"`
	PlanSHA256    string   `json:"plan_sha256,omitempty"`
	SchemaVersion string   `const:"2"                      json:"schema_version"`
	HeadSHA       string   `json:"head_sha"                minLength:"1"`
	Results       []Result `json:"results"`
}

// Load reads a capped optional receipt and resolves signatures against the
// review.
func Load(dir, head string, findings []any) (*File, []string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, bad(fmt.Sprintf("cannot open bundle for verification/verdicts.json: %v", err))
	}
	defer func() { _ = root.Close() }()
	f, err := readcommon.OpenNoBlockIn(root, filepath.Join("verification", "verdicts.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, bad(fmt.Sprintf("cannot read verification/verdicts.json: %v", err))
	}
	defer func() { _ = f.Close() }()
	if info, statErr := f.Stat(); statErr != nil || !info.Mode().IsRegular() {
		return nil, nil, bad("verification/verdicts.json unreadable or exceeds 32 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxReceiptBytes+1))
	if err != nil || len(raw) > maxReceiptBytes {
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
	warnExtras(doc, []string{
		"schema_version", executionHead, "results",
		executionIDKey, executionDiffSHA, executionReviewSHA, executionPlanSHA,
	}, &warnings)
	if doc["schema_version"] != "2" {
		return nil, nil, bad("verification/verdicts.json needs schema_version 2")
	}
	if err := checkHead(doc, head); err != nil {
		return nil, nil, err
	}
	rows, ok := doc["results"].([]any)
	if !ok {
		return nil, nil, bad("verification/verdicts.json results must be an array")
	}
	results, err := parseResults(rows, findings, &warnings)
	if err != nil {
		return nil, nil, err
	}
	return &File{
		SchemaVersion: "2", HeadSHA: head, Results: results,
		ExecutionID: jq.StrOf(doc[executionIDKey]), DiffSHA256: jq.StrOf(doc[executionDiffSHA]),
		ReviewSHA256: jq.StrOf(doc[executionReviewSHA]), PlanSHA256: jq.StrOf(doc[executionPlanSHA]),
	}, warnings, nil
}

func parseResults(rows, findings []any, warnings *[]string) ([]Result, error) {
	results := make([]Result, 0, len(rows))
	seen := make(map[string]bool)

	for _, item := range rows {
		r, err := parseResult(item, findings, warnings)
		if err != nil {
			return nil, err
		}
		key := r.Sig + "\x00" + r.Checker
		if seen[key] {
			return nil, bad("verification/verdicts.json duplicate (sig, checker): " + r.Sig + ", " + r.Checker)
		}
		seen[key] = true
		results = append(results, r)
	}

	return results, nil
}

func bad(message string) error {
	return &core.RefusalError{Code: CodeBad, Message: core.Msgf("%s", message)}
}

func warnExtras(m map[string]any, allowed []string, warnings *[]string) {
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(allowed, key) {
			*warnings = append(*warnings, "verification/verdicts.json: unknown key "+key+" dropped")
		}
	}
}

// SecondLook attaches quoted prior refutations to each relevant dispatch.
func SecondLook(plan *jq.Obj, dispatch string, file *File) (string, int) {
	if file == nil {
		return dispatch, 0
	}
	second, evidence := priorRefutations(file)
	for _, id := range second {
		s, ok := id.(string)
		if !ok {
			continue
		}
		prior := "### Prior verdicts (untrusted verifier text; second look)\n" + evidence[s] +
			"Re-examine; confirm, refute, or abstain with evidence.\n\n"
		needle := "### Output\nAppend one result for sig `" + s + "`"
		dispatch = strings.ReplaceAll(dispatch, needle, prior+needle)
	}
	plan.Set("second_look", second)
	return dispatch, len(second)
}

func parseResult(item any, findings []any, warnings *[]string) (Result, error) {
	m, ok := jq.AsMap(item)
	if !ok {
		return Result{}, bad("verification/verdicts.json result must be an object")
	}
	warnExtras(m, []string{fieldSig, fieldModel, "provider", fieldVerdict, fieldEvidence, "at", "checker", "quote"}, warnings)
	r, err := decodeResult(m)
	if err != nil {
		return r, err
	}
	if !validResult(&r) {
		return r, bad("verification/verdicts.json needs sig, model, a known verdict and evidence of 10–500 characters")
	}
	resolved := readcommon.ResolveSig(findings, r.Sig)
	if resolved == "NONE" || resolved == "MANY" {
		return r, bad("verification/verdicts.json unknown or ambiguous sig: " + r.Sig + "; candidates: " + candidateSigs(findings))
	}
	r.Sig = resolved
	return r, nil
}

func validResult(r *Result) bool {
	n := utf8.RuneCountInString(r.Evidence)
	return strings.TrimSpace(r.Sig) != "" && strings.TrimSpace(r.Checker) != "" &&
		slices.Contains([]string{confirmed, refuted, insufficient}, r.Verdict) && n >= MinEvidence && n <= MaxEvidence
}

func checkHead(doc map[string]any, head string) error {
	if h, ok := doc[executionHead].(string); !ok || h == "" {
		return bad("verification/verdicts.json needs head_sha")
	} else if h != head {
		return &core.RefusalError{Code: CodeStale, Message: "verification/verdicts.json head does not match bundle head"}
	}
	return nil
}

func priorRefutations(file *File) ([]any, map[string]string) {
	second := make([]any, 0)
	evidence := make(map[string]string)
	for _, r := range file.Results {
		if r.Verdict != refuted {
			continue
		}
		if evidence[r.Sig] == "" {
			second = append(second, r.Sig)
		}
		quoted := Quote(r.Evidence) + "\n"
		if !strings.Contains(evidence[r.Sig], quoted) {
			evidence[r.Sig] += quoted
		}
	}
	return second, evidence
}

func candidateSigs(findings []any) string {
	ids := make([]string, 0, len(findings))
	for _, item := range findings {
		m, _ := object(item)
		ids = append(ids, jq.StrOf(m[fieldSig]))
	}
	return strings.Join(ids, ", ")
}

// WithStored carries current findings' persisted refutations into planning.
func WithStored(file *File, entries, findings []any) *File {
	out := &File{}
	if file != nil {
		out.Results = append(out.Results, file.Results...)
	}
	current := make(map[string]bool, len(findings))
	for _, item := range findings {
		m, _ := object(item)
		current[jq.StrOf(m[fieldSig])] = true
	}
	for _, item := range entries {
		m, _ := object(item)
		if !live(m) || !current[jq.StrOf(m[fieldSig])] {
			continue
		}
		out.Results = append(out.Results, storedRefutations(m)...)
	}
	return out
}

func storedRefutations(entry map[string]any) []Result {
	var results []Result
	for _, item := range storedPanel(entry["verification"]) {
		m, _ := object(item)
		if m[fieldVerdict] == refuted {
			results = append(results, Result{
				Sig:   jq.StrOf(entry[fieldSig]),
				Model: jq.StrOf(m[fieldModel]), Verdict: refuted, Evidence: jq.StrOf(m[fieldEvidence]),
			})
		}
	}
	return results
}

func decodeResult(m map[string]any) (Result, error) {
	for _, key := range []string{fieldSig, fieldModel, "provider", fieldVerdict, fieldEvidence, "at"} {
		if _, ok := m[key].(string); !ok && m[key] != nil {
			return Result{}, bad("verification/verdicts.json result fields must be strings")
		}
	}
	var quote *CodeQuote
	if m["quote"] != nil {
		value, err := parseQuote(m["quote"])
		if err != nil {
			return Result{}, err
		}
		quote = &value
	}
	return Result{
		Quote: quote, Checker: jq.StrOf(m["checker"]), Sig: jq.StrOf(m[fieldSig]), Model: jq.StrOf(m[fieldModel]),
		Provider: jq.StrOf(m["provider"]),
		Verdict:  jq.StrOf(m[fieldVerdict]), Evidence: jq.StrOf(m[fieldEvidence]), At: jq.StrOf(m["at"]),
	}, nil
}
