package recordcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// CheckResult is CheckReview's output: every problem in document order, the
// overflow past the 50-problem cap, the evidence receipt the bundle bytes
// support, and the summary a clean dry run reports.
type CheckResult struct {
	Evidence  *EvidenceCheck    `json:"evidence,omitempty"`
	Problems  []Problem         `json:"problems"`
	Summary   core.CheckSummary `json:"summary"`
	Truncated int               `json:"problems_truncated"`
	// Errors counts the shape errors and the evidence errors together, so
	// "no errors" means the review is both well shaped and evidenced.
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// EvidenceCheck is the check's evidence half, kept behind one pointer so
// CheckResult stays small enough to pass by value: the receipt the bundle
// stores, the pass's gaps as ordinary problems in the pass's order, and how
// many of those gaps are errors. The gaps are separate from Problems so a
// consumer that refuses on a malformed document (record's bad-review-input)
// keeps reading only the shape check, while the repair loop feeds both lists to
// the agent in one re-prompt.
type EvidenceCheck struct {
	Receipt  *core.EvidenceReceipt `json:"receipt,omitempty"`
	Problems []Problem             `json:"problems"`
	Errors   int                   `json:"errors"`
}

// evidenceReceipt returns the stored receipt, or nil when the bytes never
// decoded and no evidence pass ran.
func (c CheckResult) evidenceReceipt() *core.EvidenceReceipt {
	if c.Evidence == nil {
		return nil
	}
	return c.Evidence.Receipt
}

// ShapeErrors is the count of the review's own (non-evidence) errors: the
// number record's bad-review-input refusal reports.
func (c CheckResult) ShapeErrors() int {
	if c.Evidence == nil {
		return c.Errors
	}
	return c.Errors - c.Evidence.Errors
}

// CheckInput carries everything the one review checker can validate beyond
// review.json: the fetched diff, the chunk manifest's names, and the optional
// verification claims. Empty fields leave that check out.
type CheckInput struct {
	ExcludedPaths   map[string]bool
	Diff            string
	HeadSHA         string
	ChunkNames      []string
	ChunkManifest   string
	Verification    []byte
	HasVerification bool
}

// Problem is one CheckReview finding.
type Problem = core.Problem

const (
	problemCap           = 50
	nearestKeyMax        = 64
	problemRoot          = "$"
	fieldSchemaVersion   = "schema_version"
	problemSchemaVersion = "$." + fieldSchemaVersion
	problemFindings      = "findings"
	problemSpecChecks    = "spec_checks"
	problemReceipts      = "receipts"
	problemSummary       = "summary"
	problemRationale     = "rationale"
	problemPathSuffix    = ".path"
	exampleFile          = "file.go"
	exampleLine          = 10
	exampleEllipsis      = "..."
	exampleImportant     = "important"
	exampleWontFix       = "wont-fix"
	exampleFalsePositive = "false-positive"
	maxSummaryRunes      = 2000
	prefixMissing        = "missing "
	prefixWrite          = "write "
)

var (
	knownReviewKeys = []string{
		"execution", fieldSchemaVersion, problemFindings, problemSpecChecks, problemReceipts, problemSummary, suppressedKey,
		fieldResolved, fieldTriage, fieldChunks, fieldCoverage, fieldSecuritySummary, fieldRisk,
	}
	knownFindingKeys = []string{
		fieldPath, fieldLine, fieldStartLine, fieldSide, fieldClaim, fieldQuote, fieldConfidenceInput,
		fieldRigor, fieldSeverity, fieldCategory, fieldSources, fieldSuggestion, fieldBrief, fieldKB, "reproduction",
	}
	knownSpecCheckKeys = []string{fieldItem, fieldStatus, fieldEvidence}
	knownEvidenceKeys  = []string{fieldPath, fieldLine, fieldQuote}
	knownReceiptKeys   = []string{"hunk", fieldQuote}
	knownTriageKeys    = []string{fieldSig, fieldStatus, problemRationale}
	knownCoverageKeys  = []string{fieldFile, fieldRanges}
	knownRiskKeys      = []string{fieldLevel, problemRationale, fieldAssessedHead}
	reviewKeys         = []string{tierCritical, exampleImportant, "informational"}
	finderConfidence   = []string{confidenceHigh, "medium", "low"}
	specStatuses       = []string{"met", "violated", "n/a"}
	riskLevels         = []string{"low", "medium", "high", "critical"}
	rigorKeys          = []string{rungSaid, rungPointed, rungWalked, rungRan, rungReproduced}
	triageStatuses     = []string{"open", "addressed", exampleWontFix, exampleFalsePositive, "deferred"}
)

// CheckReview validates a review.json against a unified diff. It is pure: it
// reads no store and writes nothing, and it collects every problem instead of
// stopping at the first.
func CheckReview(review []byte, diff string) CheckResult {
	return CheckReviewInput(review, CheckInput{Diff: diff})
}

// CheckReviewInput runs the same checker with the bundle inputs it can also
// validate: chunk attestation names and verification claims. It reads nothing.
//
//nolint:gocritic // the small input struct is copied once per call
func CheckReviewInput(review []byte, in CheckInput) CheckResult {
	c := &checker{
		res:             CheckResult{Problems: []Problem{}},
		hunks:           hunks.Parse(in.Diff),
		excludedPaths:   in.ExcludedPaths,
		headSHA:         in.HeadSHA,
		chunkNames:      in.ChunkNames,
		chunkManifest:   in.ChunkManifest,
		hasChunks:       in.ChunkNames != nil,
		verification:    in.Verification,
		hasVerification: in.HasVerification,
	}
	if problem := invalidJSON(review); problem != nil {
		c.add(*problem)
		return *c.result()
	}
	doc, err := jq.DecodeOrdered(review)
	if err != nil {
		c.invalidJSON(err)
		return *c.result()
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		c.add(c.problem(problemRoot, core.ProblemCodeWrongType, core.ProblemLevelError,
			"review.json must hold a JSON object",
			"write one JSON object with schema_version and findings[]", reviewObjectExample()))
		return *c.result()
	}
	c.checkReview(obj)
	c.checkVerification()
	return *c.result()
}

type checker struct {
	excludedPaths   map[string]bool
	hunks           []hunks.Hunk
	chunkNames      []string
	chunkManifest   string
	headSHA         string
	verification    []byte
	res             CheckResult
	comments        int
	moved           int
	hasChunks       bool
	hasVerification bool
}

func (c *checker) result() *CheckResult {
	c.res.Summary = core.CheckSummary{
		Findings:           c.comments,
		AnchorsMovedToBody: c.moved,
	}
	if c.res.Problems == nil {
		c.res.Problems = []Problem{}
	}
	return &c.res
}

//nolint:gocritic // Problem is copied once into the result
func (c *checker) add(p Problem) {
	switch p.Level {
	case core.ProblemLevelError:
		c.res.Errors++
	case core.ProblemLevelWarning:
		c.res.Warnings++
	}
	if len(c.res.Problems) >= problemCap {
		c.res.Truncated++
		return
	}
	c.res.Problems = append(c.res.Problems, p)
}

func (c *checker) problem(path, code, level, message, fix string, example ...any) Problem {
	p := Problem{Path: path, Code: code, Level: level, Message: message, Fix: fix}
	if len(example) > 0 {
		p.Example = example[0]
	}
	return p
}

func (c *checker) wrongType(path, message, fix string, example ...any) {
	c.add(c.problem(path, core.ProblemCodeWrongType, core.ProblemLevelError, message, fix, example...))
}

func (c *checker) missing(path, message, fix string, example ...any) {
	c.add(c.problem(path, core.ProblemCodeMissingField, core.ProblemLevelError, message, fix, example...))
}

func (c *checker) badEnum(path, message, fix string, example any) {
	c.add(c.problem(path, core.ProblemCodeBadEnum, core.ProblemLevelError, message, fix, example))
}

func (c *checker) outOfRange(path, message, fix string, example any) {
	c.add(c.problem(path, core.ProblemCodeOutOfRange, core.ProblemLevelError, message, fix, example))
}

// unknownRoot refuses a root key review.schema.json does not publish. The
// document is closed, so an old-shape key such as approved, body or comments
// has no reader and must be rewritten, not ignored.
func (c *checker) unknownRoot(key string) {
	fix := "remove it"
	if near := nearest(key, knownReviewKeys); near != "" {
		fix = fmt.Sprintf("remove it or rename it to the nearest known key %q", near)
	}
	c.add(c.problem(problemRoot+"."+key, core.ProblemCodeUnknownField, core.ProblemLevelError,
		fmt.Sprintf("unknown field %q", key), fix))
}

func (c *checker) unknown(path, key string, known []string) {
	fix := "remove it"
	if near := nearest(key, known); near != "" {
		fix = fmt.Sprintf("remove it or rename it to the nearest known key %q", near)
	}
	c.add(c.problem(path+"."+key, core.ProblemCodeUnknownField, core.ProblemLevelWarning,
		fmt.Sprintf("unknown field %q", key), fix))
}

// checkReview walks the top-level object in document order.
//
//nolint:cyclop,revive // one branch per published review key
func (c *checker) checkReview(obj *jq.Obj) {
	for _, key := range obj.Keys {
		v, _ := obj.Get(key)
		switch key {
		case "execution":
		case fieldSchemaVersion:
			execution, _ := obj.Get("execution")
			c.checkSchemaVersion(v, execution != nil)
		case problemFindings:
			c.checkFindings(v)
		case problemSpecChecks:
			c.checkSpecChecks(v)
		case problemReceipts:
			c.checkReceipts(v)
		case problemSummary:
			c.checkSummary(v)
		case suppressedKey:
		case fieldResolved:
			c.checkStringArray("$.resolved", v, "list finding sigs as strings")
		case fieldTriage:
			c.checkTriage(v)
		case fieldSecuritySummary:
			c.checkSecuritySummary(v)
		case fieldRisk:
			c.checkRisk(v)
		case fieldChunks:
			c.checkChunks(v)
		case fieldCoverage:
			c.checkCoverage(v)
		default:
			c.unknownRoot(key)
		}
	}
	if _, ok := obj.Get(fieldSchemaVersion); !ok {
		c.missing(problemSchemaVersion, "missing schema_version", "set \"schema_version\": \"1\"", schemaVersion1)
	}
	if _, ok := obj.Get(problemFindings); !ok {
		c.missing("$.findings", "missing findings", "write \"findings\": []", []any{})
	}
	if findings, ok := obj.Get(problemFindings); ok {
		if items, ok := findings.([]any); ok && len(items) == 0 {
			summary, _ := obj.Get(problemSummary)
			if jq.StrOf(summary) == "" {
				c.add(c.problem(problemRoot+"."+problemSummary, core.ProblemCodeEmptyFindingsNeedsSummary,
					core.ProblemLevelError, "a review with no findings needs a non-empty summary",
					"set summary to a short explanation of the clean review",
					"Reviewed the diff; no defects found."))
			}
		}
	}
	if c.hasChunks {
		if _, ok := obj.Get(fieldChunks); !ok {
			c.unattestedChunks()
		}
	}
}

func (c *checker) checkSchemaVersion(v any, automatic bool) {
	s, ok := v.(string)
	if !ok {
		c.wrongType(problemSchemaVersion, "schema_version must be the string \"1\"", "set \"schema_version\": \"1\"", schemaVersion1)
		return
	}
	if s != schemaVersion1 && (s != "2" || !automatic) {
		c.badEnum(problemSchemaVersion, fmt.Sprintf("unknown schema_version %q", s),
			"write the only supported version", schemaVersion1)
	}
}

func (c *checker) checkString(path string, v any, required bool, fix string) {
	if v == nil && !required {
		return
	}
	if _, ok := v.(string); !ok {
		if required && v == nil {
			c.missing(path, "missing required string", fix, "...")
			return
		}
		c.wrongType(path, "value must be a string", fix, exampleEllipsis)
	}
}

// checkFilled checks a string review.schema.json closes with minLength 1: an
// empty value is as useless as an absent one, so it refuses the same way.
func (c *checker) checkFilled(path string, v any, fix string) {
	str, isString := v.(string)
	if isString && str != "" {
		return
	}
	if v == nil {
		c.missing(path, "missing required string", fix, exampleEllipsis)
		return
	}
	if !isString {
		c.wrongType(path, "value must be a string", fix, exampleEllipsis)
		return
	}
	c.outOfRange(path, "value must not be empty", fix, exampleEllipsis)
}

func (c *checker) checkStringArray(path string, v any, fix string) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType(path, "value must be an array of strings", fix, []string{exampleEllipsis})
		return
	}
	for i, item := range items {
		if _, ok := item.(string); !ok {
			c.wrongType(fmt.Sprintf("%s[%d]", path, i), "array entry must be a string", fix, exampleEllipsis)
		}
	}
}

