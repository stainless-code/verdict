// Package corpuscore loads the benchmark corpus manifests and grades a
// bundle's findings against their recorded ground truth. A corpus bundle is
// a .review/corpus-* directory carrying corpus.json (one case manifest),
// findings.json (the agent output under test), and the normal provenance
// stores (record.json/cost.json/panel). Nothing here touches the network or
// writes to disk: calibrate renders one bundle, trend aggregates many.
package corpuscore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

// Bundle conventions and defaults.
const (
	// DirPrefix marks a corpus bundle: any .review/corpus-* directory.
	DirPrefix = ".review/corpus-"
	// manifestName is the per-bundle copy of a case manifest.
	manifestName = "/corpus.json"
	// DefaultLineTolerance is the +/- line window that still counts as the
	// same defect when a manifest does not pin one.
	DefaultLineTolerance = 3
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
	// RateDecimals is the published precision for every rate.
	RateDecimals = 4
)

// refusal code for an invalid case manifest.
const codeCorpusInvalid = "corpus-invalid"

// IsDir reports whether dir names a marked corpus bundle.
func IsDir(dir string) bool {
	return strings.HasPrefix(dir, DirPrefix)
}

// Load reads and validates one bundle's corpus.json. Every failure is a
// typed corpus-invalid refusal naming the file and the reason.
func Load(dir string) (*core.CorpusCase, *core.RefusalError) {
	path := dir + manifestName
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
			"provenance, and at least one bug; try: copy bench/corpus/<id>/case.json)", path, reason)}
}

// validate enforces the semantic rules the JSON Schema cannot express.
func validate(c *core.CorpusCase, path string) *core.RefusalError {
	if c.SchemaVersion != "1" {
		return invalid(path, "missing schema_version \"1\"")
	}
	if strings.TrimSpace(c.ID) == "" {
		return invalid(path, "missing id")
	}
	if c.Kind != "seeded" && c.Kind != "real" {
		return invalid(path, "kind must be seeded or real")
	}
	if c.Provenance.Kind != c.Kind {
		return invalid(path, "provenance.kind must match kind")
	}
	if ref := validateProvenance(c, path); ref != nil {
		return ref
	}
	return validateBugs(c.Bugs, path)
}

// validateProvenance enforces the per-kind origin requirements.
func validateProvenance(c *core.CorpusCase, path string) *core.RefusalError {
	switch c.Kind {
	case "seeded":
		if strings.TrimSpace(c.Provenance.Injected) == "" {
			return invalid(path, "seeded cases need provenance.injected (what was injected)")
		}
	case "real":
		if strings.TrimSpace(c.Provenance.Repo) == "" || c.Provenance.PR == nil ||
			strings.TrimSpace(c.Provenance.FixCommit) == "" {
			return invalid(path, "real cases need provenance.repo, provenance.pr, and provenance.fix_commit")
		}
	}
	return nil
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
	key := b.Path + ":" + b.Kind + ":" + strconv.Itoa(b.Line)
	if seen[key] {
		return invalid(path, "duplicate ground-truth bug at "+key)
	}
	seen[key] = true
	return nil
}

// Tolerance reads the manifest's line window (default 3).
func Tolerance(c *core.CorpusCase) int {
	if c.LineTolerance != nil && *c.LineTolerance >= 0 {
		return *c.LineTolerance
	}
	return DefaultLineTolerance
}

// Outcome is one reported finding graded against the ground truth.
type Outcome struct {
	Sig       string
	Path      string
	Result    string
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
// true positive when it names the same path and its line sits within the
// tolerance window of an as-yet-unmatched bug; everything else reported is a
// false positive. Each bug counts once: a second finding on the same bug is
// a false positive.
func Evaluate(c *core.CorpusCase, entries []any) *Report {
	tol := Tolerance(c)
	used := make([]bool, len(c.Bugs))
	rep := &Report{Case: c, Reported: len(entries), Outcomes: make([]Outcome, 0, len(entries))}
	for _, e := range entries {
		out, bug := grade(e, c.Bugs, used, tol)
		rep.Outcomes = append(rep.Outcomes, out)
		if bug >= 0 {
			used[bug] = true
			rep.Matched++
		} else {
			rep.FalsePositives++
		}
	}
	for i := range c.Bugs {
		if !used[i] {
			rep.Missed = append(rep.Missed, c.Bugs[i])
		}
	}
	return rep
}

// grade matches one finding to the first unused bug inside the window.
func grade(e any, bugs []core.CorpusBug, used []bool, tol int) (Outcome, int) {
	out := Outcome{Result: ResultFalsePositive}
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return out, -1
	}
	if sig, ok := m.Get("sig"); ok {
		out.Sig = jq.StrOf(sig)
	}
	if !readAnchor(m, &out) {
		return out, -1
	}
	if i := matchBug(out.Path, out.Line, bugs, used, tol); i >= 0 {
		out.Result = ResultTruePositive
		out.BugLine, out.HasBugHit = bugs[i].Line, true
		return out, i
	}
	return out, -1
}

// readAnchor fills path and line from one finding object.
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

// matchBug returns the first unused bug within the line window, or -1.
func matchBug(path string, line int, bugs []core.CorpusBug, used []bool, tol int) int {
	for i := range bugs {
		b := &bugs[i]
		if used[i] || b.Path != path || abs(line-b.Line) > tol {
			continue
		}
		return i
	}
	return -1
}

// abs returns the absolute value of n.
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
	f, ok := rate.(float64)
	if !ok {
		return "n/a"
	}
	return strconv.FormatFloat(f, 'f', RateDecimals, rateBits)
}

// rateBits is the float width RateText renders.
const rateBits = 64

// rateScale keeps published rates stable at four decimals.
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
