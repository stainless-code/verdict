// Package corpuscore loads the benchmark corpus manifests and grades a
// bundle's findings against their recorded ground truth. A corpus bundle is
// a corpus-* directory in the bundle home carrying corpus.json (one case
// manifest), findings.json (the agent output under test), and the normal
// provenance stores (record.json/cost.json/panel). Nothing here touches the
// network or writes to disk: calibrate renders one bundle, trend
// aggregates many.
package corpuscore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

// Bundle conventions and defaults.
const (
	// manifestName is the per-bundle copy of a case manifest.
	manifestName = "/corpus.json"
	// DefaultLineTolerance is the +/- line window that still counts as the
	// same defect when a manifest does not pin one. One line admits the second
	// line of a statement split across lines and the brace a reviewer anchors
	// to, while keeping the window from covering a neighboring statement. Zero
	// rejects an otherwise correct anchor on a multi-line statement; a wider
	// window is what made every anchor in a short hunk score. A case pins its
	// own window when it knows the exact line.
	DefaultLineTolerance = 1
	// maxManifestBytes caps one operator-written case manifest.
	maxManifestBytes = 1 << 20
	// unrecorded and unpaneled are the honest grouping labels when a run
	// carries no model or panel provenance.
	unrecorded = "unrecorded"
	unpaneled  = "unpaneled"
	// ResultTruePositive marks a finding that matched ground truth.
	ResultTruePositive = "true-positive"
	// ResultFalsePositive marks a finding with no matching ground-truth bug.
	ResultFalsePositive = "false-positive"
	// ReasonUnanchored marks a finding that carries no path and line.
	ReasonUnanchored = "unanchored"
	// ReasonMovedToBody marks a finding whose anchor record moved out of every
	// reviewed hunk, so it posts as body text rather than a line claim.
	ReasonMovedToBody = "moved-to-body"
	// ReasonOffLine marks a finding whose anchor is outside every ground-truth
	// bug's line window.
	ReasonOffLine = "off-line"
	// ReasonUnnamed marks a finding inside a bug's window whose text never names
	// that bug's defect.
	ReasonUnnamed = "unnamed"
	// ReasonAlreadyMatched marks a second finding on an already-matched bug.
	ReasonAlreadyMatched = "already-matched"
	// RateDecimals is the published precision for every rate.
	RateDecimals = 4
)

// refusal code for an invalid case manifest.
const codeCorpusInvalid = "corpus-invalid"

const (
	kindControl = "control"
	kindSeeded  = "seeded"
	kindReal    = "real"
	// decimalBase is the radix atoi folds a hunk header's digits in.
	decimalBase = 10
	// space is the separator normalize folds and a selector dot may cross.
	space = " "
)

// IsDir reports whether dir names a marked corpus bundle: the last
// segment carries the corpus- prefix, whatever root anchors it.
func IsDir(dir string) bool {
	return strings.HasPrefix(filepath.Base(dir), "corpus-")
}

// Load reads and validates one bundle's corpus.json.
func Load(dir string) (*core.CorpusCase, *core.RefusalError) {
	return LoadFile(dir + manifestName)
}

// LoadFile reads and validates one corpus case manifest at path. Every failure
// is a typed corpus-invalid refusal naming the file and the reason.
func LoadFile(path string) (*core.CorpusCase, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, invalid(path, "unreadable")
	}
	if len(raw) > maxManifestBytes {
		return nil, invalid(path, "larger than 1 MiB")
	}
	var c core.CorpusCase
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, invalid(path, "not a corpus case manifest: "+err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalid(path, "trailing data after the manifest object")
	}
	if ref := validate(&c, path); ref != nil {
		return nil, ref
	}
	return &c, nil
}

// invalid builds the corpus-invalid refusal.
func invalid(path, reason string) *core.RefusalError {
	return &core.RefusalError{Code: codeCorpusInvalid, Message: fmt.Sprintf(
		"corpus manifest %s is %s (write a schemas/corpus.schema.json case: id, kind, "+
			"provenance, bugs (empty for controls); try: copy bench/corpus/<id>/case.json)", path, reason)}
}