func (c *checker) checkFindings(v any) {
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.findings", "findings must be an array",
			"write an array of {path, line, severity, claim, quote, confidence} objects", []any{findingExample()})
		return
	}
	c.comments = len(items)
	for i, item := range items {
		c.checkFinding(i, item)
	}
}

func (c *checker) checkSpecChecks(v any) {
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.spec_checks", "spec_checks must be an array", "write an array of spec check objects", []any{})
		return
	}
	for i, item := range items {
		base := fmt.Sprintf("$.spec_checks[%d]", i)
		m, ok := item.(*jq.Obj)
		if !ok {
			c.wrongType(base, "spec check must be an object", "write {item, status, evidence}",
				specCheckExample())
			continue
		}
		c.checkSpecCheckFields(base, m)
		c.requireKeys(base, m, knownSpecCheckKeys)
	}
}

// checkSpecCheckFields dispatches every published spec-check key.
func (c *checker) checkSpecCheckFields(base string, m *jq.Obj) {
	for _, key := range m.Keys {
		value, _ := m.Get(key)
		switch key {
		case fieldItem:
			c.checkFilled(base+pathDot+fieldItem, value, "quote the spec item")
		case "finding_sig":
			c.checkString(base+".finding_sig", value, false, "use the full union finding signature")
		case fieldStatus:
			c.checkEnum(base+pathDot+fieldStatus, value, specStatuses, "use met, violated or n/a")
		case fieldEvidence:
			c.checkEvidence(base+pathDot+fieldEvidence, value)
		default:
			c.unknown(base, key, knownSpecCheckKeys)
		}
	}
}

