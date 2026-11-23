// Package coverage evaluates an agent's diff-coverage attestation against the
// bundle's own hunk snapshot. It is a read receipt, not surveillance: the agent
// says which changed-line ranges it read, and the evaluator reports what is
// still unattested, so a partial review is visible instead of assumed.
//
// One evaluator serves post, record, and next. Coverage never changes a
// finding, a status, or a post — it produces a summary and, when the operator
// asked for one, a typed refusal.
package coverage

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// ChunkWarning is the one line a chunked bundle emits. It is not a blanket
// notice: the caller surfaces it only when the operator asked for the gate or
// the agent wrote an attestation, so a bundle that never used coverage reads
// exactly as it did before this feature.
const ChunkWarning = "coverage: chunked bundle — chunks[] is the attestation; coverage[] is ignored"

// Basis names what the coverage percentage is a percentage of: the evaluator
// scores every hunk span on both sides, context included. Every surface that
// prints the number uses this one constant so the wording cannot drift between
// `next --coverage` and the record warnings.
const Basis = "full hunk spans, both sides and context"

// Field is the review.json key the attestation lives under. FieldChunks is the
// chunked bundle's equivalent: the manifest chunk names the review claimed.
const (
	Field       = "coverage"
	FieldChunks = "chunks"
	FieldFile   = "file"
	FieldRanges = "ranges"
	// RangesAll attests a whole file.
	RangesAll = "all"
)

// Bundle files the evaluator reads, relative to the bundle directory.
const (
	hunksFile       = "/hunks.tsv"
	fullDiffFile    = "/full.diff"
	chunksDir       = "/chunks"
	manifestFile    = "/manifest.json"
	incrementalFile = "/incremental.diff"
	priorFile       = "/prior-coverage.json"
)

// Codes and messages.
const (
	// CodeIncomplete is the coverage gate's refusal.
	CodeIncomplete = "coverage-incomplete"
	// CodeBadReviewInput is the review-input refusal record's checker raises.
	CodeBadReviewInput = "bad-review-input"

	msgReviewProblem      = "review.json has %d problem(s); fix every one, then re-run"
	msgRangesWrongType    = "value must be a string"
	msgRangesWrongTypeFix = `use "1-240,300-320", "all", or omit it`

	msgHunksUnreadable = "hunks.tsv unreadable: %v"
	msgEntryNotObject  = "coverage: entry %d is not an object — ignored"
	msgEntryNoFile     = "coverage: entry %d has no file — ignored"
	msgBadRanges       = "coverage: %s has malformed ranges %q — counted as unattested"
	msgUnknownFile     = "coverage: %s is not a changed file with hunks — entry ignored"
	msgNotArray        = "coverage: is not an array — treated as an empty attestation"
	msgChunked         = ChunkWarning
	msgNoAttestation   = "coverage: no coverage[] attestation present"

	msgIncrementalNoPrior = "coverage: incremental bundle has no recorded prior coverage — grading against full hunks"
	msgPriorUnreadable    = "coverage: recorded prior coverage unreadable — no prior ranges credited"
	msgPriorBadRanges     = "coverage: prior entry %d has malformed ranges %q — ignored"
	msgPriorNotObject     = "coverage: prior entry %d is not an object — ignored"
	msgPriorNoFile        = "coverage: prior entry %d has no file — ignored"
	msgPriorCarried       = "coverage: incremental — prior attestation carried %d line(s) over from the recorded prior round"
)

// Range is one 1-based inclusive source-line interval.
type Range struct{ Start, End int }

// Gap names the ranges of one file the agent did not attest.
type Gap struct {
	File   string `json:"file"`
	Ranges string `json:"ranges"`
}