// invalidDiff refuses a real bundle whose reviewed diff cannot be used. The
// message names the path and never any content: grading without the diff would
// keep the built-in phrases it spells.
func invalidDiff(path string) *core.RefusalError {
	return &core.RefusalError{Code: codeCorpusInvalid, Message: fmt.Sprintf(
		"corpus bundle %s is missing, empty, or unreadable; a real case must grade against its "+
			"reviewed full.diff (rebuild the bundle and re-run the review)", path)}
}

// validate enforces the semantic rules the JSON Schema cannot express.
func validate(c *core.CorpusCase, path string) *core.RefusalError {
	if c.SchemaVersion != "1" {
		return invalid(path, "missing schema_version \"1\"")
	}
	if strings.TrimSpace(c.ID) == "" {
		return invalid(path, "missing id")
	}
	if ref := validateKind(c, path); ref != nil {
		return ref
	}
	if ref := validateProvenance(c, path); ref != nil {
		return ref
	}
	if c.Kind == kindControl {
		return validateControl(c, path)
	}

	return validateBugs(c.Bugs, path)
}

func validateKind(c *core.CorpusCase, path string) *core.RefusalError {
	if c.Kind != kindSeeded && c.Kind != kindReal && c.Kind != kindControl {
		return invalid(path, "kind must be seeded, real, or control")
	}
	if c.Provenance.Kind != c.Kind && c.Kind != kindControl {
		return invalid(path, "provenance.kind must match kind")
	}
	return nil
}

func validateControl(c *core.CorpusCase, path string) *core.RefusalError {
	if c.Bugs == nil || len(c.Bugs) != 0 {
		return invalid(path, "control cases require bugs: []")
	}
	if c.Provenance.Kind != kindSeeded && c.Provenance.Kind != kindReal {
		return invalid(path, "control provenance.kind must be seeded or real")
	}
	return nil
}

// validateProvenance enforces the per-kind origin requirements.
func validateProvenance(c *core.CorpusCase, path string) *core.RefusalError {
	switch c.Provenance.Kind {
	case kindSeeded:
		if strings.TrimSpace(c.Provenance.Injected) == "" {
			return invalid(path, "seeded cases need provenance.injected (what was injected)")
		}
	case kindReal:
		if ref := validateRealProvenance(c, path); ref != nil {
			return ref
		}
	}
	return nil
}

// validateRealProvenance requires the origin every real case names: the
// repository, the PR, and the two commits its diff spans, which are the commits
// a bundle stamps and a reader can check the diff against. The later fix
// commit is required only where a defect was fixed: a clean control is a merged
// PR nothing had to repair, so it has none to name.
func validateRealProvenance(c *core.CorpusCase, path string) *core.RefusalError {
	p := c.Provenance
	if strings.TrimSpace(p.Repo) == "" || p.PR == nil {
		return invalid(path, "real cases need provenance.repo and provenance.pr")
	}
	if !commitID(p.BaseSHA) {
		return invalid(path, "provenance.base_sha "+strconv.Quote(p.BaseSHA)+" is not a 40-character lowercase hex commit id")
	}
	if !commitID(p.HeadSHA) {
		return invalid(path, "provenance.head_sha "+strconv.Quote(p.HeadSHA)+" is not a 40-character lowercase hex commit id")
	}
	if c.Kind != kindControl && strings.TrimSpace(p.FixCommit) == "" {
		return invalid(path, "real bug cases need provenance.fix_commit (the commit that fixed the defect)")
	}
	return nil
}

// commitIDLen is the length of a full lowercase hex git commit id.
const commitIDLen = 40

