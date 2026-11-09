package recordcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/brief"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// CheckResult is CheckReview's output: every problem in document order, the
// overflow past the 50-problem cap, and the summary a clean dry run reports.
type CheckResult struct {
	Problems  []Problem         `json:"problems"`
	Truncated int               `json:"problems_truncated"`
	Errors    int               `json:"errors"`
	Warnings  int               `json:"warnings"`
	Summary   core.CheckSummary `json:"summary"`
}

// CheckInput carries everything the one review checker can validate beyond
// review.json: the fetched diff, the chunk manifest's names, and the optional
// verification claims. Empty fields leave that check out.
type CheckInput struct {
	Diff            string
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
	problemSchemaVersion = "$.schema_version"
	problemApproved      = "approved"
	problemRationale     = "rationale"
	problemPathSuffix    = ".path"
	exampleFile          = "file.go"
	exampleLine          = 10
	exampleEllipsis      = "..."
	exampleImportant     = "important"
	exampleWontFix       = "wont-fix"
	exampleFalsePositive = "false-positive"
	exampleConfidence    = 3
	minConfidence        = 1
	maxConfidence        = 5
)

var (
	//nolint:goconst // published schema literals
	knownReviewKeys = []string{
		"schema_version", fieldBody, problemApproved, fieldComments,
		fieldResolved, fieldTriage, fieldSections, fieldChunks, fieldCoverage, fieldConfidence, fieldSecuritySummary,
	}
	//nolint:goconst // schema keys are published literals
	knownCommentKeys = []string{
		fieldPath, fieldLine, "start_line", fieldSide, "body",
		"suggestion", fieldBrief, fieldRigor, fieldSeverity, fieldCategory, fieldInBoundsInput, "kb",
	}
	knownTriageKeys   = []string{fieldSig, fieldStatus, problemRationale}
	knownSectionKeys  = []string{"heading", fieldSeverity}
	knownCoverageKeys = []string{"file", "ranges"}
	reviewKeys        = []string{"critical", exampleImportant, "informational"}
	rigorKeys         = []string{rungSaid, rungPointed, rungWalked, rungRan, rungReproduced}
	triageStatuses    = []string{"open", "addressed", exampleWontFix, exampleFalsePositive, "deferred"}
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
			"write one JSON object with body and comments[]", reviewObjectExample()))
		return *c.result()
	}
	c.checkReview(obj)
	c.checkVerification()
	return *c.result()
}

//nolint:govet // the fields group one check pass
type checker struct {
	res             CheckResult
	hunks           []hunks.Hunk
	chunkNames      []string
	chunkManifest   string
	verification    []byte
	comments        int
	sections        int
	moved           int
	hasChunks       bool
	hasVerification bool
}

func (c *checker) result() *CheckResult {
	c.res.Summary = core.CheckSummary{
		Comments:           c.comments,
		Sections:           c.sections,
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
		case "schema_version":
			c.checkSchemaVersion(v)
		case "body":
			c.checkString("$."+fieldBody, v, true, "write the full review text")
		case problemApproved:
			if _, ok := v.(bool); !ok {
				c.wrongType("$."+problemApproved, "approved must be a boolean", "use true or false", false)
			}
		case fieldComments:
			c.checkComments(v)
		case fieldResolved:
			c.checkStringArray("$.resolved", v, "list finding sigs as strings")
		case fieldTriage:
			c.checkTriage(v)
		case fieldSections:
			c.checkSections(v)
		case fieldConfidence:
			c.checkConfidence(v)
		case fieldSecuritySummary:
			c.checkSecuritySummary(v)
		case fieldChunks:
			c.checkChunks(v)
		case fieldCoverage:
			c.checkCoverage(v)
		default:
			c.unknown("$", key, knownReviewKeys)
		}
	}
	if _, ok := obj.Get(fieldBody); !ok {
		c.missing("$."+fieldBody, "missing body", "write the full review text", exampleEllipsis)
	}
	if _, ok := obj.Get(fieldComments); !ok {
		c.add(c.problem("$.comments", core.ProblemCodeMissingField, core.ProblemLevelWarning,
			"comments is missing; treating it as an empty list",
			"write \"comments\": []", []any{}))
	}
	c.checkBodyQuality(obj)
	if _, ok := obj.Get("schema_version"); !ok {
		c.add(c.problem(problemSchemaVersion, core.ProblemCodeMissingField, core.ProblemLevelWarning,
			"schema_version is missing; assuming 1",
			"write \"schema_version\": \"1\"", schemaVersion1))
	}
	if c.hasChunks {
		if _, ok := obj.Get(fieldChunks); !ok {
			c.unattestedChunks()
		}
	}
}