// Summary is the one coverage shape: the stored receipt in post.json and
// record.json, the --json payload of next --coverage, and the value the body
// line is rendered from. Keys are absent from a stored receipt when nothing was
// evaluated, so a legacy store stays byte-identical.
type Summary struct {
	Percent *int `json:"percent"`
	// Unattested is the per-file gap list the body line and the --json payload
	// render; Gaps counts the same ranges, which is what the warning reports.
	Unattested []Gap `json:"unattested,omitempty"`
	// AttestedRanges is the per-file attested union PersistPrior saves as
	// the next round's recorded prior credit. It never rides a receipt.
	AttestedRanges []Gap `json:"-"`
	Attested       int   `json:"attested"`
	Total          int   `json:"total"`
	Files          int   `json:"files"`
	Exempt         int   `json:"exempt"`
	Gaps           int   `json:"gaps"`
	Chunks         int   `json:"chunks,omitempty"`
	Chunked        bool  `json:"chunked,omitempty"`
	Present        bool  `json:"-"`
	// Incremental reports a delta bundle (incremental.diff present): its
	// percent already credits recorded prior ranges. It never rides a
	// receipt; it only keeps the run's own lines truthful about the credit.
	Incremental bool `json:"-"`
	// ChunksAttested counts the manifest chunks the review claimed. It rides
	// the store receipt and never the --json payload, which reports chunks
	// alone; a chunked bundle is not scored in lines.
	ChunksAttested int `json:"-"`
	// PriorLines counts prior-attested lines the current claims did not
	// already cover: the lines credited from the recorded prior round.
	PriorLines int `json:"-"`
}

// ChunkReceipt returns the chunked bundle's stored attestation, or nil for a
// bundle with no chunk manifest. A chunked post.json or record.json carries
// this so `gate` can grade the bundle from the store instead of re-reading
// review.json, the same way it reads the line-coverage receipt beside it.
func (s *Summary) ChunkReceipt() *core.ChunkCoverage {
	if s == nil || !s.Chunked {
		return nil
	}
	return &core.ChunkCoverage{Attested: s.ChunksAttested, Total: s.Chunks}
}

// Receipt returns the published receipt shape, or nil when the run has nothing
// to store: a legacy review with no attestation and a chunked bundle both keep
// their old artifacts byte-identical.
func (s *Summary) Receipt() *core.CoverageSummary {
	if s == nil || s.Chunked || !s.Present {
		return nil
	}
	return &core.CoverageSummary{
		Percent:  s.Percent,
		Attested: s.Attested,
		Total:    s.Total,
		Files:    s.Files,
		Exempt:   s.Exempt,
		Gaps:     s.Gaps,
	}
}

// percentTotal is the denominator a coverage percentage is taken over.
const percentTotal = 100

// Measured reports whether the bundle had any non-exempt changed line. A
// bundle where every file is exempt has nothing to attest and nothing to
// report: it is neither complete nor partial.
func (s *Summary) Measured() bool { return s.Total > 0 }

// Complete reports a fully attested non-chunked run.
func (s *Summary) Complete() bool { return s.Percent != nil && *s.Percent == percentTotal }

// claim is one parsed coverage[] entry.
type claim struct {
	file   string
	ranges []Range
	whole  bool
	bad    bool
}

// Evaluate reads the bundle's hunk snapshot and the review's attestation, and
// returns the summary plus the warnings a caller should surface. A chunked
// bundle is not evaluated line by line: chunks[] is its attestation, and one
// gate per bundle is the contract.
//
// generated is the shared generated-path predicate; this package never owns a
// second list.
func Evaluate(dir string, review *jq.Obj, generated func(string) bool) (Summary, []string, *core.RefusalError) {
	if attested, total, ok := chunked(dir, review); ok {
		var warns []string
		_, _, problems := parseClaims(review, &warns)
		if len(problems) > 0 {
			return Summary{}, warns, badReviewInput(problems)
		}
		return Summary{Chunked: true, Chunks: total, ChunksAttested: attested}, nil, nil
	}
	rows, ref := loadRows(dir)
	if ref != nil {
		return Summary{}, nil, ref
	}
	files := intervalsByFile(rows)
	var warns []string
	claims, present, problems := parseClaims(review, &warns)
	if len(problems) > 0 {
		return Summary{}, warns, badReviewInput(problems)
	}
	warns = append(warns, unknownClaims(claims, files)...)
	sum, outWarns := summarizeIncremental(dir, files, claims, generated, &warns)
	sum.Present = present
	return sum, outWarns, nil
}

