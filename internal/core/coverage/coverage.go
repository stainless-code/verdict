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
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// ChunkWarning is the one line a chunked bundle emits. It is not a blanket
// notice: the caller surfaces it only when the operator asked for the gate or
// the agent wrote an attestation, so a bundle that never used coverage reads
// exactly as it did before this feature.
const ChunkWarning = "coverage: chunked bundle — chunks[] is the attestation; coverage[] is ignored"

// Field is the review.json key the attestation lives under.
const (
	Field       = "coverage"
	FieldFile   = "file"
	FieldRanges = "ranges"
	// RangesAll attests a whole file.
	RangesAll = "all"
)

// Bundle files the evaluator reads, relative to the bundle directory.
const (
	hunksFile    = "/hunks.tsv"
	fullDiffFile = "/full.diff"
	chunksDir    = "/chunks"
	chunkSuffix  = ".diff"
	manifestFile = "/manifest.json"
)

// Codes and messages.
const (
	// CodeIncomplete is the one refusal this package raises.
	CodeIncomplete = "coverage-incomplete"

	msgHunksUnreadable = "hunks.tsv unreadable: %v"
	msgEntryNotObject  = "coverage: entry %d is not an object — ignored"
	msgEntryNoFile     = "coverage: entry %d has no file — ignored"
	msgBadRanges       = "coverage: %s has malformed ranges %q — counted as unattested"
	msgUnknownFile     = "coverage: %s is not a changed file with hunks — entry ignored"
	msgNotArray        = "coverage: is not an array — treated as an empty attestation"
	msgChunked         = ChunkWarning
	msgNoAttestation   = "coverage: no coverage[] attestation present"
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
	Percent    *int  `json:"percent"`
	Attested   int   `json:"attested"`
	Total      int   `json:"total"`
	Files      int   `json:"files"`
	Exempt     int   `json:"exempt"`
	Gaps       int   `json:"gaps"`
	Unattested []Gap `json:"unattested,omitempty"`
	Chunked    bool  `json:"chunked,omitempty"`
	Chunks     int   `json:"chunks,omitempty"`
	Present    bool  `json:"-"`
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

// Complete reports a fully attested non-chunked run.
func (s Summary) Complete() bool { return s.Percent != nil && *s.Percent == 100 }

// claim is one parsed coverage[] entry.
type claim struct {
	file   string
	ranges []Range
	whole  bool
	bad    bool
}

// Evaluate reads the bundle's hunk snapshot and the review's attestation, and
// returns the summary plus the warnings a caller should surface. A chunked
// bundle is not evaluated: chunks[] is its attestation, and one gate per bundle
// is the contract.
//
// generated is the shared generated-path predicate; this package never owns a
// second list.
func Evaluate(dir string, review *jq.Obj, generated func(string) bool) (Summary, []string, *core.RefusalError) {
	if chunks, ok := chunked(dir); ok {
		return Summary{Chunked: true, Chunks: chunks}, nil, nil
	}
	rows, ref := loadRows(dir)
	if ref != nil {
		return Summary{}, nil, ref
	}
	files := intervalsByFile(rows)
	var warns []string
	claims, present := parseClaims(review, &warns)
	warns = append(warns, unknownClaims(claims, files)...)
	sum := summarize(files, claims, generated)
	sum.Present = present
	return sum, warns, nil
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

// summarize is the pure part: intervals in, summary out.
func summarize(files map[string][]Range, claims []claim, generated func(string) bool) Summary {
	order := make([]string, 0, len(files))
	for f := range files {
		order = append(order, f)
	}
	sort.Strings(order)

	attested := map[string][]Range{}
	for _, c := range claims {
		if c.bad {
			continue
		}
		switch {
		case c.whole:
			// A whole-file attestation is the common case: expand it to the
			// file's own intervals so a partial hunk set still counts exactly.
			attested[c.file] = merge(append(attested[c.file], files[c.file]...))
		case len(c.ranges) > 0:
			attested[c.file] = merge(append(attested[c.file], c.ranges...))
		}
	}

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
		}
	}
	sum.Gaps = len(sum.Unattested)
	if sum.Total > 0 {
		percent := sum.Attested * 100 / sum.Total
		sum.Percent = &percent
	}
	return sum
}