func (c *checker) checkSchemaVersion(v any) {
	if v == nil || v == false {
		c.add(c.problem(problemSchemaVersion, core.ProblemCodeMissingField, core.ProblemLevelWarning,
			"schema_version is missing; assuming 1",
			"write \"schema_version\": \"1\"", schemaVersion1))
		return
	}
	s, ok := v.(string)
	if !ok {
		c.wrongType(problemSchemaVersion, "schema_version must be the string \"1\"", "set \"schema_version\": \"1\"", schemaVersion1)
		return
	}
	if s != schemaVersion1 {
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

func (c *checker) checkComments(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.comments", "comments must be an array",
			"write an array of {path, line, body} objects", []any{commentExample()})
		return
	}
	c.comments = len(items)
	for i, item := range items {
		c.checkComment(i, item)
	}
}

//nolint:cyclop,revive // one branch per published comment key
func (c *checker) checkComment(i int, item any) {
	base := fmt.Sprintf("$.comments[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "comment must be an object",
			"write one {path, line, body, side} object per finding", commentExample())
		return
	}
	pathValue, hasPath := m.Get(fieldPath)
	path, pathOK := pathValue.(string)
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldPath:
			if !pathOK {
				c.wrongType(base+problemPathSuffix, "path must be a string", "use a changed file path", exampleFile)
			}
		case fieldLine, fieldStartLine:
			c.checkAnchorValue(base+"."+key, key, v)
		case fieldSide:
			c.checkSide(base+".side", v)
		case fieldBody:
			c.checkString(base+".body", v, true, "write the finding text")
		case fieldSuggestion:
			c.checkSuggestion(base+".suggestion", v)
		case fieldBrief:
			c.checkBrief(base, v)
		case fieldRigor:
			c.checkRigor(base, v)
		case fieldSeverity:
			c.checkSeverity(base+".severity", v)
		case fieldCategory:
			c.checkCategory(base+".category", v)
		case fieldInBoundsInput:
			if _, ok := v.(bool); !ok {
				c.wrongType(base+".in-bounds", "in-bounds must be a boolean", "use true or false", true)
			}
		case fieldKB:
			c.checkKB(base+".kb", v)
		default:
			c.unknown(base, key, knownCommentKeys)
		}
	}
	if !hasPath {
		c.missing(base+problemPathSuffix, "missing path", "add the changed file path", exampleFile)
	}
	if _, hasBody := m.Get(fieldBody); !hasBody {
		c.missing(base+".body", "missing body", "add the finding text", exampleEllipsis)
	}
	if _, hasRigor := m.Get(fieldRigor); !hasRigor {
		c.checkRigor(base, nil)
	}
	moved := c.moved
	c.checkAnchor(base, m, path, pathOK)
	if hasSuggestionFence(m) && c.moved == moved {
		c.moved++
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

//nolint:revive // the anchor type gates read as one block
func (c *checker) checkAnchorValue(path, key string, v any) {
	if v == nil || v == false {
		if v == false {
			c.wrongType(path, key+" must be an integer or numeric string", "use a line number inside a hunk", exampleLine)
		}
		return
	}
	switch t := v.(type) {
	case string:
		if !jq.Digits(t) {
			c.add(c.problem(path, core.ProblemCodeWrongType, core.ProblemLevelWarning,
				"kept despite non-numeric anchor: "+t,
				"use a whole line number inside a hunk", exampleLine))
		}
		return
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			c.wrongType(path, key+" must be an integer or numeric string", "use a whole line number", exampleLine)
		}
	default:
		c.wrongType(path, "anchor must be a string or integer", "use a line number inside a hunk", exampleLine)
	}
}