func (c *checker) checkEvidence(base string, v any) {
	m, ok := v.(*jq.Obj)
	if !ok {
		c.wrongType(base, "evidence must be an object", "write {path, line, quote}", evidenceExample())
		return
	}
	for _, key := range m.Keys {
		value, _ := m.Get(key)
		switch key {
		case fieldPath:
			c.checkString(base+".path", value, true, "name the code path")
		case fieldLine:
			if _, ok := jq.IntOf(value); !ok {
				c.wrongType(base+".line", "line must be an integer", "use the code line number", exampleLine)
			}
		case fieldQuote:
			c.checkFilled(base+pathDot+fieldQuote, value, "quote the code line")
		default:
			c.unknown(base, key, knownEvidenceKeys)
		}
	}
	c.requireKeys(base, m, knownEvidenceKeys)
}

func (c *checker) checkReceipts(v any) {
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.receipts", "receipts must be an array", "write an array of {hunk, quote} objects", []any{})
		return
	}
	for i, item := range items {
		base := fmt.Sprintf("$.receipts[%d]", i)
		m, ok := item.(*jq.Obj)
		if !ok {
			c.wrongType(base, "receipt must be an object", "write {hunk, quote}",
				map[string]any{"hunk": "@@ -1 +1 @@", fieldQuote: exampleEllipsis})
			continue
		}
		c.checkReceiptFields(base, m)
		c.requireKeys(base, m, knownReceiptKeys)
	}
}