func badReviewInput(problems []core.Problem) *core.RefusalError {
	return &core.RefusalError{
		Code:     CodeBadReviewInput,
		Message:  fmt.Sprintf(msgReviewProblem, len(problems)),
		Problems: problems,
	}
}

// unknownClaims warns about entries naming a path the bundle has no hunks for:
// a binary, rename-only, or entirely absent file attests nothing and must not
// read as coverage.
func unknownClaims(claims []claim, files map[string][]Range) []string {
	var out []string
	for _, c := range claims {
		if _, ok := files[c.file]; !ok {
			out = append(out, fmt.Sprintf(msgUnknownFile, c.file))
		}
	}
	return out
}

// summarizeIncremental unions the recorded prior round into the current
// claims when incremental.diff marks a delta bundle, then scores the union
// against the full hunk snapshot: the agent must attest the delta, ranges
// the prior round already covered stay credited, and ranges it never
// covered remain gaps unless the agent attests them now. A full-diff
// bundle ignores the recorded prior entirely, so its grading is unchanged.
func summarizeIncremental(dir string, files map[string][]Range, claims []claim,
	generated func(string) bool, warns *[]string,
) (Summary, []string) {
	current := attestation(files, claims)
	attested := current
	if !isIncremental(dir) {
		sum := summarize(files, attested, generated)
		sum.AttestedRanges = attestedGaps(files, attested, generated)
		return sum, *warns
	}
	prior, priorWarns, found := loadPrior(dir, files)
	*warns = append(*warns, priorWarns...)
	switch {
	case !found:
		*warns = append(*warns, msgIncrementalNoPrior)
	default:
		attested = unionMaps(current, prior)
	}
	sum := summarize(files, attested, generated)
	sum.Incremental = true
	sum.AttestedRanges = attestedGaps(files, attested, generated)
	if found {
		sum.PriorLines = priorOnlyLines(files, prior, current, generated)
		if sum.PriorLines > 0 {
			*warns = append(*warns, fmt.Sprintf(msgPriorCarried, sum.PriorLines))
		}
	}
	return sum, *warns
}

// summarize is the pure part: intervals in, summary out.
func summarize(files map[string][]Range, attested map[string][]Range, generated func(string) bool) Summary {
	order := slices.Sorted(maps.Keys(files))

	var sum Summary
	for _, f := range order {
		all := files[f]
		if generated(f) {
			sum.Exempt++
			continue
		}
		sum.Files++
		sum.Total += span(all)
		read := intersect(all, attested[f])
		sum.Attested += span(read)
		if rest := subtract(all, read); len(rest) > 0 {
			sum.Unattested = append(sum.Unattested, Gap{File: f, Ranges: Format(rest)})
			sum.Gaps += len(rest)
		}
	}
	if sum.Total > 0 {
		percent := sum.Attested * percentTotal / sum.Total
		sum.Percent = &percent
	}
	return sum
}

// attestedGaps renders the per-file attested union as file/ranges gaps —
// the exact bytes PersistPrior saves as the next round's recorded credit.
func attestedGaps(files map[string][]Range, attested map[string][]Range, generated func(string) bool) []Gap {
	order := slices.Sorted(maps.Keys(files))
	var out []Gap
	for _, f := range order {
		if generated(f) {
			continue
		}
		if read := intersect(files[f], attested[f]); len(read) > 0 {
			out = append(out, Gap{File: f, Ranges: Format(read)})
		}
	}
	return out
}

// priorOnlyLines counts the lines the prior round covered beyond the
// current claims: the credit the incremental reading actually added.
func priorOnlyLines(files map[string][]Range, prior, current map[string][]Range,
	generated func(string) bool,
) int {
	n := 0
	for f, all := range files {
		if generated(f) {
			continue
		}
		extra := subtract(intersect(all, prior[f]), intersect(all, current[f]))
		n += span(extra)
	}
	return n
}