//nolint:revive // path, missing-line, and in-hunk gates read as one check
func (c *checker) checkAnchor(base string, m *jq.Obj, path string, pathOK bool) {
	if !pathOK {
		return
	}
	key := stripDiffPrefix(path)
	if !hunks.Indexed(c.hunks, key) {
		c.add(c.problem(base+problemPathSuffix, core.ProblemCodePathNotInDiff, core.ProblemLevelError,
			fmt.Sprintf("path %q is not in the fetched diff", path),
			"anchor the comment on a changed file or move it into the body"))
		return
	}
	lo, hi, side := anchorRange(m)
	if lo < 0 {
		if anchorAbsent(m) {
			c.moved++
			c.add(c.problem(base+".line", core.ProblemCodeMissingField, core.ProblemLevelWarning,
				"comment has no line or start_line; record moves it to the body",
				"set line or start_line inside a reviewed hunk", exampleLine))
		}
		return
	}
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
		"move the comment to the body or set line/start_line inside a reviewed hunk"))
}

func anchorAbsent(m *jq.Obj) bool {
	hasFalse := false
	for _, key := range []string{fieldLine, fieldStartLine} {
		v, has := m.Get(key)
		if !has {
			continue
		}
		if v == false {
			hasFalse = true
			continue
		}
		if v != nil {
			return false
		}
	}
	return !hasFalse
}

func (c *checker) checkDegenerateBody(body string) *core.Problem {
	if markerRe.MatchString(body) {
		return nil
	}
	switch {
	case len(body) < degenerateFloor:
		p := Problem{
			Path: "$.body", Code: core.ProblemCodeMissingField, Level: core.ProblemLevelError,
			Message: "body is degenerate (<20 chars, no verdict marker)",
			Fix:     "open with a verdict marker (see prompts/review.md) and write the review",
		}
		return &p
	case len(body) < placeholderFloor && asciiContainsLower(body, placeholderRe):
		p := Problem{
			Path: "$.body", Code: core.ProblemCodeMissingField, Level: core.ProblemLevelError,
			Message: "body looks like a short placeholder",
			Fix:     "write the real review, or open with a verdict marker",
		}
		return &p
	default:
		return nil
	}
}

func (c *checker) checkBodyQuality(obj *jq.Obj) {
	body, _ := obj.Get(fieldBody)
	text, ok := body.(string)
	if !ok {
		return
	}
	if p := c.checkDegenerateBody(text); p != nil {
		c.add(*p)
	}
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
			[]any{map[string]any{"file": "src/db.ts", "ranges": "all"}})
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
			map[string]any{"file": "src/db.ts", "ranges": "all"})
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case "file":
			c.checkString(base+".file", v, true, "use a repo-relative path with hunks in the bundle")
		case "ranges":
			c.checkString(base+".ranges", v, false, `use "1-240,300-320", "all", or omit it`)
		default:
			c.unknown(base, key, knownCoverageKeys)
		}
	}
	if _, ok := m.Get("file"); !ok {
		c.missing(base+".file", "missing file", "use a repo-relative path with hunks in the bundle", "src/db.ts")
	}
}

func (c *checker) checkSections(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.sections", "sections must be an array",
			"write an array of {heading, severity}", []any{sectionExample()})
		return
	}
	c.sections = len(items)
	for i, item := range items {
		c.checkSection(i, item)
	}
}

func (c *checker) checkSection(i int, item any) {
	base := fmt.Sprintf("$.sections[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "section must be an object", "use {heading, severity}", sectionExample())
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldHeading:
			c.checkString(base+".heading", v, true, "use the exact ### heading string")
		case fieldSeverity:
			c.checkSeverity(base+".severity", v)
		default:
			c.unknown(base, key, knownSectionKeys)
		}
	}
	if _, ok := m.Get(fieldHeading); !ok {
		c.missing(base+".heading", "missing heading", "use the exact ### heading string", "### Security")
	}
}

func (c *checker) checkConfidence(v any) {
	if v == nil {
		return
	}
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(n.String(), ".eE") {
		c.wrongType("$.confidence", "confidence must be an integer 1-5", "use an integer 1-5", exampleConfidence)
		return
	}
	level, err := n.Int64()
	if err != nil || level < minConfidence || level > maxConfidence {
		c.outOfRange("$.confidence", fmt.Sprintf("confidence %s is outside 1-5", n.String()),
			"use an integer 1-5", exampleConfidence)
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
		"schema_version": "1",
		fieldBody:        "> ✅ Review text.",
		problemApproved:  false,
		fieldComments:    []any{},
	}
}

func commentExample() map[string]any {
	return map[string]any{fieldPath: exampleFile, fieldLine: exampleLine, fieldSide: sideRight, "body": exampleEllipsis}
}

func sectionExample() map[string]any {
	return map[string]any{"heading": "### Security", fieldSeverity: exampleImportant}
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