// checkReceiptFields dispatches every published receipt key.
func (c *checker) checkReceiptFields(base string, m *jq.Obj) {
	for _, key := range m.Keys {
		value, _ := m.Get(key)
		switch key {
		case fieldHunk, fieldQuote:
			c.checkFilled(base+pathDot+key, value, prefixWrite+key)
		default:
			c.unknown(base, key, knownReceiptKeys)
		}
	}
}

// requireKeys reports every published key the object omits.
func (c *checker) requireKeys(base string, m *jq.Obj, keys []string) {
	for _, key := range keys {
		if _, has := m.Get(key); !has {
			c.missing(base+pathDot+key, prefixMissing+key, prefixWrite+key, exampleEllipsis)
		}
	}
}

func (c *checker) checkSummary(v any) {
	s, ok := v.(string)
	if !ok {
		c.wrongType("$.summary", "summary must be a string", "write a short summary", exampleEllipsis)
		return
	}
	if runes := utf8.RuneCountInString(s); runes > maxSummaryRunes {
		c.outOfRange("$.summary", fmt.Sprintf("summary must be at most %d characters (got %d)", maxSummaryRunes, runes),
			"shorten the summary", exampleEllipsis)
	}
}

func (c *checker) checkFinding(i int, item any) {
	base := fmt.Sprintf("$.findings[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "finding must be an object",
			"write one {path, line, severity, claim, quote, confidence} object", findingExample())
		return
	}
	pathValue, hasPath := m.Get(fieldPath)
	path, pathOK := pathValue.(string)
	if !pathOK {
		if hasPath {
			c.wrongType(base+problemPathSuffix, "path must be a string", "use a changed file path", exampleFile)
		} else {
			c.missing(base+problemPathSuffix, "missing path", "add the changed file path", exampleFile)
		}
	}
	c.checkFindingFields(base, m)
	c.requireKeys(base, m, []string{fieldLine, fieldSeverity, fieldClaim, fieldQuote, fieldConfidenceInput})
	moved := c.moved
	c.checkAnchor(base, m, path, pathOK)
	if hasSuggestionFence(m) && c.moved == moved {
		c.moved++
	}
}

// checkFindingFields dispatches every published finding key. The path is
// checked by the caller so its wrong-type and missing cases stay distinct.
func (c *checker) checkFindingFields(base string, m *jq.Obj) {
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		if !c.checkFindingText(base, key, v) && !c.checkFindingShape(base, key, v) {
			c.unknown(base, key, knownFindingKeys)
		}
	}
	if _, hasRigor := m.Get(fieldRigor); !hasRigor {
		c.checkRigor(base, nil)
	}
}

// checkFindingText checks the keys whose value is one scalar in place, and
// reports whether it knew the key.
func (c *checker) checkFindingText(base, key string, v any) bool {
	switch key {
	case fieldPath:
	case fieldLine, fieldStartLine:
		if _, ok := jq.IntOf(v); !ok {
			c.wrongType(base+pathDot+key, key+" must be an integer", "use the code line number", exampleLine)
		}
	case fieldSide:
		c.checkSide(base+pathDot+fieldSide, v)
	case fieldClaim:
		c.checkFilled(base+pathDot+fieldClaim, v, "state the defect")
	case fieldQuote:
		c.checkFilled(base+pathDot+fieldQuote, v, "quote the code at path:line")
	case fieldConfidenceInput:
		c.checkEnum(base+pathDot+fieldConfidenceInput, v, finderConfidence, "use high, medium or low")
	case fieldSeverity:
		c.checkSeverity(base+pathDot+fieldSeverity, v)
	case fieldCategory:
		c.checkCategory(base+pathDot+fieldCategory, v)
	default:
		return false
	}
	return true
}

