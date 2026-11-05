package recordcore

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// NormalizeReview repairs the mechanical review.json shapes weak models emit:
// a whole-document code fence, prose around the object, scalars where a string
// array belongs, common key aliases, enum synonyms and case, and trivial
// bytes. It is pure and deterministic, and applying it twice is byte-identical
// to applying it once. Every rewrite comes back as a warning problem; a
// document it cannot recover is returned as-is for CheckReview to refuse.
func NormalizeReview(review []byte) ([]byte, []Problem) {
	original := review
	text := string(review)
	var repairs []Problem
	text, repairs = stripBOM(text, repairs)
	if inner, ok := wholeDocumentFence(text); ok {
		text = strings.TrimSpace(inner)
		repairs = append(repairs, repairProblem(core.ProblemCodeNormalizedFence, problemRoot,
			"review.json was wrapped in one code fence",
			"emit the JSON object directly, with no code fence"))
	}
	doc, err := jq.DecodeOrdered([]byte(text))
	if err != nil {
		if extracted, ok := extractObject(text); ok {
			text = extracted
			repairs = append(repairs, repairProblem(core.ProblemCodeNormalizedExtracted, problemRoot,
				"review.json had prose around one JSON object",
				"emit only the JSON object, with no prose around it"))
			doc, err = jq.DecodeOrdered([]byte(text))
		}
	}
	if err != nil {
		return attempted(original, []byte(text), repairs), repairs
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		return attempted(original, []byte(text), repairs), repairs
	}
	repairs = append(repairs, normalizeReviewObject(obj)...)
	if len(repairs) == 0 {
		return original, nil
	}
	return []byte(jq.PrettyOrdered(obj)), repairs
}

// attempted returns the bytes the next check should read: the recovered text
// when a repair changed it, else the original document.
func attempted(original, text []byte, repairs []Problem) []byte {
	if len(repairs) == 0 {
		return original
	}
	return text
}

func stripBOM(text string, repairs []Problem) (string, []Problem) {
	if !strings.HasPrefix(text, "\ufeff") {
		return text, repairs
	}
	repairs = append(repairs, repairProblem(core.ProblemCodeNormalizedTrivia, problemRoot,
		"review.json started with a UTF-8 byte-order mark",
		"save the file as UTF-8 without a BOM"))
	return strings.TrimPrefix(text, "\ufeff"), repairs
}

// wholeDocumentFence unwraps exactly one ```json or ``` fence around the whole
// document. A fence with prose outside it is left to extraction.
func wholeDocumentFence(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	firstNL := strings.IndexByte(trimmed, '\n')
	lastNL := strings.LastIndexByte(trimmed, '\n')
	if firstNL < 0 || lastNL <= firstNL {
		return "", false
	}
	open := strings.ToLower(strings.TrimSpace(trimmed[:firstNL]))
	if open != "```" && open != "```json" {
		return "", false
	}
	if strings.TrimSpace(trimmed[lastNL+1:]) != "```" {
		return "", false
	}
	inner := trimmed[firstNL+1 : lastNL]
	if strings.TrimSpace(inner) == "" {
		return "", false
	}
	return inner, true
}