// unionMaps merges two per-file interval sets, one file at a time.
func unionMaps(a, b map[string][]Range) map[string][]Range {
	out := maps.Clone(a)
	for f, rs := range b {
		out[f] = merge(append(out[f], rs...))
	}
	return out
}

// isIncremental reports a delta bundle: incremental.diff parses to hunks.
// The full snapshot stays the denominator; the delta is a subset of it, so
// scoring the prior-plus-current union against the full snapshot enforces
// the delta without re-reading the delta for a second denominator. A marker
// that carries no hunk grades as a full bundle (prior credit needs a delta).
func isIncremental(dir string) bool {
	raw, err := os.ReadFile(dir + incrementalFile)
	return err == nil && len(hunks.Parse(string(raw))) > 0
}

// priorClaim is one persisted attested interval set, the prior-coverage.json
// entry shape: the same file/ranges pair the review attestation uses.
type priorClaim struct {
	File   string `json:"file"`
	Ranges string `json:"ranges"`
}

// loadPrior reads the recorded prior attestation. Only a durable prior
// record is trusted: an absent or undecodable file credits nothing (fail
// closed), and every malformed entry is skipped with a warning.
func loadPrior(dir string, files map[string][]Range) (map[string][]Range, []string, bool) {
	raw, err := os.ReadFile(dir + priorFile)
	if err != nil {
		return nil, nil, false
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return nil, []string{msgPriorUnreadable}, false
	}
	items, ok := doc.([]any)
	if !ok {
		return nil, []string{msgPriorUnreadable}, false
	}
	if len(items) == 0 {
		return nil, nil, false
	}
	out := map[string][]Range{}
	var warns []string
	for i, item := range items {
		f, rs, ok := priorEntry(item, i, files, &warns)
		if ok {
			out[f] = merge(append(out[f], rs...))
		}
	}
	return out, warns, true
}

// priorEntry parses one prior-coverage.json entry: whole-file claims expand
// to the file's own intervals, as in the review attestation.
func priorEntry(item any, i int, files map[string][]Range, warns *[]string) (string, []Range, bool) {
	m, ok := jq.AsMap(item)
	if !ok {
		*warns = append(*warns, fmt.Sprintf(msgPriorNotObject, i))
		return "", nil, false
	}
	file := strings.TrimSpace(jq.Str(m[FieldFile]))
	if file == "" {
		*warns = append(*warns, fmt.Sprintf(msgPriorNoFile, i))
		return "", nil, false
	}
	val, present := m[FieldRanges]
	if !present {
		return file, files[file], true
	}
	s, isStr := val.(string)
	if !isStr {
		*warns = append(*warns, fmt.Sprintf(msgPriorBadRanges, i, fmt.Sprintf("%v", val)))
		return "", nil, false
	}
	ranges := strings.TrimSpace(s)
	if ranges == "" || ranges == RangesAll {
		return file, files[file], true
	}
	parsed, err := ParseRanges(ranges)
	if err != nil {
		*warns = append(*warns, fmt.Sprintf(msgPriorBadRanges, i, ranges))
		return "", nil, false
	}
	return file, parsed, true
}

// PersistPrior saves one run's attested union as the next round's recorded
// prior credit. It never writes a chunked or empty attestation: both leave
// chain state untouched, so an unattested round can neither forge nor erase
// the credit earlier rounds recorded.
func PersistPrior(dir string, sum *Summary) error {
	if sum == nil || sum.Chunked || len(sum.AttestedRanges) == 0 {
		return nil
	}
	entries := make([]priorClaim, 0, len(sum.AttestedRanges))
	for _, g := range sum.AttestedRanges {
		entries = append(entries, priorClaim(g))
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		//nolint:wrapcheck // locally constructed message; record seals it
		return fmt.Errorf("could not encode prior coverage: %w", err)
	}
	return atomic.WriteFile(dir+priorFile, append(raw, '\n'))
}