// parseClaims reads coverage[] and reports whether the key was present. Every
// malformed entry warns; none of them silently attests a file.
func parseClaims(review *jq.Obj, warns *[]string) ([]claim, bool) {
	if review == nil {
		return nil, false
	}
	raw, ok := review.Get(Field)
	if !ok {
		return nil, false
	}
	items, isArr := raw.([]any)
	if !isArr {
		*warns = append(*warns, msgNotArray)
		return nil, true
	}
	out := make([]claim, 0, len(items))
	for i, item := range items {
		obj, okObj := item.(*jq.Obj)
		if !okObj || obj == nil {
			*warns = append(*warns, fmt.Sprintf(msgEntryNotObject, i))
			continue
		}
		file := strings.TrimSpace(jq.Str(fieldOf(obj, FieldFile)))
		if file == "" {
			*warns = append(*warns, fmt.Sprintf(msgEntryNoFile, i))
			continue
		}
		c := claim{file: file}
		ranges, okRanges := rangeField(obj)
		switch {
		case !okRanges:
			*warns = append(*warns, fmt.Sprintf(msgBadRanges, file, jq.Str(mustField(obj, FieldRanges))))
			c.bad = true
		case ranges == "" || ranges == RangesAll:
			c.whole = true
		default:
			parsed, err := ParseRanges(ranges)
			if err != nil {
				*warns = append(*warns, fmt.Sprintf(msgBadRanges, file, ranges))
				c.bad = true
			} else {
				c.ranges = parsed
			}
		}
		out = append(out, c)
	}
	return out, true
}

// fieldOf reads one key off a decoded object.
func fieldOf(o *jq.Obj, key string) any {
	v, _ := o.Get(key)
	return v
}

func mustField(o *jq.Obj, key string) any { return fieldOf(o, key) }

// rangeField reads ranges as a trimmed string. A key that is present but not a
// string is a malformed attestation, not a whole-file claim: attesting more
// than was written is the one error this package must never make.
func rangeField(o *jq.Obj) (string, bool) {
	raw, ok := o.Get(FieldRanges)
	if !ok || raw == nil {
		return "", true
	}
	s, isStr := raw.(string)
	if !isStr {
		return "", false
	}
	return strings.TrimSpace(s), true
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
	if rerr != nil {
		return nil, nil
	}
	return hunks.Parse(string(raw)), nil
}

// chunked reports whether the bundle carries a chunk manifest, and how many
// chunk diffs it names.
func chunked(dir string) (int, bool) {
	if _, err := os.Stat(dir + chunksDir + manifestFile); err != nil {
		return 0, false
	}
	entries, err := os.ReadDir(dir + chunksDir)
	if err != nil {
		return 0, true
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), chunkSuffix) {
			n++
		}
	}
	return n, true
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
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return nil, fmt.Errorf("empty range")
		}
		lo, hi, ok := cutRange(tok)
		if !ok {
			return nil, fmt.Errorf("malformed range %q", tok)
		}
		start, errS := strconv.Atoi(lo)
		end, errE := strconv.Atoi(hi)
		if errS != nil || errE != nil {
			return nil, fmt.Errorf("malformed range %q", tok)
		}
		if start < 1 || end < start {
			return nil, fmt.Errorf("malformed range %q", tok)
		}
		out = append(out, Range{Start: start, End: end})
	}
	return merge(out), nil
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
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
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
			if cut.End < r.Start || cut.Start > r.End {
				next = append(next, r)
				continue
			}
			if cut.Start > r.Start {
				next = append(next, Range{Start: r.Start, End: cut.Start - 1})
			}
			if cut.End < r.End {
				next = append(next, Range{Start: cut.End + 1, End: r.End})
			}
		}
		out = next
	}
	return out
}