// extractObject returns the one balanced top-level object that parses. More
// than one parseable object is ambiguous and stays an invalid-json refusal.
func extractObject(text string) (string, bool) {
	var found []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if candidate, ok := balancedCandidate(text, i); ok {
			found = append(found, candidate)
			i += len(candidate) - 1
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// balancedCandidate returns the balanced object opened at start when it
// parses, so prose around exactly one object extracts cleanly.
func balancedCandidate(text string, start int) (string, bool) {
	end, ok := objectEnd(text, start)
	if !ok {
		return "", false
	}
	candidate := text[start : end+1]
	if !json.Valid([]byte(candidate)) {
		return "", false
	}
	return candidate, true
}

// objectEnd finds the closing brace of the object opened at start, ignoring
// braces inside JSON strings.
func objectEnd(text string, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			escaped, inString = scanStringByte(c, escaped, inString)
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		var done bool
		depth, done = scanBrace(c, depth)
		if done {
			return i, true
		}
	}
	return 0, false
}

// scanBrace folds one structural byte into the brace depth, reporting the
// object's end when depth returns to zero.
func scanBrace(c byte, depth int) (int, bool) {
	switch c {
	case '{':
		return depth + 1, false
	case '}':
		if depth == 1 {
			return depth, true
		}
		return depth - 1, false
	default:
		return depth, false
	}
}

// scanStringByte folds one byte of a JSON string into the escape state.
func scanStringByte(c byte, escaped, inString bool) (bool, bool) {
	switch {
	case escaped:
		return false, inString
	case c == '\\':
		return true, inString
	case c == '"':
		return false, false
	default:
		return false, inString
	}
}

func normalizeReviewObject(obj *jq.Obj) []Problem {
	var repairs []Problem
	repairs = append(repairs, dropNulls(obj, problemRoot, topOptionalKeys)...)
	for _, key := range slices.Clone(obj.Keys) {
		v, _ := obj.Get(key)
		switch key {
		case fieldComments:
			repairs = append(repairs, normalizeComments(obj, v)...)
		case fieldResolved, fieldChunks:
			repairs = append(repairs, scalarToStringArray(obj, key, "$."+key)...)
		case fieldSections:
			repairs = append(repairs, normalizeSections(v, "$.sections")...)
		case fieldTriage:
			repairs = append(repairs, normalizeTriage(v, "$.triage")...)
		}
	}
	return repairs
}

func normalizeComments(parent *jq.Obj, v any) []Problem {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var repairs []Problem
	seen := map[string]bool{}
	kept := make([]any, 0, len(items))
	for i, item := range items {
		base := fmt.Sprintf("$.comments[%d]", i)
		m, ok := item.(*jq.Obj)
		if !ok {
			kept = append(kept, item)
			continue
		}
		repairs = append(repairs, normalizeComment(m, base)...)
		canon := canonicalJSON(m)
		if seen[canon] {
			repairs = append(repairs, repairProblem(core.ProblemCodeNormalizedTrivia, base,
				"comment is an exact duplicate of an earlier comment",
				"remove the duplicate comment"))
			continue
		}
		seen[canon] = true
		kept = append(kept, item)
	}
	if len(kept) != len(items) {
		parent.Set(fieldComments, kept)
	}
	return repairs
}

func normalizeComment(m *jq.Obj, base string) []Problem {
	var repairs []Problem
	repairs = append(repairs, dropNulls(m, base, commentNullAliases)...)
	repairs = append(repairs, renameAliases(m, base, commentAliases)...)
	repairs = append(repairs, dropNulls(m, base, commentOptionalKeys)...)
	for _, key := range slices.Clone(m.Keys) {
		v, _ := m.Get(key)
		switch key {
		case fieldRigor:
			repairs = append(repairs, normalizeRigor(m, key, v, base)...)
		case fieldSeverity:
			repairs = append(repairs, normalizeEnum(m, key, v, base, reviewKeys)...)
		case fieldSide:
			repairs = append(repairs, normalizeSide(m, key, v, base)...)
		case "kb":
			repairs = append(repairs, scalarToStringArray(m, key, base+pathDot+key)...)
		case fieldBrief:
			if brief, ok := v.(*jq.Obj); ok {
				repairs = append(repairs, normalizeBrief(brief, base+".brief")...)
			}
		}
	}
	return repairs
}

func normalizeBrief(m *jq.Obj, base string) []Problem {
	var repairs []Problem
	repairs = append(repairs, dropNulls(m, base, briefOptionalKeys)...)
	for _, key := range slices.Clone(m.Keys) {
		v, _ := m.Get(key)
		switch key {
		case "questions":
			repairs = append(repairs, scalarToStringArray(m, key, base+pathDot+key)...)
		case "sites":
			repairs = append(repairs, normalizeSites(v, base+".sites")...)
		}
	}
	return repairs
}

func normalizeSites(v any, base string) []Problem {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var repairs []Problem
	for i, item := range items {
		m, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		siteBase := fmt.Sprintf("%s[%d]", base, i)
		repairs = append(repairs, renameAliases(m, siteBase, siteAliases)...)
		repairs = append(repairs, dropNulls(m, siteBase, siteOptionalKeys)...)
	}
	return repairs
}

func normalizeSections(v any, base string) []Problem {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var repairs []Problem
	for i, item := range items {
		m, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		sectionBase := fmt.Sprintf("%s[%d]", base, i)
		repairs = append(repairs, dropNulls(m, sectionBase, sectionOptionalKeys)...)
		sev, _ := m.Get(fieldSeverity)
		repairs = append(repairs, normalizeEnum(m, fieldSeverity, sev, sectionBase, reviewKeys)...)
	}
	return repairs
}

func normalizeTriage(v any, base string) []Problem {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var repairs []Problem
	for i, item := range items {
		m, ok := item.(*jq.Obj)
		if !ok {
			continue
		}
		triageBase := fmt.Sprintf("%s[%d]", base, i)
		repairs = append(repairs, dropNulls(m, triageBase, triageOptionalKeys)...)
		status, _ := m.Get("status")
		repairs = append(repairs, normalizeEnum(m, "status", status, triageBase, triageStatuses)...)
	}
	return repairs
}

func normalizeRigor(m *jq.Obj, key string, v any, base string) []Problem {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	lower := strings.ToLower(s)
	canonical := lower
	if mapped, ok := rigorSynonyms[lower]; ok {
		canonical = mapped
	} else if !slices.Contains(rigorKeys, lower) {
		return nil
	}
	if canonical == s {
		return nil
	}
	m.Set(key, canonical)
	return []Problem{repairProblem(core.ProblemCodeEnumSynonym, base+pathDot+key,
		fmt.Sprintf("rigor %q was rewritten to %q", s, canonical),
		"use said|pointed|walked|ran|reproduced")}
}

func normalizeEnum(m *jq.Obj, key string, v any, base string, allowed []string) []Problem {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	lower := strings.ToLower(s)
	if !slices.Contains(allowed, lower) || lower == s {
		return nil
	}
	m.Set(key, lower)
	return []Problem{repairProblem(core.ProblemCodeEnumSynonym, base+pathDot+key,
		fmt.Sprintf("%s %q was rewritten to %q", key, s, lower),
		"use a lowercase published value")}
}

func normalizeSide(m *jq.Obj, key string, v any, base string) []Problem {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	upper := strings.ToUpper(s)
	if (upper != sideRight && upper != sideLeft) || upper == s {
		return nil
	}
	m.Set(key, upper)
	return []Problem{repairProblem(core.ProblemCodeEnumSynonym, base+pathDot+key,
		fmt.Sprintf("side %q was rewritten to %q", s, upper),
		"use RIGHT or LEFT")}
}

func scalarToStringArray(m *jq.Obj, key, path string) []Problem {
	v, _ := m.Get(key)
	s, ok := v.(string)
	if !ok {
		return nil
	}
	m.Set(key, []any{s})
	return []Problem{repairProblem(core.ProblemCodeScalarToArray, path,
		key+" was a string; wrapped it in a one-element array",
		"write an array of strings")}
}

func dropNulls(m *jq.Obj, base string, optional map[string]bool) []Problem {
	var repairs []Problem
	for _, key := range slices.Clone(m.Keys) {
		v, _ := m.Get(key)
		if v != nil || !optional[key] {
			continue
		}
		m.Delete(key)
		repairs = append(repairs, repairProblem(core.ProblemCodeNormalizedTrivia, base+pathDot+key,
			fmt.Sprintf("optional key %q was null; dropped it", key),
			"omit the key instead of writing null"))
	}
	return repairs
}

func renameAliases(m *jq.Obj, base string, aliases map[string]string) []Problem {
	var repairs []Problem
	for _, key := range slices.Clone(m.Keys) {
		canonical, ok := aliases[key]
		if !ok || m.Has(canonical) {
			continue
		}
		renameKey(m, key, canonical)
		repairs = append(repairs, repairProblem(core.ProblemCodeKeyAlias, base+pathDot+canonical,
			fmt.Sprintf("key %q was renamed to %q", key, canonical),
			"use the published key "+canonical))
	}
	return repairs
}

func renameKey(m *jq.Obj, from, to string) {
	v, _ := m.Get(from)
	delete(m.Fields, from)
	m.Fields[to] = v
	for i, key := range m.Keys {
		if key == from {
			m.Keys[i] = to
		}
	}
}

// repairProblem builds one warning: every NormalizeReview rewrite reports
// what it did and the exact fix, never touching prose.
const (
	// pathDot joins a JSONPath base to a child key.
	pathDot = "."
	// pathIndexBase is the decimal radix for array index ranks.
	pathIndexBase = 10
	// missingRank sorts a repair on a dropped key after surviving siblings.
	missingRank = 1 << 20
)

func repairProblem(code, path, message, fix string) Problem {
	return Problem{
		Path: path, Code: code, Level: core.ProblemLevelWarning,
		Message: message, Fix: fix,
	}
}

func canonicalJSON(v any) string {
	switch t := v.(type) {
	case *jq.Obj:
		return canonicalObject(t)
	case []any:
		return canonicalArray(t)
	default:
		return jq.Compact(v)
	}
}

// canonicalObject renders an object with sorted keys so duplicate detection
// ignores key order.
func canonicalObject(t *jq.Obj) string {
	keys := slices.Clone(t.Keys)
	slices.Sort(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(jq.Compact(key))
		sb.WriteByte(':')
		value, _ := t.Get(key)
		sb.WriteString(canonicalJSON(value))
	}
	sb.WriteByte('}')
	return sb.String()
}

// canonicalArray renders an array in order.
func canonicalArray(items []any) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = canonicalJSON(item)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

var (
	rigorSynonyms = map[string]string{
		"traced": rungWalked,
		"test":   rungRan,
		"repro":  rungReproduced,
		"read":   rungPointed,
	}
	commentAliases = map[string]string{
		"inBounds": fieldInBoundsInput,
		"rung":     fieldRigor,
		"file":     fieldPath,
	}
	siteAliases = map[string]string{"file": fieldPath}

	topOptionalKeys = map[string]bool{
		fieldResolved: true, fieldTriage: true, fieldSections: true,
		fieldConfidence: true, fieldSecuritySummary: true, fieldChunks: true,
	}
	commentOptionalKeys = map[string]bool{
		fieldLine: true, "start_line": true, fieldSide: true, "suggestion": true,
		fieldBrief: true, fieldRigor: true, fieldSeverity: true, fieldInBoundsInput: true, "kb": true,
	}
	commentNullAliases = map[string]bool{"rung": true, "inBounds": true}
	briefOptionalKeys  = map[string]bool{
		"sites": true, "approach": true, "questions": true,
	}
	siteOptionalKeys    = map[string]bool{fieldLine: true}
	sectionOptionalKeys = map[string]bool{fieldSeverity: true}
	triageOptionalKeys  = map[string]bool{"rationale": true}
)

// MergeRepairs folds the repairs NormalizeReview reported into the checker's
// problem list in document order, then reapplies the 50-problem cap. Repairs
// are warnings; strict promotion happens after this merge.
func MergeRepairs(res CheckResult, repairs []Problem, normalized []byte) CheckResult {
	if len(repairs) == 0 {
		return res
	}
	// Normalized bytes always decode — NormalizeReview built them — but a
	// caller-supplied mismatch must not fail the merge: every rank then
	// falls back to insertion order.
	doc, err := jq.DecodeOrdered(normalized)
	if err != nil {
		doc = nil
	}
	items := rankMerge(res, repairs, doc)
	sortRanked(items)
	return capRanked(res, items, len(repairs))
}

type rankedProblem struct {
	problem Problem
	rank    []int
	repair  bool
}

// rankMerge pairs every problem and repair with its document-order rank.
func rankMerge(res CheckResult, repairs []Problem, doc any) []rankedProblem {
	items := make([]rankedProblem, 0, len(res.Problems)+len(repairs))
	for _, p := range res.Problems {
		items = append(items, rankedProblem{problem: p, rank: docRank(doc, parsePath(p.Path))})
	}
	for _, p := range repairs {
		items = append(items, rankedProblem{problem: p, rank: docRank(doc, parsePath(p.Path)), repair: true})
	}
	return items
}

// sortRanked orders the merge in document order, repairs before checker
// problems on the same path.
func sortRanked(items []rankedProblem) {
	slices.SortStableFunc(items, func(a, b rankedProblem) int {
		if c := compareRank(a.rank, b.rank); c != 0 {
			return c
		}
		if a.repair != b.repair {
			if a.repair {
				return -1
			}
			return 1
		}
		return 0
	})
}

// capRanked reapplies the 50-problem cap and counts every repair as warned.
func capRanked(res CheckResult, items []rankedProblem, nrepairs int) CheckResult {
	total := len(items) + res.Truncated
	if total > problemCap {
		items = items[:problemCap]
	}
	merged := make([]Problem, len(items))
	for i := range items {
		merged[i] = items[i].problem
	}
	res.Problems = merged
	res.Truncated = total - len(merged)
	res.Warnings += nrepairs
	return res
}

type pathToken struct {
	key   string
	index int
	isIdx bool
}

func parsePath(path string) []pathToken {
	var tokens []pathToken
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '$', '.':
		case '[':
			i = parseIndexToken(path, i, &tokens)
		default:
			i = parseKeyToken(path, i, &tokens)
		}
	}
	return tokens
}