// commitID reports whether s is a 40-character lowercase hex commit id, the
// shape a bundle's base_sha and head_sha take.
func commitID(s string) bool {
	if len(s) != commitIDLen {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// validateBugs enforces at least one well-formed, unique ground-truth bug.
func validateBugs(bugs []core.CorpusBug, path string) *core.RefusalError {
	if len(bugs) == 0 {
		return invalid(path, "needs at least one ground-truth bug")
	}
	seen := map[string]bool{}
	for i := range bugs {
		if ref := validateBug(&bugs[i], i+1, seen, path); ref != nil {
			return ref
		}
	}
	return nil
}

// validateBug checks one ground-truth bug and records its identity.
func validateBug(b *core.CorpusBug, n int, seen map[string]bool, path string) *core.RefusalError {
	if strings.TrimSpace(b.Path) == "" || b.Line < 1 || strings.TrimSpace(b.Kind) == "" {
		return invalid(path, fmt.Sprintf("bug %d needs path, line >= 1, and kind", n))
	}
	if b.Severity != "critical" && b.Severity != "important" && b.Severity != "informational" {
		return invalid(path, fmt.Sprintf("bug %d severity must be critical, important, or informational", n))
	}
	if ref := validateKeywords(b.Keywords, n, path); ref != nil {
		return ref
	}
	if ref := validateAlsoLines(b, n, path); ref != nil {
		return ref
	}
	key := b.Path + ":" + b.Kind + ":" + strconv.Itoa(b.Line)
	if seen[key] {
		return invalid(path, "duplicate ground-truth bug at "+key)
	}
	seen[key] = true
	return nil
}

// validateAlsoLines refuses a blank alternate anchor: every entry must name
// a real line, and must not repeat the primary line or another entry.
func validateAlsoLines(b *core.CorpusBug, n int, path string) *core.RefusalError {
	seen := map[int]bool{b.Line: true}
	for _, line := range b.AlsoLines {
		if line < 1 {
			return invalid(path, fmt.Sprintf("bug %d has also_lines entry %d below line 1 "+
				"(drop it, or point it at the alternate anchor)", n, line))
		}
		if seen[line] {
			return invalid(path, fmt.Sprintf("bug %d repeats line %d in also_lines (each anchor must be a distinct line)", n, line))
		}
		seen[line] = true
	}
	return nil
}

// validateKeywords refuses a blank phrase, which would name every finding.
func validateKeywords(keywords []string, n int, path string) *core.RefusalError {
	for _, k := range keywords {
		if strings.TrimSpace(k) == "" {
			return invalid(path, fmt.Sprintf("bug %d has a blank keyword (drop it, or write the phrase a finding must contain)", n))
		}
	}
	return nil
}

// CheckDiff refuses a real case whose ground truth its own diff contradicts: a
// keyword the diff itself spells, or a bug line the PR did not add. Both are
// free passes for a tool-free reviewer. A vocabulary lifted from the patch
// grades "StopOnNthArg is a new field" as a defect report when it names the
// change; a ground truth on a context line is code the diff carries without
// marking, so a reviewer who reads only the added lines has no fair chance at
// it. Seeded cases are synthetic (their diff is written for the bug) and
// controls carry no bugs, so the rule binds real cases only.
func CheckDiff(raw, diff []byte, path string) *core.RefusalError {
	var c core.CorpusCase
	if err := json.Unmarshal(raw, &c); err != nil {
		return invalid(path, "not a corpus case manifest: "+err.Error())
	}
	if c.Provenance.Kind != kindReal {
		return nil
	}
	if ref := unusableVocabulary(&c, diff, path); ref != nil {
		return ref
	}
	return unaddedAnchor(&c, diff, path)
}

// unusableVocabulary refuses a case whose curator keyword the diff spells, or
// whose bug has no phrase left once the built-in phrases the diff spells are
// dropped. A built-in phrase is filtered rather than refused because the
// curator cannot change the category table; a keyword is refused because the
// curator can.
func unusableVocabulary(c *core.CorpusCase, diff []byte, path string) *core.RefusalError {
	text := normalize(string(diff))
	for i := range c.Bugs {
		b := &c.Bugs[i]
		if keyword, ok := spelledKeyword(b, text); ok {
			return invalid(path, fmt.Sprintf(
				"bug %d keyword %q is spelled by the case's own full.diff; "+
					"name the defect in words instead of reusing the diff's words", i+1, keyword))
		}
		if len(unspelledPhrases(b, text)) == 0 {
			return invalid(path, fmt.Sprintf(
				"bug %d has no phrase its own full.diff does not spell; "+
					"add a keyword the diff does not spell", i+1))
		}
	}
	return nil
}

// spelledKeyword is the first keyword the normalized diff spells, if any.
func spelledKeyword(b *core.CorpusBug, text string) (string, bool) {
	for _, keyword := range b.Keywords {
		if containsPhrase(text, normalize(keyword)) {
			return keyword, true
		}
	}
	return "", false
}

// unaddedAnchor names the first anchor the diff does not add. A bug's window
// is its line and every also_line: a context line in it is as unfair as a
// context primary anchor, because a finding there is graded as a hit.
func unaddedAnchor(c *core.CorpusCase, diff []byte, path string) *core.RefusalError {
	added := addedLines(diff)
	for i := range c.Bugs {
		b := &c.Bugs[i]
		if line, ok := firstUnadded(added[b.Path], b); !ok {
			return invalid(path, fmt.Sprintf(
				"bug %d anchors %s:%d, a line this diff does not add; put the ground truth on the line "+
					"the PR wrote, or the review is scored on code the diff only carries as context",
				i+1, b.Path, line))
		}
	}
	return nil
}

// firstUnadded reports whether every anchor of one bug is added, naming the
// first that is not.
func firstUnadded(added map[int]bool, b *core.CorpusBug) (int, bool) {
	if !added[b.Line] {
		return b.Line, false
	}
	for _, line := range b.AlsoLines {
		if !added[line] {
			return line, false
		}
	}
	return 0, true
}

// addedLines indexes the new-side lines each path's hunks add.
func addedLines(diff []byte) map[string]map[int]bool {
	scan := &addScan{added: map[string]map[int]bool{}}
	for line := range strings.SplitSeq(string(diff), "\n") {
		scan.visit(line)
	}
	return scan.added
}

// addScan walks one unified diff, marking the new-side lines its hunks add.
type addScan struct {
	added   map[string]map[int]bool
	path    string
	newLine int
}

// visit reads one diff line: a header moves the path or the counter, an
// addition marks its line, and context advances to the next new-side line.
func (s *addScan) visit(line string) {
	switch {
	case strings.HasPrefix(line, "+++ "):
		s.path = diffPath(line)
	case strings.HasPrefix(line, "@@ "):
		s.newLine, _ = hunkStart(line)
	case s.path == "" || s.newLine == 0, strings.HasPrefix(line, "-"):
	case strings.HasPrefix(line, "+"):
		s.mark(s.newLine)
		s.newLine++
	case strings.HasPrefix(line, `\`):
		// "\ No newline at end of file" annotates the line above it and is
		// not body text on either side, so the new-side counter must not
		// move: across a "-old / marker / +new" replacement the marker would
		// otherwise push every following added line one too high.
	default:
		s.newLine++
	}
}

// mark records one new-side line as added.
func (s *addScan) mark(n int) {
	if s.added[s.path] == nil {
		s.added[s.path] = map[int]bool{}
	}
	s.added[s.path][n] = true
}

// diffPath reads the new-side path out of a "+++ " header, "" for /dev/null.
func diffPath(line string) string {
	path := strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
	if path == "/dev/null" {
		return ""
	}
	return path
}

// hunkStart reads a hunk header's new-side start and count: "@@ -a,b +c,d @@".
func hunkStart(line string) (start, count int) {
	_, rest, ok := strings.Cut(line, "+")
	if !ok {
		return 0, 0
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, 0
	}
	before, after, hasCount := strings.Cut(fields[0], ",")
	start = atoi(before)
	if !hasCount {
		return start, 1
	}
	return start, atoi(after)
}

// atoi is the digits-only parse a hunk header needs; a malformed field reads 0,
// which no added line can occupy.
func atoi(s string) int {
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*decimalBase + int(s[i]-'0')
	}
	return n
}

// Tolerance reads the manifest's line window (default 1).
func Tolerance(c *core.CorpusCase) int {
	if c.LineTolerance != nil && *c.LineTolerance >= 0 {
		return *c.LineTolerance
	}
	return DefaultLineTolerance
}

// Outcome is one reported finding graded against the ground truth. Reason
// names why a false positive failed; a true positive carries none.
type Outcome struct {
	Severity  string
	Sig       string
	Path      string
	Result    string
	Reason    string
	BugIndex  int
	Line      int
	BugLine   int
	HasPath   bool
	HasLine   bool
	HasBugHit bool
}

// Report is one bundle's graded review.
type Report struct {
	Case           *core.CorpusCase
	Outcomes       []Outcome
	Missed         []core.CorpusBug
	Reported       int
	Matched        int
	FalsePositives int
}

// Evaluate grades entries against the case's ground truth. A finding is a
// true positive when it names the same path, its line sits within the
// tolerance window of the primary line or any alternate anchor of an
// as-yet-unmatched bug, and its text names that bug's defect; everything else
// reported is a false positive. Each bug counts once even when several
// anchors match it: a second finding on the same bug is a false positive,
// and the reported match keeps the primary line. A finding record moved
// to the body is never a true positive: the line claim was withdrawn, so the
// text is graded as the note it became. The tool-generated moved section is
// skipped: it is record's own wording, and the comments it lists are graded
// from the moved entries themselves. Evaluate keeps the full vocabulary; use
// EvaluateWithDiff or EvaluateDir when the reviewed diff is known.
func Evaluate(c *core.CorpusCase, entries []any) *Report {
	return evaluate(c, entries, nil)
}

// EvaluateWithDiff grades entries against a real case whose reviewed diff is
// known, dropping the built-in phrases the diff spells.
func EvaluateWithDiff(c *core.CorpusCase, entries []any, diff []byte) *Report {
	return evaluate(c, entries, diff)
}

// EvaluateDir grades a bundle, reading the reviewed full.diff beside its
// corpus.json so the built-in phrases the diff spells are filtered. A real
// case must carry that diff: a missing or unreadable one is a corpus-invalid
// refusal, never a lenient grade. A seeded or synthetic case without one keeps
// the full vocabulary.
func EvaluateDir(dir string, c *core.CorpusCase, entries []any) (*Report, *core.RefusalError) {
	diff, ref := bundleDiff(dir, c)
	if ref != nil {
		return nil, ref
	}
	return evaluate(c, entries, diff), nil
}

func evaluate(c *core.CorpusCase, entries []any, diff []byte) *Report {
	tol := Tolerance(c)
	phrases := usablePhrases(c, diff)
	used := make([]bool, len(c.Bugs))
	rep := &Report{Case: c, Outcomes: make([]Outcome, 0, len(entries))}
	for _, e := range entries {
		if toolSection(e) {
			continue
		}
		rep.Reported++
		out, bug := grade(e, c.Bugs, phrases, used, tol)
		rep.Outcomes = append(rep.Outcomes, out)
		tallyOutcome(rep, used, bug)
	}
	collectMissed(rep, c.Bugs, used)
	return rep
}

// usablePhrases is each bug's defect-naming vocabulary. A real case drops the
// built-in phrases its own diff spells; a synthetic or diff-less case keeps
// the full set.
func usablePhrases(c *core.CorpusCase, diff []byte) [][]string {
	sets := make([][]string, len(c.Bugs))
	for i := range c.Bugs {
		sets[i] = defectPhrases(&c.Bugs[i])
	}
	if diff == nil || c.Provenance.Kind != kindReal {
		return sets
	}
	text := normalize(string(diff))
	for i := range c.Bugs {
		sets[i] = unspelledPhrases(&c.Bugs[i], text)
	}
	return sets
}

// bundleDiff reads the reviewed diff a bundle carries. A real case must carry
// a non-empty one: grading without it would keep the built-in phrases the diff
// spells. A seeded or synthetic case without one keeps the full vocabulary.
func bundleDiff(dir string, c *core.CorpusCase) ([]byte, *core.RefusalError) {
	path := filepath.Join(dir, "full.diff")
	raw, err := os.ReadFile(path)
	if err != nil {
		if c.Provenance.Kind != kindReal {
			return nil, nil
		}
		return nil, invalidDiff(path)
	}
	if c.Provenance.Kind == kindReal && len(bytes.TrimSpace(raw)) == 0 {
		return nil, invalidDiff(path)
	}
	return raw, nil
}

func tallyOutcome(rep *Report, used []bool, bug int) {
	if bug >= 0 {
		used[bug] = true
		rep.Matched++
		return
	}
	rep.FalsePositives++
}

func collectMissed(rep *Report, bugs []core.CorpusBug, used []bool) {
	for i := range bugs {
		if !used[i] {
			rep.Missed = append(rep.Missed, bugs[i])
		}
	}
}

func grade(e any, bugs []core.CorpusBug, phrases [][]string, used []bool, tol int) (Outcome, int) {
	out := Outcome{Result: ResultFalsePositive, Reason: ReasonUnanchored, BugIndex: -1}
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return out, -1
	}
	if severity, ok := m.Get("severity"); ok {
		out.Severity = jq.StrOf(severity)
	}
	if sig, ok := m.Get("sig"); ok {
		out.Sig = jq.StrOf(sig)
	}
	if !readAnchor(m, &out) {
		return out, -1
	}
	if movedToBody(m) {
		out.Reason = ReasonMovedToBody
		return out, -1
	}
	i, reason := matchBug(out.Path, out.Line, findingText(m), bugs, phrases, used, tol)
	if i < 0 {
		out.Reason = reason
		return out, -1
	}
	out.Result = ResultTruePositive
	out.Reason = ""
	out.BugLine, out.HasBugHit = bugs[i].Line, true
	out.BugIndex = i
	return out, i
}

func readAnchor(m *jq.Obj, out *Outcome) bool {
	path, ok := m.Get("path")
	if !ok || path == nil {
		return false
	}
	out.Path, out.HasPath = jq.StrOf(path), true
	line, ok := m.Get("line")
	if !ok {
		return false
	}
	n, ok := jq.IntOf(line)
	if !ok {
		return false
	}
	out.Line, out.HasLine = n, true
	return true
}

// movedToBody reports whether record moved the finding out of its hunk: the
// store keeps the old path and line, and only the anchored flag says the claim
// posts as body text.
func movedToBody(m *jq.Obj) bool {
	v, ok := m.Get("anchored")
	if !ok {
		return false
	}
	anchored, ok := v.(bool)
	return ok && !anchored
}

// toolSection reports whether the entry is the section record and post
// generate from anchors they moved out of the hunks. The wording is the
// tool's, so the reviewer's own claims are the only ones graded.
func toolSection(e any) bool {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	heading, ok := m.Get("heading")
	return ok && jq.StrOf(heading) == core.MovedSectionHeading
}

// findingText is the text a finding names its defect with: the heading of a
// section finding, then the body, lowercased with runs of whitespace folded to
// one space so a phrase can match across a line wrap.
func findingText(m *jq.Obj) string {
	var sb strings.Builder
	for _, key := range []string{"heading", "body"} {
		if v, ok := m.Get(key); ok {
			sb.WriteString(jq.StrOf(v))
			sb.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(strings.ToLower(sb.String())), " ")
}

// matchBug returns the first unused bug in the line window whose defect the
// finding names, plus the reason the match failed when there is none.
func matchBug(path string, line int, text string, bugs []core.CorpusBug, phrases [][]string, used []bool, tol int) (int, string) {
	window := windowBugs(path, line, bugs, tol)
	for _, i := range window {
		if !used[i] && namesDefectPhrases(text, phrases[i]) {
			return i, ""
		}
	}
	return -1, missReason(window, text, phrases, used)
}

// windowBugs is the indices of the bugs on path whose primary line or any
// alternate anchor sits within tol. Matching any anchor counts the bug once:
// the caller marks the bug used by index, never by anchor.
func windowBugs(path string, line int, bugs []core.CorpusBug, tol int) []int {
	var out []int
	for i := range bugs {
		if bugs[i].Path != path {
			continue
		}
		if withinAnchors(line, &bugs[i], tol) {
			out = append(out, i)
		}
	}
	return out
}

// withinAnchors reports whether line sits within tol of the bug's primary
// line or any alternate anchor.
func withinAnchors(line int, b *core.CorpusBug, tol int) bool {
	if abs(line-b.Line) <= tol {
		return true
	}
	for _, alt := range b.AlsoLines {
		if abs(line-alt) <= tol {
			return true
		}
	}
	return false
}

// missReason names why no bug in the window matched: there was none, the
// finding names a bug already counted, one sat there unnamed, or every bug in
// the window was already counted.
func missReason(window []int, text string, phrases [][]string, used []bool) string {
	if len(window) == 0 {
		return ReasonOffLine
	}
	reason := ReasonAlreadyMatched
	for _, i := range window {
		switch {
		case used[i] && namesDefectPhrases(text, phrases[i]):
			return ReasonAlreadyMatched
		case !used[i]:
			reason = ReasonUnnamed
		}
	}
	return reason
}

// namesDefect reports whether text names the bug's defect with its full
// vocabulary: its kind in hyphen or space form, a phrase the kind's category
// answers to, or a phrase the case pins as a keyword.
func namesDefect(text string, b *core.CorpusBug) bool {
	return namesDefectPhrases(text, defectPhrases(b))
}

// namesDefectPhrases reports whether text names the defect with one of
// phrases. The text is normalized like the phrases, so a phrase that opens a
// sentence ("Empty positional: ...") still matches its lower-case form.
func namesDefectPhrases(text string, phrases []string) bool {
	text = normalize(text)
	for _, phrase := range phrases {
		if containsPhrase(text, normalize(phrase)) {
			return true
		}
	}
	return false
}

// unspelledPhrases are the defect phrases a normalized diff does not spell.
// A real case's grading vocabulary drops the ones it does: the built-ins
// belong to the published table, so a diff that carries one must not name the
// defect.
func unspelledPhrases(b *core.CorpusBug, text string) []string {
	var out []string
	for _, phrase := range defectPhrases(b) {
		if !containsPhrase(text, normalize(phrase)) {
			out = append(out, phrase)
		}
	}
	return out
}

// defectPhrases are the names a finding can call the bug: the kind, the
// category's vocabulary, then the case's own.
func defectPhrases(b *core.CorpusBug) []string {
	phrases := []string{b.Kind, strings.ReplaceAll(b.Kind, "-", " ")}
	phrases = append(phrases, categoryPhrases(b.Kind)...)
	return append(phrases, b.Keywords...)
}

// categoryPhrases is the published vocabulary each defect category answers to,
// so a case written before keywords existed still matches a finding that names
// its defect without repeating the kind label. A finding is only compared with
// bugs on its own path inside the line window, so each phrase need only be
// distinctive of the category, not globally unique. A kind outside the table
// is named by its kind text and keywords alone.
func categoryPhrases(kind string) []string {
	switch kind {
	case "auth-bypass":
		return []string{
			"privilege", "authoriz", "authoris", "unauthoriz", "unauthoris", "unauthenticated",
			"bypass", "role check", "admin check", "permission check",
		}
	case "missing-error-check":
		return []string{
			"short write", "unchecked", "discard", "ignored error", "error is ignored",
			"error is dropped", "error is not checked", "ignores the error", "drops the error",
		}
	case "nil-deref":
		return []string{"nil pointer", "nil deref", "nil map", "deref", "null pointer", "nil check"}
	case "off-by-one":
		return []string{"one past", "loop bound", "out of range", "out of bounds"}
	case "index-out-of-range":
		return []string{"out of range", "out of bounds", "bounds check", "len("}
	case "path-traversal":
		return []string{"traversal", "confine", "escape", ".."}
	case "race":
		return []string{"race", "goroutine", "concurrent", "wait for the close"}
	case "injection":
		return []string{"injection", "inject", "query argument"}
	}
	return nil
}

// containsPhrase reports whether text holds phrase at a word start: a phrase
// opening with a letter or digit must not be glued to one before it, so the
// keyword "race" never matches inside "trace" or "brace", while "deref"
// still matches "dereferencing". Both come from normalize, so both are lower
// case.
func containsPhrase(text, phrase string) bool {
	if phrase == "" {
		return false
	}
	for from := 0; from <= len(text)-len(phrase); {
		i := strings.Index(text[from:], phrase)
		if i < 0 {
			return false
		}
		at := from + i
		if at == 0 || !wordByte(phrase[0]) || !wordByte(text[at-1]) {
			return true
		}
		from = at + 1
	}
	return false
}

// normalize lowers, folds whitespace, and folds a spaced selector dot, so
// "cmd .Name" and "cmd.Name" are one phrase.
func normalize(s string) string {
	return foldDots(strings.Join(strings.Fields(strings.ToLower(s)), space))
}

// foldDots joins a dot written with a space before it when identifier parts sit
// on both sides, so "cmd .Name" becomes "cmd.Name". A sentence's dot has no
// space before it, so its following space stays: "ignored. Name" never becomes
// "ignored.name".
func foldDots(s string) string {
	parts := strings.Split(s, ".")
	for i := 1; i < len(parts); i++ {
		left := parts[i-1]
		if !strings.HasSuffix(left, space) || !identEnd(left) || !identStart(parts[i]) {
			continue
		}
		parts[i-1] = strings.TrimRight(left, space)
		parts[i] = strings.TrimLeft(parts[i], space)
	}
	return strings.Join(parts, ".")
}

func identEnd(s string) bool {
	t := strings.TrimRight(s, space)
	return t != "" && identPart(t[len(t)-1])
}

func identStart(s string) bool {
	t := strings.TrimLeft(s, space)
	return t != "" && identPart(t[0])
}

func identPart(b byte) bool {
	return wordByte(b) || b == '_'
}

// wordByte is the lower-case letter or digit a phrase may not be glued to.
func wordByte(b byte) bool {
	return ('a' <= b && b <= 'z') || ('0' <= b && b <= '9')
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Rate returns n/d rounded to four decimals, or ok=false when d is zero
// (unmeasured never reads as zero).
func Rate(n, d int) (float64, bool) {
	if d == 0 {
		return 0, false
	}
	return math.Round(float64(n)/float64(d)*rateScale) / rateScale, true
}

// RateOrNil renders a rate for JSON: a rounded number, or nil when the
// denominator is zero.
func RateOrNil(n, d int) any {
	if rate, ok := Rate(n, d); ok {
		return rate
	}
	return nil
}

// RateText renders a JSON rate at the published precision, n/a when
// unmeasured.
func RateText(rate any) string {
	switch v := rate.(type) {
	case *float64:
		if v == nil {
			return "n/a"
		}
		return strconv.FormatFloat(*v, 'f', RateDecimals, rateBits)
	case float64:
		return strconv.FormatFloat(v, 'f', RateDecimals, rateBits)
	default:
		return "n/a"
	}
}

const rateBits = 64

const rateScale = 10000

// ModelOf reads the run's model from the latest cost record. Empty
// provenance reads "unrecorded", never an invented model.
func ModelOf(dir string) string {
	if m := provenance.CostModel(dir); m != "" {
		return m
	}
	return unrecorded
}

// PanelOf reads the bundle's pinned panel (fetch's panel file format:
// one slug per line, blank and #-comment lines skipped).
func PanelOf(dir string) []string {
	raw, err := os.ReadFile(dir + "/panel")
	if err != nil {
		return nil
	}
	var panel []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		panel = append(panel, s)
	}
	return panel
}

// PanelLabel renders a panel list as the grouping key used by trend.
func PanelLabel(panel []string) string {
	if len(panel) == 0 {
		return unpaneled
	}
	return strings.Join(panel, "+")
}