// attestation unions the valid claims per file. A whole-file claim expands to
// the file's own intervals so a partial hunk set still counts exactly; a claim
// with no usable range attests nothing and is skipped.
func attestation(files map[string][]Range, claims []claim) map[string][]Range {
	attested := map[string][]Range{}
	for _, c := range claims {
		if c.bad {
			continue
		}
		switch {
		case c.whole:
			attested[c.file] = merge(append(attested[c.file], files[c.file]...))
		case len(c.ranges) > 0:
			attested[c.file] = merge(append(attested[c.file], c.ranges...))
		}
	}
	return attested
}

// parseClaims reads coverage[] and reports whether the key was present. Every
// malformed entry warns; none of them silently attests a file.
func parseClaims(review *jq.Obj, warns *[]string) ([]claim, bool, []core.Problem) {
	if review == nil {
		return nil, false, nil
	}
	raw, ok := review.Get(Field)
	if !ok {
		return nil, false, nil
	}
	items, isArr := raw.([]any)
	if !isArr {
		*warns = append(*warns, msgNotArray)
		return nil, true, nil
	}
	out, problems := collectClaims(items, warns)
	return out, true, problems
}

func collectClaims(items []any, warns *[]string) ([]claim, []core.Problem) {
	out := make([]claim, 0, len(items))
	var problems []core.Problem
	for i, item := range items {
		c, ok, p := claimAt(item, i, warns)
		if p != nil {
			problems = append(problems, *p)
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, problems
}

// claimAt reads one coverage[] entry, warning and reporting false for an entry
// that cannot name a file. A malformed range still yields a claim, marked bad,
// so the merge warns about it and never counts it as attested.
func claimAt(item any, i int, warns *[]string) (claim, bool, *core.Problem) {
	obj, isObj := item.(*jq.Obj)
	if !isObj || obj == nil {
		*warns = append(*warns, fmt.Sprintf(msgEntryNotObject, i))
		return claim{}, false, nil
	}
	file := strings.TrimSpace(jq.Str(fieldOf(obj, FieldFile)))
	if file == "" {
		*warns = append(*warns, fmt.Sprintf(msgEntryNoFile, i))
		return claim{}, false, nil
	}
	ranges, whole, bad, problem := claimRanges(obj, file, i, warns)
	return claim{file: file, ranges: ranges, whole: whole, bad: bad}, true, problem
}

// claimRanges resolves one entry's ranges field: a whole-file claim, a parsed
// range list, or a claim marked bad when the field is present but unreadable.
func claimRanges(obj *jq.Obj, file string, i int, warns *[]string) ([]Range, bool, bool, *core.Problem) {
	ranges, present, wrongType := rangeField(obj)
	switch {
	case wrongType:
		return nil, false, true, rangesWrongType(i)
	case !present:
		*warns = append(*warns, fmt.Sprintf(msgBadRanges, file, jq.Str(mustField(obj, FieldRanges))))
		return nil, false, true, nil
	case ranges == "" || ranges == RangesAll:
		return nil, true, false, nil
	default:
		parsed, err := ParseRanges(ranges)
		if err != nil {
			*warns = append(*warns, fmt.Sprintf(msgBadRanges, file, ranges))
			return nil, false, true, nil
		}
		return parsed, false, false, nil
	}
}

func rangesWrongType(i int) *core.Problem {
	return &core.Problem{
		Path:    fmt.Sprintf("$.coverage[%d].ranges", i),
		Code:    core.ProblemCodeWrongType,
		Level:   core.ProblemLevelError,
		Message: msgRangesWrongType,
		Fix:     msgRangesWrongTypeFix,
	}
}

// fieldOf reads one key off a decoded object.
func fieldOf(o *jq.Obj, key string) any {
	v, _ := o.Get(key)
	return v
}

func mustField(o *jq.Obj, key string) any { return fieldOf(o, key) }

// rangeField reads ranges as a trimmed string. A key that is present but not a
// string — `null` included — is a malformed attestation, not a whole-file
// claim: attesting more than was written is the one error this package must
// never make, and a claim only widens by saying `all` or omitting the key.
func rangeField(o *jq.Obj) (value string, present, wrongType bool) {
	raw, ok := o.Get(FieldRanges)
	if !ok {
		return "", true, false
	}
	s, isStr := raw.(string)
	if !isStr {
		if raw == nil {
			return "", false, false
		}
		return "", true, true
	}
	return strings.TrimSpace(s), true, false
}

// loadRows reads the fetch-time snapshot, rebuilding it in memory from
// full.diff when a hand-built bundle predates it. The snapshot stays the
// contract; the rebuild is a fallback, never a written second source.
func loadRows(dir string) ([]hunks.Hunk, *core.RefusalError) {
	rows, err := hunks.Load(dir)
	if err != nil {
		return nil, &core.RefusalError{Code: CodeIncomplete, Message: fmt.Sprintf(msgHunksUnreadable, err)}
	}
	if rows != nil {
		return rows, nil
	}
	raw, rerr := os.ReadFile(dir + fullDiffFile)
	if rerr == nil {
		return hunks.Parse(string(raw)), nil
	}
	return nil, nil
}

// chunked reports whether the bundle carries a chunk manifest, and how many of
// its chunks the review attested. The manifest is the authority, the same one
// `record` reads: counting `*.diff` files in the directory let a stray or
// missing file move the count while the attestation it describes stayed put.
// A manifest that will not parse still marks the bundle chunked with no count;
// `record` refuses such a bundle, and the count never surfaces.
func chunked(dir string, review *jq.Obj) (attested, total int, ok bool) {
	raw, err := os.ReadFile(dir + chunksDir + manifestFile)
	if err != nil {
		return 0, 0, false
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		return 0, 0, true
	}
	man, isMap := jq.AsMap(doc)
	if !isMap {
		return 0, 0, true
	}
	names := manifestChunkNames(man)
	return claimedChunks(review, names), len(names), true
}

// manifestChunkNames reads the named chunks a manifest lists. An entry that is
// not an object or names nothing is skipped, because `record` cannot require
// an attestation for a chunk it cannot name.
func manifestChunkNames(man map[string]any) []string {
	var names []string
	for _, item := range jq.Elements(man[FieldChunks]) {
		m, isMap := jq.AsMap(item)
		if !isMap {
			continue
		}
		if name := jq.Str(m["name"]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// claimedChunks counts the manifest names the review's chunks[] claimed. A
// name the manifest does not list never widens the count, so a claim can only
// attest a chunk that exists.
func claimedChunks(review *jq.Obj, names []string) int {
	claimed, n := claimSet(review), 0
	for _, name := range names {
		if claimed[name] {
			n++
		}
	}
	return n
}

// claimSet reads chunks[] into the names it claims. A bundle with no
// review.json, a key that is not an array, and an entry that is not a string
// claim nothing: attesting more than the review wrote is the one error this
// package must never make.
func claimSet(review *jq.Obj) map[string]bool {
	set := map[string]bool{}
	if review == nil {
		return set
	}
	items, isArr := fieldOf(review, FieldChunks).([]any)
	if !isArr {
		return set
	}
	for _, item := range items {
		if s, isStr := item.(string); isStr {
			set[s] = true
		}
	}
	return set
}

// intervalsByFile merges each file's OLD and NEW spans into a minimal set: a
// changed line counts once however many sides name it.
func intervalsByFile(rows []hunks.Hunk) map[string][]Range {
	out := map[string][]Range{}
	for _, row := range rows {
		out[row.File] = append(out[row.File], Range{Start: row.Start, End: row.End})
	}
	for f, rs := range out {
		out[f] = merge(rs)
	}
	return out
}

// Present reports whether the review carries a coverage key at all. A chunked
// bundle uses it to decide whether the ignored-attestation warning is relevant.
func Present(review *jq.Obj) bool {
	if review == nil {
		return false
	}
	_, ok := review.Get(Field)
	return ok
}

// ParseRanges normalizes "1-240,300-320". A zero, a reversed range, a
// non-numeric token, or an empty token is an error: a malformed attestation
// must never widen what it claims to have read.
func ParseRanges(s string) ([]Range, error) {
	var out []Range
	for tok := range strings.SplitSeq(s, ",") {
		r, err := parseRange(strings.TrimSpace(tok))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return merge(out), nil
}

// parseRange reads one "a" or "a-b" token. A zero, a reversed range, a
// non-numeric token, and an empty token are all errors.
func parseRange(tok string) (Range, error) {
	if tok == "" {
		return Range{}, jq.NewErr("empty range")
	}
	lo, hi, ok := cutRange(tok)
	if !ok {
		return Range{}, jq.NewErrf("malformed range %q", tok)
	}
	start, errS := strconv.Atoi(lo)
	end, errE := strconv.Atoi(hi)
	if errS != nil || errE != nil {
		return Range{}, jq.NewErrf("malformed range %q", tok)
	}
	if start < 1 || end < start {
		return Range{}, jq.NewErrf("malformed range %q", tok)
	}
	return Range{Start: start, End: end}, nil
}

// cutRange splits "42" into "42","42" and "42-48" into its bounds. More than
// one hyphen is malformed rather than parsed left to right.
func cutRange(tok string) (string, string, bool) {
	parts := strings.Split(tok, "-")
	switch len(parts) {
	case 1:
		return parts[0], parts[0], true
	case 2:
		lo, hi := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if lo == "" || hi == "" {
			return "", "", false
		}
		return lo, hi, true
	default:
		return "", "", false
	}
}

// Format renders minimal ranges as "16-20,46-50".
func Format(ranges []Range) string {
	var b strings.Builder
	for i, r := range ranges {
		if i > 0 {
			b.WriteByte(',')
		}
		if r.Start == r.End {
			b.WriteString(strconv.Itoa(r.Start))
			continue
		}
		b.WriteString(strconv.Itoa(r.Start))
		b.WriteByte('-')
		b.WriteString(strconv.Itoa(r.End))
	}
	return b.String()
}

// merge sorts and coalesces overlapping or adjacent intervals.
func merge(rs []Range) []Range {
	if len(rs) == 0 {
		return nil
	}
	sorted := make([]Range, len(rs))
	copy(sorted, rs)
	slices.SortFunc(sorted, func(a, b Range) int { return a.Start - b.Start })
	out := []Range{sorted[0]}
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// span counts the lines a minimal interval set covers.
func span(rs []Range) int {
	n := 0
	for _, r := range rs {
		n += r.End - r.Start + 1
	}
	return n
}

// intersect returns the parts of a covered by b.
func intersect(a, b []Range) []Range {
	var out []Range
	for _, x := range a {
		for _, y := range b {
			start := max(x.Start, y.Start)
			end := min(x.End, y.End)
			if start <= end {
				out = append(out, Range{Start: start, End: end})
			}
		}
	}
	return merge(out)
}

// subtract returns the parts of a not covered by b.
func subtract(a, b []Range) []Range {
	out := append([]Range(nil), a...)
	for _, cut := range b {
		var next []Range
		for _, r := range out {
			next = append(next, cutOne(r, cut)...)
		}
		out = next
	}
	return out
}

// cutOne returns the parts of r that survive one cut: the whole interval when
// they do not overlap, one fragment when the cut takes an end, two when it
// splits the middle, and none when it swallows the interval.
func cutOne(r, cut Range) []Range {
	if cut.End < r.Start || cut.Start > r.End {
		return []Range{r}
	}
	var out []Range
	if cut.Start > r.Start {
		out = append(out, Range{Start: r.Start, End: cut.Start - 1})
	}
	if cut.End < r.End {
		out = append(out, Range{Start: cut.End + 1, End: r.End})
	}
	return out
}