// parseIndexToken appends one [n] token and returns the index of ']'.
func parseIndexToken(path string, i int, tokens *[]pathToken) int {
	end, ok := indexEnd(path, i)
	if !ok {
		return i
	}
	n := 0
	for _, c := range path[i+1 : end] {
		n = n*pathIndexBase + int(c-'0')
	}
	*tokens = append(*tokens, pathToken{index: n, isIdx: true})
	return end
}

// indexEnd finds the ']' closing a digit run opened at i.
func indexEnd(path string, i int) (int, bool) {
	j := i + 1
	for j < len(path) && path[j] >= '0' && path[j] <= '9' {
		j++
	}
	if j < len(path) && path[j] == ']' && j > i+1 {
		return j, true
	}
	return i, false
}

// parseKeyToken appends one dotted key and returns the index before its end.
func parseKeyToken(path string, i int, tokens *[]pathToken) int {
	j := i
	for j < len(path) && path[j] != '.' && path[j] != '[' {
		j++
	}
	*tokens = append(*tokens, pathToken{key: path[i:j]})
	return j - 1
}

func docRank(doc any, tokens []pathToken) []int {
	cur := doc
	rank := make([]int, 0, len(tokens))
	for _, token := range tokens {
		cur = rankToken(cur, token, &rank)
	}
	return rank
}

// rankToken appends one token's rank and returns the child to descend into.
func rankToken(cur any, token pathToken, rank *[]int) any {
	if token.isIdx {
		return rankIndex(cur, token.index, rank)
	}
	return rankKey(cur, token.key, rank)
}

// rankIndex appends one array index rank and returns the element.
func rankIndex(cur any, index int, rank *[]int) any {
	arr, ok := cur.([]any)
	if !ok {
		*rank = append(*rank, missingRank)
		return nil
	}
	*rank = append(*rank, index)
	if index < 0 || index >= len(arr) {
		return nil
	}
	return arr[index]
}

// rankKey appends one object key rank and returns the value.
func rankKey(cur any, key string, rank *[]int) any {
	obj, ok := cur.(*jq.Obj)
	if !ok {
		*rank = append(*rank, missingRank)
		return nil
	}
	idx := slices.Index(obj.Keys, key)
	if idx < 0 {
		idx = len(obj.Keys)
	}
	*rank = append(*rank, idx)
	v, _ := obj.Get(key)
	return v
}

func compareRank(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