// checkFindingShape checks the keys that carry a nested shape, and reports
// whether it knew the key.
func (c *checker) checkFindingShape(base, key string, v any) bool {
	switch key {
	case "reproduction":
		// The bundle-bound capture validates the path and command together.
	case fieldSuggestion:
		c.checkSuggestion(base+pathDot+fieldSuggestion, v)
	case fieldBrief:
		c.checkBrief(base, v)
	case fieldRigor:
		c.checkRigor(base, v)
	case fieldSources:
		c.checkSources(base, v)
	case fieldKB:
		c.checkKB(base+pathDot+fieldKB, v)
	default:
		return false
	}
	return true
}

func (c *checker) checkEnum(path string, v any, values []string, fix string) {
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "value must be a string", fix, values[0])
		return
	}
	if !slices.Contains(values, s) {
		c.badEnum(path, fmt.Sprintf("unknown value %q", s), fix, values[0])
	}
}

func (c *checker) checkSide(path string, v any) {
	if v == nil {
		return
	}
	side, ok := v.(string)
	if !ok {
		c.wrongType(path, "side must be a string", "use RIGHT or LEFT", sideRight)
		return
	}
	if side != sideRight && side != sideLeft {
		c.badEnum(path, fmt.Sprintf("unknown side %q", side), "use RIGHT or LEFT", sideRight)
	}
}

func (c *checker) checkSuggestion(path string, v any) {
	if v == nil {
		return
	}
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "suggestion must be a string or null", "use a string code suggestion or null", exampleEllipsis)
		return
	}
	if strings.Contains(s, "```") {
		c.add(c.problem(path, core.ProblemCodeWrongType, core.ProblemLevelWarning,
			"suggestion contains a fence — demoting it to body",
			"use an unfenced code suggestion"))
	}
}

func (c *checker) checkKB(path string, v any) {
	if v == nil {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType(path, "kb must be an array of rule ids", "use an array of lowercase rule ids")
		return
	}
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			c.wrongType(fmt.Sprintf("%s[%d]", path, i), "kb id must be a string", "use a lowercase rule id")
			continue
		}
		if !kb.ValidID(s) {
			c.add(c.problem(fmt.Sprintf("%s[%d]", path, i), core.ProblemCodeBadEnum, core.ProblemLevelWarning,
				fmt.Sprintf("kb id %q is malformed", s),
				"use ^[a-z0-9][a-z0-9-]{0,64}$ from .verdict/kb.jsonl"))
		}
	}
}

//nolint:cyclop,gocognit,revive // chunk type, name, and coverage gates read as one block
func (c *checker) checkChunks(v any) {
	if v == nil || v == false {
		if c.hasChunks {
			c.unattestedChunks()
		}
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.chunks", "chunks must be an array of manifest chunk names",
			"list every manifest chunk name as a string")
		return
	}
	for i, item := range items {
		name, ok := item.(string)
		if !ok {
			c.wrongType(fmt.Sprintf("$.chunks[%d]", i), "chunk name must be a string", "use a manifest chunk name")
			continue
		}
		if c.hasChunks && !slices.Contains(c.chunkNames, name) {
			c.add(c.problem(fmt.Sprintf("$.chunks[%d]", i), core.ProblemCodeBadEnum, core.ProblemLevelWarning,
				fmt.Sprintf("unknown chunk %q in the chunks attestation", name),
				"use a chunk name from "+c.chunkManifest))
		}
	}
	if !c.hasChunks {
		return
	}
	claimed := map[string]bool{}
	for _, item := range items {
		if name, ok := item.(string); ok {
			claimed[name] = true
		}
	}
	var missing []string
	for _, name := range c.chunkNames {
		if !claimed[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		c.add(c.problem("$.chunks", core.ProblemCodeMissingField, core.ProblemLevelError,
			fmt.Sprintf("chunks attestation covers %d of %d manifest chunk(s); missing %s",
				len(c.chunkNames)-len(missing), len(c.chunkNames), strings.Join(missing, ", ")),
			"list every manifest chunk name in top-level chunks", c.chunkNames))
	}
}

func (c *checker) unattestedChunks() {
	c.add(c.problem("$.chunks", core.ProblemCodeMissingField, core.ProblemLevelWarning,
		fmt.Sprintf("chunked bundle %s lists %d chunk(s) but the review carries no chunks attestation",
			c.chunkManifest, len(c.chunkNames)),
		"stamp every manifest chunk name in top-level \"chunks\""))
}

func (c *checker) checkVerification() {
	if !c.hasVerification {
		return
	}
	doc, err := jq.DecodeOrdered(c.verification)
	if err != nil {
		return
	}
	items, ok := doc.([]any)
	if !ok {
		c.wrongType("verification.json", msgMalformedVerifyClaims,
			"write an array of {sig, verdict, verifyCmd, resultHead}")
		return
	}
	for i, item := range items {
		if _, ok := item.(*jq.Obj); !ok {
			c.wrongType(fmt.Sprintf("verification.json[%d]", i), msgNonObjectVerifyClaim,
				"use {sig, verdict, verifyCmd, resultHead}")
		}
	}
}

func (c *checker) checkAnchor(base string, m *jq.Obj, path string, pathOK bool) {
	if !pathOK {
		return
	}
	key := stripDiffPrefix(path)
	if c.excludedPaths[key] {
		return
	}
	if !hunks.Indexed(c.hunks, key) {
		c.add(c.problem(base+problemPathSuffix, core.ProblemCodePathNotInDiff, core.ProblemLevelError,
			fmt.Sprintf("path %q is not in the fetched diff", path),
			"anchor the finding on a changed file or move it into the body"))
		return
	}
	lo, hi, side := anchorRange(m)
	want := "NEW"
	if side == sideLeft {
		want = "OLD"
	}
	if hunks.Contains(c.hunks, path, want, lo, hi) || hunks.Contains(c.hunks, stripDiffPrefix(path), want, lo, hi) {
		return
	}
	c.moved++
	c.add(c.problem(base+".line", core.ProblemCodeAnchorOutsideHunk, core.ProblemLevelWarning,
		fmt.Sprintf("anchor %d-%d (%s) is outside every reviewed hunk; record moves it to the body", lo, hi, side),
		"set line/start_line inside a reviewed hunk and keep the finding inline"))
}

func (c *checker) checkRigor(base string, v any) {
	if v == nil || v == false {
		c.add(c.problem(base+".rigor", core.ProblemCodeRigorUnset, core.ProblemLevelWarning,
			"rigor is unset", "set rigor to said|pointed|walked|ran|reproduced", rungWalked))
		return
	}
	s, ok := v.(string)
	if !ok {
		c.wrongType(base+".rigor", "rigor must be a string", "use said|pointed|walked|ran|reproduced", rungWalked)
		return
	}
	if !slices.Contains(rigorKeys, s) {
		c.badEnum(base+".rigor", fmt.Sprintf("unknown rigor %q", s),
			"use said|pointed|walked|ran|reproduced", rungWalked)
	}
}

func (c *checker) checkSeverity(path string, v any) {
	if v == nil || v == false {
		if v == false {
			c.wrongType(path, "severity must be a string or null", "use critical|important|informational", "important")
		}
		return
	}
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "severity must be a string or null", "use critical|important|informational", "important")
		return
	}
	if !slices.Contains(reviewKeys, s) {
		c.badEnum(path, fmt.Sprintf("unknown severity %q", s),
			"use critical|important|informational", "important")
	}
}

// checkCategory validates one comment's category without ever refusing: an
// unknown or non-string value warns and reads null, so a taxonomy typo still
// posts. Only null reads untagged without a warning; every other non-string
// (false included) warns exactly once. Absent and null read untagged.
func (c *checker) checkCategory(path string, v any) {
	if v == nil {
		return
	}
	s, ok := v.(string)
	if !ok {
		c.add(c.problem(path, core.ProblemCodeWrongType, core.ProblemLevelWarning,
			"category must be a string or null — storing null (want "+strings.Join(core.CategoryValues, "|")+")",
			"use a published category or omit it", "security"))
		return
	}
	if !slices.Contains(core.CategoryValues, s) {
		c.add(c.problem(path, core.ProblemCodeBadEnum, core.ProblemLevelWarning,
			fmt.Sprintf("unknown category %q — storing null (want %s)", s, strings.Join(core.CategoryValues, "|")),
			"use a published category or omit it", "security"))
	}
}

func (c *checker) checkBrief(base string, v any) {
	if v == nil || v == false {
		return
	}
	_, issues := brief.Check(v, c.hunks)
	for i := range issues {
		p := issues[i].Problem()
		p.Path = base + ".brief" + p.Path
		c.add(p)
	}
}

func (c *checker) checkTriage(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.triage", "triage must be an array",
			"write an array of {sig, status, rationale}", []any{map[string]any{
				fieldSig:         exampleEllipsis,
				fieldStatus:      exampleWontFix,
				problemRationale: exampleEllipsis,
			}})
		return
	}
	for i, item := range items {
		c.checkTriageClaim(i, item)
	}
}

//nolint:revive // one branch per published triage key
func (c *checker) checkTriageClaim(i int, item any) {
	base := fmt.Sprintf("$.triage[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "triage entry must be an object", "use {sig, status, rationale}",
			map[string]any{"sig": "...", "status": "wont-fix", "rationale": "..."})
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldSig:
			c.checkString(base+".sig", v, true, "use a finding sig")
		case fieldStatus:
			c.checkTriageStatus(base+".status", v)
		case problemRationale:
			if v != nil {
				c.checkString(base+"."+problemRationale, v, false, "use a rationale string")
			}
		default:
			c.unknown(base, key, knownTriageKeys)
		}
	}
	if _, ok := m.Get(fieldSig); !ok {
		c.missing(base+".sig", "missing triage sig", "add the finding sig", "...")
	}
	if _, ok := m.Get(fieldStatus); !ok {
		c.missing(base+".status", "missing triage status", "add open|addressed|wont-fix|false-positive|deferred", "wont-fix")
	}
}

func (c *checker) checkTriageStatus(path string, v any) {
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "triage status must be a string", "use open|addressed|wont-fix|false-positive|deferred", exampleWontFix)
		return
	}
	if !slices.Contains(triageStatuses, s) {
		c.badEnum(path, fmt.Sprintf("unknown triage status %q", s),
			"use open|addressed|wont-fix|false-positive|deferred", exampleWontFix)
	}
}

// checkCoverage validates the diff-coverage attestation's shape. Range syntax
// and claim-versus-hunk matching stay with internal/core/coverage: a malformed
// range is a warning there, never a schema error here.
func (c *checker) checkCoverage(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.coverage", "coverage must be an array of {file, ranges}",
			"write [{\"file\": \"src/db.ts\", \"ranges\": \"1-240\"}]",
			[]any{map[string]any{"file": "src/db.ts", fieldRanges: "all"}})
		return
	}
	for i, item := range items {
		c.checkCoverageClaim(i, item)
	}
}

func (c *checker) checkCoverageClaim(i int, item any) {
	base := fmt.Sprintf("$.coverage[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "coverage entry must be an object", "use {file, ranges}",
			map[string]any{"file": "src/db.ts", fieldRanges: "all"})
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldFile:
			c.checkString(base+".file", v, true, "use a repo-relative path with hunks in the bundle")
		case fieldRanges:
			c.checkString(base+".ranges", v, false, `use "1-240,300-320", "all", or omit it`)
		default:
			c.unknown(base, key, knownCoverageKeys)
		}
	}
	if _, ok := m.Get(fieldFile); !ok {
		c.missing(base+".file", "missing file", "use a repo-relative path with hunks in the bundle", "src/db.ts")
	}
}

func (c *checker) checkSecuritySummary(v any) {
	if v == nil {
		return
	}
	if _, ok := v.(string); !ok {
		c.wrongType("$.security_summary", "security_summary must be a string", "use a short paragraph", "...")
	}
}

func (c *checker) checkRisk(v any) {
	if v == nil {
		return
	}
	const pathRisk = "$.risk"
	m, ok := v.(*jq.Obj)
	if !ok {
		c.wrongType(pathRisk, "risk must be an object",
			"use {level, rationale, assessed_head}",
			map[string]any{fieldLevel: riskMedium, problemRationale: exampleEllipsis, fieldAssessedHead: "HEAD"})
		return
	}
	assessedHead := c.checkRiskFields(m, pathRisk)
	c.checkMissingRiskFields(m, pathRisk)
	if c.headSHA != "" && c.headSHA != headDirty && c.headSHA != headUnknown && assessedHead != "" && assessedHead != c.headSHA {
		c.add(c.problem(riskPath(pathRisk, fieldAssessedHead), core.ProblemCodeRiskStaleHead, core.ProblemLevelWarning,
			fmt.Sprintf("risk assessed_head %q does not match bundle head %q — marking risk claim stale", assessedHead, c.headSHA),
			fmt.Sprintf("update risk.assessed_head to %q after reviewing the current head", c.headSHA),
			c.headSHA))
	}
}

func riskPath(base, key string) string { return fmt.Sprintf("%s.%s", base, key) }

func (c *checker) checkRiskFields(m *jq.Obj, pathRisk string) string {
	var assessedHead string
	for _, key := range m.Keys {
		val, _ := m.Get(key)
		switch key {
		case fieldLevel:
			c.checkRiskLevel(riskPath(pathRisk, fieldLevel), val)
		case problemRationale:
			c.checkRiskRationale(riskPath(pathRisk, problemRationale), val)
		case fieldAssessedHead:
			assessedHead = c.checkRiskHead(riskPath(pathRisk, fieldAssessedHead), val)
		default:
			c.unknown(pathRisk, key, knownRiskKeys)
		}
	}
	return assessedHead
}

func (c *checker) checkMissingRiskFields(m *jq.Obj, pathRisk string) {
	if _, ok := m.Get(fieldLevel); !ok {
		c.missing(riskPath(pathRisk, fieldLevel), "missing risk level", "use low|medium|high|critical", riskMedium)
	}
	if _, ok := m.Get(problemRationale); !ok {
		c.missing(riskPath(pathRisk, problemRationale), "missing risk rationale", "explain the assessed risk briefly", exampleEllipsis)
	}
	if _, ok := m.Get(fieldAssessedHead); !ok {
		c.missing(riskPath(pathRisk, fieldAssessedHead), "missing risk assessed_head", "record the bundle head SHA", c.headSHA)
	}
}

func (c *checker) checkRiskLevel(path string, v any) {
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "risk level must be a string", "use low|medium|high|critical", riskMedium)
		return
	}
	if !slices.Contains(riskLevels, s) {
		c.badEnum(path, fmt.Sprintf("unknown risk level %q", s), "use low|medium|high|critical", riskMedium)
	}
}

func (c *checker) checkRiskRationale(path string, v any) {
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "risk rationale must be a string", "write a concise risk rationale (10-500 characters)", exampleEllipsis)
		return
	}
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	if n < minRiskRationale || n > maxRiskRationale {
		c.outOfRange(path, fmt.Sprintf("risk rationale must be %d-%d characters (got %d)", minRiskRationale, maxRiskRationale, n),
			fmt.Sprintf("write a %d-%d character rationale explaining the risk level", minRiskRationale, maxRiskRationale),
			"Touches auth token verification with added test coverage.")
	}
}

func (c *checker) checkRiskHead(path string, v any) string {
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "risk assessed_head must be a string", "record the reviewed head SHA", "HEAD")
		return ""
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		c.outOfRange(path, "risk assessed_head must be a non-empty SHA", "record the reviewed head SHA", "HEAD")
		return ""
	}
	return trimmed
}

func invalidJSON(review []byte) *Problem {
	var v any
	err := json.Unmarshal(review, &v)
	if err == nil {
		return nil
	}
	line, col := jsonLineCol(review, err)
	p := Problem{
		Path:    problemRoot,
		Code:    core.ProblemCodeInvalidJSON,
		Level:   core.ProblemLevelError,
		Message: fmt.Sprintf("invalid JSON at %d:%d: %s", line, col, jsonErrorText(err)),
		Fix:     "emit only the JSON object, no code fence, no prose",
		Example: reviewObjectExample(),
	}
	return &p
}

func (c *checker) invalidJSON(err error) {
	line, col := jsonLineCol(nil, err)
	c.add(c.problem(problemRoot, core.ProblemCodeInvalidJSON, core.ProblemLevelError,
		fmt.Sprintf("invalid JSON at %d:%d: %s", line, col, jsonErrorText(err)),
		"emit only the JSON object, no code fence, no prose", reviewObjectExample()))
}

func jsonErrorText(err error) string {
	if syntax, ok := errors.AsType[*json.SyntaxError](err); ok {
		return syntax.Error()
	}
	return err.Error()
}

func jsonLineCol(raw []byte, err error) (int, int) {
	syntax, ok := errors.AsType[*json.SyntaxError](err)
	if !ok || syntax.Offset <= 0 {
		return 1, 1
	}
	offset := int(syntax.Offset)
	offset = min(offset, len(raw))
	line, col := 1, 1
	for i := range offset {
		if raw[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

func reviewObjectExample() map[string]any {
	return map[string]any{
		fieldSchemaVersion: schemaVersion1,
		problemFindings:    []any{},
		problemSummary:     "Reviewed the diff; no defects found.",
	}
}

func findingExample() map[string]any {
	return map[string]any{
		fieldPath: exampleFile, fieldLine: exampleLine, fieldSide: sideRight,
		fieldSeverity: exampleImportant, "claim": exampleEllipsis, "quote": exampleEllipsis, "confidence": "high",
	}
}

func specCheckExample() map[string]any {
	return map[string]any{"item": exampleEllipsis, "status": "met", "evidence": evidenceExample()}
}

func evidenceExample() map[string]any {
	return map[string]any{fieldPath: exampleFile, fieldLine: exampleLine, "quote": exampleEllipsis}
}

func nearest(s string, keys []string) string {
	if len(s) > nearestKeyMax {
		return ""
	}
	best, bestD := "", -1
	for _, key := range keys {
		d := editDistance(s, key)
		if bestD < 0 || d < bestD {
			best, bestD = key, d
		}
	}
	return best
}

//nolint:revive // bounded edit distance
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func (c *checker) checkSources(base string, v any) {
	items, ok := v.([]any)
	if !ok {
		c.wrongType(base+".sources", "sources must be an array", "use an array of source names")
		return
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			c.wrongType(base+".sources", "source must be a string", "use a source name")
		}
	}
}
